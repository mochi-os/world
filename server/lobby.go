// Mochi world: Lobby API (public HTTP: status, session list and create)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/netutil"
)

// Whole-request deadlines shared by every public HTTP listener.
// ReadHeaderTimeout alone leaves the BODY read unbounded: the handlers' size
// caps bound bytes, not duration, and the per-host limiter counts only
// completed requests.
const (
	TIMEOUT_HEADER = 10 * time.Second
	TIMEOUT_READ   = 15 * time.Second
	TIMEOUT_WRITE  = 15 * time.Second
	TIMEOUT_IDLE   = 60 * time.Second
)

// LOBBY_CONNECTIONS_MAXIMUM caps how many connections the lobby listener holds
// open at once. The deadlines bound how long one connection occupies a
// goroutine, not how many exist.
//
// Each public listener carries its OWN budget, sized for what it does:
// TRANSPORT_CONNECTIONS_MAXIMUM for the game transport, and
// RESPONDER_CONNECTIONS_MAXIMUM for the ACME responder, which needs far less.
// One shared name across all three read as a whole-server ceiling and was not
// one - the counters are per listener, so the true total was the sum. They are
// also not the same kind of quantity: listener_limit parks excess connections
// in the accept queue, where the transport refuses them outright, so a single
// counter would let traffic merely QUEUED on port 80 spend slots the game then
// refuses.
const LOBBY_CONNECTIONS_MAXIMUM = 512

// LOBBY_HOST_CONNECTIONS_MAXIMUM caps the lobby connections ONE address may hold
// at once, as HOST_CONNECTIONS_MAXIMUM does the transport's: without it one
// client opening keep-alive connections - a 429 keeps one open as well as a 200
// does - held all 512 and nobody else could read the status, list a match or
// fetch a map. A browser holds up to six HTTP/1.1 connections to a host and the
// client polls two or three things at once, so sixteen players behind one
// address fit inside it.
const LOBBY_HOST_CONNECTIONS_MAXIMUM = 64

// listener_limit caps accepted connections, in all and per address. Excess over
// maximum waits in the kernel's accept queue rather than being refused, and the
// cap is released as connections close; a connection from an address already
// holding share is closed at once, since what it waits for is its own
// address's connections to end. The figures are parameters so a test can
// assert at a small size.
func listener_limit(listener net.Listener, maximum int, share int) net.Listener {
	return netutil.LimitListener(&listener_hold{Listener: listener, share: share, held: map[string]int{}}, maximum)
}

// listener_hold is the per-address half of listener_limit.
type listener_hold struct {
	net.Listener
	share int
	lock  sync.Mutex
	held  map[string]int
}

func (l *listener_hold) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host := origin(connection.RemoteAddr().String())
		l.lock.Lock()
		if l.held[host] >= l.share {
			l.lock.Unlock()
			connection.Close()
			continue
		}
		l.held[host]++
		l.lock.Unlock()
		return &listener_held{Conn: connection, release: func() { l.release(host) }}, nil
	}
}

// release returns one address's slot, and forgets the address at zero so the
// map holds only addresses with a connection open.
func (l *listener_hold) release(host string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.held[host] <= 1 {
		delete(l.held, host)
		return
	}
	l.held[host]--
}

// listener_held is an accepted connection that returns its address's slot when
// it closes, once however often it is closed.
type listener_held struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *listener_held) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// lobby_start serves the public lobby API. World servers are open: there is
// no authentication, only rate and capacity limits.
func lobby_start(fatal chan<- error) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", lobby_status)
	mux.HandleFunc("/sessions", lobby_sessions)
	mux.HandleFunc("/withdraw", lobby_withdraw)
	mux.HandleFunc("/chat", lobby_chat)
	mux.HandleFunc("/poll", lobby_poll)
	mux.HandleFunc("/maps/", lobby_map)
	address := fmt.Sprintf("%s:%d", ini_string("lobby", "listen", ""), ini_int("lobby", "port", 4433))
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: TIMEOUT_HEADER,
		ReadTimeout:       TIMEOUT_READ,
		WriteTimeout:      TIMEOUT_WRITE,
		IdleTimeout:       TIMEOUT_IDLE,
	}
	// Bind SYNCHRONOUSLY so a taken port or bad address is a fatal startup error
	// rather than a live process that never serves (#175). Serve() then blocks in
	// the background and reports its terminal error to fatal.
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("lobby listen %s: %w", address, err)
	}
	listener = listener_limit(listener, LOBBY_CONNECTIONS_MAXIMUM, LOBBY_HOST_CONNECTIONS_MAXIMUM)
	info("lobby listening on %s", address)
	if certificate_file != "" || acme_manager != nil {
		// TLS resolves per handshake through certificate_get, so file
		// renewals and ACME rotations apply without a restart. The empty
		// cert/key args defer to TLSConfig.GetCertificate.
		server.TLSConfig, _ = certificate_tls()
		go func() { fatal <- fmt.Errorf("lobby: %w", server.ServeTLS(listener, "", "")) }()
	} else {
		go func() { fatal <- fmt.Errorf("lobby: %w", server.Serve(listener)) }()
	}
	return nil
}

// lobby_cors marks every lobby response as callable from any origin — the
// API is public by design. Returns true when the request was a preflight.
func lobby_cors(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// mapper is the optional game interface for map geometry: a game that
// carries its maps serves them here, so a client flies its prediction against
// the very geometry the server collides it with. The game validates the name.
type mapper interface {
	Map(name string) ([]byte, bool)
}

// lobby_chart is a map as the lobby serves it: its file, and the file gzipped.
type lobby_chart struct {
	raw        []byte
	compressed []byte
}

// lobby_charts holds each map served, by name. The maps are embedded and never
// change, so each is read and compressed once; a join used to fetch 128 KB of
// geometry that gzips to 20 KB, on the join's critical path.
var lobby_charts sync.Map

// lobby_chart_get is the named map from the first game that carries it, or nil.
func lobby_chart_get(name string) *lobby_chart {
	if held, ok := lobby_charts.Load(name); ok {
		return held.(*lobby_chart)
	}
	for _, g := range games {
		m, ok := g.(mapper)
		if !ok {
			continue
		}
		body, found := m.Map(name)
		if !found {
			continue
		}
		var packed bytes.Buffer
		z, _ := gzip.NewWriterLevel(&packed, gzip.BestCompression)
		z.Write(body)
		z.Close()
		c := &lobby_chart{raw: body, compressed: packed.Bytes()}
		held, _ := lobby_charts.LoadOrStore(name, c)
		return held.(*lobby_chart)
	}
	return nil
}

// lobby_gzip reports whether the request accepts a gzipped body.
func lobby_gzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, parameters, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		parameters = strings.ReplaceAll(strings.TrimSpace(parameters), " ", "")
		return parameters != "q=0" && parameters != "q=0.0" && parameters != "q=0.00" && parameters != "q=0.000"
	}
	return false
}

// lobby_map serves one map's geometry: GET /maps/<name>, gzipped when the
// request accepts it.
func lobby_map(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	if !lobby_browse(r) {
		lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
		return
	}
	c := lobby_chart_get(strings.TrimPrefix(r.URL.Path, "/maps/"))
	if c == nil {
		lobby_respond(w, http.StatusNotFound, map[string]any{"error": "map"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Vary", "Accept-Encoding")
	body := c.raw
	if lobby_gzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		body = c.compressed
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func lobby_respond(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already on the wire, so a marshal failure can only be
	// logged - but silence would leave a truncated body behind a 200.
	if err := json.NewEncoder(w).Encode(body); err != nil {
		debug("lobby: cannot encode response: %v", err)
	}
}

func lobby_status(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	// Walks every session under sessions_counts, so it carries the same budget
	// as the other reads that answer any origin.
	if !lobby_browse(r) {
		lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
		return
	}
	lobby_respond(w, http.StatusOK, lobby_state(r))
}

// lobby_state is the body of /status, for the request it answers.
func lobby_state(r *http.Request) map[string]any {
	count, players := sessions_counts()
	// present is everyone here: the pilots seen on the server page lately plus
	// the players in a match, who are not polling the page from the cockpit.
	// players stays what it was - the number flying - so an older client's
	// "N flying" keeps meaning that.
	present := presence_count() + players
	names := []string{}
	for name := range games {
		names = append(names, name)
	}
	body := map[string]any{
		"name":     ini_string("world", "name", "Mochi world"),
		"version":  build_version,
		"protocol": protocol,
		"started":  started.Unix(),
		"games":    names,
		"sessions": count,
		"players":  players,
		"present":  present,
		"address":  transport_address(r),
	}
	if hash, expires := certificate_hash(); hash != "" {
		body["certificate"] = map[string]any{"hash": hash, "expires": expires}
	}
	return body
}

// presence is the pilots seen on this server's page lately, by the address
// they polled from and then by token: the match-list poll every few seconds
// is the sighting, and the status counts the tokens sighted inside the window
// as players here. A token is the player's own stable offer token, so one
// player is one entry however many polls they make - but the token is also
// whatever the caller puts in the query, so one address stands for no more
// pilots than it may hold game connections.
var presence = map[string]map[string]time.Time{}
var presence_lock sync.Mutex

const presence_window = 20 * time.Second

func presence_touch(address string, pilot string) {
	if pilot == "" {
		return
	}
	host := origin(address)
	now := time.Now()
	presence_lock.Lock()
	defer presence_lock.Unlock()
	seen := presence[host]
	if seen == nil {
		seen = map[string]time.Time{}
		presence[host] = seen
	}
	if _, known := seen[pilot]; !known {
		for token, when := range seen {
			if now.Sub(when) > presence_window {
				delete(seen, token)
			}
		}
		if len(seen) >= ini_int("limits", "hosts", HOST_CONNECTIONS_MAXIMUM) {
			return
		}
	}
	seen[pilot] = now
}

// presence_count is the pilots sighted inside the window.
func presence_count() int {
	presence_lock.Lock()
	defer presence_lock.Unlock()
	now := time.Now()
	count := 0
	for _, seen := range presence {
		for _, when := range seen {
			if now.Sub(when) <= presence_window {
				count++
			}
		}
	}
	return count
}

// presence_prune drops sightings older than the window, and addresses left
// with none, so the map holds only who has been here lately whether or not
// anybody asks for the status.
func presence_prune() {
	presence_lock.Lock()
	defer presence_lock.Unlock()
	now := time.Now()
	for host, seen := range presence {
		for token, when := range seen {
			if now.Sub(when) > presence_window {
				delete(seen, token)
			}
		}
		if len(seen) == 0 {
			delete(presence, host)
		}
	}
}

// presence_manager runs the prune on the server's own clock, forever.
func presence_manager() {
	for range time.Tick(5 * time.Second) {
		presence_prune()
	}
}

// lobby_poll answers everything the server page shows in one request: the
// status, the match list and the lobby chat since a cursor. It is the offer
// heartbeat and the presence sighting, as the match-list read is, and spends
// one read of the budget where the three separate reads it replaces spent
// three.
func lobby_poll(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		lobby_respond(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	if !lobby_browse(r) {
		lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
		return
	}
	since, valid := chat_cursor(r)
	if !valid {
		lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "since"})
		return
	}
	pilot := clean(r.URL.Query().Get("pilot"), 64)
	sessions_touch(pilot)
	presence_touch(r.RemoteAddr, pilot)
	lines, latest := chat_since(since)
	lobby_respond(w, http.StatusOK, map[string]any{
		"status":   lobby_state(r),
		"sessions": sessions_list(r.URL.Query().Get("game"), pilot),
		"chat":     map[string]any{"lines": lines, "sequence": latest},
	})
}

func lobby_sessions(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !lobby_browse(r) {
			lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
			return
		}
		// The match-list poll doubles as the offer heartbeat (#77): a player
		// browsing this server keeps their own offer alive by being here. The
		// same token tells the listing which offer is the caller's own.
		pilot := clean(r.URL.Query().Get("pilot"), 64)
		sessions_touch(pilot)
		presence_touch(r.RemoteAddr, pilot)
		lobby_respond(w, http.StatusOK, map[string]any{"sessions": sessions_list(r.URL.Query().Get("game"), pilot)})
	case http.MethodPost:
		if !lobby_allow(r) {
			lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
			return
		}
		var request struct {
			Game       string         `json:"game"`
			Mode       string         `json:"mode"`
			Label      string         `json:"label"`
			Name       string         `json:"name"`
			Pilot      string         `json:"pilot"` // the creator's stable token: one live offer per pilot (#77)
			Capacity   int            `json:"capacity"`
			Parameters map[string]any `json:"parameters"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&request); err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "request"})
			return
		}
		// Everything a creator sends that comes back out of the public listing goes
		// through clean() - control characters stripped, the cap counted in runes.
		request.Mode = clean(request.Mode, 32)
		request.Label = clean(request.Label, 64)
		pilot := clean(request.Pilot, 64)
		s, err := sessions_make(request.Game, request.Mode, request.Label, request.Capacity, request.Parameters, false, offer{creator: origin(r.RemoteAddr), pilot: pilot})
		if err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// One offer at a time: a new match replaces whatever this pilot was
		// already offering, so the list can never fill with a single player's
		// abandoned matches. Withdrawn AFTER the create succeeds, because
		// withdrawing first meant a "full" or "unknown" error left the pilot
		// with no offer at all - a replacement that replaced it with nothing.
		// Before sessions_own, so the new session carries no owner yet and is
		// not caught by its own replacement.
		sessions_withdraw(pilot)
		sessions_own(s, pilot)
		if name := clean(request.Name, 32); name != "" {
			chat_made(name, s.spec.Label) // the lobby's system line: who just made what (#84)
		}
		body := map[string]any{
			"session": s.identifier, "game": s.spec.Game, "mode": s.spec.Mode,
			"label": s.spec.Label, "capacity": s.spec.Capacity, "address": transport_address(r),
		}
		if hash, expires := certificate_hash(); hash != "" {
			body["certificate"] = map[string]any{"hash": hash, "expires": expires}
		}
		lobby_respond(w, http.StatusOK, body)
	default:
		lobby_respond(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
	}
}

// lobby_withdraw retires the caller's own offer: leaving the server page, or
// joining somebody else's match. The heartbeat timeout is only the backstop
// for a closed tab — a deliberate departure should be immediate.
func lobby_withdraw(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		lobby_respond(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	if !lobby_retire(r) {
		lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
		return
	}
	var request struct {
		Pilot string `json:"pilot"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "request"})
		return
	}
	lobby_respond(w, http.StatusOK, map[string]any{"withdrawn": sessions_withdraw(clean(request.Pilot, 64))})
}

// The server-wide lobby chat (#84): one ring of recent lines, polled over plain
// HTTP since the audience has no game connection yet. A line is player chat
// ({name, text}) or a system event ({event, name, label}) the client localises.
var (
	chat_lines    []map[string]any
	chat_sequence uint64
	chat_lock     sync.Mutex
)

// chat_append stores one line under the next sequence number.
func chat_append(line map[string]any) {
	chat_lock.Lock()
	defer chat_lock.Unlock()
	chat_sequence++
	line["sequence"] = chat_sequence
	line["time"] = time.Now().Unix()
	chat_lines = append(chat_lines, line)
	if len(chat_lines) > 100 {
		chat_lines = chat_lines[len(chat_lines)-100:]
	}
}

// chat_made records the match-creation system event.
func chat_made(name string, label string) {
	chat_append(map[string]any{"event": "made", "name": name, "label": label})
}

func lobby_chat(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		// Copies up to 100 lines under chat_lock, contending with every poster.
		if !lobby_browse(r) {
			lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
			return
		}
		since, valid := chat_cursor(r)
		if !valid {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "since"})
			return
		}
		lines, latest := chat_since(since)
		lobby_respond(w, http.StatusOK, map[string]any{"lines": lines, "sequence": latest})
	case http.MethodPost:
		if !lobby_voice(r) {
			lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
			return
		}
		var request struct {
			Name string `json:"name"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "request"})
			return
		}
		name := clean(request.Name, 32)
		if name == "" {
			name = "pilot"
		}
		words := clean(request.Text, 200)
		if words == "" {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "empty"})
			return
		}
		chat_append(map[string]any{"name": name, "text": words})
		lobby_respond(w, http.StatusOK, map[string]any{"sent": true})
	default:
		lobby_respond(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
	}
}

// chat_cursor reads a chat read's "since" cursor: zero when absent, and not
// valid when present but not a number.
func chat_cursor(r *http.Request) (uint64, bool) {
	cursor := r.URL.Query().Get("since")
	if cursor == "" {
		return 0, true
	}
	since, err := strconv.ParseUint(cursor, 10, 64)
	return since, err == nil
}

// chat_since is the lines after since, and the newest sequence number.
func chat_since(since uint64) ([]map[string]any, uint64) {
	chat_lock.Lock()
	defer chat_lock.Unlock()
	lines := []map[string]any{}
	for _, line := range chat_lines {
		// chat_append is the only writer today, but this is an anonymous
		// request path: a single-value assertion here would panic the
		// handler goroutine the day that stops being true.
		sequence, _ := line["sequence"].(uint64)
		if sequence > since {
			lines = append(lines, line)
		}
	}
	return lines, chat_sequence
}

// clean strips control characters and caps a user string at limit runes.
func clean(words string, limit int) string {
	words = strings.Map(func(r rune) rune {
		// Format characters render as nothing but reorder or hide what follows:
		// the bidi embeddings and overrides (U+202A-202E, U+2066-2069) make text
		// read backwards, and the zero-width space and joiners (U+200B-200D,
		// U+FEFF) make one player's name visually identical to another's. The
		// C1 range is the same argument one block up from the ASCII controls.
		if r < 32 || r == 127 || (r >= 0x80 && r < 0xa0) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, words)
	words = strings.TrimSpace(words)
	if runes := []rune(words); len(runes) > limit {
		words = string(runes[:limit])
	}
	return words
}

// lobby_allow rate-limits session creation per client address.
var (
	creates      = map[string][]time.Time{}
	plays        = map[string][]time.Time{} // per-host /play connection limiter (transport.go)
	creates_lock sync.Mutex
)

func lobby_allow(r *http.Request) bool {
	return lobby_permit(creates, r, ini_int("limits", "creates", 10))
}

// lobby_voice rate-limits lobby chat per client address — its own budget, so
// conversation never spends the match-creation allowance (#84).
var says = map[string][]time.Time{}

func lobby_voice(r *http.Request) bool {
	return lobby_permit(says, r, ini_int("limits", "chats", 20))
}

// lobby_retire rate-limits offer withdrawal per client address: withdrawing is
// unauthenticated, so it needs a budget like every other write here. Exceeding
// it only leaves an offer to lapse on the grace timer.
var retires = map[string][]time.Time{}

func lobby_retire(r *http.Request) bool {
	return lobby_permit(retires, r, ini_int("limits", "withdraws", 30))
}

// lobby_browse rate-limits the reads - status, match list, chat and maps: they
// return a body proportional to the whole server and answer every origin. The
// loosest of the four budgets, because the match-list poll doubles as the offer
// heartbeat (#77).
var browses = map[string][]time.Time{}

// BROWSES_MINUTE is the default for [limits] browses, sized from the transport's
// own figure for one address, HOST_CONNECTIONS_MAXIMUM players: a player on the
// server page makes one /poll every 3 s, and a player in a match polls the
// lobby chat at the same rate, so each spends 20 a minute. Sixteen of them are
// 320, and the rest covers the map each fetches to join. At 120 the stock
// client's three separate polls - 44 a minute - admitted two players behind one
// address, and the third saw the server flicker unreachable and lost the match
// they were offering.
const BROWSES_MINUTE = 400

func lobby_browse(r *http.Request) bool {
	return lobby_permit(browses, r, ini_int("limits", "browses", BROWSES_MINUTE))
}

// lobby_permit is the shared per-host sliding-minute limiter.
func lobby_permit(table map[string][]time.Time, r *http.Request, limit int) bool {
	host := origin(r.RemoteAddr)
	creates_lock.Lock()
	defer creates_lock.Unlock()
	recent := []time.Time{}
	for _, t := range table[host] {
		if time.Since(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		table[host] = recent
		return false
	}
	table[host] = append(recent, time.Now())
	return true
}

// lobby_prune_manager drops limiter entries no longer inside anybody's window.
// It runs on its own clock because the prune it replaces ran inline, after
// insertion, whenever a table passed 10000 entries: a 10000-entry walk on every
// lobby request from anybody, holding the mutex all four budgets share, for as
// long as one source kept that many keys warm. A source holding a /48 has 65536
// distinct /64 keys and only has to touch each once a minute.
func lobby_prune_manager() {
	for range time.Tick(time.Minute) {
		lobby_prune()
	}
}

// lobby_prune drops every limiter entry whose newest timestamp has left the
// window. One pass, so it is also what a test drives.
func lobby_prune() {
	creates_lock.Lock()
	defer creates_lock.Unlock()
	for _, table := range []map[string][]time.Time{creates, plays, says, retires, browses} {
		for host, list := range table {
			if len(list) == 0 || time.Since(list[len(list)-1]) >= time.Minute {
				delete(table, host)
			}
		}
	}
}

// transport_origin is the game transport's base URL as the caller of r should
// dial it: [transport] address when set. Unset - the packaged default - it is
// the host the caller reached the lobby at, on the transport's port: the answer
// goes only to that caller, and a pinned ephemeral certificate is matched by
// its hash, not its name. The fixed https://127.0.0.1 it used to be sent every
// remote player to their own machine.
func transport_origin(r *http.Request) string {
	if address := ini_string("transport", "address", ""); address != "" {
		return strings.TrimSuffix(address, "/")
	}
	host := "127.0.0.1"
	if r != nil {
		name := r.Host
		if bare, _, err := net.SplitHostPort(r.Host); err == nil {
			name = bare
		}
		name = strings.Trim(name, "[]")
		if net.ParseIP(name) != nil || match_hostname.MatchString(name) {
			host = name
		}
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(ini_int("transport", "port", 4433)))
}

// match_hostname is what a Host header may name for transport_origin to put it
// in an address: a DNS name, nothing that could carry a path or a port.
var match_hostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})$`)

// transport_address is the WebTransport URL advertised to the caller of r.
func transport_address(r *http.Request) string {
	return transport_origin(r) + "/play"
}

// lobby_origin is the lobby's public base URL, the address the listing
// publishes: a join page fetches its /status and dials the transport address
// that answer names. [lobby] address when set; otherwise [transport] address,
// which is the lobby's too while the two listen on one port number - the
// default - and "" when they do not, since nothing then says where the lobby
// is.
func lobby_origin() string {
	if address := ini_string("lobby", "address", ""); address != "" {
		return strings.TrimSuffix(address, "/")
	}
	if ini_int("lobby", "port", 4433) != ini_int("transport", "port", 4433) {
		return ""
	}
	return strings.TrimSuffix(ini_string("transport", "address", ""), "/")
}

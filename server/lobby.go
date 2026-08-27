// Mochi world: Lobby API (public HTTP: status, session list and create)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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

// CONNECTIONS_MAXIMUM caps how many connections either public listener holds
// open at once. The deadlines bound how long one connection occupies a
// goroutine, not how many exist. Shared by both listeners.
const CONNECTIONS_MAXIMUM = 512

// listener_limit caps accepted connections. Excess connections wait in the
// kernel's accept queue rather than being refused, and the cap is released as
// connections close. maximum is a parameter so a test can assert at a small
// size.
func listener_limit(listener net.Listener, maximum int) net.Listener {
	return netutil.LimitListener(listener, maximum)
}

// lobby_start serves the public lobby API. World servers are open: there is
// no authentication, only rate and capacity limits.
func lobby_start(fatal chan<- error) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", lobby_status)
	mux.HandleFunc("/sessions", lobby_sessions)
	mux.HandleFunc("/withdraw", lobby_withdraw)
	mux.HandleFunc("/chat", lobby_chat)
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
	listener = listener_limit(listener, CONNECTIONS_MAXIMUM)
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
	count, players := sessions_counts()
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
		"address":  transport_address(),
	}
	if hash, expires := certificate_hash(); hash != "" {
		body["certificate"] = map[string]any{"hash": hash, "expires": expires}
	}
	lobby_respond(w, http.StatusOK, body)
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
		// One offer at a time: a new match replaces whatever this pilot was
		// already offering, so the list can never fill with a single player's
		// abandoned matches.
		pilot := clean(request.Pilot, 64)
		sessions_withdraw(pilot)
		s, err := sessions_create(request.Game, request.Mode, request.Label, request.Capacity, request.Parameters)
		if err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		sessions_own(s, pilot)
		if name := clean(request.Name, 32); name != "" {
			chat_made(name, s.spec.Label) // the lobby's system line: who just made what (#84)
		}
		body := map[string]any{
			"session": s.identifier, "game": s.spec.Game, "mode": s.spec.Mode,
			"label": s.spec.Label, "capacity": s.spec.Capacity, "address": transport_address(),
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
		since := uint64(0)
		if cursor := r.URL.Query().Get("since"); cursor != "" {
			parsed, err := strconv.ParseUint(cursor, 10, 64)
			if err != nil {
				lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "since"})
				return
			}
			since = parsed
		}
		chat_lock.Lock()
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
		latest := chat_sequence
		chat_lock.Unlock()
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

// clean strips control characters and caps a user string at limit runes.
func clean(words string, limit int) string {
	words = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
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

// lobby_browse rate-limits the match-list read: it returns a body proportional
// to the whole server and answers every origin. The loosest of the four
// budgets, because this poll doubles as the offer heartbeat (#77).
var browses = map[string][]time.Time{}

func lobby_browse(r *http.Request) bool {
	return lobby_permit(browses, r, ini_int("limits", "browses", 120))
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
	if len(table) > 10000 { // prune the table itself under address churn
		for h, list := range table {
			if len(list) == 0 || time.Since(list[len(list)-1]) > time.Minute {
				delete(table, h)
			}
		}
	}
	return true
}

// transport_origin is the server's advertised base URL - what a player pastes
// or picks in a join page. The client adds the /play path itself.
func transport_origin() string {
	address := ini_string("transport", "address", "")
	if address == "" {
		address = fmt.Sprintf("https://127.0.0.1:%d", ini_int("transport", "port", 4433))
	}
	return strings.TrimSuffix(address, "/")
}

// transport_address is the WebTransport URL advertised to clients.
func transport_address() string {
	return transport_origin() + "/play"
}

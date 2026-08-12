// Mochi world: Transport and session tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"

	"strings"
	"unicode/utf8"
	"world/games/air"
	"world/games/echo"
)

const test_port = 19700

func TestMain(m *testing.M) {
	os.Setenv("MOCHI_TRANSPORT_LISTEN", "127.0.0.1")
	os.Setenv("MOCHI_TRANSPORT_PORT", fmt.Sprint(test_port))
	os.Setenv("MOCHI_LIMITS_IDLE", "1")
	games_register(echo.New())
	games_register(air.New())
	if err := certificate_start(); err != nil {
		panic(err)
	}
	fatal := make(chan error, 1)
	if err := transport_start(fatal); err != nil {
		panic(err)
	}
	time.Sleep(200 * time.Millisecond)
	os.Exit(m.Run())
}

// probe is a minimal test client over one WebTransport connection.
type probe struct {
	session *webtransport.Session
	stream  *webtransport.Stream
}

func dial(t *testing.T) *probe {
	t.Helper()
	dialer := webtransport.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}}
	background, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, session, err := dialer.Dial(background, fmt.Sprintf("https://127.0.0.1:%d/play", test_port), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	stream, err := session.OpenStreamSync(background)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return &probe{session: session, stream: stream}
}

func (p *probe) send(t *testing.T, message map[string]any) {
	t.Helper()
	payload, err := encode(message)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	if _, err := p.stream.Write(append(header, payload...)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (p *probe) receive(t *testing.T) map[string]any {
	t.Helper()
	header := make([]byte, 4)
	if _, err := io.ReadFull(p.stream, header); err != nil {
		t.Fatalf("read header: %v", err)
	}
	payload := make([]byte, binary.BigEndian.Uint32(header))
	if _, err := io.ReadFull(p.stream, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	message, err := decode(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return message
}

func TestEcho(t *testing.T) {
	s, err := sessions_create("echo", "test", "echo test", 4, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	p := dial(t)
	defer p.session.CloseWithError(0, "done")
	p.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "probe", "protocol": protocol})
	welcome := p.receive(t)
	if text(welcome, "kind") != "welcome" {
		t.Fatalf("expected welcome, got %v", welcome)
	}
	slot := int(number(welcome, "slot"))

	// Send an input datagram and expect it echoed in a snapshot datagram.
	input, _ := encode(map[string]any{"kind": "input", "inputs": []map[string]any{{"sequence": 1, "value": 42}}})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.session.SendDatagram(input)
		background, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		payload, err := p.session.ReceiveDatagram(background)
		cancel()
		if err != nil {
			continue
		}
		message, err := decode(payload)
		if err != nil || text(message, "kind") != "snapshot" {
			continue
		}
		if number(message, "acknowledged") != 1 {
			continue
		}
		players, _ := message["players"].([]any)
		for _, item := range players {
			entry, _ := item.(map[string]any)
			if entry == nil || int(number(entry, "slot")) != slot {
				continue
			}
			data, _ := entry["echo"].(map[string]any)
			if data != nil && number(data, "value") == 42 {
				return // echoed back — the whole pipeline works
			}
		}
	}
	t.Fatal("input was never echoed in a snapshot")
}

func TestRefuse(t *testing.T) {
	p := dial(t)
	defer p.session.CloseWithError(0, "done")
	p.send(t, map[string]any{"kind": "join", "session": "missing", "name": "probe", "protocol": protocol})
	refuse := p.receive(t)
	if text(refuse, "kind") != "refuse" || text(refuse, "reason") != "unknown" {
		t.Fatalf("expected refuse/unknown, got %v", refuse)
	}
}

func TestSweep(t *testing.T) {
	s, err := sessions_create("echo", "test", "sweep test", 4, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sessions_get(s.identifier) == nil {
			return // idle sweep ended it (MOCHI_LIMITS_IDLE=1s)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("idle session was not swept")
}

// air_position reads the recipient's own position from a snapshot's core
// payload — the first three of the 57 full-precision float64 wire words
// (flight.State.Encode order: position, velocity, attitude, ...).
func air_position(message map[string]any) ([]float64, bool) {
	core, _ := message["core"].([]byte)
	if len(core) < 24 {
		return nil, false
	}
	position := make([]float64, 3)
	for i := range position {
		position[i] = math.Float64frombits(binary.LittleEndian.Uint64(core[i*8:]))
	}
	return position, true
}

// TestAir joins an air session and expects the authoritative aircraft
// to fly: consecutive snapshots must show the spawn position advancing.
// The open furball mode flies a lone jet immediately (a joust would hold
// the first joiner frozen in the waiting room). Receiving snapshots at
// all doubles as the oversized-datagram guard: SendDatagram drops frames
// past the MTU silently, so wire growth reads as zero snapshots here.
func TestAir(t *testing.T) {
	s, err := sessions_create("air", "furball", "test flight", 4, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	p := dial(t)
	defer p.session.CloseWithError(0, "done")
	p.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "probe", "protocol": protocol})
	welcome := p.receive(t)
	if text(welcome, "kind") != "welcome" {
		t.Fatalf("expected welcome, got %v", welcome)
	}
	spawn, _ := welcome["spawn"].(map[string]any)
	state, _ := spawn["state"].(map[string]any)
	if state == nil {
		t.Fatalf("welcome carries no spawn state: %v", welcome)
	}
	positions := [][]float64{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(positions) < 2 {
		background, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		payload, err := p.session.ReceiveDatagram(background)
		cancel()
		if err != nil {
			continue
		}
		message, err := decode(payload)
		if err != nil || text(message, "kind") != "snapshot" {
			continue
		}
		if position, found := air_position(message); found {
			positions = append(positions, position)
		}
	}
	if len(positions) < 2 {
		t.Fatal("no snapshots with our aircraft")
	}
	dx := positions[1][0] - positions[0][0]
	dz := positions[1][2] - positions[0][2]
	if dx*dx+dz*dz < 1 {
		t.Fatalf("aircraft not moving: %v -> %v", positions[0], positions[1])
	}
}

// TestPair joins two players and expects each to appear in the other's
// poses — the headless stand-in for the two-browser test, and the guard
// this suite keeps against snapshot datagrams outgrowing the QUIC MTU
// (SendDatagram drops oversized frames silently). Poses ride their own
// datagram as 35-byte records with the slot in byte 0, self first.
func TestPair(t *testing.T) {
	s, err := sessions_create("air", "joust", "pair test", 4, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a, b := dial(t), dial(t)
	defer a.session.CloseWithError(0, "done")
	defer b.session.CloseWithError(0, "done")
	a.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "alpha", "protocol": protocol})
	first := a.receive(t)
	if text(first, "kind") != "welcome" {
		t.Fatal("alpha not welcomed")
	}
	b.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "bravo", "protocol": protocol})
	second := b.receive(t)
	if text(second, "kind") != "welcome" {
		t.Fatal("bravo not welcomed")
	}
	mine, theirs := int(number(first, "slot")), int(number(second, "slot"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		background, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		payload, err := a.session.ReceiveDatagram(background)
		cancel()
		if err != nil {
			continue
		}
		message, err := decode(payload)
		if err != nil || text(message, "kind") != "poses" {
			continue
		}
		blob, _ := message["blob"].([]byte)
		slots := map[int]bool{}
		for at := 0; at+35 <= len(blob); at += 35 {
			slots[int(blob[at])] = true
		}
		if slots[mine] && slots[theirs] {
			return // both aircraft in one poses frame
		}
	}
	t.Fatal("the two players never shared a snapshot")
}

// TestStanding expects the permanent match to exist, be listed first, and
// survive the idle sweep that ends ordinary empty sessions.
func TestStanding(t *testing.T) {
	sessions_standing() // default: one standing session per game except echo
	list := sessions_list("air", "")
	var standing string
	for _, entry := range list {
		if entry["permanent"] == true {
			standing = entry["session"].(string)
			if entry["label"] != "Furball" {
				t.Fatalf("standing session label %v, want Furball (the free-for-all mode's name, not the game's)", entry["label"])
			}
		}
	}
	if standing == "" {
		t.Fatal("no standing air session")
	}
	if list[0]["permanent"] != true {
		t.Fatal("standing session not listed first")
	}
	time.Sleep(2500 * time.Millisecond) // MOCHI_LIMITS_IDLE=1s ends ordinary empty sessions
	if sessions_get(standing) == nil {
		t.Fatal("standing session was swept")
	}
}

// overheard drains a probe's reliable stream until the deadline, collecting the
// text of every chat event and discarding everything else.
func overheard(p *probe, wait time.Duration) []string {
	deadline := time.Now().Add(wait)
	texts := []string{}
	for {
		p.stream.SetReadDeadline(deadline)
		header := make([]byte, 4)
		if _, err := io.ReadFull(p.stream, header); err != nil {
			break
		}
		payload := make([]byte, binary.BigEndian.Uint32(header))
		if _, err := io.ReadFull(p.stream, payload); err != nil {
			break
		}
		message, err := decode(payload)
		if err != nil {
			continue
		}
		event, _ := message["event"].(map[string]any)
		if text(message, "kind") == "event" && text(event, "kind") == "chat" {
			texts = append(texts, text(event, "text"))
		}
	}
	p.stream.SetReadDeadline(time.Time{})
	return texts
}

// TestChat (#84): match chat relays with SERVER-side team scoping, control
// characters are stripped, the sender receives their own echo, floods are
// dropped, and a late joiner gets the replayed conversation - but only the
// lines addressed to their side.
func TestChat(t *testing.T) {
	s, err := sessions_create("air", "teams", "chat test", 8, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a, b, c := dial(t), dial(t), dial(t)
	defer a.session.CloseWithError(0, "done")
	defer b.session.CloseWithError(0, "done")
	defer c.session.CloseWithError(0, "done")
	for _, join := range []struct {
		p    *probe
		name string
		team string
	}{{a, "alpha", "red"}, {b, "bravo", "red"}, {c, "charlie", "blue"}} {
		join.p.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": join.name, "team": join.team, "protocol": protocol})
		if text(join.p.receive(t), "kind") != "welcome" {
			t.Fatalf("%s not welcomed", join.name)
		}
	}
	a.send(t, map[string]any{"kind": "chat", "text": " tally\x01 two ", "scope": "all"})
	a.send(t, map[string]any{"kind": "chat", "text": "push east", "scope": "team"})
	for n := 1; n <= 5; n++ {
		a.send(t, map[string]any{"kind": "chat", "text": fmt.Sprintf("flood %d", n), "scope": "team"})
	}
	heard := overheard(b, 1200*time.Millisecond)
	if len(heard) != 3 || heard[0] != "tally two" || heard[1] != "push east" || heard[2] != "flood 1" {
		t.Fatalf("bravo heard %v, want the sanitized all-call, the team call, and ONE flood line", heard)
	}
	if echo := overheard(a, 800*time.Millisecond); len(echo) != 3 {
		t.Fatalf("alpha's echo carried %d lines, want 3", len(echo))
	}
	if heard := overheard(c, 800*time.Millisecond); len(heard) != 1 || heard[0] != "tally two" {
		t.Fatalf("charlie (blue) heard %v, want only the all-call", heard)
	}
	d := dial(t)
	defer d.session.CloseWithError(0, "done")
	d.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "delta", "team": "red", "protocol": protocol})
	if text(d.receive(t), "kind") != "welcome" {
		t.Fatal("delta not welcomed")
	}
	if replay := overheard(d, 1200*time.Millisecond); len(replay) != 3 {
		t.Fatalf("delta's replay carried %v, want the full red-visible conversation", replay)
	}
}

// TestLobbyChat (#84): the server-wide lobby ring — post, cursor reads,
// sanitization, the structured made event, and the chat budget being
// separate from the match-creation budget.
func TestLobbyChat(t *testing.T) {
	post := func(name string, words string) int {
		body, _ := json.Marshal(map[string]any{"name": name, "text": words})
		r := httptest.NewRequest("POST", "/chat", bytes.NewReader(body))
		r.RemoteAddr = "192.0.2.9:1"
		w := httptest.NewRecorder()
		lobby_chat(w, r)
		return w.Code
	}
	read := func(since uint64) ([]any, uint64) {
		r := httptest.NewRequest("GET", fmt.Sprintf("/chat?since=%d", since), nil)
		w := httptest.NewRecorder()
		lobby_chat(w, r)
		var reply map[string]any
		if err := json.NewDecoder(w.Body).Decode(&reply); err != nil {
			t.Fatalf("read: %v", err)
		}
		lines, _ := reply["lines"].([]any)
		return lines, uint64(number(reply, "sequence"))
	}
	_, cursor := read(0)
	if code := post(" alpha ", " hello there "); code != 200 {
		t.Fatalf("post refused: %d", code)
	}
	chat_made("bravo", "bravo's match")
	lines, latest := read(cursor)
	if len(lines) != 2 {
		t.Fatalf("read %d lines after the cursor, want 2", len(lines))
	}
	first, _ := lines[0].(map[string]any)
	if text(first, "name") != "alpha" || text(first, "text") != "hello there" {
		t.Fatalf("line 1 = %v, want sanitized alpha/hello there", first)
	}
	second, _ := lines[1].(map[string]any)
	if text(second, "event") != "made" || text(second, "name") != "bravo" || text(second, "label") != "bravo's match" {
		t.Fatalf("line 2 = %v, want the made event", second)
	}
	if again, _ := read(latest); len(again) != 0 {
		t.Fatalf("cursor at head still returned %d lines", len(again))
	}
	// The voice budget: 20 per minute per host — and spending it must leave
	// the CREATE budget untouched.
	refused := 0
	for n := 0; n < 25; n++ {
		if post("alpha", fmt.Sprintf("line %d", n)) == 429 {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no flood refusals: the voice limiter is not engaged")
	}
	made, _ := json.Marshal(map[string]any{"game": "air", "mode": "furball", "label": "after the flood", "name": "alpha"})
	r := httptest.NewRequest("POST", "/sessions", bytes.NewReader(made))
	r.RemoteAddr = "192.0.2.9:1"
	w := httptest.NewRecorder()
	lobby_sessions(w, r)
	if w.Code != 200 {
		t.Fatalf("match creation refused (%d) after chat flood: the budgets are shared", w.Code)
	}
}

// TestStaleOffer pins the sweep that retires abandoned offers — now on its
// own 1 Hz clock (sessions_stale_manager) rather than every session tick, so
// this drives one sweep synchronously and checks every category: only an
// unjoined, impermanent, past-grace offer is flagged.
func TestStaleOffer(t *testing.T) {
	past := time.Now().Add(-2 * OFFER_GRACE)
	stale := time.Now().Add(-2 * ORPHAN_GRACE)
	cases := []*session{
		{identifier: "stale-flags", owner: "a", offered: past, created: time.Now()},
		{identifier: "stale-fresh", owner: "b", offered: time.Now(), created: time.Now()},
		{identifier: "stale-joined", owner: "c", offered: past, joined: true, created: stale},
		{identifier: "stale-permanent", owner: "d", offered: past, permanent: true, created: stale},
		// An ownerless session is not the OWNER rule's business however old it
		// is — that rule is about a creator who stopped heartbeating, and this
		// one never had a creator to lose. It is reaped instead by the orphan
		// rule below, on age since creation, so that omitting "pilot" cannot
		// buy a longer life than supplying it (the DoS this pins).
		{identifier: "stale-ownerless", offered: past, created: time.Now()},
	}
	orphan := &session{identifier: "stale-orphan", offered: past, created: stale}
	cases = append(cases, orphan)
	sessions_lock.Lock()
	for _, s := range cases {
		sessions[s.identifier] = s
	}
	sessions_lock.Unlock()
	defer func() {
		sessions_lock.Lock()
		for _, s := range cases {
			delete(sessions, s.identifier)
		}
		sessions_lock.Unlock()
	}()

	sessions_stale()

	sessions_lock.RLock()
	defer sessions_lock.RUnlock()
	if !cases[0].withdrawn {
		t.Error("a past-grace offer was not flagged")
	}
	if !orphan.withdrawn {
		t.Error("an ownerless session past the orphan grace was not flagged: omitting the pilot must not outlive supplying it")
	}
	for _, s := range cases[1 : len(cases)-1] {
		if s.withdrawn {
			t.Errorf("%s was flagged and should not be", s.identifier)
		}
	}
}

// TestListingRulesCopied pins the creation-time copy of the advertised rules
// (#21): spec.Parameters is the same map object the game instance holds, so
// the listing must carry a snapshot, not a live read. A game (or anyone
// holding the request map) mutating parameters after Create must not change
// what the lobby advertises — and above all must not be able to RACE it,
// which the companion race exercise below drives under -race.
func TestListingRulesCopied(t *testing.T) {
	parameters := map[string]any{
		"missiles": true,
		"cheats":   map[string]any{"fuel": true},
		"bots":     map[string]any{"ace": 1.0}, // creator-internal: must never be listed
	}
	s, err := sessions_create("echo", "test", "rules copy", 2, parameters)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sessions_lock.Lock()
		delete(sessions, s.identifier)
		sessions_lock.Unlock()
	}()

	// The hostile future: a writer mutating the original map after Create.
	parameters["missiles"] = false
	parameters["cheats"].(map[string]any)["fuel"] = false
	parameters["tod"] = "night"

	listed := func() map[string]any {
		for _, row := range sessions_list("echo", "") {
			if row["session"] == s.identifier {
				return row["parameters"].(map[string]any)
			}
		}
		t.Fatal("session not listed")
		return nil
	}
	rules := listed()
	if rules["missiles"] != true {
		t.Error("listing followed a post-create write to missiles")
	}
	if rules["cheats"].(map[string]any)["fuel"] != true {
		t.Error("listing followed a post-create write inside the nested cheats map — the copy is not deep")
	}
	if _, found := rules["tod"]; found {
		t.Error("listing picked up a key added after creation")
	}
	if _, found := rules["bots"]; found {
		t.Error("creator-internal parameters leaked into the listing")
	}

	// The race the copy exists to prevent: hammer writes into the shared map
	// while the lobby lists. Run under -race this fails on the pre-copy code
	// (sessions_list read spec.Parameters live) and passes now, because the
	// listing no longer touches the map at all.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			parameters["missiles"] = i%2 == 0
		}
	}()
	for i := 0; i < 100; i++ {
		listed()
	}
	<-done
}

// TestCreateSanitizes pins the input hygiene of the open lobby: everything a
// creator sends that comes back out of the public listing goes through
// clean(). Mode used to pass through raw and Label was byte-sliced — control
// characters reached every client's match-list poll, and the slice could
// split a multi-byte rune at byte 64, emitting invalid UTF-8.
func TestCreateSanitizes(t *testing.T) {
	// Label: 63 ASCII bytes then a multi-byte rune, so a byte slice at 64
	// would cut the rune in half; plus control characters that must vanish.
	label := strings.Repeat("x", 63) + "\u00e9\x1b[31m\ntrailing"
	made, _ := json.Marshal(map[string]any{
		"game":  "echo",
		"mode":  "furball\r\nINJECTED\x1b[2J" + strings.Repeat("m", 500),
		"label": label,
		"pilot": "sanitize-test-pilot",
	})
	r := httptest.NewRequest("POST", "/sessions", bytes.NewReader(made))
	r.RemoteAddr = "192.0.2.77:1"
	w := httptest.NewRecorder()
	lobby_sessions(w, r)
	if w.Code != 200 {
		t.Fatalf("create refused: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Session string `json:"session"`
		Mode    string `json:"mode"`
		Label   string `json:"label"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	defer func() {
		sessions_lock.Lock()
		if s := sessions[response.Session]; s != nil {
			delete(sessions, response.Session)
		}
		sessions_lock.Unlock()
	}()

	for name, value := range map[string]string{"mode": response.Mode, "label": response.Label} {
		for _, r := range value {
			if r < 32 || r == 127 {
				t.Errorf("%s kept a control character: %q", name, value)
				break
			}
		}
		if !utf8.ValidString(value) {
			t.Errorf("%s is not valid UTF-8: %q", name, value)
		}
	}
	if runes := []rune(response.Mode); len(runes) > 32 {
		t.Errorf("mode not capped: %d runes", len(runes))
	}
	if runes := []rune(response.Label); len(runes) > 64 {
		t.Errorf("label not capped: %d runes", len(runes))
	}
	// The é survived intact: rune truncation keeps it, a byte slice halved it.
	if !strings.HasSuffix(response.Label, "é") {
		t.Errorf("label lost its 64th rune to truncation: %q", response.Label)
	}
}

// TestHeartbeat pins the offer clock the match-list poll drives: a pilot's own
// poll refreshes their offer and nobody else's. A token holding no offer takes
// the cheap read-only path, and must still leave every clock alone.
func TestHeartbeat(t *testing.T) {
	stale := time.Now().Add(-2 * OFFER_GRACE)
	s := &session{identifier: "heartbeat-test", owner: "alpha-heartbeat", offered: stale}
	sessions_lock.Lock()
	sessions[s.identifier] = s
	sessions_lock.Unlock()
	defer func() {
		sessions_lock.Lock()
		delete(sessions, s.identifier)
		sessions_lock.Unlock()
	}()

	offered := func() time.Time {
		sessions_lock.RLock()
		defer sessions_lock.RUnlock()
		return s.offered
	}

	sessions_touch("bravo-heartbeat")
	if !offered().Equal(stale) {
		t.Error("another pilot's poll refreshed this offer")
	}

	sessions_touch("alpha-heartbeat")
	if !offered().After(stale) {
		t.Error("the owner's own poll did not refresh their offer")
	}

	// A joined match is no longer an offer, so its clock stops moving.
	sessions_lock.Lock()
	s.joined = true
	s.offered = stale
	sessions_lock.Unlock()
	sessions_touch("alpha-heartbeat")
	if !offered().Equal(stale) {
		t.Error("a joined match still tracks the offer heartbeat")
	}
}

// TestWithdrawBudget pins the withdrawal allowance. Withdrawing is
// unauthenticated like every other lobby write, so it carries its own per
// address budget — and spending it must leave match creation untouched.
func TestWithdrawBudget(t *testing.T) {
	withdraw := func() int {
		body, _ := json.Marshal(map[string]any{"pilot": "budget-pilot"})
		r := httptest.NewRequest("POST", "/withdraw", bytes.NewReader(body))
		r.RemoteAddr = "192.0.2.11:1"
		w := httptest.NewRecorder()
		lobby_withdraw(w, r)
		return w.Code
	}
	if code := withdraw(); code != 200 {
		t.Fatalf("first withdrawal refused: %d", code)
	}
	refused := 0
	for n := 0; n < 40; n++ {
		if withdraw() == 429 {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no refusals across 41 withdrawals: the limiter is not engaged")
	}

	made, _ := json.Marshal(map[string]any{"game": "air", "mode": "furball", "label": "after the withdrawals"})
	r := httptest.NewRequest("POST", "/sessions", bytes.NewReader(made))
	r.RemoteAddr = "192.0.2.11:1"
	w := httptest.NewRecorder()
	lobby_sessions(w, r)
	if w.Code != 200 {
		t.Fatalf("match creation refused (%d) after the withdrawal flood: the budgets are shared", w.Code)
	}
}

// TestOfferPrivacy pins that the pilot token stays private. It is the whole
// credential /withdraw and the heartbeat accept, and the lobby answers every
// origin, so a match list carrying it would let any reader — a web page
// included — retire anybody's offer. The listing reports only whether an offer
// belongs to the CALLER, matched against the token they already hold.
func TestOfferPrivacy(t *testing.T) {
	const alpha, bravo = "alpha-pilot-token", "bravo-pilot-token"

	made, _ := json.Marshal(map[string]any{"game": "air", "mode": "furball", "label": "alpha's offer", "pilot": alpha})
	r := httptest.NewRequest("POST", "/sessions", bytes.NewReader(made))
	r.RemoteAddr = "192.0.2.10:1"
	w := httptest.NewRecorder()
	lobby_sessions(w, r)
	if w.Code != 200 {
		t.Fatalf("match creation refused: %d", w.Code)
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("create reply: %v", err)
	}
	identifier := text(created, "session")
	if identifier == "" {
		t.Fatal("create returned no session identifier")
	}

	list := func(pilot string) []map[string]any {
		r := httptest.NewRequest("GET", "/sessions?game=air&pilot="+pilot, nil)
		w := httptest.NewRecorder()
		lobby_sessions(w, r)
		var reply map[string]any
		if err := json.NewDecoder(w.Body).Decode(&reply); err != nil {
			t.Fatalf("list: %v", err)
		}
		entries, _ := reply["sessions"].([]any)
		found := []map[string]any{}
		for _, e := range entries {
			if entry, ok := e.(map[string]any); ok {
				found = append(found, entry)
			}
		}
		if len(found) == 0 {
			t.Fatal("the match list came back empty: every assertion below would pass vacuously")
		}
		return found
	}

	mine := func(pilot string) bool {
		for _, entry := range list(pilot) {
			if text(entry, "session") == identifier {
				flag, _ := entry["mine"].(bool)
				return flag
			}
		}
		t.Fatalf("session %s is missing from the match list", identifier)
		return false
	}

	// The creator sees their own offer flagged. This is the positive control:
	// it proves the assertions below reach a real, matching session.
	if !mine(alpha) {
		t.Error("the creator's own offer is not flagged mine")
	}
	if mine(bravo) {
		t.Error("another pilot's offer is flagged mine")
	}
	if mine("") {
		t.Error("an anonymous poll flags an offer as mine")
	}

	// The token itself never rides the listing, under any key.
	for _, entry := range list(alpha) {
		if _, present := entry["owner"]; present {
			t.Error("the match list still carries an owner field")
		}
		for key, value := range entry {
			if word, ok := value.(string); ok && word == alpha {
				t.Errorf("the pilot token is published as %q", key)
			}
		}
	}
}

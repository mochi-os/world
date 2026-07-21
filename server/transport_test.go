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
	certificate_start()
	go transport_start()
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
// datagram as 34-byte records with the slot in byte 0, self first.
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
		for at := 0; at+34 <= len(blob); at += 34 {
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
	list := sessions_list("air")
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

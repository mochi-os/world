// Mochi world: inbound-budget, admission and lobby regression tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"world/game"

	"github.com/quic-go/webtransport-go"
)

// datagram_session hands out a fixed run of datagrams then blocks, so a test
// can drive wire.datagrams() without a network.
type datagram_session struct {
	mu        sync.Mutex
	remaining int
	size      int
	done      chan struct{}
	reason    string
}

func (d *datagram_session) SendDatagram([]byte) error { return nil }
func (d *datagram_session) ReceiveDatagram(context.Context) ([]byte, error) {
	d.mu.Lock()
	if d.remaining == 0 {
		d.mu.Unlock()
		<-d.done
		return nil, errors.New("closed")
	}
	d.remaining--
	d.mu.Unlock()
	return make([]byte, d.size), nil
}
func (d *datagram_session) CloseWithError(_ webtransport.SessionErrorCode, reason string) error {
	d.mu.Lock()
	d.reason = reason
	d.mu.Unlock()
	return nil
}

// TestDatagramsHoldTheByteBudget — datagrams were enqueued without admit()
// while read() released every payload from `queued`, so the counter drifted
// negative in proportion to the input traffic and the backlog cap stopped
// firing for the control stream too.
func TestDatagramsHoldTheByteBudget(t *testing.T) {
	const size = 1024
	session := &datagram_session{remaining: 4, size: size, done: make(chan struct{})}
	l := &wire{session: session, inbound: make(chan []byte, 8), closed: make(chan struct{})}
	go l.datagrams()

	for i := 0; i < 4; i++ {
		payload, err := l.read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if len(payload) != size {
			t.Fatalf("read %d bytes, want %d", len(payload), size)
		}
	}
	if held := l.queued.Load(); held != 0 {
		t.Errorf("%d bytes counted against the connection after four datagrams were enqueued and drained; "+
			"a negative figure means datagrams are released from the budget without ever being charged to it", held)
	}
	close(session.done)
	l.close("gone")
}

// TestDatagramsAreRateLimited — frames_minute bounded the control stream only,
// so the datagram path (the one carrying 60 Hz input) bought an unbounded CBOR
// decode: connection_read is one goroutine per connection looping read() then
// decode() with nothing in between.
func TestDatagramsAreRateLimited(t *testing.T) {
	session := &datagram_session{remaining: frames_minute + 1, size: 8, done: make(chan struct{})}
	l := &wire{session: session, inbound: make(chan []byte, 4), closed: make(chan struct{})}
	go l.datagrams()

	// Drained under a deadline. read() blocks between frames by design, so with
	// no budget in the path the loop never ends and never errors: counting
	// inside it turns a missing budget into a hung test instead of a failing one.
	drained := make(chan int, 1)
	go func() {
		count := 0
		for {
			if _, err := l.read(); err != nil {
				break
			}
			count++
		}
		drained <- count
	}()

	select {
	case count := <-drained:
		if count > frames_minute {
			t.Errorf("%d datagrams admitted before the close, over the %d budget", count, frames_minute)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was never closed: the datagram path has no rate budget")
	}
	if l.reason != "rate" {
		t.Errorf("closed as %q, want rate", l.reason)
	}
	close(session.done)
}

// TestDroppedDatagramReleasesItsBytes is the control for the two above: the
// flood path drops rather than blocks, and a dropped payload reaches no
// consumer, so read() will never release it. Leaving it charged is the same
// asymmetry pointing the other way.
func TestDroppedDatagramReleasesItsBytes(t *testing.T) {
	const size = 512
	session := &datagram_session{remaining: 3, size: size, done: make(chan struct{})}
	// A zero-length channel with no reader: every send hits the default drop.
	l := &wire{session: session, inbound: make(chan []byte), closed: make(chan struct{})}
	go l.datagrams()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		left := session.remaining
		session.mu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the last drop finish
	if held := l.queued.Load(); held != 0 {
		t.Errorf("%d bytes still counted after every datagram was dropped", held)
	}
	close(session.done)
	l.close("gone")
}

// TestTransportHoldRefusesWithoutConsuming — the budgets moved from the /play
// handler to the accept loop, where a peer that completes a QUIC handshake and
// never asks for /play is counted. A refusal must leave both counters where it
// found them or the ceiling ratchets down one connection at a time.
func TestTransportHoldRefusesWithoutConsuming(t *testing.T) {
	connections.Store(0)
	defer connections.Store(0)
	const address = "198.51.100.7:5555"

	for i := 0; i < HOST_CONNECTIONS_MAXIMUM; i++ {
		if !transport_hold(address) {
			t.Fatalf("refused hold %d, under the per-host cap of %d", i+1, HOST_CONNECTIONS_MAXIMUM)
		}
	}
	if transport_hold(address) {
		t.Fatal("admitted a hold past the per-host cap")
	}
	if got := connections.Load(); got != int64(HOST_CONNECTIONS_MAXIMUM) {
		t.Fatalf("global count %d after a per-host refusal, want %d: the refusal consumed a global slot",
			got, HOST_CONNECTIONS_MAXIMUM)
	}
	for i := 0; i < HOST_CONNECTIONS_MAXIMUM; i++ {
		transport_drop(address)
	}
	if got := connections.Load(); got != 0 {
		t.Fatalf("global count %d after releasing every hold, want 0", got)
	}
}

// closing_instance records the Close the server owes it.
type closing_instance struct {
	fake_instance
	closed int
}

func (c *closing_instance) Close() { c.closed++ }

// closing_game hands out one closing_instance, and holds Create until the test
// releases it. Create runs outside the registry lock precisely so a slow one
// does not stall the server, so blocking here is the real shape of the race the
// post-Create re-check guards - and it makes it deterministic rather than a
// sleep the creator usually wins.
type closing_game struct {
	instance *closing_instance
	entered  chan struct{}
	release  chan struct{}
}

func (c *closing_game) Name() string     { return "closing" }
func (c *closing_game) Rate() (int, int) { return 20, 10 }
func (c *closing_game) Create(game.Session) (game.Instance, error) {
	close(c.entered)
	<-c.release
	c.instance = &closing_instance{}
	return c.instance, nil
}

// TestFullSessionClosesTheInstance — Create runs outside the registry lock, so
// the capacity check is repeated under it. Returning from the losing re-check
// without Close left air's share of the server-wide practice-bot budget
// reserved for the life of the process, one lost race at a time.
func TestFullSessionClosesTheInstance(t *testing.T) {
	g := &closing_game{entered: make(chan struct{}), release: make(chan struct{})}
	games_register(g)
	t.Cleanup(func() { delete(games, g.Name()) })

	// Fill the registry to the limit so the post-Create re-check must lose.
	limit := ini_int("limits", "sessions", 100)
	sessions_lock.Lock()
	filler := []string{}
	for i := 0; len(sessions) < limit; i++ {
		id := "filler-" + strings.Repeat("x", i%8) + string(rune('a'+i%26)) + string(rune('0'+i%10))
		if _, seen := sessions[id]; seen {
			id += "-2"
		}
		sessions[id] = &session{identifier: id}
		filler = append(filler, id)
	}
	sessions_lock.Unlock()
	t.Cleanup(func() {
		sessions_lock.Lock()
		for _, id := range filler {
			delete(sessions, id)
		}
		sessions_lock.Unlock()
	})

	// The cheap pre-check also refuses at the limit, so make room for exactly
	// one creator, then take the slot back while it is inside Create - which is
	// what the re-check is there for.
	sessions_lock.Lock()
	spare := filler[len(filler)-1]
	held := sessions[spare]
	delete(sessions, spare)
	sessions_lock.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := sessions_make(g.Name(), "test", "Closing", 2, nil, false)
		done <- err
	}()

	<-g.entered // past the pre-check, inside Create, not yet holding the lock
	sessions_lock.Lock()
	sessions[spare] = held
	sessions_lock.Unlock()
	close(g.release)

	err := <-done
	if err == nil {
		t.Fatal("created past the limit: the post-Create re-check did not refuse")
	}
	if err.Error() != "full" {
		t.Fatalf("refused as %q, want full", err)
	}
	if g.instance == nil {
		t.Fatal("Create never ran")
	}
	if g.instance.closed != 1 {
		t.Errorf("instance closed %d times, want 1: the refused session dropped it, so whatever "+
			"Create reserved (air's practice-bot share) is spent until restart", g.instance.closed)
	}
}

// TestListingRefusesWithoutATransportAddress — transport_origin falls back to
// https://127.0.0.1:<port>, which is right for a client on this machine and
// wrong for the address gossiped to the network. Core accepts it, so this is
// the only place that catches it.
func TestListingRefusesWithoutATransportAddress(t *testing.T) {
	os.Setenv("MOCHI_WORLD_PUBLIC", "true")
	os.Setenv("MOCHI_WORLD_NAME", "Test world")
	t.Cleanup(func() {
		os.Unsetenv("MOCHI_WORLD_PUBLIC")
		os.Unsetenv("MOCHI_WORLD_NAME")
		os.Unsetenv("MOCHI_TRANSPORT_ADDRESS")
	})

	if _, ok := listing_ready(); ok {
		t.Error("published with no [transport] address: the loopback fallback would reach the join list")
	}

	os.Setenv("MOCHI_TRANSPORT_ADDRESS", "https://example:4433")
	name, ok := listing_ready()
	if !ok || name != "Test world" {
		t.Errorf("listing_ready() = %q, %v with an address set; want the name and true", name, ok)
	}
}

// TestFailedCreateKeepsTheOffer — withdraw ran before create, so a "full" or
// "unknown" error replaced the pilot's live offer with nothing. The comment
// said replacement; the failure path did removal.
func TestFailedCreateKeepsTheOffer(t *testing.T) {
	const pilot = "test-pilot-offer"
	standing, err := sessions_make("air", "furball", "Standing", 2, nil, false)
	if err != nil {
		t.Fatalf("could not create the pilot's existing offer: %v", err)
	}
	t.Cleanup(func() {
		sessions_lock.Lock()
		delete(sessions, standing.identifier)
		sessions_lock.Unlock()
	})
	sessions_own(standing, pilot)

	creates = map[string][]time.Time{}
	body := `{"game":"nosuchgame","mode":"test","label":"x","pilot":"` + pilot + `"}`
	request := httptest.NewRequest("POST", "/sessions", strings.NewReader(body))
	request.RemoteAddr = "198.51.100.9:5555"
	recorder := httptest.NewRecorder()
	lobby_sessions(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("create of an unknown game answered %d, want 400", recorder.Code)
	}

	sessions_lock.RLock()
	withdrawn := standing.withdrawn
	sessions_lock.RUnlock()
	if withdrawn {
		t.Error("the pilot's existing offer was withdrawn by a create that then failed")
	}
}

// TestPruneDropsOnlyStaleHosts — the prune moved off the request path, where it
// walked up to 10000 entries under the mutex all four budgets share. It must
// still drop what has left the window, and keep what has not.
func TestPruneDropsOnlyStaleHosts(t *testing.T) {
	creates = map[string][]time.Time{
		"fresh": {time.Now()},
		"stale": {time.Now().Add(-2 * time.Minute)},
		"empty": {},
	}
	t.Cleanup(func() { creates = map[string][]time.Time{} })

	lobby_prune()

	creates_lock.Lock()
	defer creates_lock.Unlock()
	if _, ok := creates["fresh"]; !ok {
		t.Error("an entry inside the window was pruned")
	}
	if _, ok := creates["stale"]; ok {
		t.Error("an entry outside the window survived the prune")
	}
	if _, ok := creates["empty"]; ok {
		t.Error("an empty entry survived the prune")
	}
}

// TestCleanStripsFormatCharacters — only ASCII controls were removed, so the
// bidi overrides and the zero-width set reached every other player's roster,
// welcome payload and chat. Anyone may pick any name here by design; a name
// made visually identical to another player's is a different thing.
func TestCleanStripsFormatCharacters(t *testing.T) {
	for _, c := range []struct {
		name  string
		input string
		want  string
	}{
		{"right-to-left override", "ab\u202ecd", "abcd"},
		{"first strong isolate", "ab\u2068cd\u2069", "abcd"},
		{"zero-width space", "Al\u200bice", "Alice"},
		{"zero-width joiner", "Al\u200dice", "Alice"},
		{"byte order mark", "ab\ufeffcd", "abcd"},
		{"C1 control", "ab\u0085cd", "abcd"},
		{"ordinary text is kept", "\u00c5lesund \u2708 \u6771\u4eac", "\u00c5lesund \u2708 \u6771\u4eac"},
	} {
		if got := clean(c.input, 64); got != c.want {
			t.Errorf("%s: clean(%q) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

// TestMostSeedsFromTheFirstElement is in tools/loadtest; see main_test.go there.

// TestJoinWithoutProtocolIsRefused — the gate read `found &&`, so a client that
// simply omitted the field opted itself out of it and went on to decode this
// protocol's pose records against a different layout.
func TestJoinWithoutProtocolIsRefused(t *testing.T) {
	for _, c := range []struct {
		name    string
		message map[string]any
		refused bool
	}{
		{"omitted", map[string]any{"kind": "join", "name": "a"}, true},
		{"wrong", map[string]any{"kind": "join", "name": "a", "protocol": protocol + 1}, true},
		{"right", map[string]any{"kind": "join", "name": "a", "protocol": protocol}, false},
	} {
		frame, err := encode(c.message)
		if err != nil {
			t.Fatalf("%s: encode: %v", c.name, err)
		}
		l := &wire{
			session:  &record_session{closed: make(chan struct{})},
			stream:   &frame_stream{frames: [][]byte{frame}},
			inbound:  make(chan []byte, 4),
			outbound: make(chan []byte, 4),
			closed:   make(chan struct{}),
		}
		go l.streams()
		_, err = connection_join(l)
		if refused := err != nil; refused != c.refused {
			t.Errorf("%s protocol: refused=%v, want %v (err %v)", c.name, refused, c.refused, err)
		}
		l.close("gone")
	}
}

// TestJoinDuringSessionEndReportsEnded — the enqueue select watched s.done but
// the reply select did not, so a join sitting in the inbox when session_close
// ran was never answered: the client waited out the whole 5 s timer and was
// told "timeout" about a session that had simply gone.
func TestJoinDuringSessionEndReportsEnded(t *testing.T) {
	// A session whose tick loop never drains the inbox, then ends.
	s := &session{identifier: "ending-test", inbox: make(chan order, 4), done: make(chan struct{})}
	sessions_lock.Lock()
	sessions[s.identifier] = s
	sessions_lock.Unlock()
	t.Cleanup(func() {
		sessions_lock.Lock()
		delete(sessions, s.identifier)
		sessions_lock.Unlock()
	})

	message, err := encode(map[string]any{
		"kind": "join", "name": "pilot", "protocol": protocol, "session": s.identifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	l := &wire{
		session:  &record_session{closed: make(chan struct{})},
		stream:   &frame_stream{frames: [][]byte{message}},
		inbound:  make(chan []byte, 4),
		outbound: make(chan []byte, 4),
		closed:   make(chan struct{}),
	}
	go l.streams()

	served := make(chan struct{})
	go func() { connection_serve(l); close(served) }()

	time.Sleep(20 * time.Millisecond) // let the join reach the inbox
	close(s.done)

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("the join waited past the session ending; the reply select does not watch s.done")
	}
	if l.reason != "ended" {
		t.Errorf("refused as %q, want ended", l.reason)
	}
}

// frame_stream serves a fixed list of length-framed messages then blocks.
type frame_stream struct {
	frames  [][]byte
	pending []byte
	done    chan struct{}
	once    sync.Once
}

func (f *frame_stream) Read(b []byte) (int, error) {
	for len(f.pending) == 0 {
		if len(f.frames) == 0 {
			f.once.Do(func() { f.done = make(chan struct{}) })
			<-f.done
			return 0, errors.New("closed")
		}
		frame := f.frames[0]
		f.frames = f.frames[1:]
		header := []byte{byte(len(frame) >> 24), byte(len(frame) >> 16), byte(len(frame) >> 8), byte(len(frame))}
		f.pending = append(header, frame...)
	}
	n := copy(b, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

func (f *frame_stream) Write(b []byte) (int, error)              { return len(b), nil }
func (f *frame_stream) SetReadDeadline(time.Time) error          { return nil }
func (f *frame_stream) SetWriteDeadline(time.Time) error         { return nil }
func (f *frame_stream) CancelWrite(webtransport.StreamErrorCode) {}
func (f *frame_stream) Close() error                             { return nil }

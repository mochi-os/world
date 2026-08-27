// Mochi world: unauthenticated denial-of-service bounds
//
// Everything this server exposes is unauthenticated, so each public path needs
// its own bound: a connection that never speaks, a frame that decodes to
// unbounded elements, and one address holding every slot.
//
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// silent is a link that never delivers a message, like a peer that completes
// the transport handshake and then says nothing. close unblocks read, which is
// how the real wire behaves.
type silent struct {
	lock   sync.Mutex
	closed chan struct{}
	reason string
	once   sync.Once
}

func silent_link() *silent { return &silent{closed: make(chan struct{})} }

func (s *silent) read() ([]byte, error) {
	<-s.closed
	return nil, io.EOF
}

func (s *silent) write(bytes []byte, reliable bool) {}

func (s *silent) close(reason string) {
	s.once.Do(func() {
		s.lock.Lock()
		s.reason = reason
		s.lock.Unlock()
		close(s.closed)
	})
}

func (s *silent) why() string {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.reason
}

// TestHandshakeDeadlineClosesSilentConnection — the slot is reserved by
// transport_admit BEFORE the handshake, the liveness sweep only walks joined
// players, and the server's own keepalives stop QUIC idling the peer out. So a
// connection that never speaks held a slot for the life of the process.
func TestHandshakeDeadlineClosesSilentConnection(t *testing.T) {
	previous := HANDSHAKE_GRACE
	HANDSHAKE_GRACE = 50 * time.Millisecond
	defer func() { HANDSHAKE_GRACE = previous }()

	l := silent_link()
	done := make(chan struct{})
	go func() {
		connection_first(l)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a silent connection was never given up on; it would hold its slot forever")
	}
	if l.why() != "handshake" {
		t.Errorf("closed with %q, want \"handshake\"", l.why())
	}
}

// TestHandshakeAcceptsAPromptClient — the deadline must not break the normal
// case, which sends its join as soon as the stream opens.
func TestHandshakeAcceptsAPromptClient(t *testing.T) {
	l := &prompt{payload: []byte("hello"), closed: make(chan struct{})}
	bytes, err := connection_first(l)
	if err != nil {
		t.Fatalf("a prompt client was refused: %v", err)
	}
	if string(bytes) != "hello" {
		t.Errorf("read %q, want \"hello\"", bytes)
	}
}

type prompt struct {
	payload []byte
	closed  chan struct{}
	once    sync.Once
}

func (p *prompt) read() ([]byte, error)             { return p.payload, nil }
func (p *prompt) write(bytes []byte, reliable bool) {}
func (p *prompt) close(reason string)               { p.once.Do(func() { close(p.closed) }) }

// TestInputsElementCap — a 64 KB frame of empty maps decodes to tens of
// thousands of elements, each retained whole as Input.Data. The sibling
// connection_departures has always capped at nine stations, which is what makes
// the missing cap here an omission rather than a decision.
func TestInputsElementCap(t *testing.T) {
	within := make([]any, INPUTS_MAXIMUM)
	for i := range within {
		within[i] = map[string]any{"sequence": i}
	}
	if got := connection_inputs(map[string]any{"inputs": within}); len(got) != INPUTS_MAXIMUM {
		t.Errorf("a full but legal batch decoded to %d, want %d", len(got), INPUTS_MAXIMUM)
	}

	over := make([]any, INPUTS_MAXIMUM+1)
	for i := range over {
		over[i] = map[string]any{"sequence": i}
	}
	if got := connection_inputs(map[string]any{"inputs": over}); got != nil {
		t.Errorf("an oversized batch decoded to %d elements; the frame must be refused whole", len(got))
	}

	// The shape the flood actually takes: thousands of empty maps.
	flood := make([]any, 65000)
	for i := range flood {
		flood[i] = map[string]any{}
	}
	if got := connection_inputs(map[string]any{"inputs": flood}); got != nil {
		t.Errorf("a 65000-element frame decoded to %d elements", len(got))
	}
}

// TestHostConnectionCap — the sliding-minute limiter beside this bounds how
// FAST one address may connect, not how many it holds. Without a concurrent
// cap a single address could sit on every slot the global ceiling allows.
func TestHostConnectionCap(t *testing.T) {
	address := "203.0.113.7:40000"
	granted := 0
	for i := 0; i < HOST_CONNECTIONS_MAXIMUM+5; i++ {
		if transport_host_admit(address) {
			granted++
		}
	}
	defer func() {
		for i := 0; i < granted; i++ {
			transport_host_release(address)
		}
	}()
	if granted != HOST_CONNECTIONS_MAXIMUM {
		t.Errorf("one address held %d connections, want a ceiling of %d", granted, HOST_CONNECTIONS_MAXIMUM)
	}

	// A different address is unaffected: the cap is per host, not global.
	other := "198.51.100.9:40000"
	if !transport_host_admit(other) {
		t.Error("a second address was refused because the first was at its cap")
	}
	transport_host_release(other)
}

// TestHostConnectionReleaseForgetsTheAddress — the map must not retain an entry
// for every address that has ever connected, or the cap becomes its own leak.
func TestHostConnectionReleaseForgetsTheAddress(t *testing.T) {
	address := "192.0.2.5:1234"
	if !transport_host_admit(address) {
		t.Fatal("first connection refused")
	}
	transport_host_release(address)

	hosts_lock.Lock()
	_, present := hosts["192.0.2.5"]
	hosts_lock.Unlock()
	if present {
		t.Error("the address is still tracked at zero connections")
	}
}

// TestBrowseIsRateLimited — every WRITE here had a per-host budget and the READ
// had none, which is the wrong way round for the endpoint that returns a body
// proportional to the whole server and sets Access-Control-Allow-Origin: *.
func TestBrowseIsRateLimited(t *testing.T) {
	browses = map[string][]time.Time{}
	request := httptest.NewRequest("GET", "/sessions", nil)
	request.RemoteAddr = "203.0.113.99:5555"

	allowed := 0
	for i := 0; i < 500; i++ {
		if lobby_browse(request) {
			allowed++
		}
	}
	if allowed == 500 {
		t.Fatal("the match-list read accepted every request; it has no budget")
	}
	if allowed == 0 {
		t.Fatal("the match-list read refused everything; a polling player would be locked out")
	}
}

// canned is a link that delivers one prepared frame and then blocks until it
// is closed, recording whatever the server wrote back. It is the handshake's
// mirror image of silent: silent never speaks, canned speaks exactly once.
type canned struct {
	frame  []byte
	lock   sync.Mutex
	spoken bool
	closed chan struct{}
	once   sync.Once
	reason string
}

func canned_link(frame []byte) *canned { return &canned{frame: frame, closed: make(chan struct{})} }

func (c *canned) read() ([]byte, error) {
	c.lock.Lock()
	if !c.spoken {
		c.spoken = true
		frame := c.frame
		c.lock.Unlock()
		return frame, nil
	}
	c.lock.Unlock()
	<-c.closed
	return nil, io.EOF
}

func (c *canned) write(bytes []byte, reliable bool) {}

func (c *canned) close(reason string) {
	c.once.Do(func() {
		c.lock.Lock()
		c.reason = reason
		c.lock.Unlock()
		close(c.closed)
	})
}

func (c *canned) refusal() string {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.reason
}

// enrol registers a session under a throwaway identifier so connection_serve
// can find it, and removes it again afterwards.
func enrol(t *testing.T, s *session, identifier string) {
	t.Helper()
	s.identifier = identifier
	sessions_lock.Lock()
	sessions[identifier] = s
	sessions_lock.Unlock()
	t.Cleanup(func() {
		sessions_lock.Lock()
		delete(sessions, identifier)
		sessions_lock.Unlock()
	})
}

// TestJoinIdentityIsCappedAndCleaned — name and team were both capped and
// stripped at the door; identity was taken verbatim, and it is retained per
// player and re-serialised into the roster every later joiner receives.
func TestJoinIdentityIsCappedAndCleaned(t *testing.T) {
	s := bareSession(&fakeInstance{}, 4)
	s.inbox = make(chan order, 4)
	s.done = make(chan struct{})
	enrol(t, s, "identity-cap")

	hostile := strings.Repeat("A", 300) + "\x07\x00 tail"
	frame, err := encode(map[string]any{"kind": "join", "session": s.identifier, "name": "pilot", "identity": hostile})
	if err != nil {
		t.Fatal(err)
	}
	l := canned_link(frame)
	served := make(chan struct{})
	go func() { connection_serve(l); close(served) }()
	// The goroutine must be joined, not left running: it reads inbox_deadline,
	// which the next test writes.
	defer func() {
		l.close("test")
		select {
		case <-served:
		case <-time.After(3 * time.Second):
			t.Error("connection_serve did not return after the link closed")
		}
	}()

	var o order
	select {
	case o = <-s.inbox:
	case <-time.After(3 * time.Second):
		t.Fatal("no join order arrived")
	}
	o.reply <- answer{slot: 0}

	if runes := []rune(o.player.Identity); len(runes) != 64 {
		t.Errorf("identity kept %d runes of a 300-rune claim, want a 64-rune cap", len(runes))
	}
	if strings.ContainsAny(o.player.Identity, "\x00\x07") {
		t.Errorf("identity kept control characters: %q", o.player.Identity)
	}
}

// TestJoinRefusesRatherThanWaitOnAFullInbox — input, chat, jettison and radar
// all carry a `default: drop`, but join and leave selected only on the inbox
// and s.done, so a session whose tick goroutine has stopped draining parked
// every arriving connection's goroutine for the life of the process.
func TestJoinRefusesRatherThanWaitOnAFullInbox(t *testing.T) {
	previous := inbox_deadline
	inbox_deadline = 150 * time.Millisecond
	t.Cleanup(func() { inbox_deadline = previous })

	s := bareSession(&fakeInstance{}, 4)
	s.inbox = make(chan order) // nothing is draining it
	s.done = make(chan struct{})
	enrol(t, s, "wedged-inbox")

	frame, err := encode(map[string]any{"kind": "join", "session": s.identifier, "name": "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	l := canned_link(frame)
	done := make(chan struct{})
	go func() { connection_serve(l); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection_serve parked on a full inbox instead of refusing")
	}
	if reason := l.refusal(); reason != "busy" {
		t.Errorf("refused with %q, want busy", reason)
	}
}

// TestOriginKeysIPv6BySubnet — every per-address budget in the server counted
// against the literal address. A residential IPv6 allocation is a /64, so one
// subscriber had a free bypass for all of them: a fresh address per request.
func TestOriginKeysIPv6BySubnet(t *testing.T) {
	first := origin("[2001:db8:1:2::1]:40000")
	second := origin("[2001:db8:1:2:ffff:ffff:ffff:ffff]:40001")
	if first != second {
		t.Errorf("two addresses in one /64 keyed apart: %q and %q", first, second)
	}
	if other := origin("[2001:db8:1:3::1]:40000"); other == first {
		t.Errorf("a different /64 keyed the same as %q", first)
	}
	// The control: IPv4 has no such allocation, so it keeps the whole address.
	if got := origin("203.0.113.7:40000"); got != "203.0.113.7" {
		t.Errorf("IPv4 keyed as %q, want the whole address", got)
	}
	if got := origin("203.0.113.8:40000"); got == origin("203.0.113.7:40000") {
		t.Error("two IPv4 addresses were merged")
	}
}

// TestHostCapCoversAWholeIPv6Subnet is the functional half of the test above:
// the connection cap has to count the subnet, not the address.
func TestHostCapCoversAWholeIPv6Subnet(t *testing.T) {
	granted := 0
	for i := 0; i < HOST_CONNECTIONS_MAXIMUM+5; i++ {
		if transport_host_admit(fmt.Sprintf("[2001:db8:9:9::%x]:40000", i+1)) {
			granted++
		}
	}
	defer func() {
		for i := 0; i < granted; i++ {
			transport_host_release("[2001:db8:9:9::1]:40000")
		}
	}()
	if granted != HOST_CONNECTIONS_MAXIMUM {
		t.Errorf("one /64 held %d connections through fresh addresses, want a ceiling of %d", granted, HOST_CONNECTIONS_MAXIMUM)
	}
}

// TestFrameRateIsBounded — the per-kind limits (chat flood, jettison cooldown)
// all run AFTER the CBOR decode, and input and radar have none at all, so with
// no budget at the frame boundary one connection bought unbounded decoding.
func TestFrameRateIsBounded(t *testing.T) {
	l := &wire{}
	for i := 0; i < frames_minute; i++ {
		if reason := l.admit(1); reason != "" {
			t.Fatalf("frame %d of the budget was refused as %q", i+1, reason)
		}
	}
	if reason := l.admit(1); reason != "rate" {
		t.Errorf("the frame past the budget was admitted (%q)", reason)
	}
}

// TestQueueIsBoundedByBytes — the inbound channel is 256 deep and a frame may
// be frame_most, so a message count alone permitted 16 MiB in flight on one
// connection.
func TestQueueIsBoundedByBytes(t *testing.T) {
	l := &wire{}
	const frame = 64 << 10
	queued := 0
	for i := 0; i < 256; i++ {
		if reason := l.admit(frame); reason != "" {
			if reason != "backlog" {
				t.Fatalf("refused as %q, want backlog", reason)
			}
			break
		}
		queued++
	}
	if queued == 256 {
		t.Fatal("the whole 256-deep channel was admitted; the queue is still counted in messages")
	}
	if want := queue_bytes / frame; queued != want {
		t.Errorf("admitted %d frames of %d bytes, want %d", queued, frame, want)
	}
}

// TestQueueBudgetIsReleasedOnRead is the control for the test above: the
// budget must be a live measure of what is waiting, not a lifetime total, or
// a long-lived connection eventually refuses everything.
func TestQueueBudgetIsReleasedOnRead(t *testing.T) {
	l := &wire{inbound: make(chan []byte, 4), closed: make(chan struct{})}
	payload := make([]byte, 64<<10)
	if reason := l.admit(len(payload)); reason != "" {
		t.Fatalf("first frame refused as %q", reason)
	}
	l.inbound <- payload
	if _, err := l.read(); err != nil {
		t.Fatal(err)
	}
	if held := l.queued.Load(); held != 0 {
		t.Errorf("%d bytes still counted against the connection after the consumer drained them", held)
	}
}

// TestStatusIsRateLimited — /status walks every session under sessions_counts
// and answers any origin, so it needs the same budget as the other reads.
func TestStatusIsRateLimited(t *testing.T) {
	browses = map[string][]time.Time{}
	allowed := 0
	for i := 0; i < 500; i++ {
		request := httptest.NewRequest("GET", "/status", nil)
		request.RemoteAddr = "203.0.113.150:5555"
		recorder := httptest.NewRecorder()
		lobby_status(recorder, request)
		if recorder.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed == 500 {
		t.Fatal("/status accepted every request; it has no budget")
	}
	if allowed == 0 {
		t.Fatal("/status refused everything; a polling client would be locked out")
	}
}

// TestChatReadIsRateLimited — the GET copies up to 100 lines under chat_lock,
// contending with every poster. Only the POST was budgeted.
func TestChatReadIsRateLimited(t *testing.T) {
	browses = map[string][]time.Time{}
	allowed := 0
	for i := 0; i < 500; i++ {
		request := httptest.NewRequest("GET", "/chat", nil)
		request.RemoteAddr = "203.0.113.151:5555"
		recorder := httptest.NewRecorder()
		lobby_chat(recorder, request)
		if recorder.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed == 500 {
		t.Fatal("the chat read accepted every request; it has no budget")
	}
	if allowed == 0 {
		t.Fatal("the chat read refused everything")
	}
}

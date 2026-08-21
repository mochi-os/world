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
	"io"
	"net/http/httptest"
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

// Mochi world: partial-frame and connection-capacity limits
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
)

// partialStream hands over a fixed prefix and then stops, like a peer that
// starts a frame and never finishes it. With a read deadline set it reports the
// timeout immediately; with none it parks, which is what the test asserts on.
type partialStream struct {
	mu       sync.Mutex
	prefix   []byte
	deadline bool
	parked   bool
	done     chan struct{}
}

func (p *partialStream) Read(b []byte) (int, error) {
	p.mu.Lock()
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		p.mu.Unlock()
		return n, nil
	}
	deadline := p.deadline
	if !deadline {
		p.parked = true
	}
	p.mu.Unlock()
	if deadline {
		return 0, os.ErrDeadlineExceeded
	}
	<-p.done
	return 0, errors.New("closed")
}

func (p *partialStream) Write(b []byte) (int, error) { return len(b), nil }
func (p *partialStream) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	p.deadline = !t.IsZero()
	p.mu.Unlock()
	return nil
}
func (p *partialStream) SetWriteDeadline(time.Time) error         { return nil }
func (p *partialStream) CancelWrite(webtransport.StreamErrorCode) {}
func (p *partialStream) Close() error                             { return nil }

func (p *partialStream) wasParked() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.parked
}

func wireOn(s wireStream) *wire {
	return &wire{
		session:  &recordSession{closed: make(chan struct{})},
		stream:   s,
		inbound:  make(chan []byte, 4),
		outbound: make(chan []byte, 4),
		closed:   make(chan struct{}),
	}
}

// TestPartialFrameIsDropped: a peer that begins a length header and stops must
// be torn down. QUIC's idle timeout watches connection packets rather than
// stream progress, so acking keepalives alone parks the reader indefinitely.
func TestPartialFrameIsDropped(t *testing.T) {
	stream := &partialStream{prefix: []byte{0x00}, done: make(chan struct{})}
	l := wireOn(stream)
	finished := make(chan struct{})
	go func() { l.streams(); close(finished) }()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("reader still parked on a half-sent header")
	}
	if l.reason != "partial" {
		t.Fatalf("closed with %q, want \"partial\"", l.reason)
	}
	if stream.wasParked() {
		t.Fatal("the mid-frame read ran with no deadline set")
	}
}

// TestTruncatedPayloadIsDropped: the same guarantee once a length is announced.
// A header promising 65536 bytes followed by nothing is the cheapest version of
// this to mount.
func TestTruncatedPayloadIsDropped(t *testing.T) {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, frame_most)
	stream := &partialStream{prefix: header, done: make(chan struct{})}
	l := wireOn(stream)
	finished := make(chan struct{})
	go func() { l.streams(); close(finished) }()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("reader still parked on an undelivered payload")
	}
	if l.reason != "partial" {
		t.Fatalf("closed with %q, want \"partial\"", l.reason)
	}
}

// TestQuietBetweenFramesIsAllowed: the deadline must cover a frame in progress
// and nothing else. A player who sends one message and then says nothing for
// minutes is not misbehaving, and must not be disconnected.
func TestQuietBetweenFramesIsAllowed(t *testing.T) {
	body := []byte("hello")
	framed := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(framed, uint32(len(body)))
	copy(framed[4:], body)
	stream := &partialStream{prefix: framed, done: make(chan struct{})}
	l := wireOn(stream)
	go l.streams()

	select {
	case got := <-l.inbound:
		if string(got) != string(body) {
			t.Fatalf("payload %q, want %q", got, body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a complete frame was not delivered")
	}

	// The reader must now be waiting without a deadline, not tearing down.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-l.closed:
		t.Fatalf("closed with %q while merely idle between frames", l.reason)
	default:
	}
	if !stream.wasParked() {
		t.Fatal("the reader left a deadline set between frames")
	}
	close(stream.done)
}

// TestConnectionsCapped: the UDP path must enforce the same ceiling
// listener_limit gives the TCP listeners. The per-host rate limiter it already
// had bounds connection churn, not how many one peer holds open.
func TestConnectionsCapped(t *testing.T) {
	connections.Store(0)
	defer connections.Store(0)

	for i := 0; i < CONNECTIONS_MAXIMUM; i++ {
		if !transport_admit() {
			t.Fatalf("refused connection %d, under the cap of %d", i+1, CONNECTIONS_MAXIMUM)
		}
	}
	if transport_admit() {
		t.Fatalf("admitted connection %d, past the cap", CONNECTIONS_MAXIMUM+1)
	}
	// A refusal must not consume a slot, or the ceiling ratchets down.
	if got := connections.Load(); got != CONNECTIONS_MAXIMUM {
		t.Fatalf("count %d after a refusal, want %d", got, CONNECTIONS_MAXIMUM)
	}
	transport_release()
	if !transport_admit() {
		t.Fatal("a released slot was not reusable")
	}
}

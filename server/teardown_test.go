// Mochi world: slow-connection teardown and sequence rollover (Codex follow-up)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
)

// stallStream is a wireStream whose Write blocks (a peer that has stopped
// reading its stream, flow-controlled) until CancelWrite aborts it.
type stallStream struct {
	unblock   chan struct{}
	mu        sync.Mutex
	cancelled bool
	writes    int
}

func (s *stallStream) Read([]byte) (int, error) { <-s.unblock; return 0, errors.New("closed") }
func (s *stallStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	<-s.unblock // block like flow control until CancelWrite/Close releases us
	return 0, errors.New("write aborted")
}
func (s *stallStream) SetWriteDeadline(time.Time) error { return nil } // deadline irrelevant: CancelWrite is the unblock under test
func (s *stallStream) CancelWrite(webtransport.StreamErrorCode) {
	s.mu.Lock()
	if !s.cancelled {
		s.cancelled = true
		close(s.unblock)
	}
	s.mu.Unlock()
}
func (s *stallStream) Close() error { s.CancelWrite(0); return nil }

type recordSession struct {
	closed chan struct{}
	once   sync.Once
}

func (r *recordSession) SendDatagram([]byte) error { return nil }
func (r *recordSession) ReceiveDatagram(context.Context) ([]byte, error) {
	<-r.closed
	return nil, errors.New("closed")
}
func (r *recordSession) CloseWithError(webtransport.SessionErrorCode, string) error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

// TestSlowWriterTeardown: a writer blocked in send() on an unreadable stream
// must tear the session down promptly when the connection is classified slow —
// not strand the goroutine and QUIC session while keepalives hold it open.
func TestSlowWriterTeardown(t *testing.T) {
	stream := &stallStream{unblock: make(chan struct{})}
	session := &recordSession{closed: make(chan struct{})}
	l := &wire{session: session, stream: stream, outbound: make(chan []byte, 256), closed: make(chan struct{})}

	done := make(chan struct{})
	go func() { l.writer(); close(done) }()

	l.outbound <- []byte("x")          // one payload: the writer picks it up and blocks in Write
	time.Sleep(50 * time.Millisecond)  // let it reach the (blocked) send
	l.close("slow")                    // classify the connection slow — must abort, not drain

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not tear down within 2s of close(slow) — stranded on flow control")
	}
	select {
	case <-session.closed:
	default:
		t.Fatal("session was not closed on teardown")
	}
}

// TestSequenceWrap: the input-sequence comparison stays correct across the
// uint32 rollover a multi-year session reaches (serial-number arithmetic).
func TestSequenceWrap(t *testing.T) {
	cases := []struct {
		a, b uint32
		want bool
	}{
		{100, 50, true},
		{50, 100, false},
		{5, 5, false},
		{1, 0xFFFFFFFF, true},  // just past wrap is newer than just before it
		{0xFFFFFFFF, 1, false}, // and not the other way round
		{0, 0xFFFFFFF0, true},
	}
	for _, c := range cases {
		if got := after(c.a, c.b); got != c.want {
			t.Fatalf("after(%d, %d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

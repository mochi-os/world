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
	"sync/atomic"
	"testing"
	"time"

	"world/game"

	"github.com/quic-go/webtransport-go"
)

// stall_stream is a wire_stream whose Write blocks (a peer that has stopped
// reading its stream, flow-controlled) until CancelWrite aborts it.
type stall_stream struct {
	unblock   chan struct{}
	mu        sync.Mutex
	cancelled bool
	writes    int
}

func (s *stall_stream) Read([]byte) (int, error) { <-s.unblock; return 0, errors.New("closed") }
func (s *stall_stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	<-s.unblock // block like flow control until CancelWrite/Close releases us
	return 0, errors.New("write aborted")
}
func (s *stall_stream) SetReadDeadline(time.Time) error  { return nil } // this fake's Read blocks on unblock, not on time
func (s *stall_stream) SetWriteDeadline(time.Time) error { return nil } // deadline irrelevant: CancelWrite is the unblock under test
func (s *stall_stream) CancelWrite(webtransport.StreamErrorCode) {
	s.mu.Lock()
	if !s.cancelled {
		s.cancelled = true
		close(s.unblock)
	}
	s.mu.Unlock()
}
func (s *stall_stream) Close() error { s.CancelWrite(0); return nil }

type record_session struct {
	closed chan struct{}
	once   sync.Once
}

func (r *record_session) SendDatagram([]byte) error { return nil }
func (r *record_session) ReceiveDatagram(context.Context) ([]byte, error) {
	<-r.closed
	return nil, errors.New("closed")
}
func (r *record_session) CloseWithError(webtransport.SessionErrorCode, string) error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

// TestSlowWriterTeardown: a writer blocked in send() on an unreadable stream
// must tear the session down promptly when the connection is classified slow —
// not strand the goroutine and QUIC session while keepalives hold it open.
func TestSlowWriterTeardown(t *testing.T) {
	stream := &stall_stream{unblock: make(chan struct{})}
	session := &record_session{closed: make(chan struct{})}
	l := &wire{session: session, stream: stream, outbound: make(chan []byte, 256), closed: make(chan struct{})}

	done := make(chan struct{})
	go func() { l.writer(); close(done) }()

	l.outbound <- []byte("x")         // one payload: the writer picks it up and blocks in Write
	time.Sleep(50 * time.Millisecond) // let it reach the (blocked) send
	l.close("slow")                   // classify the connection slow — must abort, not drain

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

// jam_session is a wire_session whose SendDatagram blocks, as quic-go's does
// once 32 datagrams wait for a peer that has stopped acknowledging.
type jam_session struct {
	record_session
	release chan struct{}
	sent    atomic.Int32
}

func (j *jam_session) SendDatagram([]byte) error {
	j.sent.Add(1)
	<-j.release
	return nil
}

// TestBlockedDatagramSparesTheTick: snapshots went out with SendDatagram on the
// session's tick goroutine, so one peer that stopped acknowledging parked the
// tick - the whole match, for everyone - until its connection died. The tick
// must go on, and the jammed link must be closed as slow.
func TestBlockedDatagramSparesTheTick(t *testing.T) {
	saved := datagram_patience
	datagram_patience = 100 * time.Millisecond
	t.Cleanup(func() { datagram_patience = saved })
	session := &jam_session{record_session: record_session{closed: make(chan struct{})}, release: make(chan struct{})}
	t.Cleanup(func() { close(session.release) })
	l := &wire{session: session, stream: &stall_stream{unblock: make(chan struct{})}, datagram: make(chan []byte, datagram_depth), closed: make(chan struct{})}
	go l.sender()
	s := bare_session(&fake_instance{}, 4)
	s.players[0] = &player{Player: game.Player{Slot: 0}, link: l}

	ticked := make(chan struct{})
	go func() {
		for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			session_snapshot(s)
		}
		close(ticked)
	}()
	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("the tick parked on a datagram the peer will never take")
	}
	if session.sent.Load() != 1 {
		t.Errorf("%d datagrams reached the session, want the one the sender is stuck in", session.sent.Load())
	}
	if l.reason != "slow" {
		t.Errorf("a link jammed for the whole run closed as %q, want slow", l.reason)
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

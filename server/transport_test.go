// Mochi world: Transport and session tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"

	"world/games/echo"
	"world/games/furball"
)

const test_port = 19700

func TestMain(m *testing.M) {
	os.Setenv("MOCHI_TRANSPORT_LISTEN", "127.0.0.1")
	os.Setenv("MOCHI_TRANSPORT_PORT", fmt.Sprint(test_port))
	os.Setenv("MOCHI_LIMITS_IDLE", "1")
	games_register(echo.New())
	games_register(furball.New())
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

// TestFurball joins a furball session and expects the authoritative aircraft
// to fly: consecutive snapshots must show the spawn position advancing.
func TestFurball(t *testing.T) {
	s, err := sessions_create("furball", "joust", "test flight", 4, nil)
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
	slot := int(number(welcome, "slot"))
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
		players, _ := message["players"].([]any)
		for _, item := range players {
			entry, _ := item.(map[string]any)
			if entry == nil || int(number(entry, "slot")) != slot {
				continue
			}
			list, _ := entry["position"].([]any)
			if len(list) == 3 {
				positions = append(positions, []float64{number(map[string]any{"v": list[0]}, "v"), number(map[string]any{"v": list[1]}, "v"), number(map[string]any{"v": list[2]}, "v")})
			}
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
// snapshots — the headless stand-in for the two-browser test.
func TestPair(t *testing.T) {
	s, err := sessions_create("furball", "joust", "pair test", 4, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a, b := dial(t), dial(t)
	defer a.session.CloseWithError(0, "done")
	defer b.session.CloseWithError(0, "done")
	a.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "alpha", "protocol": protocol})
	if text(a.receive(t), "kind") != "welcome" {
		t.Fatal("alpha not welcomed")
	}
	b.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "bravo", "protocol": protocol})
	if text(b.receive(t), "kind") != "welcome" {
		t.Fatal("bravo not welcomed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		background, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		payload, err := a.session.ReceiveDatagram(background)
		cancel()
		if err != nil {
			continue
		}
		message, err := decode(payload)
		if err != nil || text(message, "kind") != "snapshot" {
			continue
		}
		names := map[string]bool{}
		players, _ := message["players"].([]any)
		for _, item := range players {
			entry, _ := item.(map[string]any)
			if entry != nil {
				names[text(entry, "name")] = true
			}
		}
		if names["alpha"] && names["bravo"] {
			return // both aircraft in one snapshot
		}
	}
	t.Fatal("the two players never shared a snapshot")
}

// TestStanding expects the permanent match to exist, be listed first, and
// survive the idle sweep that ends ordinary empty sessions.
func TestStanding(t *testing.T) {
	sessions_standing() // default: one standing session per game except echo
	list := sessions_list("furball")
	var standing string
	for _, entry := range list {
		if entry["permanent"] == true {
			standing = entry["session"].(string)
		}
	}
	if standing == "" {
		t.Fatal("no standing furball session")
	}
	if list[0]["permanent"] != true {
		t.Fatal("standing session not listed first")
	}
	time.Sleep(2500 * time.Millisecond) // MOCHI_LIMITS_IDLE=1s ends ordinary empty sessions
	if sessions_get(standing) == nil {
		t.Fatal("standing session was swept")
	}
}

// Mochi world: Player connections
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"errors"
	"time"

	"world/game"
)

// link is one player's transport connection, independent of the transport
// beneath it (WebTransport today; a WebSocket fallback would implement the
// same interface). read blocks for the next whole message; write sends one
// message reliably (control stream) or unreliably (datagram) and must not
// block the caller meaningfully; close tears the connection down.
type link interface {
	read() ([]byte, error)
	write(bytes []byte, reliable bool)
	close(reason string)
}

// connection_serve runs a player connection: join handshake, then the input
// read loop. Runs on its own goroutine per connection.
func connection_serve(l link) {
	message, err := connection_join(l)
	if err != nil {
		return
	}
	s := sessions_get(text(message, "session"))
	if s == nil {
		connection_refuse(l, "unknown")
		return
	}
	// The same sanitizer the lobby chat runs: control characters stripped and
	// the cap counted in RUNES — the old byte slice could split a multi-byte
	// sequence, and newlines rode straight into every broadcast event.
	name := clean(text(message, "name"), 32)
	if name == "" {
		name = "pilot"
	}
	team := clean(text(message, "team"), 16)
	stores, _ := message["stores"].(map[string]any)
	joiner := game.Player{Identity: text(message, "identity"), Name: name, Team: team, Stores: stores}
	reply := make(chan answer, 1)
	cancel := make(chan struct{})
	select {
	case s.inbox <- order{kind: "join", player: joiner, link: l, reply: reply, cancel: cancel}:
	case <-s.done:
		connection_refuse(l, "ended")
		return
	}
	var a answer
	select {
	case a = <-reply:
	case <-time.After(5 * time.Second):
		// The tick is stalled: give up, and tell session_join to roll back
		// rather than admit a player onto this now-abandoned link. Without
		// this the late join committed a permanent ghost — a nil-link entry
		// the sweep never deleted, still counted against capacity (#176).
		close(cancel)
		connection_refuse(l, "timeout")
		return
	}
	if a.err != nil {
		connection_refuse(l, a.err.Error())
		return
	}
	debug("session %s: %s joined slot %d", s.identifier, name, a.slot)
	connection_read(l, s, a.slot)
}

// HANDSHAKE_GRACE bounds how long a connection may hold a slot without saying
// anything. read() blocks on a channel with no deadline, and the slot is
// reserved by transport_admit BEFORE this runs, so a peer that completes the
// QUIC handshake and then sends nothing parked a goroutine and a slot forever:
// the liveness sweep only walks joined players, and the server's own 15 s
// keepalives stop QUIC idling the connection out. Filling CONNECTIONS_MAXIMUM
// that way costs an attacker nothing but open sockets.
//
// Ten seconds is far longer than a real client needs — it sends its join
// immediately after the stream opens — and short enough that the slot returns
// before a flood can accumulate.
// A var, not a const, only so a test can shorten it.
var HANDSHAKE_GRACE = 10 * time.Second

// connection_join reads and validates the first message.
func connection_join(l link) (map[string]any, error) {
	bytes, err := connection_first(l)
	if err != nil {
		return nil, err
	}
	message, err := decode(bytes)
	if err != nil || text(message, "kind") != "join" {
		connection_refuse(l, "protocol")
		return nil, errors.New("protocol") // a real error (decode succeeds on a non-join): nil let the caller fall through to a second "unknown session" refusal
	}
	if v, found := message["protocol"]; found && int(number(map[string]any{"v": v}, "v")) != protocol {
		connection_refuse(l, "protocol")
		return nil, errors.New("protocol")
	}
	return message, nil
}

// connection_read pumps inbound messages into the session until the
// connection drops or the session ends.
func connection_read(l link, s *session, slot int) {
	defer func() {
		select {
		case s.inbox <- order{kind: "leave", slot: slot}:
		case <-s.done:
		}
		l.close("gone")
	}()
	for {
		bytes, err := l.read()
		if err != nil {
			return
		}
		message, err := decode(bytes)
		if err != nil {
			continue
		}
		switch text(message, "kind") {
		case "input":
			inputs := connection_inputs(message)
			if len(inputs) == 0 {
				continue
			}
			select {
			case s.inbox <- order{kind: "input", slot: slot, inputs: inputs}:
			case <-s.done:
				return
			default: // inbox full: drop — newer inputs supersede anyway
			}
		case "chat":
			words := text(message, "text")
			if words == "" {
				continue
			}
			if len(words) > 400 {
				words = words[:400] // a hard byte cap at the door; the session trims to runes
			}
			select {
			case s.inbox <- order{kind: "chat", slot: slot, text: words, scope: text(message, "scope")}:
			case <-s.done:
				return
			default: // inbox full: chat loses to inputs
			}
		case "jettison":
			departures := connection_departures(message)
			if len(departures) == 0 {
				continue
			}
			select {
			case s.inbox <- order{kind: "jettison", slot: slot, departures: departures}:
			case <-s.done:
				return
			default: // inbox full: a lost jettison re-sends nothing — the client's next one supersedes
			}
		case "radar":
			mode := int(number(message, "mode"))
			target := int(number(message, "target"))
			if mode < 0 || mode > 2 {
				continue // the instance validates the target; a nonsense mode dies at the door
			}
			select {
			case s.inbox <- order{kind: "radar", slot: slot, mode: mode, target: target}:
			case <-s.done:
				return
			default: // inbox full: a lost report re-sends nothing — the client's next state change supersedes
			}
		case "leave":
			return
		}
	}
}

// connection_departures decodes a jettison's station list (#18), capped at
// the nine stations — the instance validates the semantics.
func connection_departures(message map[string]any) []game.Departure {
	list, found := message["stations"].([]any)
	if !found || len(list) > 9 {
		return nil
	}
	departures := []game.Departure{}
	for _, item := range list {
		data, found := item.(map[string]any)
		if !found {
			continue
		}
		departures = append(departures, game.Departure{Station: int(number(data, "station")), What: text(data, "what")})
	}
	return departures
}

// INPUTS_MAXIMUM caps the samples one frame may carry, the way
// connection_departures caps stations at nine. A client batches the samples it
// took since its last frame, so a handful is normal and a long stall might
// carry a few dozen; sixteen covers a quarter-second gap at 60 Hz.
//
// Without a cap the list length was bounded only by the 64 KB frame: a frame of
// empty CBOR maps decodes to tens of thousands of elements, and each one is
// retained whole as Input.Data, so a single frame turned into megabytes. The
// sibling below has always capped, which is what makes this an omission rather
// than a decision.
const INPUTS_MAXIMUM = 16

// connection_inputs decodes the batched input samples, oldest first.
func connection_inputs(message map[string]any) []game.Input {
	list, found := message["inputs"].([]any)
	if !found || len(list) > INPUTS_MAXIMUM {
		return nil
	}
	inputs := []game.Input{}
	for _, item := range list {
		data, found := item.(map[string]any)
		if !found {
			continue
		}
		inputs = append(inputs, game.Input{Sequence: uint32(number(data, "sequence")), Data: data})
	}
	return inputs
}

// connection_first reads the handshake message under HANDSHAKE_GRACE.
//
// The read runs on its own goroutine because link.read() cannot be cancelled:
// closing the link is what releases it (read selects on the closed channel), so
// the timeout path closes and the reader then returns EOF into a buffered
// channel nobody is waiting on. Buffered deliberately — an unbuffered send
// would leak the goroutine that lost the race.
func connection_first(l link) ([]byte, error) {
	type result struct {
		bytes []byte
		err   error
	}
	done := make(chan result, 1)
	go func() {
		bytes, err := l.read()
		done <- result{bytes: bytes, err: err}
	}()

	timer := time.NewTimer(HANDSHAKE_GRACE)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.bytes, r.err
	case <-timer.C:
		l.close("handshake")
		return nil, errors.New("handshake")
	}
}

func connection_refuse(l link, reason string) {
	bytes, err := encode(map[string]any{"kind": "refuse", "reason": reason})
	if err == nil {
		l.write(bytes, true)
	}
	l.close(reason)
}

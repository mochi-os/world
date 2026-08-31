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
// beneath it. read blocks for the next whole message; write sends one message
// reliably (control stream) or unreliably (datagram) and must not block.
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
	name := clean(text(message, "name"), 32)
	if name == "" {
		name = "pilot"
	}
	team := clean(text(message, "team"), 16)
	stores, _ := message["stores"].(map[string]any)
	// identity is retained per player and re-serialised into the welcome every
	// later joiner receives, so it is capped and stripped like name and team.
	identity := clean(text(message, "identity"), 64)
	joiner := game.Player{Identity: identity, Name: name, Team: team, Stores: stores}
	reply := make(chan answer, 1)
	cancel := make(chan struct{})
	select {
	case s.inbox <- order{kind: "join", player: joiner, link: l, reply: reply, cancel: cancel}:
	case <-s.done:
		connection_refuse(l, "ended")
		return
	case <-time.After(inbox_deadline):
		// input/chat/jettison/radar all carry a default: drop. join and leave
		// selected only on the inbox, so a flooded queue parked this goroutine.
		connection_refuse(l, "busy")
		return
	}
	var a answer
	select {
	case a = <-reply:
	case <-s.done:
		// The session ended while the join sat in the inbox. Nobody will answer
		// it, so without this the client waited out the whole timer and was
		// told "timeout" about a session that had simply gone.
		close(cancel)
		connection_refuse(l, "ended")
		return
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

// inbox_deadline bounds how long join and leave wait on a full session inbox.
// The other order kinds drop instead; these two must be delivered, but not at
// the cost of parking their goroutine on a queue that is not draining. One
// tick interval is ~16 ms, so this is many ticks of slack. A var, not a const,
// so a test can shorten it - the same reason HANDSHAKE_GRACE below is one.
var inbox_deadline = 2 * time.Second

// HANDSHAKE_GRACE bounds how long a connection may hold a slot without saying
// anything: the slot is reserved before the join arrives and the liveness sweep
// only walks joined players. A var, not a const, so a test can shorten it.
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
	// Not `if found &&`: a client that simply omits the field opted itself out
	// of the version gate, then decoded this protocol's pose records against a
	// different layout. A missing key reads as 0 and is refused.
	if int(number(message, "protocol")) != protocol {
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
		case s.inbox <- order{kind: "leave", slot: slot, link: l}:
		case <-s.done:
		case <-time.After(inbox_deadline):
			// The liveness sweep removes the player anyway; blocking here would
			// hold the reader goroutine on a queue it cannot drain.
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
			case s.inbox <- order{kind: "input", slot: slot, link: l, inputs: inputs}:
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
			case s.inbox <- order{kind: "chat", slot: slot, link: l, text: words, scope: text(message, "scope")}:
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
			case s.inbox <- order{kind: "jettison", slot: slot, link: l, departures: departures}:
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
			case s.inbox <- order{kind: "radar", slot: slot, link: l, mode: mode, target: target}:
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

// INPUTS_MAXIMUM caps the samples one frame may carry: sixteen covers a
// quarter-second gap at 60 Hz. Uncapped, the length is bounded only by the 64
// KB frame and each element is retained whole as Input.Data.
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

// connection_first reads the handshake message under HANDSHAKE_GRACE. The read
// runs on its own goroutine because link.read() cannot be cancelled - closing
// the link releases it - and the channel is buffered so the loser cannot leak.
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

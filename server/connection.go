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
	joiner := game.Player{Identity: text(message, "identity"), Name: name, Team: team}
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

// connection_join reads and validates the first message.
func connection_join(l link) (map[string]any, error) {
	bytes, err := l.read()
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
		case "leave":
			return
		}
	}
}

// connection_inputs decodes the batched input samples, oldest first.
func connection_inputs(message map[string]any) []game.Input {
	list, found := message["inputs"].([]any)
	if !found {
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

func connection_refuse(l link, reason string) {
	bytes, err := encode(map[string]any{"kind": "refuse", "reason": reason})
	if err == nil {
		l.write(bytes, true)
	}
	l.close(reason)
}

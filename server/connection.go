// Mochi world: Player connections
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
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
	name := text(message, "name")
	if name == "" {
		name = "pilot"
	}
	if len(name) > 32 {
		name = name[:32]
	}
	team := text(message, "team")
	if len(team) > 16 {
		team = team[:16]
	}
	joiner := game.Player{Identity: text(message, "identity"), Name: name, Team: team}
	reply := make(chan answer, 1)
	select {
	case s.inbox <- order{kind: "join", player: joiner, link: l, reply: reply}:
	case <-s.done:
		connection_refuse(l, "ended")
		return
	}
	var a answer
	select {
	case a = <-reply:
	case <-time.After(5 * time.Second):
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
		return nil, err
	}
	if v, found := message["protocol"]; found && int(number(map[string]any{"v": v}, "v")) != protocol {
		connection_refuse(l, "protocol")
		return nil, err
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

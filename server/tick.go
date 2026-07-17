// Mochi world: Session tick loop
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

// session_run is the session's tick goroutine: it owns the instance and the
// player set, drains orders from connections, steps the simulation at the
// game's fixed rate, and fans out snapshots and events.
func session_run(s *session, g game.Game) {
	tickrate, snaprate := g.Rate()
	if tickrate < 1 {
		tickrate = 60
	}
	every := tickrate / max(snaprate, 1)
	if every < 1 {
		every = 1
	}
	idle := time.Duration(ini_int("limits", "idle", 300)) * time.Second
	interval := time.Second / time.Duration(tickrate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previous := time.Now()
	behind := time.Duration(0)

	for {
		select {
		case <-shutdown:
			session_close(s, "shutdown")
			return
		case <-ticker.C:
			// Wall-clock accumulator: a ticker drops fires under load, which
			// silently dilates simulation time. Run however many ticks the
			// wall says are owed, capped so a long stall (suspend, debugger)
			// jumps rather than fast-forwarding for minutes.
			now := time.Now()
			behind += now.Sub(previous)
			previous = now
			if behind > 250*time.Millisecond {
				behind = 250 * time.Millisecond
			}
			connected := 0
			for _, p := range s.players {
				if p.link == nil {
					continue
				}
				// Application-level liveness: QUIC never idles out a ghost
				// (the browser ACKs our snapshot stream even when the tab is
				// hidden or the game's script is dead), but a live client
				// streams inputs every frame. Fifteen silent seconds means
				// the pilot is gone; closing the link fires the ordinary
				// leave path from its reader.
				if !p.seen.IsZero() && time.Since(p.seen) > 15*time.Second {
					p.link.close("idle")
					p.link = nil
					continue
				}
				connected++
			}
			for ; behind >= interval; behind -= interval {
				session_orders(s)
				gathered := map[int][]game.Input{}
				for slot, p := range s.players {
					if len(p.queue) > 0 {
						gathered[slot] = p.queue
						p.queue = nil
					}
				}
				s.tick++
				s.instance.Step(s.tick, gathered)
				for _, e := range s.instance.Events() {
					session_broadcast(s, map[string]any{"kind": "event", "tick": s.tick, "event": e}, true)
				}
				if s.tick%uint64(every) == 0 {
					session_snapshot(s)
				}
				if done, results := s.instance.Finished(); done {
					session_broadcast(s, map[string]any{"kind": "end", "reason": "finished", "results": results}, true)
					session_close(s, "finished")
					return
				}
			}
			if connected > 0 {
				s.empty = time.Time{}
			} else if s.empty.IsZero() {
				s.empty = time.Now()
			} else if time.Since(s.empty) > idle && !s.permanent {
				session_close(s, "idle")
				return
			}
		}
	}
}

// session_orders drains pending joins, leaves, and inputs.
func session_orders(s *session) {
	for {
		select {
		case o := <-s.inbox:
			switch o.kind {
			case "join":
				o.reply <- session_join(s, o)
			case "leave":
				if p := s.players[o.slot]; p != nil {
					s.instance.Leave(p.Player)
					delete(s.players, o.slot)
					session_broadcast(s, map[string]any{"kind": "event", "tick": s.tick, "event": map[string]any{"kind": "leave", "slot": o.slot, "name": p.Name}}, true)
					state := ""
					if len(s.players) == 0 {
						state = "open" // everyone left: joinable again, not stuck "playing"
					}
					session_mirror(s, state)
				}
			case "input":
				if p := s.players[o.slot]; p != nil {
					p.seen = time.Now()
					for _, in := range o.inputs {
						if in.Sequence > p.sequence || len(p.queue) == 0 {
							p.queue = append(p.queue, in)
							if in.Sequence > p.sequence {
								p.sequence = in.Sequence
							}
						}
					}
					if len(p.queue) > 64 {
						p.queue = p.queue[len(p.queue)-64:]
					}
				}
			case "chat":
				session_chat(s, o)
			}
		default:
			return
		}
	}
}

// session_chat sanitizes, scopes, and relays one chat line (#84). Team scope
// is enforced HERE, not client-side — a client filter would put the other
// team's tactics on the wire for anyone to read. The sender receives their
// own line back: the echo is the delivery confirmation.
func session_chat(s *session, o order) {
	p := s.players[o.slot]
	if p == nil || p.link == nil {
		return
	}
	now := time.Now()
	window := p.talked[:0]
	for _, when := range p.talked {
		if now.Sub(when) < 5*time.Second {
			window = append(window, when)
		}
	}
	p.talked = window
	if len(p.talked) >= 3 {
		return // flooding: dropped without ceremony
	}
	words := clean(o.text, 200)
	if words == "" {
		return
	}
	p.talked = append(p.talked, now)
	scope, team := "all", ""
	if o.scope == "team" {
		if game, valid := s.instance.(sided); valid {
			if side := game.Team(o.slot); side != "" {
				scope, team = "team", side
			}
		}
	}
	event := map[string]any{"kind": "chat", "slot": o.slot, "name": p.Name, "text": words, "scope": scope}
	s.chats = append(s.chats, spoken{event: event, team: team})
	if len(s.chats) > 20 {
		s.chats = s.chats[len(s.chats)-20:]
	}
	bytes, err := encode(map[string]any{"kind": "event", "tick": s.tick, "event": event})
	if err != nil {
		return
	}
	audience, _ := s.instance.(sided)
	for _, r := range s.players {
		if r.link == nil {
			continue
		}
		if team != "" && (audience == nil || audience.Team(r.Slot) != team) {
			continue
		}
		r.link.write(bytes, true)
	}
}

// session_join admits a player on the lowest free slot and sends their
// welcome from here — the tick goroutine — so it is queued on their link
// before any event broadcast that mentions them.
func session_join(s *session, o order) answer {
	if len(s.players) >= s.spec.Capacity {
		return answer{err: errors.New("full")}
	}
	slot := 0
	for s.players[slot] != nil {
		slot++
	}
	o.player.Slot = slot
	spawn, err := s.instance.Join(o.player)
	if err != nil {
		return answer{err: err}
	}
	s.players[slot] = &player{Player: o.player, link: o.link, seen: time.Now()}
	others := []map[string]any{}
	for _, p := range s.players {
		others = append(others, map[string]any{"slot": p.Slot, "name": p.Name, "identity": p.Identity})
	}
	tickrate, snaprate := games[s.spec.Game].Rate()
	welcome, err := encode(map[string]any{
		"kind": "welcome", "protocol": protocol, "session": s.identifier,
		"slot": slot, "name": o.player.Name, "tick": s.tick,
		"rate":       map[string]any{"tick": tickrate, "snapshot": snaprate},
		"parameters": s.spec.Parameters, "seed": s.spec.Seed,
		"spawn": spawn, "players": others,
	})
	if err == nil {
		o.link.write(welcome, true)
	}
	session_broadcast(s, map[string]any{"kind": "event", "tick": s.tick, "event": map[string]any{"kind": "join", "slot": slot, "name": o.player.Name}}, true)
	// Replay the recent chat (#84) so a late joiner sees the conversation —
	// respecting each line's original audience.
	audience, _ := s.instance.(sided)
	for _, line := range s.chats {
		if line.team != "" && (audience == nil || audience.Team(slot) != line.team) {
			continue
		}
		if bytes, err := encode(map[string]any{"kind": "event", "tick": s.tick, "event": line.event}); err == nil {
			o.link.write(bytes, true)
		}
	}
	session_mirror(s, "playing")
	return answer{slot: slot}
}

// session_snapshot sends the shared state with a per-recipient input
// acknowledgement.
func session_snapshot(s *session) {
	shared := s.instance.Snapshot(s.tick)
	cores, _ := shared["cores"].(map[int]any)
	poses, _ := shared["poses"].(map[int]any)
	for _, p := range s.players {
		if p.link == nil {
			continue
		}
		envelope := map[string]any{"kind": "snapshot", "tick": s.tick, "acknowledged": p.sequence}
		for k, v := range shared {
			if k == "cores" || k == "poses" {
				continue // per-recipient below: everyone's cores (or full pose sets) would burst the datagram MTU
			}
			envelope[k] = v
		}
		if core, found := cores[p.Slot]; found {
			envelope["core"] = core
		}
		bytes, err := encode(envelope)
		if err == nil {
			p.link.write(bytes, false)
		}
		// The pose blob rides its OWN datagram: core + poses together burst
		// the MTU at scale, and datagrams are independent anyway — the client
		// stitches by tick.
		if mine, found := poses[p.Slot]; found {
			flock, ok := mine.(map[string]any)
			if !ok {
				continue
			}
			second := map[string]any{"kind": "poses", "tick": s.tick}
			for k, v := range flock {
				second[k] = v
			}
			if bytes, err := encode(second); err == nil {
				p.link.write(bytes, false)
			}
		}
	}
}

// session_broadcast sends one message to every connected player.
func session_broadcast(s *session, message map[string]any, reliable bool) {
	bytes, err := encode(message)
	if err != nil {
		return
	}
	for _, p := range s.players {
		if p.link != nil {
			p.link.write(bytes, reliable)
		}
	}
}

// session_close notifies players, then ends the session.
func session_close(s *session, reason string) {
	if reason != "finished" {
		session_broadcast(s, map[string]any{"kind": "end", "reason": reason}, true)
	}
	for _, p := range s.players {
		if p.link != nil {
			p.link.close(reason)
			p.link = nil
		}
	}
	session_end(s, reason)
}

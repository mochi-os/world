// Mochi world: Echo, the transport suite's fixture game
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Echo is the smallest possible game: each snapshot reflects every player's
// latest input back. It exists to exercise the whole pipeline — lobby,
// handshake, datagrams, snapshots — without any simulation.
//
// It lives in a _test.go file so it CANNOT reach the production binary (#188).
// Registered there it was creatable by anyone: the lobby takes the game name
// from the request body and needs no authentication, so an anonymous
// POST {"game":"echo"} got a live session that reflected arbitrary
// client-supplied maps to every player, ten times a second, re-encoded once
// per recipient. Air excludes its large keys from that per-recipient copy;
// echo's did not.

package main

import (
	"world/game"
)

type echo struct{}

func echo_new() *echo { return &echo{} }

func (e *echo) Name() string     { return "echo" }
func (e *echo) Rate() (int, int) { return 20, 10 }
func (e *echo) Create(session game.Session) (game.Instance, error) {
	return &echo_instance{last: map[int]map[string]any{}}, nil
}

type echo_instance struct {
	last map[int]map[string]any
}

func (i *echo_instance) Join(player game.Player) (map[string]any, error) {
	i.last[player.Slot] = map[string]any{}
	return map[string]any{}, nil
}

func (i *echo_instance) Leave(player game.Player) {
	delete(i.last, player.Slot)
}

func (i *echo_instance) Step(tick uint64, inputs map[int][]game.Input) {
	for slot, list := range inputs {
		if _, present := i.last[slot]; present && len(list) > 0 {
			i.last[slot] = list[len(list)-1].Data
		}
	}
}

func (i *echo_instance) Snapshot(tick uint64) map[string]any {
	players := []map[string]any{}
	for slot, data := range i.last {
		players = append(players, map[string]any{"slot": slot, "echo": data})
	}
	return map[string]any{"players": players}
}

func (i *echo_instance) Events() []map[string]any { return nil }

func (i *echo_instance) Finished() (bool, map[string]any) { return false, nil }

// Mochi world: Echo game (transport test)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Echo is the smallest possible game: each snapshot reflects every player's
// latest input back. It exists to exercise the whole pipeline — lobby,
// handshake, datagrams, snapshots — without any simulation.
package echo

import (
	"world/game"
)

type Echo struct{}

func New() *Echo { return &Echo{} }

func (e *Echo) Name() string          { return "echo" }
func (e *Echo) Rate() (int, int)      { return 20, 10 }
func (e *Echo) Create(session game.Session) (game.Instance, error) {
	return &instance{last: map[int]map[string]any{}}, nil
}

type instance struct {
	last map[int]map[string]any
}

func (i *instance) Join(player game.Player) (map[string]any, error) {
	i.last[player.Slot] = map[string]any{}
	return map[string]any{}, nil
}

func (i *instance) Leave(player game.Player) {
	delete(i.last, player.Slot)
}

func (i *instance) Step(tick uint64, inputs map[int][]game.Input) {
	for slot, list := range inputs {
		if _, present := i.last[slot]; present && len(list) > 0 {
			i.last[slot] = list[len(list)-1].Data
		}
	}
}

func (i *instance) Snapshot(tick uint64) map[string]any {
	players := []map[string]any{}
	for slot, data := range i.last {
		players = append(players, map[string]any{"slot": slot, "echo": data})
	}
	return map[string]any{"players": players}
}

func (i *instance) Events() []map[string]any { return nil }

func (i *instance) Finished() (bool, map[string]any) { return false, nil }

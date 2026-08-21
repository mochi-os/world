// Mochi world: Saddle finishing
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestSaddleFinish: a slow ace parked in gun parameters behind a compliant
// target must finish the kill, not starve into rebuild and unload away. The
// energy floor must yield to the shot, not only to a close menace.
func TestSaddleFinish(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "saddle", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"ace": 1.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	ace, prey := i.aircraft[99], i.aircraft[98]
	ace.brain.plan, ace.brain.planned = "two", 1 // the merge is history: the energy floor only arms once a merge plan exists, and this test starts mid-fight
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	// BELOW the ace's energy floor (154 m/s), dead astern at 400 m: the gift.
	slow := flight.Vec3{X: 140}
	fired, starved := false, false
	for tick := uint64(0); tick < 60*12; tick++ {
		aloft(ace, base, slow)
		aloft(prey, base.Add(flight.Vec3{X: 400}), slow)
		i.Step(tick, nil)
		if tick < 60 {
			continue // let the picture and the merge plan form
		}
		if ace.latest.Fire {
			fired = true
		}
		if ace.brain.mode == "rebuild" {
			starved = true
		}
	}
	if starved {
		t.Fatal("the ace starved out of the saddle: rebuild with a compliant target 400 m ahead")
	}
	if !fired {
		t.Fatal("twelve seconds in gun parameters behind a compliant target and the trigger never came")
	}
}

// Mochi world: the BVR ladder's two upper rungs over twelve seeds instead of
// six — the six-seed rungs move by two on seed luck (#43). AIR_PRESSURE=1.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"os"
	"testing"

	"world/game"
)

func TestBvrWide(t *testing.T) {
	if os.Getenv("AIR_PRESSURE") == "" {
		t.Skip("measurement harness: set AIR_PRESSURE=1 to run")
	}
	heavy(t)
	seeds := uint64(12)
	if v := os.Getenv("AIR_BVR_SEEDS"); v != "" {
		fmt.Sscanf(v, "%d", &seeds)
	}
	pairs := [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}}
	if os.Getenv("AIR_BVR_END") != "" {
		pairs = [][2]string{{"superhuman", "novice"}} // the end rung alone, on request
	}
	for _, pair := range pairs {
		strong, weak := pair[0], pair[1]
		wins, losses, draws, spent := 0, 0, 0, 0
		for seed := uint64(1); seed <= seeds; seed++ {
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("bvrwide%s%d", strong, seed),
				Game: "air", Mode: "joust", Seed: seed,
				Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{strong: 1.0, weak: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var top, low *craft
			for _, s := range i.slots() {
				a := i.aircraft[s]
				if a == nil || !a.bot {
					continue
				}
				if top == nil {
					top = a
				} else {
					low = a
				}
			}
			if top.brain.skill.library <= low.brain.skill.library && !(top.brain.skill.machine && !low.brain.skill.machine) {
				top, low = low, top
			}
			decided := false
			for tick := uint64(0); tick < 60*600 && !decided; tick++ {
				i.Step(tick, nil)
				switch {
				case low.model == nil || !low.alive:
					wins++
					decided = true
				case top.model == nil || !top.alive:
					losses++
					decided = true
				}
			}
			if !decided {
				draws++
			}
			for _, a := range []*craft{top, low} {
				spent += 4 - a.amraams
			}
			i.Close()
		}
		fmt.Printf("bvr wide %-11s v %-7s %d-%d, %d no result | AMRAAMs spent %d of %d\n", strong, weak, wins, losses, draws, spent, 8*seeds)
	}
}

// TestMissileLadderWide is TestLadderDuel's missiles arm over 48 seeds.
func TestMissileLadderWide(t *testing.T) {
	if os.Getenv("AIR_PRESSURE") == "" {
		t.Skip("measurement harness: set AIR_PRESSURE=1 to run")
	}
	heavy(t)
	for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
		strong, weak := pair[0], pair[1]
		wins, losses, draws := 0, 0, 0
		for seed := uint64(1); seed <= 48; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("missilewide%s%d", strong, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{strong: 1.0, weak: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var top, low *craft
			for _, slot := range i.slots() {
				c := i.aircraft[slot]
				if c == nil || c.brain == nil {
					continue
				}
				if c.brain.skill.library == skills[strong].library && c.brain.skill.wander == skills[strong].wander {
					top = c
				} else {
					low = c
				}
			}
			done := false
			for tick := uint64(0); tick < 60*240 && !done; tick++ {
				i.Step(tick, nil)
				switch {
				case low.model == nil || !low.alive:
					wins++
					done = true
				case top.model == nil || !top.alive:
					losses++
					done = true
				}
			}
			if !done {
				draws++
			}
			i.Close()
		}
		fmt.Printf("wide missiles %-11s vs %-7s  won %d  lost %d  no result %d  (of 48)\n", strong, weak, wins, losses, draws)
	}
}

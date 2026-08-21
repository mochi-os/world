// Mochi world: the BVR and missile ladders' upper rungs at wide seed counts
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"os"
	"testing"

	"world/game"
)

func TestBvrWide(t *testing.T) {
	wide(t)
	seeds := uint64(24)
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
		// THE GATE (#46): symmetric competent BVR neutralises, so order is not
		// demanded of these rungs - losing them beyond the seed band is. Twenty-four
		// seeds move by about two.
		if seeds >= 24 && losses > wins+3 {
			t.Errorf("bvr wide: %s lost to %s %d-%d over %d seeds: the rung is inverted", strong, weak, losses, wins, seeds)
		}
	}
}

// TestMissileLadderWide is TestLadderDuel's missiles arm over 48 seeds.
func TestMissileLadderWide(t *testing.T) {
	wide(t)
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
		// THE GATE (#46): the chaotic arm, four fights of daylight at this width
		// (sixteen seeds read 5-7 on a rung forty-eight read 20-18).
		if losses > wins+4 {
			t.Errorf("wide missiles: %s lost to %s %d-%d over 48 seeds: the ladder is inverted", strong, weak, losses, wins)
		}
	}
}

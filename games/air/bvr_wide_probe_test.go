// Mochi world: the BVR and missile ladders' upper rungs at wide seed counts
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"testing"

	"world/game"
)

func TestBvrWide(t *testing.T) {
	wide(t)
	// 48, not 24 (#106): at 24 seeds this rung passed while the same code failed
	// at 48 by eight fights. The gate's own tolerance is +-2 at 24 seeds and the
	// true margin was four times that, so a green run at 24 was never evidence
	// about this pair. Costs about seven extra minutes in the doctrine battery.
	seeds := uint64(48)
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
		// demanded of these rungs - losing them beyond the seed band is.
		//
		// The band SCALES with the sample (#106). A win-loss margin wanders as
		// the square root of the fights, so the +3 that is right at 24 seeds is
		// about +4 at 48; a fixed number would mean raising the seed count
		// silently TIGHTENED the gate rather than only resolving it better.
		band := 3 * math.Sqrt(float64(seeds)/24)
		if seeds >= 24 && float64(losses) > float64(wins)+band {
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
		// THE GATE (#46): the chaotic arm. Re-centred for #69: the threshold was
		// set when this rung read 20-18 positive, but the rung has drifted
		// intrinsically slightly negative (measured 2026-08-22: 19-21 at HEAD on
		// these seeds, 37-41 at 96 seeds where sigma is ~9), so four fights of
		// daylight left NO headroom — any change that re-rolls a handful of
		// fights tripped it on luck (#69's gate read 17-22 while a 96-seed
		// paired run showed a 2-fight delta). Six still catches the 13-27-class
		// inversions this gate exists for.
		if losses > wins+6 {
			t.Errorf("wide missiles: %s lost to %s %d-%d over 48 seeds: the ladder is inverted", strong, weak, losses, wins)
		}
	}
}

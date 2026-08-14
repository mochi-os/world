// Mochi world: One-off mid-ladder measurement at a bigger seed block (#22).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"testing"

	"world/game"
)

// TestMidladderMeasure re-runs the ladder's middle pairings at twenty seeds
// to separate ordering from sample noise (ace v pilot came out 2-3 at six).
// A measurement, not a gate: it runs only with BVR_MIDLADDER=1 set, prints
// per-seed outcomes, and asserts nothing.
func TestMidladderMeasure(t *testing.T) {
	if os.Getenv("BVR_MIDLADDER") == "" {
		t.Skip("measurement harness: set BVR_MIDLADDER=1 to run")
	}
	heavy(t)
	pairs := [][2]string{
		{"superhuman", "novice"},
		{"pilot", "novice"},
		{"ace", "pilot"},
		{"superhuman", "ace"},
	}
	for _, pair := range pairs {
		strong, weak := pair[0], pair[1]
		wins, losses, draws, spent := 0, 0, 0, 0
		closest := math.MaxFloat64
		for seed := uint64(1); seed <= 20; seed++ {
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("midladder%s%s%d", strong, weak, seed),
				Game: "air", Mode: "joust", Seed: seed,
				Parameters: map[string]any{"missiles": true, "start": "bvr",
					"bots": map[string]any{strong: 1.0, weak: 1.0}}})
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
			if top.brain.skill.library <= low.brain.skill.library &&
				!(top.brain.skill.machine && !low.brain.skill.machine) {
				top, low = low, top
			}
			verdict, when := "draw", 600.0
			for tick := uint64(0); tick < 60*600; tick++ {
				i.Step(tick, nil)
				if top.model != nil && low.model != nil {
					if span := i.span(top.model, low.model); span < closest {
						closest = span
					}
				}
				if low.model == nil || !low.alive {
					verdict, when = "strong", float64(tick)/60
					break
				}
				if top.model == nil || !top.alive {
					verdict, when = "weak", float64(tick)/60
					break
				}
			}
			used := 0
			for _, a := range []*craft{top, low} {
				if a != nil {
					used += 4 - a.amraams
				}
			}
			spent += used
			switch verdict {
			case "strong":
				wins++
			case "weak":
				losses++
			default:
				draws++
			}
			fmt.Printf("midladder %-18s seed %2d: %-6s at %5.1f s, %d rounds spent\n",
				strong+" v "+weak, seed, verdict, when, used)
			i.Close()
		}
		fmt.Printf("midladder %-18s TOTAL %d-%d, %d draws | closest %6.0f m | spent %d of 160\n",
			strong+" v "+weak, wins, losses, draws, closest, spent)
	}
}

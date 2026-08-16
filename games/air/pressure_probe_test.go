// Mochi world: TEMPORARY probe — what actually happens to a bot pinned at its
// own six by the scripted attacker, second by second?
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

// TestPressureTimeline answers the question task #38 turns on. The gate says
// an ace held at gun parameters for 71% of a minute by a crude scripted
// attacker is not credible. The battery also shows that ace spending 45% of
// the fight in `limp`, which is not a choice the planner makes freely — it
// needs thrust below 0.35 or a live fire, so the jet is genuinely wrecked.
//
// Two readings fit that, and they call for opposite fixes: either the bot is
// shot to pieces early and everything after is a cripple being chased (in
// which case the gate is measuring how hard the scripted attacker is to shake,
// not the defender's skill), or the damage comes late and the tracking has
// some other cause the whole minute.
//
// So: print the timeline. When do the engines go, what is the range and angle
// off at that moment, and what was the bot doing before it.
func TestPressureTimeline(t *testing.T) {
	if os.Getenv("AIR_PRESSURE") == "" {
		t.Skip("measurement harness: set AIR_PRESSURE=1 to run")
	}
	heavy(t)

	for _, level := range []string{"ace", "superhuman"} {
		fmt.Printf("\n=== %s pinned at its six, 6 seeds ===\n", level)
		var wrecked, first []float64 // seconds to thrust<0.35, and to the first engine damage at all
		trackedBefore, totalBefore := 0, 0
		trackedAfter, totalAfter := 0, 0
		for seed := uint64(1); seed <= 6; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: "pressure", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false, "bots": map[string]any{level: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
				t.Fatal(err)
			}
			bot := -1
			for slot, a := range i.aircraft {
				if a != nil && a.brain != nil {
					bot = slot
				}
			}
			if bot < 0 {
				t.Fatal("no bot in the session")
			}
			place(i, bot, 0, -600)
			me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State

			hurt, dead, burning := -1.0, -1.0, -1.0
			ended, why := -1.0, "survived the minute"
			for tick := uint64(0); tick < 60*60; tick++ {
				data := pursue(me, foe)
				i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
				if !i.aircraft[0].alive || !i.aircraft[bot].alive || i.aircraft[bot].model == nil || i.aircraft[0].model == nil {
					ended = float64(tick) / 60
					switch {
					case i.aircraft[bot].model == nil || !i.aircraft[bot].alive:
						why = "the BANDIT died"
					default:
						altitude := -1.0
						if i.aircraft[0].model != nil {
							altitude = i.aircraft[0].model.State.Position.Y
						}
						why = fmt.Sprintf("the ATTACKER died (altitude %.0f m, bot fired %d rounds, bot credited %d kills)",
							altitude, rounds-i.aircraft[bot].ammunition, i.aircraft[bot].kills)
					}
					break
				}
				seconds := float64(tick) / 60
				if i.aircraft[bot].condition.Burning && burning < 0 {
					burning = seconds
				}
				thrust := 1 - (foe.Damage.Engine[0]+foe.Damage.Engine[1])/2
				if hurt < 0 && thrust < 0.999 {
					hurt = seconds
				}
				if dead < 0 && thrust < 0.35 {
					dead = seconds
					r, off, _, _ := geometry(me, foe)
					fmt.Printf("  seed %d: thrust fell below 0.35 at %5.1f s — attacker %4.0f m, %3.0f deg off the tail, bandit %3.0f kt, mode %s\n",
						seed, seconds, r, off, foe.Velocity.Length()*1.944, i.aircraft[bot].brain.mode)
				}
				if seed == 2 && tick%15 == 0 && tick < 60*4 {
					rr, oo, _, _ := geometry(me, foe)
					_, seen := i.aircraft[bot].brain.known[0]
					fmt.Printf("      t=%4.2fs mode=%-8s g=%.1f %3.0f kt | attacker %4.0f m %3.0f deg off tail | engines %.2f/%.2f fire=%v | knows=%v | menace=%d glimpsed=%d dodge=%d\n",
						seconds, i.aircraft[bot].brain.mode, i.aircraft[bot].brain.g, foe.Velocity.Length()*1.944,
						rr, oo, foe.Damage.Engine[0], foe.Damage.Engine[1], i.aircraft[bot].condition.Burning,
						seen, i.aircraft[bot].brain.menace, i.aircraft[bot].brain.glimpsed, i.aircraft[bot].brain.dodge)
				}
				r, off, _, _ := geometry(me, foe)
				pinned := r < 900 && off < 45
				if dead < 0 {
					totalBefore++
					if pinned {
						trackedBefore++
					}
				} else {
					totalAfter++
					if pinned {
						trackedAfter++
					}
				}
			}
			fmt.Printf("  seed %d: %s at %5.1f s | first engine damage %4.1f s | caught fire %5.1f s | thrust<0.35 %5.1f s\n",
				seed, why, ended, hurt, burning, dead)
			if dead >= 0 {
				wrecked = append(wrecked, dead)
			}
			if hurt >= 0 {
				first = append(first, hurt)
			}
			i.Close()
		}
		mean := func(v []float64) float64 {
			if len(v) == 0 {
				return -1
			}
			sum := 0.0
			for _, x := range v {
				sum += x
			}
			return sum / float64(len(v))
		}
		fmt.Printf("  crippled in %d of 6 seeds, mean %.1f s | first engine damage at mean %.1f s\n",
			len(wrecked), mean(wrecked), mean(first))
		share := func(a, b int) float64 {
			if b == 0 {
				return 0
			}
			return 100 * float64(a) / float64(b)
		}
		fmt.Printf("  pinned in the rear quarter BEFORE the jet was crippled: %.0f%% of %d ticks\n", share(trackedBefore, totalBefore), totalBefore)
		fmt.Printf("  pinned AFTER: %.0f%% of %d ticks\n", share(trackedAfter, totalAfter), totalAfter)
	}
}

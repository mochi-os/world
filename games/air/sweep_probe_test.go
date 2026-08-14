// Mochi world: TEMPORARY machine-sweep probe — delete once the drone
// inversion is settled. Tests whether the machine's gun kills depended on
// OPPONENT-MODEL ERROR supplying its bore sweep (#215/#256 follow-up).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"testing"

	"world/game"
)

// TestMachineSweep flies the drone scenario the lethality ladder gates on and
// measures the two quantities the sweep hypothesis rests on: how fast the
// bore crosses the solution (solution() only prices a crossing above 80 m/s)
// and how often the arbiter changes its mind. If the accurate opponent model
// locked the machine onto one line, its crossing rate and its play churn both
// collapse relative to the ace.
func TestMachineSweep(t *testing.T) {
	heavy(t)
	for _, level := range []string{"ace", "superhuman"} {
		var sweeps, misses []float64
		crossings, churn, decisions, inrange := 0, 0, 0, 0
		dry, killed, drowned := 0, 0, 0 // belt empty / killed the drone / hunter died anyway
		var emptied []float64           // seconds at which the belt ran out
		for seed := 1; seed <= 6; seed++ {
			g := New()
			made, _ := g.Create(game.Session{Identifier: fmt.Sprintf("sweep%s%d", level, seed), Game: "air",
				Mode: "furball", Capacity: 8, Seed: uint64(seed),
				Parameters: map[string]any{"bots": map[string]any{level: 1.0, "drone": 1.0}}})
			i := made.(*instance)
			var hunter, prey *craft
			for _, s := range i.slots() {
				if i.aircraft[s].brain != nil {
					hunter = i.aircraft[s]
				} else {
					prey = i.aircraft[s]
				}
			}
			if hunter == nil || prey == nil {
				t.Fatalf("%s: roster wrong", level)
			}
			previous, last := "", 0.0
			empty := false
			landed := 0 // rounds of the hunter's that actually struck the drone
			var seedRanges, seedMisses []float64
			seedModes, seedIntent := map[string]int{}, map[string]int{}
			horizon := 180.0
			if v := os.Getenv("SWEEP_SECONDS"); v != "" {
				fmt.Sscanf(v, "%f", &horizon)
			}
			outcome, at := "TIMEOUT", horizon
			for tick := uint64(0); tick < uint64(60*horizon); tick++ {
				if hunter.model == nil || prey.model == nil || hunter.brain == nil {
					break
				}
				if !prey.alive {
					killed++
					outcome, at = "kill", float64(tick)/60
					break
				}
				if !hunter.alive {
					drowned++
					outcome, at = "DIED", float64(tick)/60
					break
				}
				if !empty && hunter.ammunition <= 0 {
					empty = true
					dry++
					emptied = append(emptied, float64(tick)/60)
				}
				i.events = i.events[:0]
				i.Step(tick, nil)
				for _, e := range i.events {
					if e["kind"] == "hit" {
						if by, ok := e["by"].(int); ok && i.aircraft[by] == hunter {
							if n, ok := e["count"].(int); ok {
								landed += n
							} else {
								landed++
							}
						}
					}
				}
				if hunter.model == nil || prey.model == nil || hunter.brain == nil {
					// The craft was replaced inside this very step — say by
					// whom, or a kill this close to the swap reads as TIMEOUT.
					outcome = fmt.Sprintf("SWAP h:%v/%v p:%v/%v", hunter.alive, hunter.model != nil, prey.alive, prey.model != nil)
					at = float64(tick) / 60
					if !prey.alive {
						killed++
					}
					break
				}
				b := hunter.brain
				if b.play != previous && previous != "" {
					churn++
				}
				if b.play != previous {
					previous = b.play
				}
				decisions++
				if os.Getenv("SWEEP_TRACE") != "" && level == "superhuman" && (seed == 1 || seed == 5) && tick%60 == 0 {
					// One line a second: the flight's actual shape on the
					// failing seeds — where it is, what it flies, whether it
					// fires — sampled before the in-range gate so the
					// out-of-range orbit is visible too.
					span := hunter.model.State.Position.Subtract(prey.model.State.Position).Length()
					_, miss, _, _ := b.pipper(hunter.model, tick)
					fire := 0
					if hunter.latest.Fire {
						fire = 1
					}
					fmt.Printf("      trace seed %d t %5.1f | span %6.0f | miss %6.0f | play %-8s intent %-8s | nearing %6.1f | fire %d | ammo %d | alt %5.0f v %3.0f\n",
						seed, float64(tick)/60, span, miss, b.play, b.intent, b.nearing, fire, hunter.ammunition,
						hunter.model.State.Position.Y, hunter.model.State.Velocity.Length())
				}
				if b.prey == nil {
					continue
				}
				span := hunter.model.State.Position.Subtract(prey.model.State.Position).Length()
				if span > 900 {
					last = 0
					continue
				}
				inrange++
				_, miss, _, _ := b.pipper(hunter.model, tick)
				if last > 0 {
					// The same quantity solution() prices: how fast the aim
					// error is closing, in metres a second.
					rate := (last - miss) * 60
					sweeps = append(sweeps, math.Abs(rate))
					if rate > 80 {
						crossings++
					}
				}
				last = miss
				misses = append(misses, miss)
				seedMisses = append(seedMisses, miss)
				seedRanges = append(seedRanges, span)
				seedModes[b.play]++
				seedIntent[b.intent]++
			}
			within := func(v []float64, p float64) float64 {
				if len(v) == 0 {
					return 0
				}
				q := append([]float64(nil), v...)
				sort.Float64s(q)
				return q[int(p*float64(len(q)-1))]
			}
			top, best := "", 0
			for m, c := range seedModes {
				if c > best {
					top, best = m, c
				}
			}
			stance, most := "neutral", 0
			for m, c := range seedIntent {
				if c > most {
					if m == "" {
						m = "neutral"
					}
					stance, most = m, c
				}
			}
			fmt.Printf("   %-11s seed %d %-8s at %5.1f s | in-range ticks %5d | range p50 %5.0f m | miss p50 %6.1f m | hits given %3d | commonest %s | posture %s %.0f%%\n",
				level, seed, outcome, at, len(seedRanges), within(seedRanges, 0.5), within(seedMisses, 0.5), landed, top, stance, 100*float64(most)/math.Max(float64(len(seedRanges)), 1))
		}
		_ = emptied
		pct := func(v []float64, p float64) float64 {
			if len(v) == 0 {
				return 0
			}
			s := append([]float64(nil), v...)
			sort.Float64s(s)
			return s[int(p*float64(len(s)-1))]
		}
		fmt.Printf("%-11s in range %6d | crossings >80 m/s %5.2f%% | |sweep| p50 %6.1f m/s | miss p50 %6.1f m | churn %.2f%% | killed %d/6 | BELT DRY %d/6 (median %.0f s) | hunter died %d\n",
			level, inrange, 100*float64(crossings)/math.Max(float64(inrange), 1),
			pct(sweeps, 0.5), pct(misses, 0.5),
			100*float64(churn)/math.Max(float64(decisions), 1),
			killed, dry, pct(emptied, 0.5), drowned)
	}
}

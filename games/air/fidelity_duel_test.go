// Mochi world: Rehearsal fidelity in the duel regime
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestFidelityDuel measures surrogate agreement where the LADDER is judged:
// the two-bot guns duel, bucketed by range. TestFidelity samples an ace
// furball, which averages the merge in with cruising and repositioning; the
// ladder's known gap — ace-vs-pilot resolves 1-1 at full fidelity and 0-0 at
// stock — lives close in, so this instrument reports the same agreement and
// regret numbers PER RANGE BAND. Diagnostic, not gated: the printout is the
// product.
func TestFidelityDuel(t *testing.T) {
	heavy(t)
	live := rehearsal
	defer func() { rehearsal = live }()
	type tally struct {
		agreed, seen int
		regret       float64
		costly       int
	}
	bands := []string{"merge <800m", "knife 800-2000m", "far >2000m"}
	band := func(d float64) string {
		switch {
		case d < 800:
			return bands[0]
		case d < 2000:
			return bands[1]
		default:
			return bands[2]
		}
	}
	results := map[string]*tally{}
	for _, b := range bands {
		results[b] = &tally{}
	}
	confusion := map[string]int{} // "truth->got" across all bands: is the drift directional?

	for seed := uint64(1); seed <= 4; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("fidduel%d", seed), Game: "air", Mode: "furball",
			Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false,
				"bots": map[string]any{"ace": 1.0, "pilot": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		for tick := uint64(0); tick < 60*120; tick++ {
			i.Step(tick, nil)
			if tick%90 != 0 {
				continue
			}
			for _, slot := range i.slots() {
				a := i.aircraft[slot]
				if a == nil || a.brain == nil || a.model == nil || !a.alive {
					continue
				}
				b := a.brain
				if b.prey == nil || b.target < 0 {
					continue
				}
				sim := flight.New(a.model.Airframe, a.model.Environment, a.model.World)
				honest := map[string]float64{}
				rehearsal = full
				truth := i.choose(slot, a, b, sim, b.prey, tick, b.distance, honest)
				rehearsal = both
				got := i.choose(slot, a, b, sim, b.prey, tick, b.distance, map[string]float64{})
				rehearsal = live
				r := results[band(b.distance)]
				r.seen++
				if got == truth {
					r.agreed++
					continue
				}
				confusion[truth+" -> "+got]++
				best, worst := math.Inf(-1), math.Inf(1)
				for _, v := range honest {
					best = math.Max(best, v)
					worst = math.Min(worst, v)
				}
				if mine, ok := honest[got]; ok && best > worst {
					lost := (best - mine) / (best - worst)
					r.regret += lost
					if lost > 0.2 {
						r.costly++
					}
				}
			}
		}
	}
	for _, name := range bands {
		r := results[name]
		if r.seen == 0 {
			fmt.Printf("%-16s no decision points sampled\n", name)
			continue
		}
		missed := r.seen - r.agreed
		mean := 0.0
		if missed > 0 {
			mean = r.regret / float64(missed)
		}
		fmt.Printf("%-16s %4d points | agrees %5.1f%% | when it differs, gives up %4.1f%% of the spread | costly picks %4.1f%%\n",
			name, r.seen, 100*float64(r.agreed)/float64(r.seen), 100*mean, 100*float64(r.costly)/float64(r.seen))
	}
	pairs := make([]string, 0, len(confusion))
	for p := range confusion {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(a, b int) bool { return confusion[pairs[a]] > confusion[pairs[b]] })
	if len(pairs) > 12 {
		pairs = pairs[:12]
	}
	for _, p := range pairs {
		fmt.Printf("disagreed %3dx  %s\n", confusion[p], p)
	}
}

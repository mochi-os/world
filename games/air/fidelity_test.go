package air

import (
	"fmt"
	"math"
	"testing"
	"time"

	"world/game"
	"world/games/air/flight"
)

// TestFidelity is the #256 experiment: does a cheaper rollout choose the same
// play as the real one, and how much cheaper is it?
//
// The comparison is PAIRED — at every decision point in one full-fidelity
// fight, each variant is asked for its answer from the SAME live state, and
// the fight then continues on the full-fidelity answer. Running each variant
// as its own fight would diverge after the first disagreement and compare
// nothing. choose() is called rather than a copy of the selection loop, so
// what is measured is what the server would fly.
//
// Agreement, not accuracy, is the question: ranking candidates is a far weaker
// requirement than simulating them. The failure mode is silent, though — the
// quarter-time bug never looked broken — so it gets measured, not assumed.
func TestFidelity(t *testing.T) {
	heavy(t)
	// Restore the LIVE setting, not a hard-coded one: an earlier version of
	// this test put `full` back and every gate that ran after it in the same
	// binary silently measured the full model instead of the shipped
	// surrogate — a test harness quietly changing the subject of every later
	// measurement, which is the same class of fault as the quarter-time bug.
	live := rehearsal
	defer func() { rehearsal = live }()
	type tally struct {
		agreed, seen int
		spent        time.Duration
		regret       float64 // how much worse its pick is, JUDGED BY THE FULL MODEL, as a share of the full spread
		costly       int     // disagreements that gave up more than a fifth of that spread
	}
	results := map[fidelity]*tally{full: {}, surrogate: {}, both: {}}
	names := map[fidelity]string{full: "full 240Hz", surrogate: "surrogate 240Hz", both: "surrogate 60Hz"}

	for seed := uint64(1); seed <= 3; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("fid%d", seed), Game: "air", Mode: "furball",
			Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 2.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		for tick := uint64(0); tick < 60*90; tick++ {
			i.Step(tick, nil)
			if tick%90 != 0 { // sample decision points across the fight, not every tick
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
				truth := ""
				honest := map[string]float64{}
				for _, level := range []fidelity{full, surrogate, both} {
					rehearsal = level
					table := map[string]float64{}
					start := time.Now()
					got, _ := i.choose(slot, a, b, sim, b.prey, tick, b.distance, table)
					spent := time.Since(start)
					rehearsal = live
					r := results[level]
					r.seen++
					r.spent += spent
					if level == full {
						truth = got
						honest = table
					}
					if got == truth {
						r.agreed++
						continue
					}
					// REGRET: agreement alone cannot say whether a
					// disagreement matters. Score the variant's pick with the
					// FULL model and compare it to the full model's own pick,
					// normalised by the spread between the best and worst
					// candidate — a different play the real model rates almost
					// as highly costs the bot nothing.
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
	}
	for _, level := range []fidelity{full, surrogate, both} {
		r := results[level]
		if r.seen == 0 {
			t.Fatal("no decision points sampled")
		}
		per := r.spent / time.Duration(r.seen)
		missed := r.seen - r.agreed
		mean := 0.0
		if missed > 0 {
			mean = r.regret / float64(missed)
		}
		fmt.Printf("%-16s %7.2f ms | %6.1fx cheaper | agrees %5.1f%% | when it differs, gives up %4.1f%% of the spread | costly picks %4.1f%%\n",
			names[level], float64(per.Microseconds())/1000,
			float64(results[full].spent)/float64(r.spent), 100*float64(r.agreed)/float64(r.seen),
			100*mean, 100*float64(r.costly)/float64(r.seen))
	}
}

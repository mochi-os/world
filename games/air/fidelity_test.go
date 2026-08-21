package air

import (
	"fmt"
	"math"
	"testing"
	"time"

	"world/game"
	"world/games/air/flight"
)

// TestFidelity: does a cheaper rollout pick the same play as the full model,
// and how much cheaper is it? PAIRED - every variant answers from the same live
// state and the fight continues on the full answer, or they diverge.
func TestFidelity(t *testing.T) {
	heavy(t)
	// Restore the LIVE setting, not a hard-coded one: `rehearsal` is package
	// state, so a wrong restore silently re-measures every later gate in the
	// binary against the wrong model.
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
					// REGRET: the variant's pick scored by the FULL model against the full
					// model's own pick, normalised by the best-worst spread. Agreement alone
					// cannot rank harm.
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

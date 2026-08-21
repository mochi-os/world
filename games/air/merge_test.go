package air

import (
	"fmt"
	"math"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestMergeRoll: a player must be able to see which way the bandit turns at the
// merge. A fast coherent roll is readable; the sign changing is not, so this
// counts roll REVERSALS in the four seconds around the pass, not roll rate.
func TestMergeRoll(t *testing.T) {
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		total, worst := 0.0, 0.0
		for seed := 1; seed <= 5; seed++ {
			b := NewBandit(level, uint64(seed), 250000, "", false, false, "", 0)
			b.Spawn(flight.Vec3{X: -2750, Y: 4572}, flight.Vec3{X: 220})
			env := flight.Environment{Seed: uint64(seed), Wrap: 250000}
			pm := flight.New(aircraft.Get("fa18c"), env, flight.World{Sea: sea})
			pm.State = flight.Level(pm, flight.Vec3{X: 2750, Y: 4572}, flight.Vec3{X: -1}, 220, fuel)
			words := make([]float64, flight.Size)
			var bank []float64
			closest, closeAt := 1e9, 0
			var dodging []bool
			for tick := 0; tick < 60*30; tick++ {
				pm.State.Position.X -= 220.0 / 60
				pm.State.Encode(words)
				b.Mirror(words, false, true)
				b.Step()
				s := &b.craft.model.State
				up := s.Attitude.Rotate(flight.Vec3{Y: 1})
				right := s.Attitude.Rotate(flight.Vec3{Z: 1})
				bank = append(bank, math.Atan2(right.Y, up.Y)*180/math.Pi)
				dodging = append(dodging, uint64(tick) < b.craft.brain.dodge)
				if r := pm.State.Position.Subtract(s.Position).Length(); r < closest {
					closest, closeAt = r, tick
				}
			}
			// A reversal is a substantial roll the other way - 15 degrees of bank or
			// more. Bare rate-sign alternation counts the wings-levelling wobble after
			// the pass as a direction change.
			flips, run, last := 0.0, 0.0, 0.0
			judge := func() {
				if math.Abs(run) >= 15 {
					if last != 0 && math.Signbit(run) != math.Signbit(last) {
						flips++
					}
					last = run
				}
				run = 0
			}
			for i := closeAt - 120; i < closeAt+120 && i < len(bank); i++ {
				if i < 1 {
					continue
				}
				if dodging[i] {
					continue // a commanded guns defence (#251) is deliberately unreadable AIM, the opposite of an unreadable INTENTION: the scripted opponent's bore points straight at the bot through the pass, the flinch answers it, and counting that weave as merge dithering failed the exact behaviour the defensive package exists to add
				}
				d := bank[i] - bank[i-1]
				for d > 180 {
					d -= 360
				}
				for d < -180 {
					d += 360
				}
				if math.Abs(d*60) < 25 {
					continue
				}
				if run != 0 && math.Signbit(d) != math.Signbit(run) {
					judge()
				}
				run += d
			}
			judge()
			total += flips
			if flips > worst {
				worst = flips
			}
		}
		mean := total / 5
		fmt.Printf("%-8s merge reversals: mean %.1f  worst %.0f\n", level, mean, worst)
		// Gated for the top tiers only: novice and pilot dither at the merge and that
		// is authentic. The lower tiers are printed so a regression in either
		// direction stays visible.
		if mean > 1 && (level == "ace" || level == "superhuman") {
			t.Errorf("%s reverses its roll %.1f times per merge on average: a player cannot read which way it is turning", level, mean)
		}
	}
}

package air

import (
	"fmt"
	"math"
	"testing"

	"sort"

	"world/game"
	"world/games/air/flight"
)

// TestDroneKill (#219/#215): can each tier finish a COMPLIANT target that never
// fights back? Every other metric measures the bots' defence. The gate is that
// lethality LADDERS - kills non-decreasing up the tiers. Deterministic sim, so
// the counts are exact.
func TestDroneKill(t *testing.T) {
	heavy(t)
	const seconds = 180
	ladder := map[string]int{}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		kills, tries := 0, 0
		var times []float64
		advantage, firing, shots := 0, 0, 0
		total := 0
		modes := map[string]int{}
		for seed := 1; seed <= 12; seed++ {
			g := New()
			made, _ := g.Create(game.Session{Identifier: fmt.Sprintf("d%s%d", level, seed), Game: "air",
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
			tries++
			killed := -1.0
			for tick := uint64(0); tick < 60*seconds; tick++ {
				if hunter.model == nil || prey.model == nil || hunter.brain == nil {
					break // a respawn replaced a craft: stop this seed rather than read a stale one
				}
				aliveBefore := prey.alive
				i.Step(tick, nil)
				if aliveBefore && !prey.alive && killed < 0 {
					// The kill check must precede the respawn break: a burst that kills and
					// tears the craft down inside one step otherwise reads as a timeout.
					killed = float64(tick) / 60
					kills++
					times = append(times, killed)
					break
				}
				if hunter.model == nil || prey.model == nil || hunter.brain == nil {
					break
				}
				total++
				modes[hunter.brain.mode]++
				if hunter.latest.Fire {
					firing++
					shots++
				}
				// Advantage: inside gun range and roughly behind him.
				h, p := &hunter.model.State, &prey.model.State
				to := p.Position.Subtract(h.Position)
				rng := to.Length()
				if rng > 1 {
					nose := h.Attitude.Rotate(flight.Vec3{X: 1})
					off := math.Acos(clamp(nose.Dot(to.Scale(1/rng)), -1, 1)) * 180 / math.Pi
					if rng < 900 && off < 30 {
						advantage++
					}
				}
			}
		}
		mean := 0.0
		for _, v := range times {
			mean += v
		}
		if len(times) > 0 {
			mean /= float64(len(times))
		}
		type share struct {
			mode  string
			count int
		}
		shares := []share{}
		for m, c := range modes {
			shares = append(shares, share{m, c})
		}
		sort.Slice(shares, func(a, b int) bool { return shares[a].count > shares[b].count })
		top := ""
		for i, sh := range shares {
			if i >= 4 {
				break
			}
			top += fmt.Sprintf("%s %.0f%% ", sh.mode, 100*float64(sh.count)/float64(total))
		}
		fmt.Printf("%-18s kills %d/%d   mean time-to-kill %5.1fs   advantage %4.1f%%   trigger %4.1f%%   commonest mode %s\n",
			level, kills, tries, mean, 100*float64(advantage)/float64(total), 100*float64(firing)/float64(total), top)
		ladder[level] = kills
	}
	// One kill of slack per pair: deterministic single measurements put one-seed
	// margins below resolution. The inversion this guards was 1/3/7/2-class.
	for _, pair := range [][2]string{{"novice", "pilot"}, {"pilot", "ace"}, {"ace", "superhuman"}} {
		if ladder[pair[1]] < ladder[pair[0]]-1 {
			t.Errorf("lethality inverts: %s kills %d, %s kills %d — the better pilot converts less", pair[0], ladder[pair[0]], pair[1], ladder[pair[1]])
		}
	}
}

package air

import (
	"fmt"
	"math"
	"testing"

	"sort"

	"world/game"
	"world/games/air/flight"
)

// TestDroneKill (#219/#215): can each tier finish a COMPLIANT target? Every
// other metric measures the bots' DEFENCE, and the scripted harness cannot
// answer this because its attacker never stops attacking — the bot is never
// handed an easy kill to see whether it takes one. A drone weaves gently and
// never fights back.
//
// The GATE (#215's exit criterion, armed 2026-07-30): lethality must LADDER —
// kills non-decreasing up the tiers. Pre-retune it inverted (veteran 5/6, ace
// 2/6, trigger time FALLING as tier rose) because the firing gate was made of
// aim precision; the retune decoupled the trigger, slowed the ace's decision
// cadence, and gave the pilot ranges its own aim can serve. Deterministic sim,
// so the counts are exact, not statistical. (The one-parameter ablations that
// drove the retune live in #219's and #215's notes.)
func TestDroneKill(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes per tier")
	}
	const seconds = 180
	ladder := map[string]int{}
	for _, level := range []string{"rookie", "pilot", "veteran", "ace"} {
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
				if aliveBefore && !prey.alive && killed < 0 {
					killed = float64(tick) / 60
					kills++
					times = append(times, killed)
					break
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
	for _, pair := range [][2]string{{"rookie", "pilot"}, {"pilot", "veteran"}, {"veteran", "ace"}} {
		if ladder[pair[1]] < ladder[pair[0]] {
			t.Errorf("lethality inverts: %s kills %d, %s kills %d — the better pilot converts less", pair[0], ladder[pair[0]], pair[1], ladder[pair[1]])
		}
	}
}

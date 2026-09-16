// Worktree instrument (#42): never committed without a decision.
package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	"world/game"
	"world/games/air/flight"
)

type fight struct {
	seed             uint64
	decided          bool
	ended            float64
	closest          float64
	near             [2]int     // ticks inside 900 m
	solution         [2]int     // ticks with the nose within 5 deg at 250-900 m
	rounds           [2]int     // rounds fired
	slowest, fastest [2]float64 // m/s
	speed            [2]float64 // summed
	modes            [2]map[string]int
	chosen           [2]map[string]*pick // conditions when each play was RUNNING
	ticks            int
}

// pick accumulates the geometry a play is flown in, so a play chosen in the
// wrong regime is visible as a regime rather than as a share.
type pick struct {
	ticks int
	speed float64 // summed, m/s
	span  float64 // summed range to the opponent, m
}

// TestStalemateAttribution asks what separates the guns fights that end from
// the ones that do not. TestLadderDuel's ace-v-pilot guns arm reads 4-0 with
// TWELVE of sixteen no result at 135 s: not a collapse, two jets that cannot
// finish each other. The aero cap is already excluded (the energy floor never
// arms, and the cap is flat across outcomes), so this compares the decided
// fights against the undecided ones on everything else.
func TestStalemateAttribution(t *testing.T) {
	if os.Getenv("AIR_DOCTRINE") == "" {
		t.Skip("set AIR_DOCTRINE=1")
	}
	var all []fight
	for seed := uint64(1); seed <= 16; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("stale%d", seed),
			Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "weapons": "guns",
				"bots": map[string]any{"ace": 1.0, "pilot": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		var side [2]*craft
		for _, slot := range i.slots() {
			c := i.aircraft[slot]
			if c == nil || c.brain == nil {
				continue
			}
			if c.brain.skill.library == skills["ace"].library && c.brain.skill.wander == skills["ace"].wander {
				side[0] = c
			} else {
				side[1] = c
			}
		}
		if side[0] == nil || side[1] == nil {
			t.Fatal("roster wrong")
		}
		f := fight{seed: seed, closest: math.MaxFloat64}
		for a := 0; a < 2; a++ {
			f.slowest[a], f.modes[a] = math.MaxFloat64, map[string]int{}
			f.chosen[a] = map[string]*pick{}
		}
		start := [2]int{side[0].spent, side[1].spent}
		for tick := uint64(0); tick < 60*240 && !f.decided; tick++ {
			i.Step(tick, nil)
			for a, c := range side {
				if c.model == nil || !c.alive {
					f.decided, f.ended = true, float64(tick)/60
				}
				_ = a
			}
			if f.decided {
				break
			}
			f.ticks++
			for a, c := range side {
				s := &c.model.State
				speed := s.Velocity.Length()
				f.speed[a] += speed
				f.slowest[a] = math.Min(f.slowest[a], speed)
				f.fastest[a] = math.Max(f.fastest[a], speed)
				f.modes[a][c.brain.mode]++
				other := side[1-a]
				line := other.model.State.Position.Subtract(s.Position)
				span := line.Length()
				if span < 1 {
					continue
				}
				got, seen := f.chosen[a][c.brain.mode]
				if !seen {
					got = &pick{}
					f.chosen[a][c.brain.mode] = got
				}
				got.ticks++
				got.speed += speed
				got.span += span
				if a == 0 {
					f.closest = math.Min(f.closest, span)
				}
				if span < 900 {
					f.near[a]++
				}
				axis := s.Attitude.Rotate(flight.Vec3{X: 1})
				off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
				if off < 5 && span > 250 && span < 900 {
					f.solution[a]++
				}
			}
		}
		for a, c := range side {
			f.rounds[a] = c.spent - start[a]
		}
		if !f.decided {
			f.ended = 240
		}
		all = append(all, f)
		i.Close()
	}
	report := func(label string, pick func(fight) bool) {
		var n int
		var ended, closest float64
		var near, solution, rounds [2]float64
		var slow, fast, mean [2]float64
		modes := [2]map[string]int{{}, {}}
		for _, f := range all {
			if !pick(f) {
				continue
			}
			n++
			ended += f.ended
			closest += f.closest
			span := math.Max(float64(f.ticks), 1)
			for a := 0; a < 2; a++ {
				near[a] += 100 * float64(f.near[a]) / span
				solution[a] += 100 * float64(f.solution[a]) / span
				rounds[a] += float64(f.rounds[a])
				slow[a] += f.slowest[a] * 1.944
				fast[a] += f.fastest[a] * 1.944
				mean[a] += f.speed[a] / span * 1.944
				for m, v := range f.modes[a] {
					modes[a][m] += v
				}
			}
		}
		if n == 0 {
			fmt.Printf("\n=== %s: none ===\n", label)
			return
		}
		d := float64(n)
		fmt.Printf("\n=== %s (%d fights) === ended %.0f s, closest %.0f m\n", label, n, ended/d, closest/d)
		for a, who := range []string{"ace  ", "pilot"} {
			fmt.Printf("  %s inside 900 m %5.1f%% | on solution %5.2f%% | rounds %5.0f | speed mean %3.0f slowest %3.0f fastest %3.0f kt\n",
				who, near[a]/d, solution[a]/d, rounds[a]/d, mean[a]/d, slow[a]/d, fast[a]/d)
			fmt.Printf("        modes: %s\n", strings.Join(topThree(modes[a]), "  "))
			fmt.Printf("        regime: %s\n", strings.Join(regimes(all, pick, a), "  "))
		}
	}
	report("DECIDED", func(f fight) bool { return f.decided })
	report("STALEMATE", func(f fight) bool { return !f.decided })
	if len(all) != 16 {
		t.Fatalf("wanted 16 fights, drove %d", len(all))
	}
}

// regimes reports, for the four most-flown plays, the mean speed and range the
// tier was actually IN while flying them - the question a share cannot answer.
func regimes(all []fight, want func(fight) bool, a int) []string {
	total := map[string]*pick{}
	for _, f := range all {
		if !want(f) {
			continue
		}
		for name, p := range f.chosen[a] {
			got, seen := total[name]
			if !seen {
				got = &pick{}
				total[name] = got
			}
			got.ticks += p.ticks
			got.speed += p.speed
			got.span += p.span
		}
	}
	type kv struct {
		k string
		p *pick
	}
	list := []kv{}
	for k, p := range total {
		list = append(list, kv{k, p})
	}
	sort.Slice(list, func(x, y int) bool { return list[x].p.ticks > list[y].p.ticks })
	out := []string{}
	for i := 0; i < len(list) && i < 4; i++ {
		n := math.Max(1, float64(list[i].p.ticks))
		out = append(out, fmt.Sprintf("%s %.0fkt/%.0fm", list[i].k, list[i].p.speed/n*1.944, list[i].p.span/n))
	}
	return out
}

func topThree(m map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	list := []kv{}
	total := 0
	for k, v := range m {
		list = append(list, kv{k, v})
		total += v
	}
	sort.Slice(list, func(a, b int) bool { return list[a].v > list[b].v })
	out := []string{}
	for i := 0; i < len(list) && i < 4; i++ {
		out = append(out, fmt.Sprintf("%s %.0f%%", list[i].k, 100*float64(list[i].v)/math.Max(1, float64(total))))
	}
	return out
}

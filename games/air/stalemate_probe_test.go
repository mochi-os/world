// Worktree instrument (#42): never committed without a decision.
package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
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
	window           [2]int     // ticks inside the gun window 250-900 m at all, solution or not
	aspect           [2][6]int  // where the nose was during those ticks, by band (see bands)
	rounds           [2]int     // rounds fired (craft.spent: cumulative, and the one counter the ammunition cheat cannot confuse)
	hits             [2]int     // rounds that LANDED, counted off the kill pipeline's own "hit" events
	bursts           [2]int     // firing passes: runs of consecutive ticks with the trigger down
	slowest, fastest [2]float64 // m/s
	speed            [2]float64 // summed
	modes            [2]map[string]int
	chosen           [2]map[string]*pick // conditions when each play was RUNNING
	ticks            int
}

// bands are the upper edges, in degrees, of the nose-off histogram kept while
// the jet is inside the gun window. The first is the solution cone itself.
var bands = [6]float64{5, 10, 20, 45, 90, 181}

// pick accumulates the geometry a play is flown in, so a play chosen in the
// wrong regime is visible as a regime rather than as a share.
type pick struct {
	ticks  int
	speed  float64 // summed, m/s
	span   float64 // summed range to the opponent, m
	window int     // of those ticks, the ones inside the gun window 250-900 m
	aimed  int     // ... and of THOSE, the ones with the nose inside 20 deg
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
	// The seed count is a knob because the two columns need different depths:
	// the STALEMATE side fills fast (twelve of sixteen), the DECIDED side is
	// four fights at sixteen seeds and cannot carry a conclusion on its own
	// (#42's own caveat, and #46's sample-size trap on the ordering gates).
	seeds := uint64(16)
	if n, err := strconv.Atoi(os.Getenv("AIR_SEEDS")); err == nil && n > 0 {
		seeds = uint64(n)
	}
	var all []fight
	for seed := uint64(1); seed <= seeds; seed++ {
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
		was := [2]int{side[0].spent, side[1].spent}
		firing := [2]bool{}
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
			// Hits are ground truth, not a reconstruction: air.go raises one
			// "hit" event per strike carrying the victim, the shooter and the
			// rounds in it. Drain them here so the count is per fight.
			for _, event := range i.events {
				if event["kind"] != "hit" {
					continue
				}
				by, ok := event["by"].(int)
				if !ok {
					continue
				}
				count, _ := event["count"].(int)
				if count == 0 {
					count = 1
				}
				for a, c := range side {
					if i.aircraft[by] == c {
						f.hits[a] += count
					}
				}
			}
			i.events = i.events[:0]
			for a, c := range side {
				if c.spent > was[a] {
					if !firing[a] {
						f.bursts[a]++
						firing[a] = true
					}
				} else {
					firing[a] = false
				}
				was[a] = c.spent
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
				axis := s.Attitude.Rotate(flight.Vec3{X: 1})
				off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
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
				if span > 250 && span < 900 {
					// The solution share alone cannot say WHY a tier does not
					// shoot: in range with the nose 8 deg off is a tracking
					// problem, in range with it 70 deg off is a turning one,
					// and both read as 0%. Band the whole window, and keep the
					// same question PER PLAY - a nose that is never brought to
					// bear under one play and comes round under another is an
					// arbitration finding, while every play holding the same
					// angle is the regime and no mix would help it (#42).
					f.window[a]++
					got.window++
					if off < 20 {
						got.aimed++
					}
					for band, edge := range bands {
						if off < edge {
							f.aspect[a][band]++
							break
						}
					}
					if off < 5 {
						f.solution[a]++
					}
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
		var near, solution, rounds, hits, bursts [2]float64
		var slow, fast, mean [2]float64
		var window [2]float64
		var aspect [2][6]int
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
				window[a] += 100 * float64(f.window[a]) / span
				for band := range aspect[a] {
					aspect[a][band] += f.aspect[a][band]
				}
				rounds[a] += float64(f.rounds[a])
				hits[a] += float64(f.hits[a])
				bursts[a] += float64(f.bursts[a])
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
			// What the rounds DO (#42). The 5 deg solution share is a 52 m cone
			// at 600 m and cannot tell a hit from a near miss; these three can.
			fmt.Printf("        rounds landed %4.0f of %4.0f (%4.1f%%) | firing passes %4.1f | rounds per pass %4.0f\n",
				hits[a]/d, rounds[a]/d, 100*hits[a]/math.Max(1, rounds[a]), bursts[a]/d,
				rounds[a]/math.Max(1, bursts[a]))
			// Where the nose was whenever the jet WAS in the gun window: the
			// question "why did it not shoot" that a 0% solution share cannot
			// answer on its own.
			fmt.Printf("        gun window %5.1f%% of the fight | nose off: %s\n", window[a]/d, strings.Join(spread(aspect[a]), "  "))
			fmt.Printf("        modes: %s\n", strings.Join(topThree(modes[a]), "  "))
			fmt.Printf("        regime: %s\n", strings.Join(regimes(all, pick, a), "  "))
		}
	}
	report("DECIDED", func(f fight) bool { return f.decided })
	report("STALEMATE", func(f fight) bool { return !f.decided })
	// POSITIVE CONTROL for the hit counter. The decided fights end in a gun
	// kill, so rounds MUST have landed in them: a zero here means the "hit"
	// events are not reaching the probe, which would otherwise read as the far
	// more interesting "every round misses" - the exact false finding this
	// measurement exists to avoid.
	landed := 0
	for _, f := range all {
		if f.decided {
			landed += f.hits[0] + f.hits[1]
		}
	}
	if landed == 0 {
		t.Fatal("no rounds landed in any DECIDED fight: the hit events are not reaching the probe")
	}
	// POSITIVE CONTROL for the nose-off histogram. A banding bug loses ticks
	// silently and reads as a tier that is rarely in the window, so pin both
	// halves: every windowed tick lands in exactly one band, and the first
	// band IS the solution counter the rest of this probe already trusts.
	for _, f := range all {
		for a := 0; a < 2; a++ {
			total := 0
			for _, v := range f.aspect[a] {
				total += v
			}
			if total != f.window[a] {
				t.Fatalf("seed %d side %d: %d windowed ticks banded against %d counted", f.seed, a, total, f.window[a])
			}
			if f.aspect[a][0] != f.solution[a] {
				t.Fatalf("seed %d side %d: the <5 band holds %d against the solution counter's %d", f.seed, a, f.aspect[a][0], f.solution[a])
			}
		}
	}
	if uint64(len(all)) != seeds {
		t.Fatalf("wanted %d fights, drove %d", seeds, len(all))
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
			got.window += p.window
			got.aimed += p.aimed
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
		out = append(out, fmt.Sprintf("%s %.0fkt/%.0fm %.0f%%win/%.0f%%aimed", list[i].k,
			list[i].p.speed/n*1.944, list[i].p.span/n,
			100*float64(list[i].p.window)/n, 100*float64(list[i].p.aimed)/math.Max(1, float64(list[i].p.window))))
	}
	return out
}

// spread renders the nose-off histogram as shares of the time in the window.
func spread(counts [6]int) []string {
	total := 0
	for _, v := range counts {
		total += v
	}
	names := [6]string{"<5", "5-10", "10-20", "20-45", "45-90", ">90"}
	out := []string{}
	for band, v := range counts {
		out = append(out, fmt.Sprintf("%s %.0f%%", names[band], 100*float64(v)/math.Max(1, float64(total))))
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

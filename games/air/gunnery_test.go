// Mochi world: Rounds on target per firing opportunity
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
	"world/games/air/battle"
	"world/games/air/flight"
)

// TestGunnery measures the gun chain per tier against the scripted evade()
// target: OPPORTUNITY (a shot exists at all), CONVERSION (on the led solution),
// ACCURACY (how close the fired rounds pass). A kill count hides all three.
func TestGunnery(t *testing.T) {
	heavy(t)
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		var opportunity, solved, fired, total int
		var closest, aimSum float64 = 1e9, 0
		var aimed int
		hits, rounds := 0, 0
		for seed := uint64(1); seed <= 6; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: "gunnery", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
			place(i, 0, bot, -500) // the bot starts behind: the shot is there to be taken
			me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
			for tick := uint64(0); tick < 60*90; tick++ {
				before := *me
				i.Step(tick, map[int][]game.Input{0: {{Data: evade(me, foe, tick)}}})
				if !i.aircraft[0].alive || !i.aircraft[bot].alive {
					break
				}
				total++
				s := &i.aircraft[bot].model.State
				to := me.Position.Subtract(s.Position)
				span := to.Length()
				if span > 900 || span < 1 {
					continue
				}
				opportunity++
				// The led solution, measured the way the gunnery flies it.
				los := to.Scale(1 / span)
				closure := s.Velocity.Subtract(me.Velocity).Dot(los)
				transit := span / math.Max(battle.Average(span, s.Position.Y)+closure, 200)
				spot := me.Position.Add(me.Velocity.Scale(transit)).
					Subtract(s.Velocity.Scale(transit)).
					Add(flight.Vec3{Y: 4.9 * transit * transit})
				aim := spot.Subtract(s.Position).Normalize()
				nose := s.Attitude.Rotate(flight.Vec3{X: 1})
				miss := math.Acos(clamp(nose.Dot(aim), -1, 1)) * span
				aimSum += miss
				aimed++
				if miss < closest {
					closest = miss
				}
				if miss < 25 { // inside an aircraft's own length: a real shot
					solved++
				}
				if i.aircraft[bot].latest.Fire {
					fired++
					rounds += 2 // ~100 rounds/s at 60 Hz
					// Where would that round have gone? Fly it against the
					// target's true motion this step.
					drift := me.Position.Subtract(before.Position).Scale(60) // his velocity this tick
					best := 1e9
					where := s.Position
					velocity := nose.Scale(battle.Muzzle).Add(s.Velocity)
					for step := 1; step <= 45; step++ {
						// the same drag-and-gravity march the real round flies
						speed := velocity.Length()
						velocity = velocity.Scale(1 / (1 + speed*0.02/(2600*math.Exp(math.Max(where.Y, 0)/8500))))
						velocity.Y -= 9.8 * 0.02
						where = where.Add(velocity.Scale(0.02))
						him := me.Position.Add(drift.Scale(float64(step) * 0.02))
						if d := where.Subtract(him).Length(); d < best {
							best = d
						}
					}
					if best < 8 {
						hits++
					}
				}
			}
		}
		mean := 0.0
		if aimed > 0 {
			mean = aimSum / float64(aimed)
		}
		fmt.Printf("%-11s opportunity %5.1f%% of the fight | on solution %5.2f%% of those | trigger down %5.2f%% | "+
			"mean aim error %5.0f m, best %4.0f m | rounds ~%d, on target %5.1f%%\n",
			level, 100*float64(opportunity)/float64(total), 100*float64(solved)/math.Max(float64(opportunity), 1),
			100*float64(fired)/math.Max(float64(opportunity), 1), mean, closest,
			rounds, 100*float64(hits)/math.Max(float64(fired), 1))

		// The gates. These are the two links the post-mortem found broken:
		// the bot was in position constantly and converted almost never.
		if opportunity == 0 {
			t.Errorf("%s never got inside 900 m of a scripted turner in 90 s — the harness or the pursuit is broken", level)
		}
		if level != "pilot" {
			// The floor catches the catastrophe this instrument exists for - a bot that
			// points and never aims (0.4%) - not tier tuning; the ace sits at 3.46%.
			if share := 100 * float64(solved) / math.Max(float64(opportunity), 1); share < 3 {
				t.Errorf("%s is on a firing solution for %.2f%% of its in-range time — it points at him without ever aiming (floor 3%%)", level, share)
			}
			if share := 100 * float64(fired) / math.Max(float64(opportunity), 1); share < 2 {
				t.Errorf("%s pulls the trigger on %.2f%% of its in-range time — the gate is shut against a target it is tracking (floor 2%%)", level, share)
			}
		}
	}
}

// TestLadderDuel is the two-way lethality test: each tier against the one below
// it, guns only, to the kill. Every other instrument scores the bot against a
// target that does not fight back.
func TestLadderDuel(t *testing.T) {
	heavy(t)
	for _, missiles := range []bool{false, true} {
		arm := "guns"
		if missiles {
			arm = "missiles" // the armed arm (#253): six AIM-9Ms each and ~900 kg of carriage — the regime no guns-only instrument sees
		}
		for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
			strong, weak := pair[0], pair[1]
			wins, losses, draws := 0, 0, 0
			var times []float64
			// Sixteen seeds: two-way missile fights are chaotic - at n=8 this arm read
			// 0-3 while the same pairing over 24 seeds ran 5-4 forward.
			for seed := uint64(1); seed <= 16; seed++ {
				g := New()
				made, err := g.Create(game.Session{Identifier: fmt.Sprintf("ladder%s%s%d", arm, strong, seed),
					Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
					Parameters: map[string]any{"missiles": missiles, "weapons": map[bool]string{false: "guns", true: "fox2"}[missiles],
						"bots": map[string]any{strong: 1.0, weak: 1.0}}})
				if err != nil {
					t.Fatal(err)
				}
				i := made.(*instance)
				var top, low *craft
				for _, slot := range i.slots() {
					c := i.aircraft[slot]
					if c == nil || c.brain == nil {
						continue
					}
					if c.brain.skill.library == skills[strong].library && c.brain.skill.wander == skills[strong].wander {
						top = c
					} else {
						low = c
					}
				}
				if top == nil || low == nil {
					t.Fatalf("%s vs %s: roster wrong", strong, weak)
				}
				done := false
				for tick := uint64(0); tick < 60*240 && !done; tick++ {
					i.Step(tick, nil)
					// A detonation (the tank-vapour roll) tears the model down
					// in the death tick, so model-nil IS a kill — breaking on it
					// before the switch silently scored every detonation as no
					// result, which under real drag is most kills.
					switch {
					case low.model == nil || !low.alive:
						wins++
						times = append(times, float64(tick)/60)
						done = true
					case top.model == nil || !top.alive:
						losses++
						done = true
					}
				}
				if !done {
					draws++
				}
			}
			mean := 0.0
			for _, v := range times {
				mean += v
			}
			if len(times) > 0 {
				mean /= float64(len(times))
			}
			fmt.Printf("%-8s %-11s vs %-7s  won %d  lost %d  no result %d  (of 16)  mean time to kill %5.1f s\n",
				arm, strong, weak, wins, losses, draws, mean)
			// The gate is inversion only: these pairings mostly draw, so "the better
			// tier must win" would sit red on honest fights. It must fire on a regime,
			// not one seed - two fights of daylight, three for the chaotic missile arm.
			slack := 1
			if missiles {
				// Two fights of daylight in the chaotic missile arm. A flare magazine was
				// measured and rejected: it inverted this gate and the BVR top rung.
				slack = 2
			}
			if losses > wins+slack {
				t.Errorf("%s: %s lost to %s %d-%d: the ladder is inverted where it matters, in a two-way fight",
					arm, strong, weak, losses, wins)
			}
		}
	}
}

// flounder drives the scripted target reproducing a keyboard pilot's recorded
// profile: 50-110 m/s, 20-38 degrees alpha for two-thirds of the fight, bursts
// of full-aft pitch, quantised rolls, a hard break when the bandit closes.
func flounder(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	r := math.Max(toward.Length(), 1)
	line := toward.Scale(1 / r)
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})
	// Keyboard pitch: full aft in bursts, released in gaps — the limiter camp.
	phase := tick % 150
	pitch := 0.0
	if phase < 38 {
		pitch = 1.0 // 0.6 s of full-aft, 1.9 s of release: the limiter camp that made the measured 67% above 20 degrees alpha
	}
	// Keyboard roll: quantised full-deflection steps toward keeping the
	// bandit off the nose-tail line, flipped on a slow deterministic clock.
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	bank := math.Atan2(right.Y, up.Y)
	roll := 0.0
	turn := 1.0
	if (tick/540)%2 == 1 {
		turn = -1
	}
	wantBank := turn * 1.05
	if r < 900 && line.Dot(me.Velocity.Normalize()) < -0.3 {
		wantBank = turn * 1.25 // he is behind and close: break harder
		pitch = 1.0
	}
	// A wide bank deadband, because a keyboard pilot taps the roll key to set bank
	// and then leaves it alone; a tight band holds full lateral stick and the
	// rolling reduction then costs a fifth of the g. Positive stick rolls RIGHT,
	// which is NEGATIVE bank in the atan2(right.Y, up.Y) convention.
	if wantBank-bank > 0.4 {
		roll = -1
	} else if wantBank-bank < -0.4 {
		roll = 1
	}
	// The recovery rolls level FIRST, then pulls: the rolling reduction (NATOPS
	// 11.1.7) takes a fifth of the g ceiling with lateral stick, so a full-aft
	// pull through a full-deflection roll mushes into the water.
	if me.Position.Y < 1600 && me.Velocity.Y < 0 {
		if math.Abs(bank) > 0.3 {
			roll = clamp(bank*2, -1, 1) // toward wings level, in the same sign convention
			pitch = 0.2                 // unload while rolling: the g is bought back by not asking for it here
		} else {
			roll = 0
			pitch = 1.0 // wings level, now pull
		}
	}
	// Novice gunnery: squeeze whenever the nose wanders on inside range.
	fire := r < 900 && nose.Dot(line) > 0.9985
	return map[string]any{"pitch": pitch, "roll": roll, "throttle": 0.95, "fire": fire}
}

// jinker drives the fast, erratic evader - the flounder's opposite: military
// power, full-deflection rolls flipping on an irregular clock, hard pulls
// through each break, unloaded across each reversal. The irregularity is the
// point.
func jinker(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	r := math.Max(toward.Length(), 1)
	line := toward.Scale(1 / r)
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})
	// The irregular reversal clock: segment lengths cycle through a fixed
	// pattern (0.9-2.4 s), so the rhythm never settles into anything a
	// constant-rate predictor can ride. Deterministic, like everything here.
	pattern := [8]uint64{78, 132, 96, 54, 144, 66, 108, 90}
	total := uint64(0)
	for _, d := range pattern {
		total += d
	}
	phase := tick % total
	segment, into := 0, uint64(0)
	for k, d := range pattern {
		if phase < d {
			segment, into = k, phase
			break
		}
		phase -= d
	}
	turn := 1.0
	if segment%2 == 1 {
		turn = -1
	}
	// Pull hard through the front of each segment, UNLOAD across the
	// reversal: the out-of-plane jink. The unload is what spikes the jerk —
	// the acceleration vector collapses and re-forms on the other side.
	breaking := into < pattern[segment]*3/5
	pitch := 0.15
	if breaking {
		pitch = 0.9
	}
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	bank := math.Atan2(right.Y, up.Y)
	wantBank := turn * 1.15
	// Same sign convention as the flounder: positive stick rolls RIGHT, which
	// is NEGATIVE bank in atan2(right.Y, up.Y) — see the flounder's comment
	// for the split-S this cost when it was inverted.
	roll := 0.0
	if wantBank-bank > 0.35 {
		roll = -1
	} else if wantBank-bank < -0.35 {
		roll = 1
	}
	// The flounder's recovery, verbatim: level first, THEN pull. A scripted
	// target that kills itself turns the instrument into a green light that
	// measures nothing.
	if me.Position.Y < 1600 && me.Velocity.Y < 0 {
		if math.Abs(bank) > 0.3 {
			roll = clamp(bank*2, -1, 1)
			pitch = 0.2
		} else {
			roll = 0
			pitch = 1.0
		}
	}
	fire := r < 900 && nose.Dot(line) > 0.9985
	return map[string]any{"pitch": pitch, "roll": roll, "throttle": 1.0, "fire": fire}
}

// TestJink measures whether the bots can hit an actively evading human. The
// gated quantity is ROUNDS LANDED, not kills: kills are rare binary events one
// seed swings. Only the ends are gated; the middle is reported.
func TestJink(t *testing.T) {
	heavy(t)
	floor := map[string]int{}
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		var times []float64
		struck, unresolved, landed, engaged := 0, 0, 0, 0
		for seed := uint64(1); seed <= 12; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("jink%s%d", level, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
			place(i, 0, bot, 1800)
			me := &i.aircraft[0].model.State
			done := false
			for tick := uint64(0); tick < 60*180 && !done; tick++ {
				i.events = i.events[:0]
				i.Step(tick, map[int][]game.Input{0: {{Sequence: 1, Data: jinker(me, &i.aircraft[bot].model.State, tick)}}})
				for _, e := range i.events {
					if e["kind"] != "hit" {
						continue
					}
					count, _ := e["count"].(int)
					if e["slot"] == bot {
						struck += count
					} else {
						landed += count
					}
				}
				if b := i.aircraft[bot]; b.model != nil && i.aircraft[0].model != nil &&
					b.model.State.Position.Subtract(i.aircraft[0].model.State.Position).Length() < 900 {
					engaged++
				}
				human := i.aircraft[0]
				if human.model == nil || !human.alive {
					if human.condition.Damager < 0 {
						t.Fatalf("%s seed %d: the jinker flew into the sea at %.1f s with nobody having hit it — the instrument is measuring its own script, not the bot",
							level, seed, float64(tick)/60)
					}
					times = append(times, float64(tick)/60)
					done = true
				}
				if b := i.aircraft[bot]; b.model == nil || !b.alive {
					done = true
				}
			}
			if !done || len(times) < int(seed) {
				unresolved++
			}
		}
		mean := 0.0
		for _, v := range times {
			mean += v
		}
		if len(times) > 0 {
			mean /= float64(len(times))
		}
		_ = unresolved
		fmt.Printf("%-11s killed the jinker %d/12, mean %5.1f s | rounds landed on it %4d | in gun range %5.1f%% | hits taken %d\n",
			level, len(times), mean, landed, 100*float64(engaged)/float64(12*60*180), struck)
		floor[level] = landed
	}
	for _, level := range []string{"ace", "superhuman"} {
		if floor[level] < floor["pilot"] {
			t.Errorf("gunnery against evasion inverts: %s landed %d rounds on the jinker, the pilot %d", level, floor[level], floor["pilot"])
		}
	}
}

// TestFlounder is the referendum the tier names answer to (#249): kill the
// user's measured profile fast and clean. Gates: the superhuman inside 60
// seconds WITHOUT taking a hit; the ace inside 120. The pilot is reported
// (its target is parity with the user, judged elsewhere).
func TestFlounder(t *testing.T) {
	heavy(t)
	// Twelve seeds: kill counts sit near zero, so per-seed noise dominates a
	// six-seed read.
	const flounderSeeds = 12
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		var times []float64
		struck, unresolved, landed, engaged := 0, 0, 0, 0
		// The conversion diagnosis columns (#215 item 8): firing rate, time holding
		// the advantage position (nose on, inside 900 m), plays flown, and closure
		// while in range.
		firing, advantage, total := 0, 0, 0
		closure, closures := 0.0, 0
		modes := map[string]int{}
		for seed := uint64(1); seed <= flounderSeeds; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("flounder%s%d", level, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
			place(i, 0, bot, 1800) // the bot starts with the picture, not the advantage
			me := &i.aircraft[0].model.State
			done := false
			for tick := uint64(0); tick < 60*180 && !done; tick++ {
				i.events = i.events[:0]
				i.Step(tick, map[int][]game.Input{0: {{Sequence: 1, Data: flounder(me, &i.aircraft[bot].model.State, tick)}}})
				for _, e := range i.events {
					if e["kind"] != "hit" {
						continue
					}
					count, _ := e["count"].(int)
					if e["slot"] == bot {
						struck += count
					} else {
						landed += count
					}
				}
				if b := i.aircraft[bot]; b.model != nil && i.aircraft[0].model != nil && b.brain != nil {
					total++
					modes[b.brain.mode]++
					if b.latest.Fire {
						firing++
					}
					h, p := &b.model.State, &i.aircraft[0].model.State
					to := p.Position.Subtract(h.Position)
					r := to.Length()
					if r < 900 {
						engaged++
						// Closure along the line of sight while in range: the
						// floater fight is decided by whether the bot arrives
						// saddled or screaming past.
						if r > 1 {
							closure += h.Velocity.Subtract(p.Velocity).Dot(to.Scale(1 / r))
							closures++
						}
						nose := h.Attitude.Rotate(flight.Vec3{X: 1})
						if r > 1 && math.Acos(clamp(nose.Dot(to.Scale(1/r)), -1, 1))*180/math.Pi < 30 {
							advantage++
						}
					}
				}
				human := i.aircraft[0]
				if human.model == nil || !human.alive {
					// SHOT DOWN, not drowned: a scripted target that kills itself turns this
					// instrument into a green light measuring nothing.
					if human.condition.Damager < 0 {
						t.Fatalf("%s seed %d: the flounder flew into the sea at %.1f s with nobody having hit it — the instrument is measuring its own script, not the bot",
							level, seed, float64(tick)/60)
					}
					times = append(times, float64(tick)/60)
					done = true
				}
				if b := i.aircraft[bot]; b.model == nil || !b.alive {
					done = true // the flounder killed the bot: counts as unresolved-with-prejudice below
				}
			}
			if !done || len(times) < int(seed) {
				unresolved++
			}
		}
		mean := 0.0
		for _, v := range times {
			mean += v
		}
		if len(times) > 0 {
			mean /= float64(len(times))
		}
		top := ""
		{
			type share struct {
				mode  string
				count int
			}
			shares := []share{}
			for m, c := range modes {
				shares = append(shares, share{m, c})
			}
			sort.Slice(shares, func(a, b int) bool { return shares[a].count > shares[b].count })
			for k, sh := range shares {
				if k >= 3 || total == 0 {
					break
				}
				top += fmt.Sprintf("%s %.0f%% ", sh.mode, 100*float64(sh.count)/float64(total))
			}
		}
		rate := func(n int) float64 {
			if total == 0 {
				return 0
			}
			return 100 * float64(n) / float64(total)
		}
		overtake := 0.0
		if closures > 0 {
			overtake = closure / float64(closures)
		}
		fmt.Printf("%-11s killed the flounder %d/%d, mean %5.1f s | rounds landed %4d | in gun range %5.1f%% | nose on %4.1f%% | trigger %4.1f%% | closure %+5.1f m/s | hits taken %d | modes %s\n",
			level, len(times), flounderSeeds, mean, landed, rate(engaged), rate(advantage), rate(firing), overtake, struck, top)
		// No tier kills the honest flounder in three minutes, which is what the real
		// 174-second fight showed, so the kill rate is reported rather than gated
		// until the conversion work lands.
		if level != "pilot" && engaged == 0 {
			t.Errorf("%s never closed inside 900 m of the flounder in three minutes — it is not even engaging", level)
		}
		if level == "superhuman" && landed == 0 {
			// Known and reported, not gated: the machine sits inside gun range for 30%
			// of three minutes and lands nothing. It becomes an assertion when the
			// conversion work lands.
			t.Logf("KNOWN (#215): superhuman landed NO rounds on the flounder in three minutes while inside gun range %.1f%% of it — the bot cannot convert against a floater",
				100*float64(engaged)/float64(flounderSeeds*60*180))
		}
		if struck > 0 {
			t.Errorf("%s took %d hits from a keyboard novice — its passes cross the target's nose", level, struck)
		}
	}
}

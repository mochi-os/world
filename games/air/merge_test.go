package air

import (
	"fmt"
	"math"
	"sort"
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
			b := NewBandit(level, uint64(seed), 250000, "", false, false, "", 0, false)
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

// approach is what a run-in came to, up to the moment he crossed the bandit's
// 3/9 line: the plays it flew, the roll before the pass (a commanded guns defence
// left out, as TestMergeRoll leaves it out), the closest the two came, which side
// of the bandit's track he went by (world Z, +1 or -1), the merge plan, the
// range the lead turn began at (0 for none), and the play flown a second and a
// half on (the novice sees the pass a second late: its track of him lags).
type approach struct {
	plays               []string
	roll, closest, side float64
	began               float64
	plan, after         string
	reversed            bool   // inside 2 km it turned one way and then the other before the pass
	first               string // the plan as first chosen
	turned, chosen      int    // the tick his lead turn began, and the tick the bandit last chose its own
}

// approached flies a joust run-in from 5.5 km with the weapons held, as the
// client flies it: the bandit of the given tier at the given stage (on stages
// 6, 11 and 14 beneath it) against a jet at 220 m/s starting offset across its
// track. The pilot flies straight; or pursues, turning at 15 deg/s to hold his
// nose on the bandit all the way in, as the pilot of 01a0d4ad did; or flies
// straight to 1.6 km and then lead-turns at 12 deg/s toward the bandit's side of
// him or away from it, as the pilots of 01a0d516 and 01a0d51c (toward) and
// 01a0d50a (away) did, turning first. Committed, the bandit starts with a play a
// minute from running out, as a pass met in the middle of a fight can.
func approached(t *testing.T, level string, stage int, seed uint64, entry, offset float64, committed bool, pilot string) approach {
	t.Helper()
	b := NewBandit(level, seed, 250000, "", false, true, "fox2", 0, true)
	b.Stage(stage, 14208)
	b.Spawn(flight.Vec3{X: -2750, Y: 4572}, flight.Vec3{X: entry})
	if committed {
		b.craft.brain.play, b.craft.brain.until = "press", 60*60 // the pass must still hand the fight back
	}
	env := flight.Environment{Seed: seed, Wrap: 250000}
	pm := flight.New(aircraft.Get("fa18c"), env, flight.World{Sea: sea})
	pm.State = flight.Level(pm, flight.Vec3{X: 2750, Y: 4572, Z: offset}, flight.Vec3{X: -1}, 220, fuel)
	words := make([]float64, flight.Size)
	out, previous, passed := approach{closest: math.Inf(1)}, math.NaN(), -1
	sense, heading := 0.0, math.NaN() // the bandit's first turn inside 2 km, and its heading a tick ago
	lead := 0.0                       // the pilot's lead turn, once begun: +1 turning left, -1 right
	for tick := 0; tick < 60*25; tick++ {
		s, brain := &b.craft.model.State, b.craft.brain
		if (pilot == "toward" || pilot == "away") && passed < 0 {
			at := s.Position.Subtract(pm.State.Position)
			if lead == 0 && at.Length() < 1600 {
				out.turned = tick
				v := pm.State.Velocity
				lead = math.Copysign(1, v.X*at.Z-v.Z*at.X) // the bandit's side of my track
				if pilot == "away" {
					lead = -lead
				}
			}
			if lead != 0 {
				turn := lead * 12 * math.Pi / 180 / 60
				sin, cos := math.Sin(turn), math.Cos(turn)
				v := pm.State.Velocity
				pm.State.Velocity = flight.Vec3{X: v.X*cos - v.Z*sin, Z: v.X*sin + v.Z*cos}
				pm.State.Attitude = flight.Look(pm.State.Velocity.Normalize())
			}
		}
		if pilot == "pursuit" && passed < 0 {
			// Nose on the bandit, turning toward it at 15 deg/s: a hard turn at this
			// speed, where a velocity re-pointed every tick would be a jet no break
			// could ever open a pass from.
			at := s.Position.Subtract(pm.State.Position)
			at.Y = 0
			have, want := pm.State.Velocity.Normalize(), at.Normalize()
			if off := math.Acos(clamp(have.Dot(want), -1, 1)); off > 1e-9 {
				share := math.Min(1, 15*math.Pi/180/60/off)
				have = have.Scale(1 - share).Add(want.Scale(share)).Normalize()
			}
			pm.State.Velocity = have.Scale(220)
			pm.State.Attitude = flight.Look(have)
		}
		pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
		pm.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		line := pm.State.Position.Subtract(s.Position)
		if passed >= 0 {
			if tick-passed >= 90 {
				out.after = brain.play
				return out
			}
			continue
		}
		if r := line.Length(); r < out.closest {
			out.closest, out.side = r, math.Copysign(1, line.Z)
		}
		if out.first == "" && brain.meet.decided {
			out.first = brain.plan
		}
		if brain.turning != 0 && out.began == 0 {
			out.began = line.Length()
		}
		if now := math.Atan2(s.Velocity.Z, s.Velocity.X); line.Length() < 2000 && !math.IsNaN(heading) {
			if rate := math.Remainder(now-heading, 2*math.Pi) * 60 * 180 / math.Pi; math.Abs(rate) > 4 {
				if sense != 0 && math.Signbit(rate) != math.Signbit(sense) {
					out.reversed = true
				}
				sense = rate
			}
		}
		heading = math.Atan2(s.Velocity.Z, s.Velocity.X)
		if line.Normalize().Dot(s.Velocity.Normalize()) < 0 {
			passed, out.plan, out.chosen = tick, brain.plan, int(brain.meet.chosen)
			continue
		}
		if len(out.plays) == 0 || out.plays[len(out.plays)-1] != brain.play {
			out.plays = append(out.plays, brain.play)
		}
		up, right := s.Attitude.Rotate(flight.Vec3{Y: 1}), s.Attitude.Rotate(flight.Vec3{Z: 1})
		bank := math.Atan2(right.Y, up.Y) * 180 / math.Pi
		if !math.IsNaN(previous) && uint64(tick) >= brain.dodge {
			out.roll += math.Abs(math.Mod(bank-previous+540, 360) - 180)
		}
		previous = bank
	}
	t.Fatalf("%s stage %d seed %d: no pass in 25 s", level, stage, seed)
	return out
}

// TestMergeHeld: at stage 15 every tier flies the run-in as one committed pass
// (#19), and passes the pilot at 150 m or more (a training merge's bubble: the
// first form passed one at 19 m), whether he flies straight, points at the
// bandit all the way in, or lead-turns first at 1.6 km toward the bandit's side
// or away from it. Against a pilot turning first the instructor tiers choose
// their lead turn again, after his began, and turn one way only inside 2 km:
// 01a0d516's pilot turned toward it first and was passed at 115 m, and
// 01a0d51c's bandit re-aimed away from a pilot turning onto its line and then
// rolled 150 degrees into a lead turn toward him. A pilot who turns away that
// hard at 1.6 km opens the range before any pass, and the merge is released as
// spent. The side is the one the run-in gives when he starts
// 300 m off its track; head-on it is drawn or rehearsed, and both sides turn up
// at every tier, so no pilot can count on one. The ace holds a line where the
// arbiter rolled 250-420 degrees (the stage 14 control); the pilot's lead turn
// starts at a range that varies from seed to seed, sometimes after the pass; the novice sometimes holds
// through the pass and sometimes turns away from him two kilometres out; and a
// second and a half after the pass the arbiter has the fight back.
func TestMergeHeld(t *testing.T) {
	sides := map[string]map[float64]bool{}
	pilots, novice := []float64{}, map[string]bool{}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		sides[level] = map[float64]bool{}
		for seed := uint64(1); seed <= 8; seed++ {
			for _, offset := range []float64{0, 300, -300} {
				for _, pilot := range []string{"straight", "pursuit", "toward", "away"} {
					if pilot == "pursuit" && (offset != 0 || seed > 3) || (pilot == "toward" || pilot == "away") && seed > 3 {
						continue
					}
					got := approached(t, level, 15, seed, 220, offset, true, pilot)
					name := fmt.Sprintf("%s seed %d offset %+.0f pilot %s", level, seed, offset, pilot)
					if got.closest < 150 {
						t.Errorf("%s: passed %.0f m from him, inside the bubble", name, got.closest)
					}
					if offset != 0 && pilot != "toward" && pilot != "away" && got.side != math.Copysign(1, offset) {
						t.Errorf("%s: he went by on %+.0f, not the side the run-in gave", name, got.side)
					}
					if offset == 0 && pilot == "straight" {
						sides[level][got.side] = true
					}
					if got.after == "merge" {
						t.Errorf("%s: still flying the merge a second and a half after the pass", name)
					}
					if level == "ace" && pilot != "away" && (len(got.plays) != 1 || got.plays[0] != "merge") {
						t.Errorf("%s: flew %v before the pass, not one committed merge", name, got.plays)
					}
					if level == "ace" && offset != 0 && pilot == "straight" && got.roll > 200 {
						t.Errorf("%s: rolled %.0f degrees before the pass", name, got.roll)
					}
					if (level == "ace" || level == "superhuman") && (pilot == "toward" || pilot == "away") && got.chosen <= got.turned {
						t.Errorf("%s: its lead turn was chosen at %.1f s, before his began at %.1f s, and not again", name, float64(got.chosen)/60, float64(got.turned)/60)
					}
					if (level == "ace" || level == "superhuman") && got.reversed {
						t.Errorf("%s: turned one way and then the other inside 2 km of the pass (plan %q)", name, got.plan)
					}
					if level == "pilot" && offset == 0 && pilot == "straight" {
						pilots = append(pilots, got.began)
					}
					if level == "novice" && offset == 0 && pilot == "straight" {
						switch {
						case got.plan == "late" && got.began == 0:
							novice["late"] = true
						case got.plan == "one" && got.began > 1800:
							novice["early"] = true
						default:
							t.Errorf("%s: plan %q with the lead turn begun at %.0f m: neither late nor early", name, got.plan, got.began)
						}
					}
				}
			}
		}
		if len(sides[level]) != 2 {
			t.Errorf("%s: head-on, eight seeds all passed him on the same side", level)
		}
	}
	sort.Float64s(pilots)
	if len(pilots) == 0 || pilots[len(pilots)-1]-pilots[0] < 200 {
		t.Errorf("the pilot's lead turn began at %v m (0: after the pass, its track of him half a second old): its timing does not vary", pilots)
	}
	if !novice["late"] || !novice["early"] {
		t.Errorf("the novice's merges over eight seeds were only %v", novice)
	}
	for seed := uint64(1); seed <= 3; seed++ {
		got := approached(t, "ace", 14, seed, 220, 300, false, "straight")
		if len(got.plays) < 2 || got.roll < 200 {
			t.Errorf("stage 14 seed %d flew %v and rolled %.0f degrees before the pass: the control no longer shows the run-in dithering, so this test has lost its reference", seed, got.plays, got.roll)
		}
	}
}

// TestMergeCalledOff: a jet that turns away during the run-in ends the merge
// (#19). Held on a jet that has left, the merge would fly a pursuit with the
// arbiter shut out: once he is heading more than a kilometre wide of my line it
// turns toward him, so the range keeps closing and he stays ahead of my 3/9
// line, and neither of those releases ever comes. He turns 90 degrees at 20
// deg/s from 3.5 km, well before the lead-turn range; the arbiter must have the
// fight back within three seconds.
func TestMergeCalledOff(t *testing.T) {
	for seed := uint64(1); seed <= 3; seed++ {
		b := NewBandit("ace", seed, 250000, "", false, true, "fox2", 0, true)
		b.Stage(15, 14208)
		b.Spawn(flight.Vec3{X: -2750, Y: 4572}, flight.Vec3{X: 220})
		env := flight.Environment{Seed: seed, Wrap: 250000}
		pm := flight.New(aircraft.Get("fa18c"), env, flight.World{Sea: sea})
		pm.State = flight.Level(pm, flight.Vec3{X: 2750, Y: 4572, Z: 300}, flight.Vec3{X: -1}, 220, fuel)
		words := make([]float64, flight.Size)
		heading, turned, merged := math.Pi, -1, false // his course, radians from +X
		for tick := 0; tick < 60*20; tick++ {
			s, brain := &b.craft.model.State, b.craft.brain
			if turned < 0 && pm.State.Position.Subtract(s.Position).Length() < 3500 {
				turned = tick
			}
			if turned >= 0 && heading > math.Pi/2 {
				heading -= 20 * math.Pi / 180 / 60
			}
			pm.State.Velocity = flight.Vec3{X: 220 * math.Cos(heading), Z: 220 * math.Sin(heading)}
			pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
			pm.State.Encode(words)
			b.Mirror(words, false, true)
			b.Step()
			merged = merged || brain.play == "merge"
			if turned >= 0 && tick-turned == 180 {
				t.Logf("seed %d: three seconds after he turned away, flying %q, merge held %v", seed, brain.play, brain.merging)
				if !merged {
					t.Fatalf("seed %d: the run-in was never flown as a merge, so this test proves nothing", seed)
				}
				if brain.play == "merge" || brain.merging {
					t.Errorf("seed %d: still flying the merge three seconds after he turned away", seed)
				}
				break
			}
		}
	}
}

// TestMergeRunInOnly: stage 15 commits only the run-in, while the joust still
// holds weapons. A pass met once the fight is on is the fight, and the
// arbiter's: held there, against the scripted mush, the merge flew its line
// through a second nose-to-nose pass and left the bandit extending tail-on to a
// heater. With weapons free from the start the same run-in is never a merge.
func TestMergeRunInOnly(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, true, "fox2", 0, false) // no hold: weapons free, as after the merge
	b.Stage(15, 14208)
	b.Spawn(flight.Vec3{X: -2750, Y: 4572}, flight.Vec3{X: 220})
	env := flight.Environment{Seed: 1, Wrap: 250000}
	pm := flight.New(aircraft.Get("fa18c"), env, flight.World{Sea: sea})
	pm.State = flight.Level(pm, flight.Vec3{X: 2750, Y: 4572, Z: 300}, flight.Vec3{X: -1}, 220, fuel)
	words := make([]float64, flight.Size)
	for tick := 0; tick < 60*12; tick++ {
		pm.State.Position.X -= 220.0 / 60
		pm.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		if b.craft.brain.play == "merge" || b.craft.brain.merging {
			t.Fatalf("flying a committed merge at %.1f s with weapons free", float64(tick)/60)
		}
	}
}

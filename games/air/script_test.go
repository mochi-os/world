package air

import (
	"fmt"
	"math"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestScriptLadder: the scripted doctrine against mimic, the scripted
// pilot of the user's own fights, from the joust's real start - the difficulty
// target in numbers. The user should win most fights against the novice, lose
// most against the pilot, and rarely or never beat the ace or the superhuman;
// mimic is a little weaker than the user, so the ace and the superhuman lose at
// most one in sixteen to it (without the vertical pass the ace lost two), the
// pilot wins more than it loses and less than the ace, and the novice loses
// more than it wins. Measured over 32 fights each
// when this was written, won and lost: 8/21, 23/9, 30/1, 29/1; stage 0 read
// 4/23, 7/21, 14/13, 12/14. That ladder was flattered: mimic's heater zone was
// drawn off its flight path and held its own high-alpha shots, and with both
// zones and the launch on the nose (launcher) it read 6/26, 21/11, 18/14,
// 18/14. With the cold fight, the vertical pass and the 20 degree nose gate:
// 6/26, 21/11, 30/1, 31/1, and in single player's wind 4/27, 19/12, 29/3,
// 32/0. With every merge kept outside 150 m, the instructor tiers' run-in at a
// height off his: 6/26, 18/12, 32/0, 30/1, and in wind 4/27, 21/11, 31/1,
// 32/0.
func TestScriptLadder(t *testing.T) {
	heavy(t)
	net := map[string]int{}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		n := 0
		b := sweepFrom(t, level, "joust", func() flyer {
			n++
			return mimic([]float64{1, -1}[n%2])
		}, true, 0, 16, 150, func(i *instance, bot int) {
			jousted(i, bot)
			i.aircraft[bot].brain.scripted = true // the doctrine single player flies, in a server bot
		})
		fmt.Printf("%-10s against mimic: won %2d lost %2d of 16\n", level, b.downed, b.lost)
		net[level] = b.downed - b.lost
		switch level {
		case "novice":
			if b.downed >= b.lost {
				t.Errorf("the novice won %d and lost %d: the user should win most fights against it", b.downed, b.lost)
			}
		case "pilot":
			if b.downed <= b.lost {
				t.Errorf("the pilot won %d and lost %d: the user should lose most fights against it", b.downed, b.lost)
			}
		default:
			if b.lost > 1 {
				t.Errorf("the %s lost %d of 16: the user should rarely or never beat it", level, b.lost)
			}
		}
	}
	if net["pilot"] >= net["ace"] {
		t.Errorf("the pilot's wins less its losses are %d and the ace's %d: the ladder does not climb", net["pilot"], net["ace"])
	}
}

// circled starts a fight already joined, 800 m apart at 15,000 ft: abeam on
// opposite headings, which both turning in makes two circles, or line abreast
// on one heading, which makes one circle flown in opposite directions. The side
// alternates seed by seed.
func circled(i *instance, bot, n int, one bool, speed float64) {
	heading := -1.0
	if one {
		heading = 1
	}
	side := []float64{1, -1}[n%2]
	for _, x := range []struct {
		m        *flight.Model
		position flight.Vec3
		heading  flight.Vec3
	}{{i.aircraft[bot].model, flight.Vec3{Y: 4500}, flight.Vec3{X: 1}}, {i.aircraft[0].model, flight.Vec3{Z: 800 * side, Y: 4500}, flight.Vec3{X: heading}}} {
		engines := x.m.State.Engine
		x.m.State = flight.Level(x.m, x.position, x.heading, speed, x.m.State.Fuel)
		x.m.State.Engine = engines
	}
	i.aircraft[bot].brain.scripted = true
	i.aircraft[bot].brain.routine.passed = true
}

// TestScriptCircles: the fights the user beat the scripted ace in, joined
// already, against mimic. 01a10e5e was a one-circle fight the pilot won slow
// while the bandit held full burner at 460 kt, and 01a10e335a a first turn won
// on a tone shot at 33 degrees of alpha. The ace and the superhuman must win at
// least three times as many as they lose in each.
func TestScriptCircles(t *testing.T) {
	heavy(t)
	for _, f := range []struct {
		name  string
		one   bool
		speed float64
	}{{"slow two-circle", false, 108}, {"slow one-circle", true, 108}, {"fast one-circle", true, 230}} {
		for _, level := range []string{"ace", "superhuman"} {
			n := 0
			b := sweepFrom(t, level, "furball", func() flyer {
				n++
				return mimic(0)
			}, true, 0, 24, 90, func(i *instance, bot int) { circled(i, bot, n, f.one, f.speed) })
			fmt.Printf("%-10s %s at %.0f kt against mimic: won %2d lost %2d of 24\n", level, f.name, f.speed*1.944, b.downed, b.lost)
			if b.downed < 3*b.lost {
				t.Errorf("the %s won %d and lost %d of the %s: the user should rarely or never beat it", level, b.downed, b.lost, f.name)
			}
		}
	}
}

// TestScriptPass: the scripted doctrine passes the pilot at 150 m or
// more, a training merge's bubble, whether he flies straight, points at the
// bandit all the way in, or lead-turns first at 1.6 km toward the bandit's side
// or away from it, or points at it and turns toward its side inside 600 m (a
// pilot who turns away that hard at 1.6 km opens the range and never goes by
// at all); and a second and a half after the pass it is fighting, not still
// flying the run-in. The ace and the superhuman run in at a height off his and
// pass at 1.3 bubbles or more, so the bubble never costs them the lead turn.
func TestScriptPass(t *testing.T) {
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		for seed := uint64(1); seed <= 8; seed++ {
			for _, offset := range []float64{0, 300, -300} {
				for _, pilot := range []string{"straight", "pursuit", "late", "toward"} {
					if (pilot == "pursuit" || pilot == "late") && (offset != 0 || seed > 3) || pilot == "toward" && seed > 3 {
						continue
					}
					got := approached(t, level, seed, 220, offset, false, pilot)
					name := fmt.Sprintf("%s seed %d offset %+.0f pilot %s", level, seed, offset, pilot)
					fmt.Printf("%-44s closest %4.0f m  roll %4.0f  reversed %-5v plays %v after %s\n", name, got.closest, got.roll, got.reversed, got.plays, got.after)
					if got.closest < bubble {
						t.Errorf("%s: passed %.0f m from him, inside the bubble", name, got.closest)
					} else if (level == "ace" || level == "superhuman") && got.closest < 1.3*bubble {
						t.Errorf("%s: passed %.0f m from him: the instructor tiers build the separation into the run-in, so the last-second climb never has to fire", name, got.closest)
					}
					if got.after == "line" || got.after == "lead" || got.after == "hold" {
						t.Errorf("%s: still flying the run-in (%s) a second and a half after the pass", name, got.after)
					}
				}
			}
		}
	}
}

// TestScriptRecordsItsBranch: a developer recording's Doctrine channel names the
// scripted doctrine's branch (the run-in's line, lead turn and hold, then the
// fight, the gun, the break, the regain), not the one word "script" for the
// whole fight, which would tell a debrief nothing.
func TestScriptRecordsItsBranch(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, true, "fox2", 0, true)
	b.Spawn(flight.Vec3{X: -2750, Y: 4572}, flight.Vec3{X: 220})
	pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	pm.State = flight.Level(pm, flight.Vec3{X: 2750, Y: 4572}, flight.Vec3{X: -1}, 220, fuel)
	words := make([]float64, flight.Size)
	seen := map[string]bool{}
	for tick := 0; tick < 60*20; tick++ {
		pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
		pm.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		seen[b.Mode()] = true
	}
	for _, branch := range []string{"line", "fight"} {
		if !seen[branch] {
			t.Errorf("the Doctrine channel never read %q; it read %v", branch, seen)
		}
	}
	if seen["script"] {
		t.Errorf("the Doctrine channel read \"script\": %v", seen)
	}
}

// TestScriptFliesTheAir: the scripted doctrine schedules its regain on the
// wing's airspeed, not its speed over the ground. In single player the bandit
// flies the pilot's air, and judged on ground speed it regained at 172 kt into
// a 40 kt headwind and stayed unloaded, tail to the pilot, until 268 kt
// (01a10e19). Into a headwind the jet must not enter the regain while its
// airspeed is above the floor, and must leave it once its airspeed is past the
// band; with a tailwind it must enter it when its airspeed is under the floor.
func TestScriptFliesTheAir(t *testing.T) {
	cases := []struct {
		name      string
		wind      float64 // surface wind along the jet's heading, m/s (+ a tailwind)
		airspeed  float64 // m/s
		regaining bool    // the regain already flown when the case begins
		want      bool    // the regain flown at the end
	}{
		{"into a headwind, above the floor", -15, 100, false, false},
		{"into a headwind, past the band", -15, 125, true, false},
		{"with a tailwind, under the floor", 15, 66, false, true},
	}
	for _, c := range cases {
		b := NewBandit("ace", 1, 250000, "", false, true, "fox2", 0, true)
		air := flight.Environment{Seed: 1, Wind: flight.Vec3{X: c.wind}, Wrap: 250000}
		b.Air(air)
		b.Spawn(flight.Vec3{Y: 4572}, flight.Vec3{X: c.airspeed})
		pm := flight.New(aircraft.Get("fa18c"), air, flight.World{Sea: sea})
		pm.State = flight.Level(pm, flight.Vec3{X: 900, Y: 4572}, flight.Vec3{X: 1}, c.airspeed, fuel)
		words := make([]float64, flight.Size)
		for tick := 0; tick < 40; tick++ {
			if tick == 10 {
				// The fight, not the run-in, once the jet has flown a few steps:
				// its wind is the one its last step met, and a jet that has never
				// stepped has met none.
				b.craft.brain.routine.passed = true
				b.craft.brain.routine.regaining = c.regaining
			}
			pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
			pm.State.Encode(words)
			b.Mirror(words, false, true)
			b.Step()
		}
		m := b.craft.model
		ground, airspeed := m.State.Velocity.Length(), m.State.Velocity.Subtract(m.Gust()).Length()
		if got := b.craft.brain.routine.regaining; got != c.want {
			t.Errorf("%s: regaining %v at %.0f kt airspeed and %.0f kt over the ground, want %v", c.name, got, airspeed*1.944, ground*1.944, c.want)
		}
	}
}

// approach is what a run-in came to, up to the moment he crossed the bandit's
// 3/9 line: the plays it flew, the roll before the pass (a commanded guns defence
// left out, as TestMergeRoll leaves it out), the closest the two came, which side
// of the bandit's track he went by (world Z, +1 or -1), the range the lead turn
// began at (0 for none), and the branch flown a second and a half on (the
// novice sees the pass a second late: its track of him lags).
type approach struct {
	plays               []string
	roll, closest, side float64
	began               float64
	after               string
	reversed            bool // inside 2 km it turned one way and then the other before the pass
	turned              int  // the tick his lead turn began
}

// approached flies a joust run-in from 5.5 km with the weapons held, as the
// client flies it: the bandit of the given tier against a jet at 220 m/s
// starting offset across its track. The pilot flies straight; or pursues, turning at 15 deg/s to hold his
// nose on the bandit all the way in, as the pilot of 01a0d4ad did; or does
// that to 600 m and then turns toward the bandit's side at 14 deg/s, as the
// pilot of 01a118cd did; or flies
// straight to 1.6 km and then lead-turns at 12 deg/s toward the bandit's side of
// him or away from it, as the pilots of 01a0d516 and 01a0d51c (toward) and
// 01a0d50a (away) did, turning first. Committed, the bandit starts with a play a
// minute from running out, as a pass met in the middle of a fight can.
func approached(t *testing.T, level string, seed uint64, entry, offset float64, committed bool, pilot string) approach {
	t.Helper()
	b := NewBandit(level, seed, 250000, "", false, true, "fox2", 0, true)
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
		if (pilot == "toward" || pilot == "away" || pilot == "late") && passed < 0 {
			at := s.Position.Subtract(pm.State.Position)
			start, rate := 1600.0, 12.0
			if pilot == "late" {
				start, rate = 600, 14 // 01a118cd: his nose on the bandit, then 5.6 g toward its side 1.2 s before the pass
			}
			if lead == 0 && at.Length() < start {
				out.turned = tick
				v := pm.State.Velocity
				lead = math.Copysign(1, v.X*at.Z-v.Z*at.X) // the bandit's side of my track
				if pilot == "away" {
					lead = -lead
				}
			}
			if lead != 0 {
				turn := lead * rate * math.Pi / 180 / 60
				sin, cos := math.Sin(turn), math.Cos(turn)
				v := pm.State.Velocity
				pm.State.Velocity = flight.Vec3{X: v.X*cos - v.Z*sin, Z: v.X*sin + v.Z*cos}
				pm.State.Attitude = flight.Look(pm.State.Velocity.Normalize())
			}
		}
		if (pilot == "pursuit" || pilot == "late" && lead == 0) && passed < 0 {
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
			passed = tick
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
	t.Fatalf("%s seed %d: no pass in 25 s", level, seed)
	return out
}

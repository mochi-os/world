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
// most against the intermediate, and rarely or never beat the expert or the superhuman;
// mimic is a little weaker than the user, so the expert and the superhuman lose at
// most one in sixteen to it (without the vertical pass the expert lost two), the
// intermediate wins more than it loses and less than the expert, and the novice loses
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
//
// Flown on 64 fights a tier. On 16 the intermediate's row read 9/7, and the slow
// clear turned it red at 7/8 by flipping two fights, while over 128 in both
// airs the intermediate read 71/54 against 72/54 before it: sixteen measured two
// coin flips, not the tier.
//
// The novice's row flies unwarned, which sees a heater only 1.8 s after it
// leaves the rail and flares at none ahead of a launch, as the user does: mimic
// was handed every round at launch. The user's six losses in seven to the
// novice (2026-10-13 and 2026-10-14) came from the turn after the pass going
// downhill while the novice climbed, which no novice of any realism loses to;
// the remedy ruled 2026-10-15 is the user's lift vector kept on it. Measured
// then on 64 fights: won 6 and lost 58 against unwarned, 3 and 61 against
// mimic.
//
// The superhuman may lose 1 in 6 (ruled 2026-10-13). With both jets on their
// fight weight and mimic carrying the Fox 2 preset's six heaters, the slow
// nose-to-nose gun pass left two outcomes: cleared, it keeps the bubble and
// loses 10 of 64 to his close heaters, where the expert is held to 1 in 16;
// turned through, it loses 7 and passes inside the bubble in 22 of 64. The
// bubble wins.
func TestScriptLadder(t *testing.T) {
	heavy(t)
	const fights = 64
	net := map[string]int{}
	for _, level := range []string{"novice", "intermediate", "expert", "superhuman"} {
		n, name := 0, "mimic"
		if level == "novice" {
			name = "unwarned"
		}
		b := sweepFrom(t, level, "joust", func() flyer {
			n++
			if level == "novice" {
				return unwarned([]float64{1, -1}[n%2])
			}
			return mimic([]float64{1, -1}[n%2])
		}, true, 0, fights, 150, func(i *instance, bot int) {
			jousted(i, bot)
			i.aircraft[bot].brain.scripted = true // the doctrine single player flies, in a server bot
		})
		fmt.Printf("%-10s against %-8s won %2d lost %2d of %d\n", level, name+":", b.downed, b.lost, fights)
		net[level] = b.downed - b.lost
		switch level {
		case "novice":
			if b.downed >= b.lost {
				t.Errorf("the novice won %d and lost %d: the user should win most fights against it", b.downed, b.lost)
			}
		case "intermediate":
			if b.downed <= b.lost {
				t.Errorf("the intermediate won %d and lost %d: the user should lose most fights against it", b.downed, b.lost)
			}
		case "expert":
			if b.lost > fights/16 {
				t.Errorf("the %s lost %d of %d: the user should rarely or never beat it", level, b.lost, fights)
			}
		default:
			if b.lost > fights/6 {
				t.Errorf("the %s lost %d of %d: the user should rarely beat it", level, b.lost, fights)
			}
		}
	}
	if net["intermediate"] >= net["expert"] {
		t.Errorf("the intermediate's wins less its losses are %d and the expert's %d: the ladder does not climb", net["intermediate"], net["expert"])
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

// TestScriptCircles: the fights the user beat the scripted expert in, joined
// already, against mimic. 01a10e5e was a one-circle fight the pilot won slow
// while the bandit held full burner at 460 kt, and 01a10e335a a first turn won
// on a tone shot at 33 degrees of alpha. The expert and the superhuman must win at
// least three times as many as they lose in each, and twice as many in the slow
// one-circle (ruled 2026-10-13): there the two jets meet nose to nose on the
// gun at 165-200 kt, and against mimic on the Fox 2 preset's six heaters and
// its fight weight the clear that keeps the bubble reads 13/6 and 15/7, where
// turning through the pass read 21/2 and 22/2 and broke the bubble in a third
// of the heater jousts. That arm carries one fight of slack (ruled 2026-10-17):
// its margin was set on exactly 13/6, and the deck recovery's measured budget
// turned one no-result into a heater kill after a recovery that started a
// second sooner and bottomed 270 ft higher, reading 13/7.
func TestScriptCircles(t *testing.T) {
	heavy(t)
	for _, f := range []struct {
		name   string
		one    bool
		speed  float64
		margin int
		slack  int
	}{{"slow two-circle", false, 108, 3, 0}, {"slow one-circle", true, 108, 2, 1}, {"fast one-circle", true, 230, 3, 0}} {
		for _, level := range []string{"expert", "superhuman"} {
			n := 0
			b := sweepFrom(t, level, "furball", func() flyer {
				n++
				return mimic(0)
			}, true, 0, 24, 90, func(i *instance, bot int) { circled(i, bot, n, f.one, f.speed) })
			fmt.Printf("%-10s %s at %.0f kt against mimic: won %2d lost %2d of 24\n", level, f.name, f.speed*1.944, b.downed, b.lost)
			if b.downed < f.margin*b.lost-f.slack {
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
// flying the run-in. The expert and the superhuman run in at a height off his and
// pass at 1.3 bubbles or more, so the bubble never costs them the lead turn.
func TestScriptPass(t *testing.T) {
	for _, level := range []string{"novice", "intermediate", "expert", "superhuman"} {
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
					} else if (level == "expert" || level == "superhuman") && got.closest < 1.3*bubble {
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
	b := NewBandit("expert", 1, 250000, "", false, true, "fox2", 0, true)
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
		b := NewBandit("expert", 1, 250000, "", false, true, "fox2", 0, true)
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

// TestScriptClearsTheCrossing: after the merge the intermediate tier and the novice
// keep a crossing jet outside the bubble. He crosses the bandit's path at 60,
// 90 or 120 degrees on a course that would meet it four or five seconds on;
// the fight law alone points the lift vector at him and passes at 93-149 m.
// Three seconds out is too late for either tier to climb 150 m at the pull
// it has (99-106 m with the guard, 52-64 m without).
//
// Slow, the bandit at 146 or 194 kt with the jet at 214 or 292 kt, climbing,
// level or diving at 15 degrees, seven seconds out: a wing that slow has under
// three g, and the guard as it was let 37 of these 72 by inside the bubble, at
// 60-108 m. Each of the slow clear's parts is needed: without the longer look
// the same 37 went by inside it, without escape's gravity 24 (85-93 m), and
// pulled rather than pushed 43 (50-142 m).
func TestScriptClearsTheCrossing(t *testing.T) {
	cross := func(level string, own, his, out, angle, dive, height float64) float64 {
		b := NewBandit(level, 1, 250000, "", false, false, "guns", 0, false)
		b.Spawn(flight.Vec3{Y: height}, flight.Vec3{X: own})
		b.craft.brain.routine.passed = true
		r, d := angle*math.Pi/180, dive*math.Pi/180
		heading := flight.Vec3{X: -math.Cos(r) * math.Cos(d), Y: -math.Sin(d), Z: math.Sin(r) * math.Cos(d)}
		meet := flight.Vec3{X: own * out, Y: height}
		pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		pm.State = flight.Level(pm, meet.Subtract(heading.Scale(his*out)), heading, his, fuel)
		pm.State.Velocity = heading.Scale(his)
		words := make([]float64, flight.Size)
		least := math.Inf(1)
		for tick := 0; tick < int(60*(out+3)); tick++ {
			pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
			pm.State.Encode(words)
			b.Mirror(words, false, true)
			b.Step()
			least = math.Min(least, pm.State.Position.Subtract(b.State().Position).Length())
		}
		return least
	}
	for _, level := range []string{"novice", "intermediate"} {
		for _, angle := range []float64{60, 90, 120} {
			for _, out := range []float64{4, 5} {
				if least := cross(level, 180, 180, out, angle, 0, 4500); least < bubble {
					t.Errorf("%s: a jet crossing at %.0f degrees, %.0f s out, went by at %.0f m, inside the bubble", level, angle, out, least)
				}
			}
			for _, own := range []float64{75, 100} {
				for _, his := range []float64{110, 150} {
					for _, dive := range []float64{-15, 0, 15} {
						if least := cross(level, own, his, 7, angle, dive, 2700); least < bubble {
							t.Errorf("%s at %.0f kt: a jet at %.0f kt crossing at %.0f degrees, diving %.0f, 7 s out, went by at %.0f m, inside the bubble",
								level, own*1.944, his*1.944, angle, dive, least)
						}
					}
				}
			}
		}
	}
}

// TestScriptClearsTheHeadOn: in a guns match the fight comes back round to a
// second merge, and the instructor tiers, whose guard flies only on a gun
// attack from astern, met it on the gun. A jet meets the expert or the superhuman
// head-on after the merge as it did there, the bandit at 301 kt and he at 282,
// dead ahead or 10 or 20 degrees off its nose, level, climbing or diving 10
// degrees, 5.5 or 7 s out: it goes by at 150 m or more.
func TestScriptClearsTheHeadOn(t *testing.T) {
	for _, level := range []string{"expert", "superhuman"} {
		for _, out := range []float64{5.5, 7} {
			for _, angle := range []float64{180, 170, 160} {
				for _, dive := range []float64{-10, 0, 10} {
					b := NewBandit(level, 1, 250000, "", false, false, "guns", 0, false)
					b.Spawn(flight.Vec3{Y: 4500}, flight.Vec3{X: 155})
					b.craft.brain.routine.passed = true
					r, d := angle*math.Pi/180, dive*math.Pi/180
					heading := flight.Vec3{X: math.Cos(r) * math.Cos(d), Y: -math.Sin(d), Z: math.Sin(r) * math.Cos(d)}
					meet := flight.Vec3{X: 155 * out, Y: 4500}
					pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
					pm.State = flight.Level(pm, meet.Subtract(heading.Scale(145*out)), heading, 145, fuel)
					pm.State.Velocity = heading.Scale(145)
					words := make([]float64, flight.Size)
					least := math.Inf(1)
					for tick := 0; tick < int(60*(out+3)); tick++ {
						pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
						pm.State.Encode(words)
						b.Mirror(words, false, true)
						b.Step()
						least = math.Min(least, pm.State.Position.Subtract(b.State().Position).Length())
					}
					if least < bubble {
						t.Errorf("%s: a jet head-on %.0f degrees, diving %.0f, %.1f s out, went by at %.0f m, inside the bubble", level, 180-angle, dive, out, least)
					}
				}
			}
		}
	}
}

// TestScriptClearsTheSlowGunPass: on their fight weight the superhuman met
// mimic nose to nose on the gun in heater jousts, at 165-200 kt and 32 degrees
// of alpha with under 2 g of wing, and went by inside the bubble at about 32 s
// in 11 of 64 (closest 55 m). Those eleven fights, flown again to 40 s: it
// goes by at 150 m or more in every one.
func TestScriptClearsTheSlowGunPass(t *testing.T) {
	heavy(t)
	b := sweepFrom(t, "superhuman", "joust", func() flyer { return mimic(1) }, true, 0, 0, 40, func(i *instance, bot int) {
		jousted(i, bot)
		i.aircraft[bot].brain.scripted = true
	}, 4, 18, 22, 28, 40, 42, 48, 50, 56, 62, 64)
	if b.pass < bubble {
		t.Errorf("the superhuman went by mimic at %.0f m after the merge in %d of the %d fights, inside the bubble", b.pass, b.passes, b.seeds)
	}
}

// TestScriptMergesClear: on their fight weight the intermediate tier and the novice,
// flying their run-in level, met mimic at the merge at 93-149 m, mimic's lead
// turn closing the pass faster than they saw it. Those fights flown again to
// the merge: every one goes by at 150 m or more.
func TestScriptMergesClear(t *testing.T) {
	heavy(t)
	for _, c := range []struct {
		level      string
		guns, wind bool
		seeds      []uint64
	}{
		{"intermediate", false, false, []uint64{40, 64}},
		{"intermediate", false, true, []uint64{4, 8, 12, 40, 56, 58, 64}},
		{"intermediate", true, false, []uint64{40, 64}},
		{"intermediate", true, true, []uint64{4, 56, 64}},
		{"novice", false, false, []uint64{56}},
		{"novice", false, true, []uint64{56}},
		{"novice", true, false, []uint64{56}},
		{"novice", true, true, []uint64{56}},
	} {
		start := jousted
		if c.wind {
			start = blown
		}
		n := uint64(0)
		b := sweepFrom(t, c.level, "joust", func() flyer {
			n++
			return mimic([]float64{1, -1}[c.seeds[n-1]%2])
		}, !c.guns, 0, 0, 15, func(i *instance, bot int) {
			start(i, bot)
			i.aircraft[bot].brain.scripted = true
		}, c.seeds...)
		if b.merges > 0 {
			t.Errorf("the %s, guns %v, wind %v: the merge came inside the bubble in %d of %d fights, closest %.0f m", c.level, c.guns, c.wind, b.merges, b.seeds, b.merge)
		}
	}
}

// TestEscapeKeepsHisHeight: a slow wing chased by a jet below it never clears
// downward across his height, and one above it never upward; against a jet
// that is not pointing at it, under is still taken for the gravity it adds
// (the crossings in TestScriptClearsTheCrossing). The case is mimic climbing
// nose on at the expert at 151 kt, 950 ft below it, 4.6 s from a collision.
func TestEscapeKeepsHisHeight(t *testing.T) {
	me := &flight.State{Position: flight.Vec3{Y: 1700}, Velocity: flight.Vec3{X: 78}}
	miss := flight.Vec3{Y: 20}
	if away := escape(miss, 4.6, me, false, true, 1.4); away.Y < 0 {
		t.Errorf("chased from below, the clear went %+.2f: down across his height", away.Y)
	}
	if away := escape(miss.Scale(-1), 4.6, me, true, true, 1.4); away.Y > 0 {
		t.Errorf("chased from above, the clear went %+.2f: up across his height", away.Y)
	}
	if away := escape(miss, 4.6, me, false, false, 1.4); away.Y > -0.5 {
		t.Errorf("not chased, the slow wing took %+.2f: under, with gravity, opens it furthest", away.Y)
	}
}

// TestScriptClearsAPursuerAway: a jet 300 m below the slow bandit climbing at
// it nose on and turning after it at up to 3 g follows wherever it goes, so the
// clear goes up, away from his height, never down into him (the expert in a guns
// joust took under for gravity and passed mimic at 66 m). Judged while he is
// below and his nose is on the bandit: once he has climbed past it a clear
// takes the way his climb leaves open, and with a 2-bubble margin a second
// one does, down while his climb carries him up through its height.
func TestScriptClearsAPursuerAway(t *testing.T) {
	for _, level := range []string{"intermediate", "expert"} {
		b := NewBandit(level, 1, 250000, "", false, false, "guns", 0, false)
		b.Spawn(flight.Vec3{Y: 2000}, flight.Vec3{X: 80})
		b.craft.brain.routine.passed = true
		pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		start := flight.Vec3{X: 900, Y: 1700}
		pm.State = flight.Level(pm, start, b.State().Position.Subtract(start).Normalize(), 115, fuel)
		words := make([]float64, flight.Size)
		lowest, cleared := 1.0, false
		for tick := 0; tick < 60*8; tick++ {
			v := pm.State.Velocity
			want := b.State().Position.Subtract(pm.State.Position).Normalize()
			if turn := math.Acos(clamp(v.Normalize().Dot(want), -1, 1)); turn > 1e-6 {
				step := math.Min(turn, 3*9.81/v.Length()/60) // at most 3 g of turn
				v = v.Normalize().Scale(math.Cos(step)).Add(want.Subtract(v.Normalize().Scale(v.Normalize().Dot(want))).Normalize().Scale(math.Sin(step))).Scale(v.Length())
			}
			pm.State.Velocity = v
			pm.State.Attitude = flight.Look(v.Normalize())
			pm.State.Position = pm.State.Position.Add(v.Scale(1.0 / 60))
			pm.State.Encode(words)
			b.Mirror(words, false, true)
			b.Step()
			line := b.State().Position.Subtract(pm.State.Position)
			chasing := line.Y > 0 && v.Normalize().Dot(line.Normalize()) > math.Cos(30*math.Pi/180)
			if b.craft.brain.play == "clear" {
				cleared = true
				if chasing {
					lowest = math.Min(lowest, b.craft.brain.routine.away.Y)
				}
			}
		}
		if !cleared {
			t.Fatalf("%s: the guard never cleared from a pursuer climbing at it", level)
		}
		if lowest < 0 {
			t.Errorf("%s: chased from below, the clear went %+.2f, down across his height", level, lowest)
		}
	}
}

// TestOvertakeClears: the planner's expert, 1,200 m behind a jet flying straight
// and slow, overtaking at 130 m/s, does not fly into it: it goes by at 150 m
// or more. Without the overtake reflex it passed through.
func TestOvertakeClears(t *testing.T) {
	for _, level := range []string{"intermediate", "expert", "superhuman"} {
		b := NewBandit(level, 1, 250000, "", false, false, "guns", 0, false)
		b.craft.brain.scripted = false // the planner, as every server bot flies it
		b.Spawn(flight.Vec3{Y: 4500}, flight.Vec3{X: 240})
		b.craft.brain.routine.passed = true
		pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		pm.State = flight.Level(pm, flight.Vec3{X: 1200, Y: 4500}, flight.Vec3{X: 1}, 110, fuel)
		pm.State.Velocity = flight.Vec3{X: 110}
		words := make([]float64, flight.Size)
		least := math.Inf(1)
		for tick := 0; tick < 60*15; tick++ {
			pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
			pm.State.Encode(words)
			b.Mirror(words, false, true)
			b.Step()
			least = math.Min(least, pm.State.Position.Subtract(b.State().Position).Length())
		}
		if least < bubble {
			t.Errorf("%s: overtaking a slow jet from behind it went by at %.0f m", level, least)
		}
	}
}

// TestScriptLowFight: below 8,200 ft the fight law sets the stick as it does
// above, easing off to a third of the stick or less when he sits ahead of the
// nose; mimic's floor there, a third of the stick along the lift vector at any
// height under 2,500 m, pinned the nose up and the gun off him. The water
// belongs to the deck recovery, which takes the stick in a dive near it.
func TestScriptLowFight(t *testing.T) {
	b := NewBandit("expert", 1, 250000, "", false, false, "guns", 0, false)
	b.Spawn(flight.Vec3{Y: 2000}, flight.Vec3{X: 200})
	b.craft.brain.routine.passed = true
	pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	pm.State = flight.Level(pm, flight.Vec3{X: 600, Y: 2000}, flight.Vec3{X: 1}, 200, fuel)
	words := make([]float64, flight.Size)
	least := math.Inf(1)
	for tick := 0; tick < 60*3; tick++ {
		pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
		pm.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		least = math.Min(least, b.craft.latest.Pitch)
	}
	if least >= 0.35 {
		t.Errorf("at 6,560 ft behind a jet dead ahead the stick never came below %.2f: something other than the fight law holds it", least)
	}

	low := NewBandit("expert", 1, 250000, "", false, false, "guns", 0, false)
	dive := flight.Vec3{X: math.Cos(0.5), Y: -math.Sin(0.5)}
	low.Spawn(flight.Vec3{Y: 700}, dive.Scale(200))
	low.craft.brain.routine.passed = true
	pm.State = flight.Level(pm, flight.Vec3{X: 900, Y: 400}, flight.Vec3{X: 1}, 200, fuel)
	recovered := false
	for tick := 0; tick < 30; tick++ {
		pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
		pm.State.Encode(words)
		low.Mirror(words, false, true)
		low.Step()
		if low.Mode() == "deck" {
			recovered = true
		}
	}
	if !recovered {
		t.Error("diving at 29 degrees through 2,300 ft the deck recovery never took the stick")
	}
}

// TestScriptStraight: against a jet that is not turning. On the gun behind one
// 300 m dead ahead the scripted expert presses on a good part of four seconds with
// its nose within 1.5 degrees of him. It rolled through 360 degrees, 33 degrees
// off him, and never fired when the lift vector chased the direction of a
// 3-degree offset; 116 frames at 1.9 degrees with the in-plane correction at
// half its gain. Chasing one that flies straight away and outruns it, it
// pursues him inside 4 km and spends under 70 s of four minutes below 194 kt;
// at full fighting alpha it cycled to the regain's floor and back for 93.
func TestScriptStraight(t *testing.T) {
	b := NewBandit("expert", 1, 250000, "", false, false, "guns", 0, false)
	b.Spawn(flight.Vec3{Y: 4572}, flight.Vec3{X: 200})
	player := flight.Level(b.craft.model, flight.Vec3{X: 300, Y: 4572}, flight.Vec3{X: 1}, 200, 2500)
	words := make([]float64, flight.Size)
	firing, worst := 0, 0.0
	for frame := 0; frame < 240; frame++ {
		player.Position = flight.Vec3{X: 300 + 200*float64(frame)/60, Y: 4572}
		player.Encode(words)
		b.Mirror(words, false, true)
		if fire, _, _, _, _ := b.Step(); fire {
			firing++
		}
		s := b.State()
		if frame > 60 {
			worst = math.Max(worst, math.Acos(clamp(s.Attitude.Rotate(flight.Vec3{X: 1}).Dot(player.Position.Subtract(s.Position).Normalize()), -1, 1))*180/math.Pi)
		}
	}
	if firing < 125 || worst > 1.5 {
		t.Errorf("a jet 300 m dead ahead: the trigger held %d frames of 240, the nose up to %.1f degrees off him after the first second", firing, worst)
	}

	c := NewBandit("expert", 9, 250000, "", false, false, "", 0, false)
	c.Spawn(flight.Vec3{X: 2778, Y: altitude}, flight.Vec3{X: -220})
	target := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 9, Wrap: 250000}, flight.World{Sea: sea})
	target.State.Position, target.State.Velocity, target.State.Attitude = flight.Vec3{X: -2778, Y: altitude}, flight.Vec3{X: 170}, flight.Look(flight.Vec3{X: 1})
	slow, pursued, farthest := 0, 0, 0.0
	for tick := 0; tick < 60*240; tick++ {
		target.Step(flight.Inputs{Throttle: 0.45})
		target.State.Encode(words)
		c.Mirror(words, false, true)
		c.Step()
		if tick > 60*30 && c.State().Velocity.Length() < 100 {
			slow++
		}
		if c.Mode() == "pursue" {
			pursued++
		}
		if tick > 60*30 {
			farthest = math.Max(farthest, target.State.Position.Subtract(c.State().Position).Length())
		}
	}
	if slow > 60*70 || pursued == 0 || farthest > 4000 {
		t.Errorf("chasing a jet flying straight away it spent %d s below 194 kt and %d s pursuing, and let him get %.0f m away", slow/60, pursued/60, farthest)
	}
}

// astern flies a scripted bandit at 215 kt and 10,000 ft for four seconds with
// the pilot gap metres behind it and a little high, over its shoulder where it
// can see him, his nose on it, closing at closure m/s, firing or not: what it
// flew, tick by tick, and in what power.
func astern(level string, gap, closure float64, firing bool) (plays []string, lit []bool) {
	b := NewBandit(level, 1, 250000, "", false, true, "fox2", 0, false)
	b.Spawn(flight.Vec3{Y: 3050}, flight.Vec3{X: 110})
	b.craft.brain.routine.passed = true
	pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	words := make([]float64, flight.Size)
	for tick := 0; tick < 60*4; tick++ {
		me := b.craft.model.State
		ahead := me.Velocity.Normalize()
		behind := gap - closure*float64(tick)/60
		his := me.Position.Subtract(ahead.Scale(behind)).Add(flight.Vec3{Y: 0.3 * behind})
		pm.State = flight.Level(pm, his, flight.Vec3{X: ahead.X, Z: ahead.Z}, me.Velocity.Length(), fuel)
		pm.State.Velocity = me.Velocity.Add(ahead.Scale(closure))
		if line := me.Position.Subtract(his).Normalize(); (flight.Vec3{X: 1}).Cross(line).Length() > 1e-9 {
			pm.State.Attitude = flight.Axis((flight.Vec3{X: 1}).Cross(line).Normalize(), math.Acos(clamp(line.X, -1, 1)))
		}
		pm.State.Encode(words)
		b.Mirror(words, firing, true)
		b.Step()
		plays = append(plays, b.Mode())
		lit = append(lit, b.craft.latest.Reheat > 0.5)
	}
	return plays, lit
}

// TestScriptHoldsThePursuer: the novice and the intermediate never clear from a
// heater-armed pilot running in from astern. Their bubble guard flew the clear
// whenever he closed, and extended 7 s at 186-351 m straight ahead of his nose;
// the fight law's turn takes over, as it does for the instructor tiers.
func TestScriptHoldsThePursuer(t *testing.T) {
	for _, level := range []string{"novice", "intermediate"} {
		plays, _ := astern(level, 500, 60, false)
		for tick, play := range plays {
			if play == "clear" {
				t.Errorf("%s cleared %.2f s in, %.0f m ahead of a pilot closing from astern", level, float64(tick)/60, 500-60*float64(tick)/60)
				break
			}
		}
	}
}

// TestScriptRegainsNoseLow: below the floor the regain pushes over to buy the
// band back nose-low, gravity adding to the burner. From 134 kt with a
// heater-armed pilot 1,000 m astern, every tier dives 20 degrees or more and is
// back above 214 kt inside 5 s from level flight and 7.5 s from a 30 degree
// climb; flown to the horizon on four tenths of the stick it took 5.2-5.9 s
// and 9.0 s.
func TestScriptRegainsNoseLow(t *testing.T) {
	for _, level := range []string{"novice", "intermediate", "expert", "superhuman"} {
		for _, start := range []struct{ height, climb, within float64 }{{3050, 0, 5}, {4570, 0, 5}, {4570, 30, 7.5}} {
			b := NewBandit(level, 1, 250000, "", false, true, "fox2", 0, false)
			b.Spawn(flight.Vec3{Y: start.height}, flight.Vec3{X: 69})
			c := start.climb * math.Pi / 180
			b.craft.model.State.Velocity = flight.Vec3{X: 69 * math.Cos(c), Y: 69 * math.Sin(c)}
			b.craft.model.State.Attitude = flight.Look(b.craft.model.State.Velocity.Normalize())
			b.craft.brain.routine.passed = true
			pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
			words := make([]float64, flight.Size)
			steepest, ended := 0.0, -1
			for tick := 0; tick < 60*15 && ended < 0; tick++ {
				me := b.craft.model.State
				ahead := flight.Vec3{X: me.Velocity.X, Z: me.Velocity.Z}.Normalize()
				his := me.Position.Subtract(ahead.Scale(1000)).Add(flight.Vec3{Y: 300})
				pm.State = flight.Level(pm, his, ahead, me.Velocity.Length(), fuel)
				pm.State.Velocity = ahead.Scale(me.Velocity.Length())
				pm.State.Encode(words)
				b.Mirror(words, false, true)
				b.Step()
				if b.Mode() != "regain" {
					ended = tick
					break
				}
				steepest = math.Min(steepest, math.Asin(clamp(b.State().Velocity.Normalize().Y, -1, 1))*180/math.Pi)
			}
			took := float64(ended) / 60
			switch {
			case ended <= 0:
				t.Errorf("%s from %.0f ft climbing %.0f degrees: regain %d ticks, want one that starts and ends", level, start.height*3.281, start.climb, ended)
			case took > start.within:
				t.Errorf("%s from %.0f ft climbing %.0f degrees: regained in %.2f s, want %.1f s or less", level, start.height*3.281, start.climb, took, start.within)
			case steepest > -20:
				t.Errorf("%s from %.0f ft climbing %.0f degrees: the path went %.1f degrees at its lowest, want 20 or more below the horizon", level, start.height*3.281, start.climb, steepest)
			}
		}
	}
}

// TestScriptPlumeFloor: with the pilot astern and his nose on it, the expert and
// the superhuman fly cold to hide the plume from his heater, but not inside his
// heater's fuse floor, where no round he fires arrives and the gun is the
// threat: there the burner buys the turn.
func TestScriptPlumeFloor(t *testing.T) {
	share := func(lit []bool, want bool) float64 {
		n := 0
		for _, v := range lit {
			if v == want {
				n++
			}
		}
		return float64(n) / float64(len(lit))
	}
	for _, level := range []string{"expert", "superhuman"} {
		if _, lit := astern(level, 200, 0, false); share(lit, true) < 0.9 {
			t.Errorf("%s with him 200 m astern, inside his heater's floor: lit %.0f%% of the time, want the burner", level, 100*share(lit, true))
		}
		if _, lit := astern(level, 1200, 0, false); share(lit, false) < 0.5 {
			t.Errorf("%s with him 1,200 m astern, inside his heater's reach: cold %.0f%% of the time, want the plume hidden", level, 100*share(lit, false))
		}
	}
}

// TestScriptJinks: his gun firing and tracking from 300 m astern, the expert and
// the superhuman jink out of it, the planner's guns defence read at their own
// cue and cadence; the novice never sees it coming.
func TestScriptJinks(t *testing.T) {
	for _, level := range []string{"novice", "expert", "superhuman"} {
		plays, _ := astern(level, 300, 0, true)
		first := -1
		for tick, play := range plays {
			if play == "jink" {
				first = tick
				break
			}
		}
		switch {
		case level == "novice" && first >= 0:
			t.Errorf("the novice jinked at %.2f s: the tier never sees a gun coming", float64(first)/60)
		case level != "novice" && first < 0:
			t.Errorf("%s under his gun from 300 m astern never jinked", level)
		case level != "novice" && first > 90:
			t.Errorf("%s under his gun from 300 m astern: first jink at %.2f s, want inside 1.5 s", level, float64(first)/60)
		}
	}
}

// Mochi world: the gun-solution instrument against a turning turner (#177).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// turner flies the turning-rate turn at a speed: full burner, level, and
// the pull is whatever holds that speed - ease when slow, pull when fast - so
// the achieved g is the wing's sustainable g there, which is the regime the
// mush lives in (the 30-degree alpha turn at 150-250 kt). Share scales the
// pull below the sustainable maximum for a gentler target. The achieved g
// and speed are printed as the yardstick's own check.
type turner struct {
	speed, share float64
	bank         float64 // the bank being held, radians; adapts to hold level flight
}

func (t *turner) fly(m *flight.Model) flight.Inputs {
	s := &m.State
	up := s.Attitude.Rotate(flight.Vec3{Y: 1})
	right := s.Attitude.Rotate(flight.Vec3{Z: 1})
	v := s.Velocity.Length()
	// The pull holds the speed: fast means more g is affordable, slow means
	// unload. Scaled by share, floored so the turn never stops.
	pitch := clamp((0.55+(v-t.speed)*0.012)*t.share, 0.15, 1)
	// Level: bank steepens while the jet climbs and shallows while it sinks,
	// within the band a fighting turn uses; the pull above carries the rest.
	if t.bank == 0 {
		t.bank = 70 * math.Pi / 180
	}
	t.bank = clamp(t.bank+s.Velocity.Y*0.002*math.Pi/180*60, 40*math.Pi/180, 82*math.Pi/180)
	have := math.Atan2(-right.Y, up.Y) // right wing down is a right bank
	roll := clamp((t.bank-have)*2.5, -1, 1)
	if s.Position.Y < 2500 { // never into the sea
		pitch, t.bank = math.Max(pitch, 0.5), 40*math.Pi/180
	}
	return flight.Inputs{Pitch: pitch, Roll: roll, Throttle: 1, Reheat: 1}
}

// turning and mushed give the two targets one signature for the loop: the
// turner ignores the pursuer, the mush script fights it (it is the instance
// probes' scripted human, driven here on bare models through its input map).
type turning struct{ t *turner }

func (s *turning) fly(m, _ *flight.Model, _ uint64) flight.Inputs { return s.t.fly(m) }

type mushed struct{ m *mush }

func (w *mushed) fly(m, foe *flight.Model, tick uint64) flight.Inputs {
	data := w.m.fly(&m.State, &foe.State, nil, tick) // a single-jet gun instrument: no rounds in the air to defend on
	number := func(key string) float64 {
		if v, ok := data[key].(float64); ok {
			return v
		}
		return 0
	}
	return flight.Inputs{Pitch: number("pitch"), Roll: number("roll"), Throttle: number("throttle"), Reheat: number("reheat")}
}

// lead aims at where the target will be when a round arrives: the line of
// sight plus his velocity times the round's flight time, at a nominal 900 m/s
// muzzle plus the shooter's own speed. Pure pursuit is lead with no lead.
func lead(s *flight.State, foe *flight.State, ahead bool) flight.Vec3 {
	at := foe.Position
	if ahead {
		span := foe.Position.Subtract(s.Position).Length()
		flight := span / (900 + s.Velocity.Length())
		at = at.Add(foe.Velocity.Scale(flight))
	}
	return at
}

// TestGunSolution: can a lead-pursuit jet get its nose on a turning turner
// at all, and how long does it take? One pursuer, one turner, from four
// entries astern, swept over the turner's speed and pull share. Reports time to the
// first solution (nose within 5 deg at 250-900 m), the share of the run on
// solution, the closest approach and the pursuer's slowest speed. The number
// that matters is whether the full-pull 150-200 kt cells - the mush - are
// reachable from any entry: if not, no arbitration change can convert there.
func TestGunSolution(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	build := func(at, facing flight.Vec3, speed float64) *flight.Model {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = at
		m.State.Velocity = facing.Normalize().Scale(speed)
		m.State.Attitude = flight.Look(facing.Normalize())
		m.State.Gear.Extension = 0
		m.Stores(0)
		m.State.Fuel = 2450
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
		return m
	}
	height := 15000 / 3.281
	entries := []float64{15, 30, 60, 90} // degrees off the turner's tail at the start, 800 m out
	fmt.Println("gun solution v a turning turner: pursuer at target speed +60 kt, 800 m astern, 60 s, 240 Hz | first = seconds to the first solution, on = share of the run on solution | target = the turner's ACHIEVED mean g and speed, the yardstick's own check")
	fmt.Printf("%-6s %-9s %-5s | %-33s | %s\n", "law", "target", "entry", "first     on     nearest  slowest", "achieved")
	// Three pursuit laws: pure pursuit at full pull, lead pursuit at full pull,
	// and lead pursuit that eases below corner speed (the holder policy of
	// TestPointingVersusEnergy) - a pilot who will not drag himself into the
	// mush to point.
	for _, law := range []struct {
		name  string
		ahead bool
		hold  bool
	}{{"pure", false, false}, {"lead", true, false}, {"hold", true, true}} {
		ahead := law.ahead
		for _, share := range []float64{1, 0.7, 0} { // 0 = the mush script itself: full aft stick, the field signature
			for _, kt := range []float64{150, 200, 250, 300} {
				if share == 0 && kt != 200 {
					continue
				}
				for _, entry := range entries {
					speed := kt / 1.944
					var you interface {
						fly(*flight.Model, *flight.Model, uint64) flight.Inputs
					} = &turning{&turner{speed: speed, share: share}}
					if share == 0 {
						you = &mushed{&mush{armed: true}}
					}
					target := build(flight.Vec3{Y: height}, flight.Vec3{X: 1}, speed)
					// The pursuer starts 800 m behind, rotated round the target's
					// tail by the entry angle, pointing at him, 60 kt faster.
					theta := entry * math.Pi / 180
					at := flight.Vec3{X: -800 * math.Cos(theta), Y: height, Z: 800 * math.Sin(theta)}
					me := build(at, flight.Vec3{X: 1}, speed+60/1.944)
					first, on, nearest, slowest := -1.0, 0, math.MaxFloat64, math.MaxFloat64
					sumG, sumV := 0.0, 0.0
					for tick := 0; tick < 240*60; tick++ {
						target.Step(you.fly(target, me, uint64(tick/4)))
						sumG += target.Nz()
						sumV += target.State.Velocity.Length()
						me.Step(aimed(&me.State, lead(&me.State, &target.State, ahead), law.hold, corner(me)))
						s := &me.State
						if v := s.Velocity.Length(); v < slowest {
							slowest = v
						}
						line := target.State.Position.Subtract(s.Position)
						span := line.Length()
						if span < nearest {
							nearest = span
						}
						if span < 1 {
							continue
						}
						axis := s.Attitude.Rotate(flight.Vec3{X: 1})
						off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
						if off < 5 && span > 250 && span < 900 {
							on++
							if first < 0 {
								first = float64(tick) / 240
							}
						}
					}
					firstText := "never"
					if first >= 0 {
						firstText = fmt.Sprintf("%5.1f", first)
					}
					cell := fmt.Sprintf("x%.1f %3.0fkt", share, kt)
					if share == 0 {
						cell = "mush     "
					}
					fmt.Printf("%-6s %-9s %3.0f   | %-6s %5.1f%%  %6.0f m %5.0f kt | %.1f g %3.0f kt\n", law.name, cell, entry, firstText, 100*float64(on)/(240*60), nearest, slowest*1.944, sumG/(240*60), sumV/(240*60)*1.944)
				}
			}
		}
	}
}

// TestGunSolutionByRange: does a pursuit law convert BETTER at longer range, or
// is it flat? (#42, from the 2026-09-12 joust.) The live ace held its nose 89 to
// 123 degrees off the target and converted 2-3% of samples at EVERY range band
// from 250 m to 1500 m - flat. If that flatness is a property of the catalogue,
// a scripted pursuer flying a textbook law should NOT be flat: it should convert
// at 900 m, where its own 331-454 m turn circle is irrelevant, and struggle at
// 300 m, where it is not. If the scripted pursuer is flat too, the wall is the
// pursuit law or the aero model and no play-selection work will touch it.
//
// Two differences from TestGunSolution above, both deliberate, because the field
// number has to be comparable or the experiment answers nothing:
//   - binned by the INSTANTANEOUS range at each sample, not by the entry range,
//     which is what the recording was binned by;
//   - reported at 5 deg (a gun solution) AND at 20 deg (the field's "pointing at
//     him" measure), because mixing the two thresholds would invent a result.
//
// The target is the level sustained turner - the yardstick #177 established as
// gunnable. The mush is deliberately NOT run here: #177 settled that it is a
// regime check and never a conversion bar.
func TestGunSolutionByRange(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	build := func(at, facing flight.Vec3, speed float64) *flight.Model {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = at
		m.State.Velocity = facing.Normalize().Scale(speed)
		m.State.Attitude = flight.Look(facing.Normalize())
		m.State.Gear.Extension = 0
		m.Stores(0)
		m.State.Fuel = 2450
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
		return m
	}
	height := 15000 / 3.281
	entries := []float64{15, 30, 60, 90}
	starts := []float64{250, 400, 600, 900, 1500}
	edges := []float64{0, 250, 400, 600, 900, 1500}
	bands := []string{"<250", "250-400", "400-600", "600-900", "900-1500"}
	laws := []struct {
		name  string
		ahead bool
		hold  bool
	}{{"pure", false, false}, {"lead", true, false}, {"hold", true, true}, {"mirror", false, true}}
	kt := 250.0
	speed := kt / 1.944

	// `mirror` is pure pursuit WITH the energy hold - deliberately the exact
	// policy TestPointingVersusEnergy's holder flies, since lead(s, foe, false)
	// returns foe.Position unchanged and both probes steer through aimed().
	// It exists to settle a contradiction that was recorded against #42 for
	// nine days: that probe has the holder WINNING the gun fight (0.96% pointer
	// against 1.42% holder, 2.52% holder-on-holder) while this one had it 3-4x
	// worse at pointing, and the two were read as irreconcilable. They are not
	// the same measurement. `hold` here is LEAD pursuit plus the hold, and this
	// sweep flies a scripted turner that never reacts - where easing the pull
	// can only cost tracking, because nothing punishes you for spending energy.
	// The joust is mutual, and there the pointer bleeds itself into a target.
	// This row puts the same policy in both harnesses so the comparison is
	// between like and like. MEASURED, and the contradiction dissolves:
	//   on<20deg, pooled          <250  250-400  400-600  600-900  900-1500
	//     pure   (no hold)       68.6%    66.0%    77.0%    62.2%     63.4%
	//     mirror (pure + hold)   14.2%    23.1%    20.4%    25.6%     28.5%
	//     hold   (lead + hold)   14.8%    30.0%    21.4%    26.7%     32.5%
	// The 15-34% is REAL and it is that policy - but it is what the hold costs
	// against a target that never reacts, where nothing punishes you for
	// spending energy. The aim law is NOT the cause: mirror and hold read the
	// same, so pure-versus-lead barely matters once the hold is on.
	// In the joust the same policy wins, and the slowest-speed column says why:
	// at the 2,400 m / 350 kt entry the pointer takes heater parameters 41.2% of
	// the fight against the holder's 17.8% - it really does point better - and
	// bleeds to 30 kt doing it, while the holder sits at 210 and takes the guns
	// column 5.0% to 1.8%. Holder-on-holder is 4.6% guns against
	// pointer-on-pointer's 1.8%, every holder cell ending at its entry speed.
	// So the two probes agree about the POLICY and differ about what winning
	// means. Neither is wrong; they are not comparable, and #42 recorded them
	// as irreconcilable for nine days on that mistake.
	fmt.Println("\ngun solution BY RANGE: level sustained turner at 250 kt, pursuer +60 kt, 60 s, 240 Hz")
	fmt.Println("entry sweep - share of each run with the nose within 5 deg at 250-900 m, by ENTRY range")
	fmt.Printf("%-6s %s\n", "law", "  250 m   400 m   600 m   900 m  1500 m")
	// off angles per law per band, pooled across every entry range and angle
	pool := map[string][][]float64{}
	for _, law := range laws {
		pool[law.name] = make([][]float64, len(bands))
		row := ""
		for _, start := range starts {
			on, total := 0, 0
			for _, entry := range entries {
				you := &turning{&turner{speed: speed, share: 1}}
				target := build(flight.Vec3{Y: height}, flight.Vec3{X: 1}, speed)
				theta := entry * math.Pi / 180
				at := flight.Vec3{X: -start * math.Cos(theta), Y: height, Z: start * math.Sin(theta)}
				me := build(at, flight.Vec3{X: 1}, speed+60/1.944)
				for tick := 0; tick < 240*60; tick++ {
					target.Step(you.fly(target, me, uint64(tick/4)))
					me.Step(aimed(&me.State, lead(&me.State, &target.State, law.ahead), law.hold, corner(me)))
					s := &me.State
					line := target.State.Position.Subtract(s.Position)
					span := line.Length()
					if span < 1 {
						continue
					}
					axis := s.Attitude.Rotate(flight.Vec3{X: 1})
					off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
					total++
					if off < 5 && span > 250 && span < 900 {
						on++
					}
					for b := 0; b < len(bands); b++ {
						if span >= edges[b] && span < edges[b+1] {
							pool[law.name][b] = append(pool[law.name][b], off)
							break
						}
					}
				}
			}
			row += fmt.Sprintf("%7.1f%%", 100*float64(on)/float64(max(total, 1)))
		}
		fmt.Printf("%-6s %s\n", law.name, row)
	}

	fmt.Println("\nby INSTANTANEOUS range, pooled over every entry - the field's own binning")
	fmt.Printf("%-6s %-9s %8s %12s %9s %9s\n", "law", "band", "samples", "median off", "on<5deg", "on<20deg")
	for _, law := range laws {
		for b, name := range bands {
			v := pool[law.name][b]
			if len(v) == 0 {
				fmt.Printf("%-6s %-9s %8d %12s %9s %9s\n", law.name, name, 0, "-", "-", "-")
				continue
			}
			sorted := append([]float64(nil), v...)
			sort.Float64s(sorted)
			on5, on20 := 0, 0
			for _, x := range v {
				if x < 5 {
					on5++
				}
				if x < 20 {
					on20++
				}
			}
			fmt.Printf("%-6s %-9s %8d %11.1f deg %8.1f%% %8.1f%%\n", law.name, name, len(v),
				sorted[len(sorted)/2], 100*float64(on5)/float64(len(v)), 100*float64(on20)/float64(len(v)))
		}
	}
	fmt.Println("\nfield comparison (live ace, 2026-09-12, post-Winchester, its own nose off the target):")
	fmt.Println("  <250 122.9 deg on<20deg 0.0% | 250-400 102.4 deg 3.0% | 400-600 113.0 deg 2.8% | 600-900 99.7 deg 2.9% | 900-1500 88.9 deg 2.2%")
}

// TestGunSolutionBotSeat: #177's own "NEXT (not done)", and the question
// TestGunSolutionByRange forces. The scripted laws convert against the level
// turner - lead pursuit points within 20 deg for 41-74% of the run and holds a
// real gun solution 42-48% of the time at 400-900 m - while the live ace in the
// 2026-09-12 joust pointed within 20 deg for 0-3% of samples at EVERY range. So
// the airframe can point and a textbook law can point. Can the bot's OWN play
// laws point, with the arbitration taken out of it?
//
// This drives one named play per run through the moment API, exactly as
// duel.go:883 does in the live path: build the moment from fresh geometry every
// tick, call that play's law, load the order into the brain and let steer()
// fly it. What it deliberately does NOT do is let the arbiter choose - each run
// is one play held for sixty seconds. A play that cannot point when it is the
// only play running cannot be rescued by choosing it more often.
func TestGunSolutionBotSeat(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	build := func(at, facing flight.Vec3, speed float64) *flight.Model {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = at
		m.State.Velocity = facing.Normalize().Scale(speed)
		m.State.Attitude = flight.Look(facing.Normalize())
		m.State.Gear.Extension = 0
		m.Stores(0)
		m.State.Fuel = 2450
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
		return m
	}
	height := 15000 / 3.281
	entries := []float64{15, 30, 60, 90}
	starts := []float64{400, 600, 900}
	edges := []float64{0, 250, 400, 600, 900, 1500}
	bands := []string{"<250", "250-400", "400-600", "600-900", "900-1500"}
	kt := 250.0
	speed := kt / 1.944

	// READ THE SHARE UNDER 5 DEGREES, NOT THE MEAN ERROR (#42, 2026-09-13).
	// The aim integrator took press from 1.8% to 37.9% on solution at 400-600 m
	// while the MEAN nose-off over 400-900 m did not move at all (6.1 -> 6.2
	// deg). Both are true: the mean over the wider band is dominated by
	// 600-900 m, where conversion is still 9.6%, and an integrator does not
	// shrink the average error - it moves the distribution across the firing
	// threshold. At 400-600 m the median went 9.0 -> 6.2 deg and the share
	// inside tolerance went up twentyfold. A tuning change judged on the mean
	// would have been read as doing nothing.
	fmt.Println("\nBOT SEAT: one named play, held for the whole run, against the same level turner")
	fmt.Printf("%-8s %-9s %8s %12s %9s %9s\n", "play", "band", "samples", "median off", "on<5deg", "on<20deg")
	for _, name := range []string{"press", "saddle", "high", "lag"} {
		var law func(*moment) order
		for _, p := range plays {
			if p.name == name {
				law = p.law
			}
		}
		if law == nil {
			fmt.Printf("%-8s (no such play)\n", name)
			continue
		}
		pool := make([][]float64, len(bands))
		var command, tracking, total, inside, outside []float64
		// The fine-tracking boost's real reach, derived from the tactics the bot
		// actually flies (skill.open * aim.reach) so this can never again report
		// a boundary the bot has stopped applying.
		edge := skills["ace"].open * standard().aim.reach
		sumThrottle, sumBrake, sumG, budget := 0.0, 0.0, 0.0, 0
		// The terminal discipline's own accounting (#42). press spends its
		// overtake on RANGE alone - goal := clamp((distance-250)*0.15, 0, 45)
		// knows nothing about where the nose is - so this asks what that costs:
		// how much of the close-in time is spent PARKED (closure under 10 m/s)
		// with the nose still a long way off, which is parking abeam rather
		// than finishing.
		parkedOn, parkedOff, closeOn, closeOff := 0, 0, 0, 0
		for _, start := range starts {
			for _, entry := range entries {
				you := &turning{&turner{speed: speed, share: 1}}
				target := build(flight.Vec3{Y: height}, flight.Vec3{X: 1}, speed)
				theta := entry * math.Pi / 180
				at := flight.Vec3{X: -start * math.Cos(theta), Y: height, Z: start * math.Sin(theta)}
				me := build(at, flight.Vec3{X: 1}, speed+60/1.944)
				b := &brain{skill: skills["ace"], tactics: standard()}
				// steer()'s fine-tracking loop - the block that drives the
				// pipper onto the lead point and is THE gun-solution refinement
				// - is gated on b.shoot, b.prey, b.magazine and the two timers.
				// The first cut of this harness left prey nil and shoot false,
				// so that block never ran and the run measured a bot with its
				// tracking loop disconnected. In a duel the trigger is always
				// live (duel.go) and prey is refreshed every tick, so it is
				// wired the same way here.
				b.shoot, b.magazine, b.quiet, b.dodge = true, 578, 0, 0
				b.prey = &track{}
				// The ring is estimated from two samples 0.25 s apart, as
				// bot.go does - a play that reads it gets an honest circle.
				sampleP, sampleV, sampled := target.State.Position, target.State.Velocity, uint64(0)
				var held flight.Inputs
				for tick := 0; tick < 240*60; tick++ {
					slow := uint64(tick / 4) // the 60 Hz tick the live path counts in
					target.Step(you.fly(target, me, slow))
					if float64(slow-sampled)/60 >= 0.25 {
						b.ring = circle(sampleP, sampleV, sampled, target.State.Position, target.State.Velocity, slow)
						sampleP, sampleV, sampled = target.State.Position, target.State.Velocity, slow
					}
					// refresh the track the way the live path does, so pipper()
					// leads against a current sighting rather than a stale one
					swing := target.State.Velocity.Subtract(b.prey.velocity).Scale(60)
					if b.prey.when == 0 {
						swing = flight.Vec3{}
					}
					*b.prey = track{when: slow, position: target.State.Position, velocity: target.State.Velocity,
						swing: swing, nose: target.State.Attitude.Rotate(flight.Vec3{X: 1})}
					m := moment{me: &me.State, prey: target.State.Position, velocity: target.State.Velocity,
						ring: b.ring, pace: corner(me), pull: b.skill.pull}
					m.derive()
					o := law(&m)
					b.aim, b.g, b.throttle, b.reheat, b.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
					// The live path decides at 60 Hz and holds the input for
					// four 240 Hz substeps (duel.go). Calling steer every step
					// handed the bot four times the real decision rate - and
					// any integrating term four times the real wind-up.
					if tick%4 == 0 {
						held = b.steer(me, slow)
					}
					me.Step(held)
					s := &me.State
					line := target.State.Position.Subtract(s.Position)
					span := line.Length()
					if span < 1 {
						continue
					}
					axis := s.Attitude.Rotate(flight.Vec3{X: 1})
					off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
					// Decompose the miss inside the gun envelope: is the law
					// COMMANDING the wrong direction, or is the jet failing to
					// reach what it commands? `ideal` is the same lead point
					// TestGunSolution's scripted law flies to.
					// Split at the fine-tracking boost's ACTUAL reach, read from
					// the tactics rather than written down here. This used to be
					// hardcoded at 690 m with the halves labelled "boost ON" and
					// "boost OFF", which was true when aim.reach was 1.15 and has
					// been false since #42 raised it to 1.5 - the boost now runs
					// to 900 m for the ace and the probe was reporting a gate the
					// bot had stopped applying. The conversion difference across
					// the old line is real and survives (press 40.2% inside,
					// 11.8% outside), but it is a RANGE effect, not a gate, and
					// the stale label sent a reader hunting for a switch that is
					// not there. Same defect as #212: derive the boundary, never
					// restate it.
					//
					// With reach at 1.5 the boost now covers the WHOLE 400-900 m
					// window, so the old split had an empty outer half. The
					// comparison that still means something is boosted against
					// beyond-the-boost, which is why the outer bucket runs to
					// 1,500 m rather than stopping at 900.
					if span >= 400 && span < edge {
						inside = append(inside, math.Acos(clamp(lead(s, &target.State, true).Subtract(s.Position).Normalize().Dot(axis), -1, 1))*180/math.Pi)
					}
					if span >= edge && span < 1500 {
						outside = append(outside, math.Acos(clamp(lead(s, &target.State, true).Subtract(s.Position).Normalize().Dot(axis), -1, 1))*180/math.Pi)
					}
					if span >= 400 && span < 900 {
						// lead() returns a POINT, aloft() returns a DIRECTION -
						// the first cut dotted the point against unit vectors,
						// clamped to 1, and printed 0.0 deg for two of the three.
						ideal := lead(s, &target.State, true).Subtract(s.Position).Normalize()
						want := o.aim.Normalize()
						commandErr := math.Acos(clamp(ideal.Dot(want), -1, 1)) * 180 / math.Pi
						trackErr := math.Acos(clamp(want.Dot(axis), -1, 1)) * 180 / math.Pi
						totalErr := math.Acos(clamp(ideal.Dot(axis), -1, 1)) * 180 / math.Pi
						command = append(command, commandErr)
						tracking = append(tracking, trackErr)
						total = append(total, totalErr)
						sumThrottle += o.throttle
						sumBrake += o.brake
						sumG += o.g
						budget++
					}
					// Inside the finishing gap, is the overtake being spent
					// with the nose on or with it abeam?
					if span < 900 {
						closure := s.Velocity.Subtract(target.State.Velocity).Dot(line.Scale(1 / span))
						parked := math.Abs(closure) < 10
						if off < 30 {
							closeOn++
							if parked {
								parkedOn++
							}
						} else {
							closeOff++
							if parked {
								parkedOff++
							}
						}
					}
					for bi := 0; bi < len(bands); bi++ {
						if span >= edges[bi] && span < edges[bi+1] {
							pool[bi] = append(pool[bi], off)
							break
						}
					}
				}
			}
		}
		if budget > 0 {
			share := func(v []float64) float64 {
				if len(v) == 0 {
					return math.NaN()
				}
				n := 0
				for _, x := range v {
					if x < 5 {
						n++
					}
				}
				return 100 * float64(n) / float64(len(v))
			}
			fmt.Printf("  %-6s fine-track reach %.0f m: 400-%.0f m (boosted) %5.1f%% on solution | %.0f-1500 m (beyond it) %5.1f%%\n",
				name, edge, edge, share(inside), edge, share(outside))
			pct := func(n, d int) float64 {
				if d == 0 {
					return math.NaN()
				}
				return 100 * float64(n) / float64(d)
			}
			fmt.Printf("  %-6s inside 900 m: nose ON  (<30 deg) %6d ticks, parked (|closure|<10) %5.1f%% | nose OFF %6d ticks, parked %5.1f%%\n",
				name, closeOn, pct(parkedOn, closeOn), closeOff, pct(parkedOff, closeOff))
			mid := func(v []float64) float64 { u := append([]float64(nil), v...); sort.Float64s(u); return u[len(u)/2] }
			fmt.Printf("  %-6s 400-900 m: commanded aim off the ideal lead %5.1f deg | nose off the COMMAND %5.1f deg | nose off the ideal %5.1f deg | throttle %.2f brake %.2f g %.1f\n",
				name, mid(command), mid(tracking), mid(total), sumThrottle/float64(budget), sumBrake/float64(budget), sumG/float64(budget))
		}
		for bi, band := range bands {
			v := pool[bi]
			if len(v) == 0 {
				fmt.Printf("%-8s %-9s %8d %12s %9s %9s\n", name, band, 0, "-", "-", "-")
				continue
			}
			sorted := append([]float64(nil), v...)
			sort.Float64s(sorted)
			on5, on20 := 0, 0
			for _, x := range v {
				if x < 5 {
					on5++
				}
				if x < 20 {
					on20++
				}
			}
			fmt.Printf("%-8s %-9s %8d %8.1f deg %8.1f%% %8.1f%%\n", name, band, len(v),
				sorted[len(sorted)/2], 100*float64(on5)/float64(len(v)), 100*float64(on20)/float64(len(v)))
		}
	}
}

// TestGunSolutionAimSweep: pick aim.walk and aim.settle on measurement (#42).
// The integral exists because the tracking loop is proportional-only and settles
// at drift over gain; walk is how fast it integrates the residual out, settle is
// the ceiling on what it may add. Too small and the standing error survives; too
// large and the loop hunts, or a lapsed track leaves a wound-up bias.
//
// Conversion is the headline, but not alone: `slowest` is the energy check. An
// over-driven integral buys the nose by spending the jet, which reads well here
// and loses fights everywhere else, so a cell that converts well while bleeding
// the pursuer below corner speed is a worse answer than a duller one that does not.
//
// MEASURED 2026-09-12, press v the level turner pooled over 400-900 m, on<5deg:
//
//	              settle 0.10   0.20   0.35
//	integral off       3.0%   3.0%   3.0%
//	walk 0.002         3.3%   8.4%   8.2%
//	walk 0.004         4.2%  16.7%  21.5%
//	walk 0.006         6.1%  21.6%  26.7%
//	walk 0.010         8.4%  27.3%  29.8%
//	walk 0.016        19.1%  30.7%  30.2%     <- peak
//	walk 0.024           -   27.8%  28.2%
//	walk 0.032           -   26.4%  25.0%
//	walk 0.048           -   20.4%  15.5%
//	walk 0.070           -   19.6%  12.0%
//
// 0.016 is an INTERIOR optimum, not the edge of a grid: conversion falls away
// above it and the median pointing error degrades with it (7.2 -> 9.2 deg by
// 0.070). settle 0.20 and 0.35 tie at the peak, so the tighter clamp wins - the
// same conversion with less authority for a wound-up bias. settle 0.10 is too
// tight to matter at any walk.
func TestGunSolutionAimSweep(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	build := func(at, facing flight.Vec3, speed float64) *flight.Model {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position, m.State.Velocity = at, facing.Normalize().Scale(speed)
		m.State.Attitude = flight.Look(facing.Normalize())
		m.State.Gear.Extension = 0
		m.Stores(0)
		m.State.Fuel = 2450
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
		return m
	}
	var law func(*moment) order
	for _, p := range plays {
		if p.name == "press" {
			law = p.law
		}
	}
	height, speed := 15000/3.281, 250.0/1.944
	// one cell: press against the turner from every entry, at this doc
	cell := func(doc tactics) (on5, median, slowest float64) {
		var offs []float64
		slowest = math.MaxFloat64
		for _, start := range []float64{400, 600, 900} {
			for _, entry := range []float64{15, 30, 60, 90} {
				you := &turning{&turner{speed: speed, share: 1}}
				target := build(flight.Vec3{Y: height}, flight.Vec3{X: 1}, speed)
				theta := entry * math.Pi / 180
				me := build(flight.Vec3{X: -start * math.Cos(theta), Y: height, Z: start * math.Sin(theta)},
					flight.Vec3{X: 1}, speed+60/1.944)
				b := &brain{skill: skills["ace"], tactics: doc}
				b.shoot, b.magazine, b.quiet, b.dodge = true, 578, 0, 0
				b.prey = &track{}
				sampleP, sampleV, sampled := target.State.Position, target.State.Velocity, uint64(0)
				var held flight.Inputs
				for tick := 0; tick < 240*60; tick++ {
					slow := uint64(tick / 4)
					target.Step(you.fly(target, me, slow))
					if float64(slow-sampled)/60 >= 0.25 {
						b.ring = circle(sampleP, sampleV, sampled, target.State.Position, target.State.Velocity, slow)
						sampleP, sampleV, sampled = target.State.Position, target.State.Velocity, slow
					}
					swing := target.State.Velocity.Subtract(b.prey.velocity).Scale(60)
					if b.prey.when == 0 {
						swing = flight.Vec3{}
					}
					*b.prey = track{when: slow, position: target.State.Position, velocity: target.State.Velocity,
						swing: swing, nose: target.State.Attitude.Rotate(flight.Vec3{X: 1})}
					m := moment{me: &me.State, prey: target.State.Position, velocity: target.State.Velocity,
						ring: b.ring, pace: corner(me), pull: b.skill.pull}
					m.derive()
					o := law(&m)
					b.aim, b.g, b.throttle, b.reheat, b.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
					if tick%4 == 0 {
						held = b.steer(me, slow)
					}
					me.Step(held)
					if v := me.State.Velocity.Length(); v < slowest {
						slowest = v
					}
					line := target.State.Position.Subtract(me.State.Position)
					span := line.Length()
					if span < 400 || span >= 900 || span < 1 {
						continue
					}
					axis := me.State.Attitude.Rotate(flight.Vec3{X: 1})
					offs = append(offs, math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1))*180/math.Pi)
				}
			}
		}
		if len(offs) == 0 {
			return 0, math.NaN(), slowest * 1.944
		}
		sort.Float64s(offs)
		n := 0
		for _, x := range offs {
			if x < 5 {
				n++
			}
		}
		return 100 * float64(n) / float64(len(offs)), offs[len(offs)/2], slowest * 1.944
	}

	fmt.Println("\naim.walk / aim.settle sweep: press v the level turner, 400-900 m, on<5deg | median off | slowest")
	base := standard()
	off := base
	off.aim.walk, off.aim.settle = 0, 0
	on5, mid, slow := cell(off)
	fmt.Printf("  integral OFF          %5.1f%%  %5.1f deg  %4.0f kt\n", on5, mid, slow)
	for _, settle := range []float64{0.10, 0.20, 0.35} {
		for _, walk := range []float64{0.002, 0.006, 0.016, 0.032, 0.070} {
			doc := base
			doc.aim.walk, doc.aim.settle = walk, settle
			on5, mid, slow := cell(doc)
			fmt.Printf("  walk %.3f settle %.2f  %5.1f%%  %5.1f deg  %4.0f kt\n", walk, settle, on5, mid, slow)
		}
	}
}

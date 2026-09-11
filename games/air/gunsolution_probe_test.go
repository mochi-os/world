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

package air

import (
	"fmt"
	"math"
	"testing"
	"world/games/air/aircraft"
	"world/games/air/flight"
)

// The FIXED harness semantics: freeze until first contact, release instantly.
// Measure the honest post-release skip: does the jet leave the ground, how high?
func TestBounce2(t *testing.T) {
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000},
		flight.World{Fields: []flight.Field{{Height: 0, Strips: []flight.Strip{{A: flight.Vec3{X: -4000}, B: flight.Vec3{X: 4000}, Width: 60}}}}})
	m.State.Position = flight.Vec3{Y: 6}
	m.State.Velocity = flight.Vec3{X: 69.95, Y: -2.5}
	m.State.Attitude = flight.Axis(flight.Vec3{Z: 1}, 4*math.Pi/180)
	m.State.Gear = flight.GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Engine[0] = flight.EngineState{Spool: 0.3}
	m.State.Engine[1] = flight.EngineState{Spool: 0.3}
	held, touched := true, false
	base := m.State.Position.Y
	skips, apex := 0, 0.0
	wasWow := false
	attitude := m.State.Attitude
	for tick := 0; tick < 240*20; tick++ {
		if held {
			m.State.Velocity = flight.Vec3{X: 69.95, Y: -2.5}
			m.State.Attitude = attitude
			m.State.Omega = flight.Vec3{}
			if m.State.Gear.Wow || m.State.Gear.Touch.Occurred {
				held = false // the fixed test_drive: release AT contact
			}
		}
		m.Step(flight.Inputs{Gear: true})
		wow := m.State.Gear.Wow
		if wow && !touched {
			touched = true
			base = m.State.Position.Y // CG height with struts compressed
		}
		if touched {
			if !wow && wasWow {
				skips++
			}
			if !wow && m.State.Position.Y-base > apex {
				apex = m.State.Position.Y - base
			}
		}
		wasWow = wow
	}
	fmt.Printf("skips=%d, highest airborne excursion after touchdown: %.2f m, final v=%.0f\n", skips, apex, m.State.Velocity.Length())
	// Naval gear plants: heavily damped rebound eats a no-flare touchdown.
	if skips > 0 || apex > 0.05 {
		t.Fatalf("a 2.5 m/s touchdown skipped %d times (%.2f m): the oleo rebound is bouncing", skips, apex)
	}
}

package furball

import (
	"fmt"
	"math"
	"testing"
	"world/games/furball/aircraft"
	"world/games/furball/flight"
)

// TestBellyProbe: a gentle gear-up arrival slides out to a stop — a scrape,
// not a crash (the project rule permits wheels, BELLY, and hook).
func TestBellyProbe(t *testing.T) {
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000},
		flight.World{Fields: []flight.Field{{Height: 2, Strips: []flight.Strip{{A: flight.Vec3{X: -3000, Y: 2, Z: 0}, B: flight.Vec3{X: 3000, Y: 2, Z: 0}, Width: 60}}}}})
	m.State.Position = flight.Vec3{Y: 8}
	m.State.Velocity = flight.Vec3{X: 64, Y: -2}
	m.State.Attitude = flight.Axis(flight.Vec3{Z: 1}, 5*math.Pi/180)
	m.State.Gear = flight.GearState{Extension: 0, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	for tick := 0; tick < 240*40; tick++ {
		m.Step(flight.Inputs{})
		if m.State.Gear.Contact >= 0 {
			t.Fatalf("tick %d: crash probe %d fired during the belly slide (v=%.0f pitch=%.1f)", tick, m.State.Gear.Contact,
				m.State.Velocity.Length(), math.Asin(m.State.Attitude.Rotate(flight.Vec3{X: 1}).Y)*57.3)
		}
		if m.State.Velocity.Length() < 1 {
			fmt.Printf("slid to a stop at t=%.1fs\n", float64(tick)/240)
			return
		}
	}
	t.Fatal("never stopped sliding")
}

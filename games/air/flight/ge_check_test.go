package flight

import (
	"fmt"
	"math"
	"testing"
)

// The #132 claim check: identical flares onto a sea-level strip and a 500 m
// plateau strip must land the same way — the cushion follows the FIELD, not
// sea level. Under the old sea-referencing the plateau had no ground effect.
func TestElevatedFieldCushion(t *testing.T) {
	flare := func(elevation float64) (float64, float64) {
		world := World{Sea: 0, Fields: []Field{{Height: elevation, Strips: []Strip{{A: Vec3{X: -3000, Z: 0}, B: Vec3{X: 3000, Z: 0}, Width: 60}}}}}
		m := New(Fighter, Environment{}, world)
		m.State.Position = Vec3{X: -400, Y: elevation + 8}
		m.State.Velocity = Vec3{X: 69, Y: -3}
		m.State.Attitude = Axis(Vec3{Z: 1}, 8.1*math.Pi/180)
		m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
		m.State.Engine[0] = EngineState{Spool: 0.72}
		m.State.Engine[1] = EngineState{Spool: 0.72}
		for i := 0; i < 240*12; i++ {
			m.Step(Inputs{Gear: true, Throttle: 0.72})
			if m.State.Gear.Wow {
				return m.State.Position.X, -m.State.Velocity.Y
			}
		}
		return math.NaN(), math.NaN()
	}
	x0, s0 := flare(0)
	x500, s500 := flare(500)
	fmt.Printf("sea-level strip: touchdown x=%.1f sink=%.2f | 500 m plateau: x=%.1f sink=%.2f\n", x0, s0, x500, s500)
	// density control: sea raised to 500 m too, so ground effect is correct in
	// ANY reference scheme and the only variable left is the thinner air.
	{
		world := World{Sea: 500, Fields: []Field{{Height: 500, Strips: []Strip{{A: Vec3{X: -3000, Z: 0}, B: Vec3{X: 3000, Z: 0}, Width: 60}}}}}
		m := New(Fighter, Environment{}, world)
		m.State.Position = Vec3{X: -400, Y: 508}
		m.State.Velocity = Vec3{X: 69, Y: -3}
		m.State.Attitude = Axis(Vec3{Z: 1}, 8.1*math.Pi/180)
		m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
		m.State.Engine[0] = EngineState{Spool: 0.72}
		m.State.Engine[1] = EngineState{Spool: 0.72}
		for i := 0; i < 240*12; i++ {
			m.Step(Inputs{Gear: true, Throttle: 0.72})
			if m.State.Gear.Wow {
				fmt.Printf("density control (sea=500): x=%.1f sink=%.2f\n", m.State.Position.X, -m.State.Velocity.Y)
				// The guard: the plateau flare must match the control, where the
				// reference scheme cannot be wrong — so the elevated field gets
				// its full cushion. Sea-referencing missed by ~0.11 m/s of sink
				// and ~0.3 m of float; the surface reference matches exactly.
				if math.IsNaN(s500) || math.Abs(s500-(-m.State.Velocity.Y)) > 0.05 || math.Abs(x500-m.State.Position.X) > 0.15 {
					t.Fatalf("plateau flare diverges from the density control: sink %.2f vs %.2f, x %.1f vs %.1f", s500, -m.State.Velocity.Y, x500, m.State.Position.X)
				}
				break
			}
		}
	}
	_, _ = x0, s0

}

package air

import (
	"math"
	"os"
	"testing"

	"world/games/air/flight"
	"world/games/air/round"
)

// #104: is arrival monotonic in range? rung() bisects for the OUTERMOST
// arriving range, and zoned() then accepts every range inside it -- which
// assumes a shot that works at R works at every range below R. That holds for
// a reach or energy limit. It is exactly inverted for the seeker's track-rate
// ceiling, whose line-of-sight rate goes as crossing speed over range: closing
// in RAISES it. This sweep is the check, and it is the whole of #104.
func TestArrivalAgainstRange(t *testing.T) {
	if os.Getenv("AIR_BEAM") == "" {
		t.Skip("measurement probe: set AIR_BEAM=1")
	}
	for _, c := range recorded() {
		shooter := round.Target{Position: c.shooterP, Velocity: c.shooterV}
		target := round.Target{Position: c.targetP, Velocity: c.targetV}
		direction := c.targetP.Subtract(c.shooterP).Normalize()
		band := ""
		for r := 400.0; r <= 3000; r += 200 {
			if reaches(shooter, target, direction, c.swing, 0, r, true) {
				band += "#"
			} else {
				band += "."
			}
		}
		t.Logf("%s (fired at %.0f m)", c.name, c.fired)
		t.Logf("   400 m -> 3000 m in 200 m steps, # arrives: %s", band)
		t.Logf("   at the range actually fired from (%.0f m): arrives %v",
			c.fired, reaches(shooter, target, direction, c.swing, 0, c.fired, true))
	}
}

func TestLadderAtRecordedShots(t *testing.T) {
	if os.Getenv("AIR_BEAM") == "" {
		t.Skip("measurement probe: set AIR_BEAM=1")
	}
	for _, c := range recorded() {
		line := c.targetP.Subtract(c.shooterP)
		distance := line.Length()
		aspect := math.Acos(clamp(line.Scale(-1/distance).Dot(c.targetV.Normalize()), -1, 1)) * 180 / math.Pi
		for _, lit := range []float64{0, 1} {
			for _, turn := range []struct {
				label string
				sw    flight.Vec3
			}{{"straight", flight.Vec3{}}, {"real 6.5g", c.swing}} {
				z := Heat(round.Target{Position: c.shooterP, Velocity: c.shooterV},
					round.Target{Position: c.targetP, Velocity: c.targetV}, turn.sw, lit, 0)
				t.Logf("%s lit=%.0f %-9s aspect %.0f deg | min %4.0f escape %5.0f max %5.0f | fired at %.0f: endorsed %v",
					c.name, lit, turn.label, aspect, z.Minimum, z.Escape, z.Max, c.fired,
					c.fired > z.Minimum && c.fired <= z.Escape)
			}
		}
	}
}

func TestLadderByAspect(t *testing.T) {
	if os.Getenv("AIR_BEAM") == "" {
		t.Skip("measurement probe: set AIR_BEAM=1")
	}
	shooter := round.Target{Position: flight.Vec3{Y: 4500}, Velocity: flight.Vec3{X: 190}}
	for _, aspect := range []float64{0, 30, 60, 75, 90, 105, 120, 150, 180} {
		rad := aspect * math.Pi / 180
		target := round.Target{
			Position: flight.Vec3{X: 1600, Y: 4500},
			Velocity: flight.Vec3{X: 270 * math.Cos(rad), Z: 270 * math.Sin(rad)},
		}
		cold := Heat(shooter, target, flight.Vec3{}, 0, 0)
		lit := Heat(shooter, target, flight.Vec3{}, 1, 0)
		t.Logf("aspect %3.0f deg | cold: min %4.0f escape %5.0f max %5.0f | lit: min %4.0f escape %5.0f max %5.0f",
			aspect, cold.Minimum, cold.Escape, cold.Max, lit.Minimum, lit.Escape, lit.Max)
	}
}

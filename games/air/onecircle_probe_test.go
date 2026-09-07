package air

import (
	"fmt"
	"math"
	"os"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// The one-circle question: entering level at a given speed and pulling for all
// the jet has, how far does the NOSE come round, and how fast? Sustained turn
// (TestEnvelopeMap) answers who wins a long fight at constant energy. It says
// nothing about who points first, which is what decides a slow fight -- and
// the F/A-18's real reputation rests on exactly that.
func TestOneCirclePoint(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	fmt.Println("entering level at each speed, full aft stick, max burner, 10,000 ft")
	fmt.Println("   kt |  peak rate | min radius  | peak alpha | turned in 3 s | turned in 6 s | kt after 6 s")
	for _, kt := range []float64{150, 200, 250, 300, 350, 400, 450, 500} {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = flight.Vec3{Y: 10000 / 3.281}
		m.State.Velocity = flight.Vec3{X: kt / 1.944}
		m.State.Attitude = flight.Look(flight.Vec3{X: 1})
		m.State.Gear.Extension = 0
		m.Stores(0)
		m.State.Fuel = 2450
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}

		previous := m.State.Velocity.Normalize()
		turned, peak, peakAlpha, radius := 0.0, 0.0, 0.0, math.MaxFloat64
		var at3 float64
		for tick := 0; tick < 240*6; tick++ {
			m.Step(flight.Inputs{Pitch: 1, Roll: 0, Throttle: 1, Reheat: 1})
			s := &m.State
			now := s.Velocity.Normalize()
			step := math.Acos(clamp(previous.Dot(now), -1, 1))
			turned += step
			if rate := step * 240 * 180 / math.Pi; rate > peak {
				peak = rate
			}
			// The one-circle currency is RADIUS, not rate: V / omega, and the
			// smallest circle flown at any point in the pull is what decides
			// who ends up inside whom.
			if step > 1e-6 {
				if r := s.Velocity.Length() / (step * 240); r < radius {
					radius = r
				}
			}
			previous = now
			// alpha: the angle between the body axis and the flight path,
			// in the body's own pitch plane.
			axis := s.Attitude.Rotate(flight.Vec3{X: 1})
			up := s.Attitude.Rotate(flight.Vec3{Y: 1})
			if a := math.Atan2(-now.Dot(up), now.Dot(axis)) * 180 / math.Pi; a > peakAlpha {
				peakAlpha = a
			}
			if tick == 240*3-1 {
				at3 = turned * 180 / math.Pi
			}
		}
		fmt.Printf("  %4.0f | %6.1f d/s | %10.0f m   | %7.1f d  | %10.0f d  | %10.0f d  | %8.0f\n",
			kt, peak, radius, peakAlpha, at3, turned*180/math.Pi, m.State.Velocity.Length()*1.944)
	}
}

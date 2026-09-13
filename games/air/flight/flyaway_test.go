// Mochi world: Catapult flyaway
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// flyaway is one hands-off catapult shot: the settle below the release
// height, the pitch at release, the peak alpha after it, the alpha each
// second after release, whether the takeoff trim latch was still set once
// airborne, whether the flyaway capture outlived the approach law, and the
// pitch at the end of the flight.
type flyaway struct {
	settle, pitch, peak float64
	alphas              []float64
	latched, lingered   bool
	final               float64
}

// shoot flies the game's deck start: the Fox 3 fit with its centreline tank,
// HALF flap, military power short of the burner and the ship's 25 kt down the
// deck. stroke is the catapult's run; the harbor deck edge is 117 m ahead of
// the shuttle. With clean set the pilot raises the gear and selects AUTO six
// seconds after a positive rate, as the launch hints coach it, and the flight
// runs on for 40 s.
func shoot(t *testing.T, fuel float64, stroke float64, clean bool) flyaway {
	t.Helper()
	world := harbor()
	world.Carrier.Catapults[0].Stroke = stroke
	m := New(Fighter, Environment{Wind: Vec3{X: -12.9}}, world)
	park(m, 42.7, -0.6)
	var mask uint64
	for i, store := range m.Airframe.Stores {
		switch store.Name {
		case "tip1", "tip9", "rail2", "120c2", "pylon3", "120c3", "rail4", "120c4", "pylon5", "tank5", "rail6", "120c6", "pylon7", "120c7", "rail8", "120c8":
			mask |= 1 << uint(i)
		}
	}
	m.Stores(mask)
	m.State.Fuel = fuel
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true, Flap: 1})
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("did not attach: %d", m.State.Gear.Catapult)
	}
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Gear: true, Flap: 1, Throttle: 0.93})
	}
	duration := 12
	if clean {
		duration = 40
	}
	result := flyaway{}
	release, lowest := math.NaN(), math.Inf(1)
	released, positive := -1, -1
	gear, flap := true, 1.0
	airborne := false // off the deck since the previous step, so the FCS has run once without weight on wheels
	for i := 0; i < 240*duration; i++ {
		m.Step(Inputs{Gear: gear, Flap: flap, Throttle: 0.93, Launch: i < 240})
		if math.IsNaN(release) {
			if i > 0 && m.State.Gear.Catapult < 0 && m.State.Gear.Stroke < 0 {
				release, released = m.State.Position.Y, i
				forward := m.State.Attitude.Rotate(Vec3{X: 1})
				result.pitch = math.Asin(clamp(forward.Y, -1, 1))
			}
			continue
		}
		body := m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust))
		lowest = math.Min(lowest, m.State.Position.Y)
		result.peak = math.Max(result.peak, alpha(body))
		if (i-released)%240 == 0 {
			result.alphas = append(result.alphas, alpha(body))
		}
		if airborne && m.launch {
			result.latched = true
		}
		airborne = !m.State.Gear.Wow
		if positive < 0 && airborne && m.State.Velocity.Y > 0.5 {
			positive = i
		}
		if clean && positive >= 0 && i-positive >= 240*6 {
			gear, flap = false, 0
		}
		if !m.pa && m.flyaway {
			result.lingered = true
		}
	}
	if math.IsNaN(release) {
		t.Fatal("the catapult never released")
	}
	forward := m.State.Attitude.Rotate(Vec3{X: 1})
	result.final = math.Asin(clamp(forward.Y, -1, 1))
	result.settle = release - lowest
	return result
}

// TestCatapultSettle: NATOPS 8.2.5 - at 44,000 lb and below "little to no
// sink should be observed" off the bow (4 to 6 ft at normal endspeed for the
// heavier boards), and the flyaway rotates to as high as 13 degrees alpha
// before settling on the reference. The Fox 3 fit weighs 32,700 lb with the
// saved 2,100 lb of fuel and 41,400 lb full. Both deck geometries are flown:
// the game's, where the jet rolls 33 m of deck after the stroke, and a stroke
// that ends at the bow.
func TestCatapultSettle(t *testing.T) {
	for _, stroke := range []float64{85, 112} {
		for _, fuel := range []float64{952, 4900} {
			shot := shoot(t, fuel, stroke, false)
			t.Logf("stroke %.0f m, fuel %.0f kg: settle %.1f ft, release pitch %.1f°, peak alpha %.1f°",
				stroke, fuel, shot.settle*3.28084, shot.pitch*180/math.Pi, shot.peak*180/math.Pi)
			if shot.settle > 6/3.28084 {
				t.Errorf("stroke %.0f m, fuel %.0f kg: settled %.1f ft off the bow", stroke, fuel, shot.settle*3.28084)
			}
			// The launch bar holds the deck attitude down the stroke: trim
			// that bit before the release rotated the jet on the shuttle.
			if shot.pitch > 2*math.Pi/180 {
				t.Errorf("stroke %.0f m, fuel %.0f kg: %.1f° nose up at release", stroke, fuel, shot.pitch*180/math.Pi)
			}
			// The rotation reaches the reference region, and does not overshoot
			// into the alpha NATOPS warns costs lateral control.
			if shot.peak < 10*math.Pi/180 || shot.peak > 15*math.Pi/180 {
				t.Errorf("stroke %.0f m, fuel %.0f kg: peak alpha %.1f°", stroke, fuel, shot.peak*180/math.Pi)
			}
			// The trim is the ground law's: once airborne the flyaway law owns
			// the integrator, and a latch left set would trim the next rollout.
			if shot.latched {
				t.Errorf("stroke %.0f m, fuel %.0f kg: takeoff trim still latched airborne", stroke, fuel)
			}
		}
	}
}

// TestFlyawayCapture: NATOPS 8.2.8 - "the longitudinal flight control system
// is designed to rotate the aircraft to a reference or capture AOA following
// catapult launch", the trim setting mapping linearly to it; below 48,000 lb
// a hands-off rotation peaks near 12° and settles toward 10-11°. The 16° trim
// is not an attitude: captured as one, the light jet's alpha bled to 7° by
// six seconds as it accelerated. The capture ends with the approach law at the
// clean-up, and the climb survives the change to the up-and-away law.
func TestFlyawayCapture(t *testing.T) {
	for _, fuel := range []float64{952, 4900} {
		shot := shoot(t, fuel, 85, true)
		for second := 3; second <= 6; second++ {
			a := shot.alphas[second] * 180 / math.Pi
			t.Logf("fuel %.0f kg: alpha %.1f° at t+%d s", fuel, a, second)
			if a < 8.5 || a > 11.5 {
				t.Errorf("fuel %.0f kg: alpha %.1f° at t+%d s, want the ~10° reference", fuel, a, second)
			}
		}
		if shot.lingered {
			t.Errorf("fuel %.0f kg: the flyaway capture outlived the approach law", fuel)
		}
		t.Logf("fuel %.0f kg: pitch %.1f° after 40 s", fuel, shot.final*180/math.Pi)
		if shot.final < 6*math.Pi/180 {
			t.Errorf("fuel %.0f kg: climb collapsed after the law switch: pitch %.1f°", fuel, shot.final*180/math.Pi)
		}
	}
}

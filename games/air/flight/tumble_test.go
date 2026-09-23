// Mochi world: A damaged jet's tumble stays a jet
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight_test

import (
	"math"
	"testing"

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

// tumble is a damaged ace one tick before the model threw it from 1.9 rad/s
// into a 20 rad/s tumble, and a few seconds later to not-a-number: one engine
// dead and the other down 36%, 300 kg of structure gone, 93 m/s at 3,821 m.
// Captured with State.Encode from the guns fight of two aces with identifier
// "revisedguns35" at seed 35, tick 8,843.
var tumble = []float64{
	10499.618788692182, 3820.6176530970824, 7946.907382401872, -78.01836059270448, -19.29907459409454, -48.355060759437094,
	0.09754958309890725, 0.1502974892953282, 0.25971149184318243, 0.9489176384456866, 0.7651428853780623, -1.6758109910192585,
	-0.2005987198986199, 4221.633446057283, 0.9948041330254531, 0.9988418819641695, 0.9948041330254531, 0.9988418819641695,
	0, 0, 0, 0, -0.42, -0.3608638279753544,
	-0.020632685571275698, -0.10544667944933367, -0.015060829770509605, 0, 0.058414551725722894, 0,
	-0.5032982764550855, 0.3449104842511021, 0.7955505338922748, 0.3814707340020549, -0.07208689395794321, 0,
	-1, -1, -1, 0, -1, 0,
	0, 0, 0, 0.3581, 1, 0,
	0, 3.34575, 1.7036250000000017, 0, 0, 0.08,
	1.1581310892089114, 147.38333333328146, 0.2622468901249477, 0.77625, 1, 0.5,
	0.10125, 1, 1, 1, 1, 0.10125,
	0.2025, 1, 0.725, 0.5, 1, 1,
	1, 0.95, 1, 0.225, 1, 0.675,
	1, 0, 0, 0.225, 0.10125, 0.45,
	0, 0, 1, 1, 1, 0.2025,
	0, 0, 0, 0, 0, 0,
	0, 0, 0, 1, 1, 1,
	0, 0, 0, 300, 0, 1,
	0.45, -0.6366217534752444, 0, 0, 0, 0,
}

// TestTumbleStaysPhysical: flown for a second on the inputs it had, that jet
// must stay a jet. One strake element's section flow fell under a metre a
// second against 47 m/s of flow over the body, and the ratio that carries the
// calibrated terms onto the section pressure rose into the thousands: 4.9 MN
// of force at 13 m/s. A healthy jet turns under 7 rad/s, and no aerodynamic
// force at this speed changes it by a metre a second in a 240th of a second.
func TestTumbleStaysPhysical(t *testing.T) {
	m := flight.New(fa18c.Airframe, flight.Environment{Seed: 35, Wrap: 250000}, flight.World{Sea: 3})
	m.State = flight.Decode(tumble)
	m.Stores(0)
	in := flight.Inputs{Pitch: -0.15006032667482405, Roll: 1, Throttle: 1, Reheat: 1}
	speed := m.State.Velocity.Length()
	for step := 1; step <= 240; step++ {
		m.Step(in)
		now, spin := m.State.Velocity.Length(), m.State.Omega.Length()
		if math.IsNaN(now) || math.IsNaN(spin) || spin > 15 || math.Abs(now-speed) > 1 {
			t.Fatalf("step %d: %.3g m/s (was %.3g) turning at %.3g rad/s: the tumble has left the airframe", step, now, speed, spin)
		}
		speed = now
	}
}

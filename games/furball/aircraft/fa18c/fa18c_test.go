// Mochi world: F/A-18C dataset gates
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Aircraft-level gates through the core's public surface: the legacy
// Hornet must trim, hold hands-off, accelerate in its documented band
// (quicker than the F everywhere), dash supersonic, and work the deck.

package fa18c

import (
	"math"
	"testing"

	"world/games/furball/flight"
)

func model() *flight.Model {
	m := flight.New(Airframe, flight.Environment{}, flight.World{})
	m.State.Fuel = 2450 // half internal
	return m
}

// TestTrim: the bare airframe glide-trims.
func TestTrim(t *testing.T) {
	if _, _, _, ok := flight.Glide(model(), 200, 3000); !ok {
		t.Fatal("glide trim did not converge")
	}
}

// TestHold: hands-off level flight stays near one g across the envelope.
func TestHold(t *testing.T) {
	for _, speed := range []float64{150, 260, 380} {
		m := model()
		m.State = flight.Level(m, flight.Vec3{Y: 2000}, flight.Vec3{X: 1}, speed, 2450)
		in := flight.Inputs{Throttle: m.State.Engine[0].Spool}
		for i := 0; i < 240*6; i++ {
			m.Step(in)
		}
		if nz := m.Nz(); math.Abs(nz-1) > 0.2 {
			t.Fatalf("hands-off at %.0f m/s: nz %.2f", speed, nz)
		}
	}
}

// TestAccelerate: the documented benchmark shape — level M0.8 to M1.2 at
// 35,000 ft in full afterburner, held level by a small pitch loop. The C
// must be markedly quicker than the F here (its documented edge).
func TestAccelerate(t *testing.T) {
	m := flight.New(Airframe, flight.Environment{}, flight.World{})
	altitude := 10668.0
	sound := flight.Atmosphere(altitude, m.Environment).Sound
	m.State = flight.Level(m, flight.Vec3{Y: altitude}, flight.Vec3{X: 1}, 0.8*sound, 2450)
	reached := -1.0
	for i := 0; i < 240*140; i++ {
		pitch := clamp((altitude-m.State.Position.Y)*0.002-m.State.Velocity.Y*0.04, -0.4, 0.4)
		m.Step(flight.Inputs{Throttle: 1, Reheat: 1, Pitch: pitch})
		if m.Mach() >= 1.2 {
			reached = m.State.Time
			break
		}
	}
	if reached < 0 || reached < 55 || reached > 95 {
		t.Fatalf("M0.8->1.2 at 35k ft took %.0f s (band 55-95)", reached)
	}
}

// TestDash: thrust exceeds drag past M1.45 at 35,000 ft (top ~M1.55).
func TestDash(t *testing.T) {
	m := model()
	altitude := 10668.0
	sound := flight.Atmosphere(altitude, m.Environment).Sound
	speed := 1.45 * sound
	angle := 0.0
	weightForce := (Airframe.Mass.Empty + 2450) * 9.80665
	pressure := 0.5 * flight.Atmosphere(altitude, m.Environment).Density * speed * speed
	for sweep := -0.02; sweep < 0.2; sweep += 0.002 {
		cl, _ := m.Evaluate(speed, sweep, altitude)
		angle = sweep
		if cl*pressure*Airframe.Reference.Area >= weightForce {
			break
		}
	}
	_, drag := m.Evaluate(speed, angle, altitude)
	_, wet := m.Thrust(speed, altitude)
	if drag*pressure*Airframe.Reference.Area >= wet {
		t.Fatalf("cannot hold M1.45 at 35k ft: drag %.0f kN vs %.0f kN", drag*pressure*Airframe.Reference.Area/1000, wet/1000)
	}
}

// TestStall: maximum lift in the fighter band.
func TestStall(t *testing.T) {
	m := model()
	best := 0.0
	for sweep := 0.0; sweep < 0.6; sweep += 0.01 {
		cl, _ := m.Evaluate(100, sweep, 2000)
		if cl > best {
			best = cl
		}
	}
	if best < 1.3 || best > 2.1 {
		t.Fatalf("CLmax %.2f outside 1.3-2.1", best)
	}
}

// TestDeck: rests on a deck with brakes held, then a full-burner cat shot
// releases at flying speed — the C's gear and hook geometry work the boat.
func TestDeck(t *testing.T) {
	world := flight.World{Carrier: &flight.Carrier{
		Position: flight.Vec3{Y: 19},
		Deck:     []flight.Vec3{{X: -165, Z: -22}, {X: -165, Z: 22}, {X: 165, Z: 22}, {X: 165, Z: -22}},
		Catapults: []flight.Catapult{
			{Position: flight.Vec3{X: 48, Z: -0.6}, Heading: 0.028, Stroke: 85, Speed: 88},
		},
	}}
	m := flight.New(Airframe, flight.Environment{}, world)
	m.State.Fuel = 2450
	m.State.Position = flight.Vec3{X: 48 - Airframe.Gear.Nose.Attach.X, Y: 19 - Airframe.Gear.Nose.Attach.Y, Z: -0.6}
	m.State.Attitude = flight.Axis(flight.Vec3{Y: 1}, -0.028)
	held := m.State.Position
	for i := 0; i < 240*10; i++ {
		m.Step(flight.Inputs{Gear: true, Brake: true})
	}
	if !m.State.Gear.Wow {
		t.Fatal("no weight on wheels at rest")
	}
	if drift := m.State.Position.Subtract(held).Length(); drift > 1 {
		t.Fatalf("drifted %.2f m parked with brakes", drift)
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("did not attach to the catapult: %d", m.State.Gear.Catapult)
	}
	released := false
	for i := 0; i < 240*8; i++ {
		m.Step(flight.Inputs{Gear: true, Throttle: 1, Reheat: 1, Launch: i > 480})
		if m.State.Gear.Catapult < 0 && !released && i > 480 {
			released = true
			if speed := m.State.Velocity.Length(); speed < 80 {
				t.Fatalf("limp cat shot: %.1f m/s at release", speed)
			}
		}
	}
	if !released {
		t.Fatal("the shot never fired")
	}
	if m.State.Position.Y < 15 {
		t.Fatalf("settled after launch: %.1f m", m.State.Position.Y)
	}
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Min(math.Max(v, low), high)
}

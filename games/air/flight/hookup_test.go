// Mochi world: Catapult hookup gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// approaching parks the jet crabbed off the catapult track by crab degrees,
// its nose gear back metres short of the shuttle along its own heading and
// offset metres to the side of the track line, rolling at speed.
func approaching(m *Model, crab, back, offset, speed float64) {
	c := m.World.Carrier
	cat := &c.Catapults[0]
	shuttle := c.world(cat.Position, 0)
	heading := c.Heading + cat.Heading
	track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
	side := Vec3{X: track.Z, Z: -track.X}
	theta := heading + crab*math.Pi/180
	park(m, 0, 0)
	m.State.Attitude = Quat{W: math.Cos(theta / 2), Y: math.Sin(theta / 2)}
	fwd := m.State.Attitude.Rotate(Vec3{X: 1})
	nose := m.Airframe.Gear.Nose.Attach.Subtract(m.center)
	point := shuttle.Subtract(track.Scale(back)).Add(side.Scale(offset))
	m.State.Position = Vec3{X: point.X - fwd.X*nose.X, Y: 21.52, Z: point.Z - fwd.Z*nose.X}
	m.State.Velocity = fwd.Scale(speed)
}

// posture reports the jet's bank and pitch, degrees, and its swing off the
// catapult track, degrees, positive with the nose left of the track.
func posture(m *Model) (bank, pitch, swing float64) {
	s := &m.State
	c := m.World.Carrier
	heading := c.Heading + c.Catapults[0].Heading
	track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
	f := s.Attitude.Rotate(Vec3{X: 1})
	r := s.Attitude.Rotate(Vec3{Z: 1})
	u := s.Attitude.Rotate(Vec3{Y: 1})
	return math.Atan2(r.Y, u.Y) * 180 / math.Pi, math.Asin(f.Y) * 180 / math.Pi, math.Asin(clamp(f.X*track.Z-f.Z*track.X, -1, 1)) * 180 / math.Pi
}

// hooked idles the jet up to the given seconds and reports when it attached,
// or -1.
func hooked(m *Model, seconds float64) float64 {
	for i := 0; i < int(240*seconds); i++ {
		m.Step(Inputs{Gear: true, Throttle: 0.15})
		if m.State.Gear.Catapult >= 0 {
			return float64(i+1) / 240
		}
	}
	return -1
}

// TestHookupGate: the crew hook up a jet whose nose gear is on the track line
// and pointing down it - the launch bar has to drop into the shuttle. A nose
// off the line, or a heading well off the track, taxis past unhooked.
func TestHookupGate(t *testing.T) {
	cases := []struct {
		name          string
		crab, offset  float64
		speed, expect float64
	}{
		{"square on the line", 0, 0, 2, 1},
		{"8 degrees, half a metre off", 8, 0.5, 2, 1},
		{"20 degrees on the line", 20, 0, 2, 0},
		{"square, three metres off the line", 0, 3, 2, 0},
		{"parked square on the shuttle", 0, 0, 0, 1},
		{"parked 15 degrees on the shuttle", 15, 0, 0, 0},
	}
	for _, c := range cases {
		m := aboard()
		back := 3.0
		if c.speed == 0 {
			back = 0
		}
		approaching(m, c.crab, back, c.offset, c.speed)
		at := hooked(m, 4)
		if (at >= 0) != (c.expect == 1) {
			t.Errorf("%s: attached at %.2f s, want attached %v", c.name, at, c.expect == 1)
		}
	}
}

// TestHookupCrab: a hookup that arrives crabbed is squared by the bar in the
// slot at idle, and stays square and upright under mil power and through the
// launch - the topple was a crabbed jet under thrust leaning on its tyres.
func TestHookupCrab(t *testing.T) {
	// A parked crab has the mains scrubbing against the couple and squares to
	// a few degrees; a rolling hookup, the way a jet actually arrives, squares
	// while the wheels turn.
	cases := []struct {
		name                string
		crab, offset, speed float64
		square              float64 // degrees of crab allowed once held
	}{
		{"parked square", 0, 0, 0, 1.5},
		{"parked 5 degrees", 5, 0, 0, 5},
		{"parked 10 degrees", 10, 0, 0, 5},
		{"rolling in square", 0, 0, 3, 1.5},
		{"rolling in 5 degrees", 5, 0.4, 3, 1.5},
		{"rolling in 10 degrees", 10, 0.8, 3, 1.5},
	}
	for _, c := range cases {
		m := aboard()
		back := 6.0
		if c.speed == 0 {
			back = 0
		}
		approaching(m, c.crab, back, c.offset, c.speed)
		if at := hooked(m, 6); at < 0 {
			t.Fatalf("%s: never attached", c.name)
		}
		peak := 0.0
		for i := 0; i < 240*3; i++ {
			m.Step(Inputs{Gear: true, Throttle: 0.15})
			b, _, _ := posture(m)
			peak = math.Max(peak, math.Abs(b))
		}
		_, _, idle := posture(m)
		t.Logf("%s: idle after 3 s: swing %.2f, peak bank %.2f", c.name, idle, peak)
		if math.Abs(idle) > c.square || peak > 3 {
			t.Errorf("%s: squared to %.1f° with a peak bank of %.1f° at idle, want under %.1f° and 3°", c.name, idle, peak, c.square)
		}
		peak = 0
		for i := 0; i < 240*6; i++ {
			m.Step(Inputs{Gear: true, Throttle: 1})
			b, _, _ := posture(m)
			peak = math.Max(peak, math.Abs(b))
		}
		b, p, mil := posture(m)
		t.Logf("%s: mil after 6 s: bank %.2f pitch %.2f swing %.2f, peak bank %.2f", c.name, b, p, mil, peak)
		if m.State.Gear.Catapult < 0 || peak > 3 || math.Abs(mil) > c.square || m.State.Gear.Contact >= 0 {
			t.Errorf("%s: at mil: cat %d, peak bank %.1f°, swing %.1f°, contact %d - want held, under 3° and %.1f°, no strike", c.name, m.State.Gear.Catapult, peak, mil, m.State.Gear.Contact, c.square)
		}
		fired := -1.0
		for i := 0; i < 240*6; i++ {
			m.Step(Inputs{Gear: true, Throttle: 1, Launch: true})
			if m.State.Gear.Stroke >= 0 && fired < 0 {
				fired = float64(i+1) / 240
			}
		}
		if fired < 0 || fired > 4 {
			t.Errorf("%s: the shot fired at %.1f s after the launch request, want within 4 s", c.name, fired)
		}
		if m.State.Position.Y < 15 || math.IsNaN(m.State.Position.Y) || m.State.Gear.Contact >= 0 {
			t.Errorf("%s: did not fly away: y=%.1f contact %d", c.name, m.State.Position.Y, m.State.Gear.Contact)
		}
	}
}

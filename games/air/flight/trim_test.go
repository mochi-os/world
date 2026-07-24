// Mochi world: Trim and spawn helper tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestLevel: the spawn helper produces flight the FCS holds without a
// transient — a second later the aircraft is still level, near 1g, on speed.
func TestLevel(t *testing.T) {
	m := New(Fighter, Environment{Wrap: 250000}, World{})
	s := Level(m, Vec3{Y: 4572}, Vec3{X: 1}, 220, 3000)
	m.State = s
	in := Inputs{Throttle: s.Engine[0].Spool}
	for i := 0; i < 240*3; i++ {
		m.Step(in)
	}
	if math.Abs(m.State.Position.Y-4572) > 60 {
		t.Fatalf("altitude drifted to %.1f", m.State.Position.Y)
	}
	if speed := m.State.Velocity.Length(); math.Abs(speed-220) > 25 {
		t.Fatalf("speed drifted to %.1f", speed)
	}
	if nz := m.State.Fcs.Normal; math.Abs(nz-1) > 0.3 {
		t.Fatalf("load factor %.2f three seconds after spawn", nz)
	}
}

// TestApproach: the approach spawn helper produces a trimmed on-speed descent
// the PA law holds hands-off. This is the regression the carrier landing start
// lacked — its trim was carried as hand-measured constants in the client, so a
// core change silently left the spawn ballooning off the glideslope.
func TestApproach(t *testing.T) {
	for _, slope := range []float64{-3.5, -4.0} {
		path := slope * math.Pi / 180
		m := New(Fighter, Environment{Wrap: 250000}, World{})
		s, spool := Approach(m, Vec3{Y: 400}, Vec3{X: 1}, path, 3000)
		m.State = s
		onspeed := m.Airframe.Control.Onspeed * 180 / math.Pi

		entry := s.Velocity.Length()
		sink := -s.Velocity.Y
		low, high := entry, entry
		for i := 0; i < 240*20; i++ {
			m.Step(Inputs{Throttle: spool, Gear: true})
			speed := m.State.Velocity.Length()
			low, high = math.Min(low, speed), math.Max(high, speed)
		}

		// On-speed: the wing condition is what the helper solves for, so alpha
		// is the assertion that matters — the speed is whatever the weight makes it.
		v := m.State.Attitude.Unrotate(m.State.Velocity)
		if held := alpha(v) * 180 / math.Pi; math.Abs(held-onspeed) > 0.6 {
			t.Errorf("%.1f°: alpha %.2f° after 20 s, want on-speed %.2f°", slope, held, onspeed)
		}
		// Hands-off it must still be descending at about the requested slope —
		// the ballooning spawn climbed instead.
		descent := -m.State.Velocity.Y
		if math.Abs(descent-sink) > 1.5 {
			t.Errorf("%.1f°: sink %.2f m/s after 20 s, spawned at %.2f", slope, descent, sink)
		}
		// No phugoid excursion worth the name: a mistrimmed spawn shows up here
		// as a speed swing long before it shows up in alpha.
		if high-low > 6 {
			t.Errorf("%.1f°: speed ranged %.1f..%.1f (spawn %.1f) — spawn is not trimmed", slope, low, high, entry)
		}
		if spool <= 0.05 || spool >= 0.9 {
			t.Errorf("%.1f°: implausible approach power %.2f", slope, spool)
		}
	}
}

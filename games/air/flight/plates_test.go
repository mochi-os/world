// Mochi world: Configuration flat-plate and store tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// wind-axis drag of the composed state at 150 m/s clean-level attitude.
func dragOf(m *Model, shape func(*Model)) float64 {
	s := &m.State
	s.Position = Vec3{Y: 2000}
	s.Velocity = Vec3{X: 150}
	s.Attitude = Axis(Vec3{Z: 1}, 0.02)
	s.Omega = Vec3{}
	s.Fcs = FcsState{}
	s.Gear.Extension = 0
	m.probe, m.arrestor = 0, 0
	m.stores = 0
	shape(m)
	m.weigh()
	m.gust = Vec3{}
	local := air(2000, m.Environment)
	total := m.forces(s, Inputs{}, local)
	world := s.Attitude.Rotate(total.Force)
	return -world.X
}

// TestPlates: each deployable adds its published flat-plate drag — the gear
// plate was the first (a pilot caught the gear having no drag at all), and
// the probe, hook, stores, and a dead windmilling engine follow the pattern.
func TestPlates(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State.Fuel = 2500
	dynamic := 0.5 * air(2000, m.Environment).Density * 150 * 150
	clean := dragOf(m, func(*Model) {})
	cases := []struct {
		name string
		area float64
		set  func(*Model)
	}{
		{"probe", 0.05, func(m *Model) { m.probe = 1 }},
		{"hook", 0.01, func(m *Model) { m.arrestor = 1 }},
		{"stores", 0.10, func(m *Model) { m.stores = 3 }},
		{"windmill", 0.15, func(m *Model) { m.State.Damage.Engine[0] = 1 }},
	}
	for _, c := range cases {
		delta := dragOf(m, c.set) - clean
		want := dynamic * c.area
		if math.Abs(delta-want) > want*0.2+1 {
			t.Errorf("%s drag delta %.0f N, want ~%.0f", c.name, delta, want)
		}
		// reset damage for the next case
		m.State.Damage.Engine[0] = 0
	}
}

// TestWindmillYaws: a dead LEFT engine's plate sits at -Z, so its drag must
// yaw the nose toward the failure — the single-engine handling cue.
func TestWindmillYaws(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State.Fuel = 2500
	s := &m.State
	s.Position = Vec3{Y: 2000}
	s.Velocity = Vec3{X: 150}
	s.Attitude = Axis(Vec3{Z: 1}, 0.02)
	s.Fcs = FcsState{}
	s.Gear.Extension = 0
	m.stores = 0
	s.Damage.Engine[0] = 1 // engine 0 = -Z (port)
	m.weigh()
	m.gust = Vec3{}
	local := air(2000, m.Environment)
	total := m.forces(s, Inputs{}, local)
	if total.Moment.Y <= 0 {
		t.Fatalf("dead port engine yawed the wrong way: My=%.1f (want nose toward the failure, +Y)", total.Moment.Y)
	}
}

// TestStoresWeigh: the wingtip missiles carry real mass — attached they add
// it symmetrically; firing ONE shifts the CG toward the remaining tip.
func TestStoresWeigh(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State.Fuel = 2500
	m.Stores(0)
	m.weigh()
	bare := m.mass
	m.Stores(3)
	m.weigh()
	if both := m.mass - bare; math.Abs(both-172) > 1 {
		t.Fatalf("two wingtip stores added %.1f kg, want 172", both)
	}
	if math.Abs(m.center.Z) > 0.01 {
		t.Fatalf("symmetric stores skewed the CG: Z=%.3f", m.center.Z)
	}
	m.Stores(2) // port missile fired, starboard (+Z) remains
	m.weigh()
	if m.center.Z <= 0 {
		t.Fatalf("one remaining starboard store should pull the CG to +Z, got %.3f", m.center.Z)
	}
}

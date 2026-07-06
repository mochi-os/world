// Mochi world: Damage application tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Stage-1 gates for #78: the flight core APPLIES damage correctly. Damage
// decisions are the battle package's business and are tested there.

package flight_test

import (
	"math"
	"testing"

	"world/games/furball/aircraft/fa18c"
	"world/games/furball/flight"
)

func cruising(t *testing.T) *flight.Model {
	t.Helper()
	m := flight.New(fa18c.Airframe, flight.Environment{}, flight.World{Sea: 0})
	m.State = flight.Level(m, flight.Vec3{Y: 2000}, flight.Vec3{X: 1}, 180, fa18c.Airframe.Mass.Fuel*0.7)
	return m
}

// wing returns the flattened element index range of the wing on one side.
func wing(a *flight.Airframe, side float64) (int, int) {
	base := 0
	for si := range a.Surfaces {
		s := &a.Surfaces[si]
		if s.Kind == flight.Wing && s.Side == side {
			return base, base + len(s.Elements)
		}
		base += len(s.Elements)
	}
	return -1, -1
}

// TestElementLossRolls: killing one wing's outboard elements must roll the
// jet toward that wing, mirror-symmetric between sides — lift asymmetry
// emerging from geometry, not scripted moments. The absolute visual sign is
// a stage-4 (eyes-on) check; here we gate on asymmetry and symmetry.
func TestElementLossRolls(t *testing.T) {
	rate := func(side float64) float64 {
		m := cruising(t)
		m.Direct = true // raw airframe: the FCS would fight the asymmetry and mask the physics
		first, last := wing(fa18c.Airframe, side)
		if first < 0 {
			t.Fatalf("no wing found for side %+.0f", side)
		}
		m.State.Damage.Element = make([]float64, flight.Elements)
		for i := (first + last) / 2; i < last; i++ {
			m.State.Damage.Element[i] = 1 // outboard half gone
		}
		for i := 0; i < 24; i++ { // 0.1 s: the initial response, before the roll couples
			m.Step(flight.Inputs{Throttle: 0.5})
		}
		return m.State.Omega.X
	}
	left, right := rate(-1), rate(1)
	if math.Abs(left) < 0.2 {
		t.Fatalf("wing loss produced no meaningful roll: %.3f rad/s after 0.1 s", left)
	}
	if math.Abs(left+right) > 0.02 {
		t.Fatalf("wing loss must be mirror-symmetric: left-damaged %.3f, right-damaged %.3f", left, right)
	}
}

// TestJamFreezes: a fully jammed stabilator holds its deflection against
// commands; the healthy one keeps moving.
func TestJamFreezes(t *testing.T) {
	m := cruising(t)
	for i := 0; i < 60; i++ {
		m.Step(flight.Inputs{Throttle: 0.5})
	}
	m.State.Damage.Jam = make([]float64, flight.Channels)
	m.State.Damage.Jam[flight.ChannelStabilatorLeft] = 1
	held := m.State.Fcs.Stabilator.Left
	for i := 0; i < 240; i++ {
		m.Step(flight.Inputs{Throttle: 0.5, Pitch: 1})
	}
	if math.Abs(m.State.Fcs.Stabilator.Left-held) > 1e-9 {
		t.Fatalf("jammed stabilator moved: %.4f -> %.4f", held, m.State.Fcs.Stabilator.Left)
	}
	if math.Abs(m.State.Fcs.Stabilator.Right-held) < 1e-3 {
		t.Fatal("healthy stabilator should have answered the pitch command")
	}
}

// TestLeakFlameout: a leak drains the tanks and the engines flame out —
// no thrust, no reheat, forever.
func TestLeakFlameout(t *testing.T) {
	m := cruising(t)
	m.State.Fuel = 20
	m.State.Damage.Leak = 50 // catastrophic leak: dry in under a second
	full := flight.Inputs{Throttle: 1, Reheat: 1}
	for i := 0; i < 240*3; i++ {
		m.Step(full)
	}
	if m.State.Fuel > 0 {
		t.Fatalf("leak failed to drain the tanks: %.1f kg left", m.State.Fuel)
	}
	if m.State.Engine[0].Spool > 0.05 || m.State.Engine[0].Reheat > 0.01 {
		t.Fatalf("flameout failed: spool %.3f reheat %.3f on dry tanks", m.State.Engine[0].Spool, m.State.Engine[0].Reheat)
	}
}

// TestEncodeDamage: the full damage state survives the wire bit-exact, and
// a pristine state round-trips to nil slices (no allocation, stable).
func TestEncodeDamage(t *testing.T) {
	s := flight.State{}
	s.Damage.Element = make([]float64, flight.Elements)
	s.Damage.Element[7] = 0.5
	s.Damage.Element[33] = 1
	s.Damage.Jam = make([]float64, flight.Channels)
	s.Damage.Jam[flight.ChannelRudder] = 0.5
	s.Damage.Loss = 300
	s.Damage.Stress = 2.5
	buffer := make([]float64, flight.Size)
	if n := s.Encode(buffer); n != flight.Size {
		t.Fatalf("Encode returned %d, want %d", n, flight.Size)
	}
	back := flight.Decode(buffer)
	if back.Damage.Element[7] != 0.5 || back.Damage.Element[33] != 1 || back.Damage.Jam[flight.ChannelRudder] != 0.5 || back.Damage.Loss != 300 || back.Damage.Stress != 2.5 {
		t.Fatal("damage did not survive the round trip")
	}
	clean := flight.Decode(make([]float64, flight.Size))
	if clean.Damage.Element != nil || clean.Damage.Jam != nil {
		t.Fatal("a pristine wire must decode to nil slices")
	}
}

// TestLossLightens: shed mass reduces total weight — probed through thrust
// response (F = ma) in Direct mode, where no control loop can mask it.
func TestLossLightens(t *testing.T) {
	gain := func(loss float64) float64 {
		m := cruising(t)
		m.Direct = true
		m.State.Damage.Loss = loss
		start := m.State.Velocity.Length()
		for i := 0; i < 240; i++ { // 1 s at full throttle
			m.Step(flight.Inputs{Throttle: 1})
		}
		return m.State.Velocity.Length() - start
	}
	pristine, lighter := gain(0), gain(3000)
	if lighter <= pristine*1.05 {
		t.Fatalf("a 3 t lighter jet must out-accelerate the pristine one: %.2f vs %.2f m/s gained", lighter, pristine)
	}
}

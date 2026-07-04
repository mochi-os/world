// Mochi world: Rigid-body tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// block is a bare test airframe: a 1000 kg body with an asymmetric inertia
// tensor (distinct principal moments for the Dzhanibekov test), no fuel.
func block() *Airframe {
	a := &Airframe{}
	a.Mass.Empty = 1000
	a.Inertia = Mat3{{200, 0, 0}, {0, 1000, 0}, {0, 0, 600}}
	return a
}

// TestParabola: with no aero forces a thrown body follows the closed-form
// ballistic arc.
func TestParabola(t *testing.T) {
	m := New(block(), Environment{}, World{})
	m.State.Velocity = Vec3{X: 100, Y: 50}
	steps := 240 * 4 // four seconds
	for i := 0; i < steps; i++ {
		m.Step(Inputs{})
	}
	elapsed := float64(steps) * Dt
	wantX := 100 * elapsed
	wantY := 50*elapsed - 0.5*m.Gravity*elapsed*elapsed
	if math.Abs(m.State.Position.X-wantX) > 1e-6 || math.Abs(m.State.Position.Y-wantY) > 1e-6 {
		t.Fatalf("got (%f, %f), want (%f, %f)", m.State.Position.X, m.State.Position.Y, wantX, wantY)
	}
	if m.State.Time < elapsed-1e-9 {
		t.Fatalf("time not advanced: %f", m.State.Time)
	}
}

// TestMomentum: with zero applied moment, world-frame angular momentum is
// conserved even while the body tumbles.
func TestMomentum(t *testing.T) {
	m := New(block(), Environment{}, World{})
	m.Gravity = 0
	m.State.Omega = Vec3{X: 1.0, Y: 0.3, Z: 2.0}
	m.weigh()
	initial := m.State.Attitude.Rotate(m.inertia.Apply(m.State.Omega))
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{})
	}
	final := m.State.Attitude.Rotate(m.inertia.Apply(m.State.Omega))
	if final.Subtract(initial).Length() > 1e-3*initial.Length() {
		t.Fatalf("angular momentum drifted: %+v -> %+v", initial, final)
	}
}

// TestDzhanibekov: a spin about the intermediate principal axis (z here:
// 600 between 200 and 1000) is unstable and must flip, proving the inertia
// coupling terms are right.
func TestDzhanibekov(t *testing.T) {
	m := New(block(), Environment{}, World{})
	m.Gravity = 0
	m.State.Omega = Vec3{X: 0.001, Y: 0.001, Z: 3.0} // intermediate axis + tiny perturbation
	flipped := false
	for i := 0; i < 240*120 && !flipped; i++ {
		m.Step(Inputs{})
		if m.State.Omega.Z < -1.0 {
			flipped = true // the tumble reversed the spin axis
		}
	}
	if !flipped {
		t.Fatal("no Dzhanibekov flip: inertia coupling is wrong")
	}
	// A stable-axis spin (largest moment, y) must NOT flip.
	m = New(block(), Environment{}, World{})
	m.Gravity = 0
	m.State.Omega = Vec3{X: 0.001, Y: 3.0, Z: 0.001}
	for i := 0; i < 240*120; i++ {
		m.Step(Inputs{})
		if m.State.Omega.Y < 0 {
			t.Fatal("stable-axis spin flipped")
		}
	}
}

// TestHold: a no-force, no-gravity, no-rotation run holds state constant.
func TestHold(t *testing.T) {
	m := New(block(), Environment{Wrap: 250000}, World{})
	m.Gravity = 0
	m.State.Position = Vec3{X: 5000, Y: 3000, Z: -2000}
	m.State.Velocity = Vec3{}
	before := m.State
	for i := 0; i < 240; i++ {
		m.Step(Inputs{})
	}
	after := m.State
	if after.Position != before.Position || after.Velocity != before.Velocity ||
		after.Attitude != before.Attitude || after.Omega != before.Omega || after.Fuel != before.Fuel {
		t.Fatalf("state drifted with no forces:\n%+v\n%+v", before, after)
	}
}

// TestFuelShift: burning fuel moves the CG toward the empty CG and reduces
// mass (weigh coherence).
func TestFuelShift(t *testing.T) {
	a := block()
	a.Mass.Fuel = 500
	a.Center = Vec3{X: 1}
	a.Tank = Vec3{X: -2}
	m := New(a, Environment{}, World{})
	m.weigh()
	if math.Abs(m.mass-1500) > 1e-9 {
		t.Fatalf("mass %f", m.mass)
	}
	full := m.center.X
	m.State.Fuel = 0
	m.weigh()
	if m.center.X <= full {
		t.Fatalf("CG did not move toward the empty CG: %f -> %f", full, m.center.X)
	}
}

// TestAllocations: the hot path must not allocate.
func TestAllocations(t *testing.T) {
	m := New(block(), Environment{Wrap: 250000}, World{})
	in := Inputs{Throttle: 0.8, Pitch: 0.2}
	if avg := testing.AllocsPerRun(1000, func() { m.Step(in) }); avg != 0 {
		t.Fatalf("Step allocates: %f allocations per run", avg)
	}
}

// TestEngineGuard: an airframe declaring more than four engines must fail
// loudly at construction, not index out of range mid-flight.
func TestEngineGuard(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("five engines were accepted")
		}
	}()
	a := block()
	a.Engines = make([]Engine, 5)
	New(a, Environment{}, World{})
}

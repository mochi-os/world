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

// TestEngineLossSlows: a dead engine must cost measurable acceleration —
// probed like TestLossLightens, in Direct mode at full throttle.
func TestEngineLossSlows(t *testing.T) {
	gain := func(damage float64) float64 {
		m := cruising(t)
		m.Direct = true
		m.State.Damage.Engine[0] = damage
		start := m.State.Velocity.Length()
		for i := 0; i < 240; i++ { // 1 s at full throttle
			m.Step(flight.Inputs{Throttle: 1})
		}
		return m.State.Velocity.Length() - start
	}
	healthy, wounded := gain(0), gain(1)
	if wounded >= healthy*0.75 {
		t.Fatalf("losing an engine must cost real acceleration: %.2f vs %.2f m/s gained", wounded, healthy)
	}
}

// stalls decelerates at idle under an altitude-holding stick and returns the
// speed at sink onset — the operational stall, damaged or not. Symmetric
// damage keeps the wings level so the same governor serves both cases.
func stalls(t *testing.T, wound func(*flight.Model)) float64 {
	t.Helper()
	m := flight.New(fa18c.Airframe, flight.Environment{}, flight.World{Sea: 0})
	m.State = flight.Level(m, flight.Vec3{Y: 2000}, flight.Vec3{X: 1}, 145, fa18c.Airframe.Mass.Fuel*0.5)
	wound(m)
	altitude := m.State.Position.Y
	stick := 0.0
	for i := 0; i < 240*180; i++ {
		s := &m.State
		stick = clamped(stick+clamped(((altitude-s.Position.Y)*0.002-s.Velocity.Y*0.02-stick*4)*0.002, -0.004, 0.004), -0.5, 1)
		m.Step(flight.Inputs{Throttle: 0, Pitch: stick})
		if s.Velocity.Length() > 130 {
			continue // arm on decayed speed, not elapsed time: the trim-to-governor handoff sags a few metres at entry and must not read as the stall
		}
		if altitude-s.Position.Y > 8 && s.Velocity.Y < -1.5 {
			return s.Velocity.Length()
		}
	}
	t.Fatal("the idle decel never reached sink onset")
	return 0
}

func clamped(v float64, low float64, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

// TestWingLossStalls: losing the INBOARD wing structure (both sides, so the
// jet stays level) must wreck slow flight — the surviving area demands far
// more speed for the same weight. The inboard half is the gate because it
// carries the lift; outboard tips detach first at the boundary in this model
// and clipping them measurably LOWERS the sink onset (~6% — a tip-stall/trim
// artifact worth eyes at some point), so they make a treacherous assertion.
func TestWingLossStalls(t *testing.T) {
	pristine := stalls(t, func(m *flight.Model) {})
	clipped := stalls(t, func(m *flight.Model) {
		m.State.Damage.Element = make([]float64, flight.Elements)
		for _, side := range []float64{-1, 1} {
			first, last := wing(fa18c.Airframe, side)
			if first < 0 {
				t.Fatalf("no wing found for side %+.0f", side)
			}
			for i := first; i < (first+last)/2; i++ {
				m.State.Damage.Element[i] = 1
			}
		}
	})
	if clipped < pristine*1.2 {
		t.Fatalf("gutted wings must wreck slow flight: sink onset %.1f vs %.1f m/s pristine", clipped, pristine)
	}
}

// TestJamBluntsPitch: with BOTH stabilators frozen the full-aft pull must
// lose most of its pitch rate — the surface-level freeze (TestJamFreezes)
// has to become a flying consequence.
func TestJamBluntsPitch(t *testing.T) {
	pull := func(jam bool) float64 {
		m := cruising(t)
		for i := 0; i < 240; i++ { // settle the trim first
			m.Step(flight.Inputs{Throttle: 0.5})
		}
		if jam {
			m.State.Damage.Jam = make([]float64, flight.Channels)
			m.State.Damage.Jam[flight.ChannelStabilatorLeft] = 1
			m.State.Damage.Jam[flight.ChannelStabilatorRight] = 1
		}
		peak := 0.0
		for i := 0; i < 240*3/2; i++ { // 1.5 s of full aft stick
			m.Step(flight.Inputs{Throttle: 0.5, Pitch: 1})
			if rate := math.Abs(m.State.Omega.Z); rate > peak {
				peak = rate
			}
		}
		return peak
	}
	healthy, jammed := pull(false), pull(true)
	if jammed >= healthy*0.35 {
		t.Fatalf("jammed stabilators must blunt the pull: peak %.3f vs %.3f rad/s", jammed, healthy)
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

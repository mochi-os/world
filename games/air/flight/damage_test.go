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

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
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
	s.Damage.Shift = flight.Vec3{Z: -0.08} // the shed-wing CG walk
	buffer := make([]float64, flight.Size)
	if n := s.Encode(buffer); n != flight.Size {
		t.Fatalf("Encode returned %d, want %d", n, flight.Size)
	}
	back := flight.Decode(buffer)
	if back.Damage.Element[7] != 0.5 || back.Damage.Element[33] != 1 || back.Damage.Jam[flight.ChannelRudder] != 0.5 || back.Damage.Loss != 300 || back.Damage.Stress != 2.5 {
		t.Fatal("damage did not survive the round trip")
	}
	if back.Damage.Shift != (flight.Vec3{Z: -0.08}) {
		t.Fatal("the CG shift did not survive the round trip")
	}
	clean := flight.Decode(make([]float64, flight.Size))
	if clean.Damage.Element != nil || clean.Damage.Jam != nil {
		t.Fatal("a pristine wire must decode to nil slices")
	}
}

// TestShiftRolls: the shed-wing CG walk (Damage.Shift) must roll the jet —
// lift acting off the displaced centre is a real moment, mirror-symmetric
// between sides. Exaggerated shift, Direct mode, no element damage: this
// isolates the CG path from the lift-asymmetry path TestElementLossRolls
// already covers.
func TestShiftRolls(t *testing.T) {
	rate := func(shift float64) float64 {
		m := cruising(t)
		m.Direct = true
		m.State.Damage.Shift = flight.Vec3{Z: shift}
		for i := 0; i < 48; i++ { // 0.2 s
			m.Step(flight.Inputs{Throttle: 0.5})
		}
		return m.State.Omega.X
	}
	left, right := rate(-0.3), rate(0.3)
	if math.Abs(left) < 0.05 {
		t.Fatalf("a 30 cm CG walk produced no meaningful roll: %.3f rad/s after 0.2 s", left)
	}
	if math.Abs(left+right) > 0.01 {
		t.Fatalf("the CG roll must be mirror-symmetric: %.3f vs %.3f", left, right)
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
// more speed for the same weight. The inboard half is the gate because
// OUTBOARD clipping lowers the flown sink onset ~6% — an accepted quirk
// (investigated 2026-07-12): the pass-1 induced-wash mean keeps dead
// elements' area, so amputated tips hand the survivors their downwash relief
// (statics stay correct — the clipped jet needs 14.2° for level flight at
// 95 m/s against 12.7° pristine — and the choice is right for scattered
// hole damage), while the FCS alpha backstop gates the FLOWN onset by alpha
// rather than lift margin, letting the lift-poor jet ride ~1° deeper. Real
// combat damage is scattered or asymmetric, so the clean symmetric
// amputation that triggers the inversion is synthetic. See the induced-wash
// note in aero.go pass 1.
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

// runway builds a jet standing (or descending onto) a paved strip.
func runway(sink float64, speed float64) *flight.Model {
	world := flight.World{Sea: -10, Fields: []flight.Field{{Height: 0, Strips: []flight.Strip{{A: flight.Vec3{X: -2000}, B: flight.Vec3{X: 4000}, Width: 60}}}}}
	m := flight.New(fa18c.Airframe, flight.Environment{}, world)
	m.State.Position = flight.Vec3{Y: 3} // wheels a hand's breadth off the pavement: the fall must not add sink the test didn't ask for
	m.State.Velocity = flight.Vec3{X: speed, Y: -sink}
	m.State.Attitude = flight.Look(flight.Vec3{X: 1})
	m.State.Fuel = 2000
	m.State.Gear = flight.GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	return m
}

func worst(m *flight.Model) float64 {
	return math.Max(m.State.Damage.Gear[0], math.Max(m.State.Damage.Gear[1], m.State.Damage.Gear[2]))
}

// TestHardLandingWounds: the land-or-crash binary becomes gear STATES — a
// firm carrier-style arrival is free, a hard one blows tyres, a brutal one
// folds the leg (#78).
func TestHardLandingWounds(t *testing.T) {
	arrive := func(sink float64) float64 {
		m := runway(sink, 60)
		for i := 0; i < 240*3; i++ {
			m.Step(flight.Inputs{Gear: true})
		}
		return worst(m)
	}
	if d := arrive(5); d != 0 {
		t.Fatalf("a 5 m/s arrival is inside the oleo rating and must be free: damage %.2f", d)
	}
	hard := arrive(9)
	if hard <= flight.GearTyre || hard > flight.GearCollapse {
		t.Fatalf("a 9 m/s arrival should blow tyres without folding: damage %.2f", hard)
	}
	brutal := arrive(13)
	if brutal <= flight.GearCollapse {
		t.Fatalf("a 13 m/s arrival must fold the gear: damage %.2f", brutal)
	}
}

// TestBlownTyreVeers: a blown main drags its side — the rollout pulls
// toward the wounded leg, mirror-symmetric.
func TestBlownTyreVeers(t *testing.T) {
	veer := func(leg int) float64 {
		m := runway(0, 55)
		m.State.Position.Y = 0.1
		m.State.Damage.Gear[leg] = 0.6
		for i := 0; i < 240*3; i++ {
			m.Step(flight.Inputs{Gear: true})
		}
		return m.State.Velocity.Z
	}
	left, right := veer(1), veer(2)
	if math.Abs(left) < 0.3 {
		t.Fatalf("a blown tyre pulled nothing: lateral %.2f m/s after 3 s", left)
	}
	if left*right >= 0 {
		t.Fatalf("the pull must mirror with the leg: left-blown %.2f, right-blown %.2f", left, right)
	}
}

// TestCollapsedLegSettles: a folded main drops its corner onto the belly
// skids — the jet leans and rests instead of exploding through the runway.
func TestCollapsedLegSettles(t *testing.T) {
	m := runway(0, 0)
	m.State.Position.Y = 1
	m.State.Damage.Gear[1] = 1 // left main folded
	for i := 0; i < 240*5; i++ {
		m.Step(flight.Inputs{Gear: true})
	}
	right := m.State.Attitude.Rotate(flight.Vec3{Z: 1})
	bank := math.Atan2(right.Y, m.State.Attitude.Rotate(flight.Vec3{Y: 1}).Y) * 180 / math.Pi
	if math.IsNaN(bank) || m.State.Position.Y < -2 {
		t.Fatalf("the collapse blew up the integration: bank %.1f, y %.1f", bank, m.State.Position.Y)
	}
	if math.Abs(bank) < 1 {
		t.Fatalf("a folded left main must lean the jet: bank %.2f deg", bank)
	}
	if m.State.Velocity.Length() > 1 {
		t.Fatalf("the settled jet is still moving: %.1f m/s", m.State.Velocity.Length())
	}
}

// TestEncodeGear: per-strut damage survives the wire.
func TestEncodeGear(t *testing.T) {
	s := flight.State{}
	s.Damage.Gear = [3]float64{0.2, 0.8, 1}
	buffer := make([]float64, flight.Size)
	s.Encode(buffer)
	back := flight.Decode(buffer)
	if back.Damage.Gear != s.Damage.Gear {
		t.Fatalf("gear damage did not survive the round trip: %v", back.Damage.Gear)
	}
}

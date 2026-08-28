// Mochi world: NATOPS handling gates (#86)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// The asserting half of the NATOPS measurement (TestNatopsMeasure prints, this
// file gates): the Case I defect was full-aft stick at 245-260 kt gear-down
// yielding 1.9 g / alpha 10, because both laws wired the gear/flap placard
// (+2.0 g, NATOPS 4.1.8) into the command path. The placard is procedural in
// the real jet — the pilot observes it, the airframe records violations, and
// the law delivers what the stick asks.

func configured(kt float64, flap float64) (*Model, Inputs) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 250}, Vec3{X: 1}, kt*knot, Fighter.Mass.Fuel*0.5)
	m.State.Gear.Extension = 1
	in := Inputs{Gear: true, Flap: flap, Throttle: lever(m.State.Engine[0].Spool)}
	fly(m, in, 3, nil)
	return m, in
}

// TestPatternAuthority: the defect scenario itself. Full aft at pattern speed
// in the landing configuration must deliver a real pull — well past the 1.9 g
// / alpha 10 the wired placard pinned it at.
func TestPatternAuthority(t *testing.T) {
	for _, flap := range []float64{0, 2} {
		m, in := configured(250, flap)
		r := pull(m, in, 1, 4)
		t.Logf("flap %.0f: full aft at 250 kt gear down: alpha %.1f nz %.1f rate %.1f deg/s", flap, r.alpha, r.nz, r.rate)
		if r.nz < 2.5 {
			t.Fatalf("flap %.0f: full aft at 250 kt gear down delivers only %.1f g — the placard is wired into the law again", flap, r.nz)
		}
		if r.alpha < 12 {
			t.Fatalf("flap %.0f: full aft at 250 kt gear down reaches only alpha %.1f", flap, r.alpha)
		}
	}
}

// TestOnspeedAuthority: the waveoff reserve — full aft at on-speed must have
// alpha headroom above the trim point, not sit against a command ceiling.
func TestOnspeedAuthority(t *testing.T) {
	m, in := configured(135, 2)
	trim := m.Alpha() * 180 / math.Pi
	r := pull(m, in, 1, 4)
	t.Logf("on-speed trim alpha %.1f, full aft alpha %.1f nz %.1f", trim, r.alpha, r.nz)
	// Floor between the old squared-stick law (measured 15.8) and the linear
	// span (measured 20.5): the reserve must be the whole configured envelope,
	// not the placard-shaped stub.
	if r.alpha < trim+9 {
		t.Fatalf("full aft at on-speed raises alpha only %.1f past the %.1f trim", r.alpha-trim, trim)
	}
}

// TestPlacardRecorded: exceeding the gear-down placard is permitted and
// measured — the pull crosses 2.0 g uncapped, and the airframe logs the
// exposure in Damage.Stress instead of the law refusing it.
func TestPlacardRecorded(t *testing.T) {
	m, in := configured(250, 2)
	before := m.State.Damage.Stress
	r := pull(m, in, 1, 4)
	if r.nz <= 2.0 {
		t.Fatalf("the pull never crossed the 2.0 g placard (nz %.1f) — nothing to record", r.nz)
	}
	if m.State.Damage.Stress <= before {
		t.Fatalf("nz reached %.1f past the gear-down placard with no Stress exposure recorded", r.nz)
	}
	t.Logf("nz %.1f, stress exposure %.2f g·s", r.nz, m.State.Damage.Stress-before)
}

// TestFlareEffectiveness: the round-out. From a 3-degree runway approach, a
// modest checked pull must actually break the sink — the squared stick made
// 0.35 stick command ~1 degree of alpha and the jet flew into the ground.
func TestFlareEffectiveness(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	state, throttle := Approach(m, Vec3{Y: 20}, Vec3{X: 1}, -3.0*math.Pi/180, Fighter.Mass.Fuel*0.4)
	state.Gear.Extension = 1
	m.State = state
	in := Inputs{Gear: true, Flap: 2, Throttle: lever(throttle)}
	sink := -m.State.Velocity.Y * 196.85
	pull(m, in, 0.35, 2.5)
	after := -m.State.Velocity.Y * 196.85
	t.Logf("flare: sink %.0f -> %.0f fpm", sink, after)
	if after > sink*0.4 {
		t.Fatalf("0.35 stick for 2.5 s only reduced the sink %.0f -> %.0f fpm — the flare is dead again", sink, after)
	}
}

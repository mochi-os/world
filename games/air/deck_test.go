// Mochi world: Air game module tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestRecoveryOutranksTheJink pins the ordering inside steer(): the deck
// recovery is checked BEFORE the commanded-evasion block and RETURNS, so a jink
// can never fly the jet into the water.
//
// #224 was filed on the opposite reading - that polish() calls guard() before
// steer() runs, leaving the jink unbounded - and it is wrong, because steer()
// carries a deck recovery of its own. Measured across 80 fights before this
// test was written: the jink ran 14,368 ticks, its lowest at 814 m, and ZERO of
// them below 900 m descending, while the recovery fired 33,595 ticks and took
// the stick in all 486 where a jink also wanted it. A sweep is a poor guard for
// a one-line invariant, so this drives the two states directly instead.
//
// What it protects: the evasion block sits after an early return, and an edit
// that reorders them, or turns that return into a fallthrough, reads as
// harmless and is not. The negative control for it is exactly that - delete the
// `return` at the end of the recovery branch and this fails.
func TestRecoveryOutranksTheJink(t *testing.T) {
	seat := func(height, climb float64, dodging bool) flight.Inputs {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = flight.Vec3{Y: height}
		m.State.Velocity = flight.Vec3{X: 200, Y: climb}
		m.State.Attitude = flight.Look(m.State.Velocity.Normalize())
		m.State.Fuel = 2450
		b := &brain{skill: skills["ace"], aim: flight.Vec3{X: 1}, g: 4, throttle: 1}
		if dodging {
			// The window stands well past the tick we steer at, and the
			// quadrant matters: (dodge/15)%4 picks it, and 0 and 2 push along
			// the LIFT vector, which at this synthetic state lies in the
			// velocity plane - so their whole effect lands on pitch, which the
			// recovery has already saturated at full aft, and the difference is
			// invisible. 135 selects the SIDE quadrant, out of plane, which
			// must move roll. Measured on a build with the return deleted:
			// pitch 1.000/roll 0.000 becomes 0.819/0.120.
			b.dodge = 135
		}
		return b.steer(m, 60)
	}
	// Low and descending, the recovery owns the stick and a live jink must
	// change NOTHING. Asserting merely that the demand is nose-up is too weak
	// to bite: with the return deleted the jink only PERTURBS the recovery's
	// aim by half a lift-vector, which leaves the pitch positive, and
	// want = max(limit, pull*0.9) is the limit either way. Identity is the
	// invariant the return actually provides.
	calmLow, jinkingLow := seat(600, -40, false), seat(600, -40, true)
	if calmLow != jinkingLow {
		t.Errorf("low and descending, a live jink moved the stick: pitch %.3f->%.3f roll %.3f->%.3f yaw %.3f->%.3f - the deck recovery no longer returns before the evasion block (#224)",
			calmLow.Pitch, jinkingLow.Pitch, calmLow.Roll, jinkingLow.Roll, calmLow.Yaw, jinkingLow.Yaw)
	}
	if calmLow.Pitch <= 0 {
		t.Errorf("low and descending: pitch %.2f, expected a nose-up demand from the deck recovery", calmLow.Pitch)
	}
	// The control that keeps the assertion honest: high and level, the recovery
	// is not armed, so the two cases must DIFFER - otherwise the test above
	// would pass on a build where the jink never runs at all.
	calm, jinking := seat(6000, 0, false), seat(6000, 0, true)
	if calm.Pitch == jinking.Pitch && calm.Roll == jinking.Roll {
		t.Error("high and level, a live jink changed neither pitch nor roll: the evasion block is not running, so the ordering above is untested")
	}
}

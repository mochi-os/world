// Mochi world: FCS law-transition regression tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The seams between the powered-approach and up-and-away laws (#203/#204).
// Every past transition defect (the gear-cycle trim jump, the post-launch
// pitch-down, the gear-up g-snap) was found by a user report; these pin the
// boundary behaviour instead. The stick is held displaced through each
// crossing because the trim integral only feeds the command path while the
// stick flies the jet — a neutral-stick crossing hides the mis-trim.

package flight

import (
	"math"
	"testing"
)

// cross flies a gear-down speed crossing of the law boundary under the given
// throttle and stick, returning the worst pitch rate and load-factor excursion
// seen in the boundary region plus the number of law changes.
func cross(t *testing.T, start float64, throttle float64, stick float64, until func(speed float64) bool) (worstQ float64, worstNz float64, flips int) {
	t.Helper()
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 600}, Vec3{X: 1}, start, 2000)
	for i := 0; i < 240*8; i++ { // settle into the starting law's trim
		m.Step(Inputs{Throttle: 0.55, Gear: true})
	}
	was := m.pa
	for i := 0; i < 240*120; i++ {
		m.Step(Inputs{Throttle: throttle, Pitch: stick, Gear: true})
		if m.pa != was {
			flips++
			was = m.pa
		}
		speed := m.State.Velocity.Length()
		if speed > 115 && speed < 145 { // the boundary region
			_, q, _ := rates(m.State.Omega)
			if a := math.Abs(q); a > worstQ {
				worstQ = a
			}
			if d := math.Abs(m.State.Fcs.Normal - 1); d > worstNz {
				worstNz = d
			}
		}
		if until(speed) {
			return worstQ, worstNz, flips
		}
	}
	t.Fatal("never crossed the boundary")
	return 0, 0, 0
}

// TestLawBoundaryAcceleration: a gear-down MIL acceleration through the PA/UA
// boundary with the stick displaced (the bolter/waveoff shape) must hand over
// smoothly — the laundered trim cannot be reinterpreted as a rate bias.
func TestLawBoundaryAcceleration(t *testing.T) {
	// 0.12 stick ≈ holding attitude through the acceleration (a real waveoff),
	// not a sustained pull — a big pull climbs, sags the speed back through the
	// band, and the commanded g swamps the handover transient being measured.
	worstQ, worstNz, flips := cross(t, 110, 1.0, 0.12, func(speed float64) bool { return speed > 150 })
	t.Logf("accel: worst q %.1f deg/s, worst |nz-1| %.2f, law flips %d", worstQ*180/math.Pi, worstNz, flips)
	if flips != 1 {
		t.Fatalf("law must flip exactly once, flipped %d times", flips)
	}
	if worstQ > 12*math.Pi/180 {
		t.Fatalf("pitch-rate excursion %.1f deg/s at the law handover (the old instant flip snapped the nose)", worstQ*180/math.Pi)
	}
	if worstNz > 1.0 {
		t.Fatalf("load excursion |nz-1| = %.2f at the law handover", worstNz)
	}
}

// TestLawBoundaryDeceleration: the mirrored crossing — dirtying up early and
// decelerating into the approach with a touch of stick held.
func TestLawBoundaryDeceleration(t *testing.T) {
	worstQ, worstNz, flips := cross(t, 150, 0.15, 0.15, func(speed float64) bool { return speed < 115 })
	t.Logf("decel: worst q %.1f deg/s, worst |nz-1| %.2f, law flips %d", worstQ*180/math.Pi, worstNz, flips)
	if flips != 1 {
		t.Fatalf("law must flip exactly once, flipped %d times", flips)
	}
	if worstQ > 12*math.Pi/180 {
		t.Fatalf("pitch-rate excursion %.1f deg/s at the law handover", worstQ*180/math.Pi)
	}
	if worstNz > 1.2 {
		t.Fatalf("load excursion |nz-1| = %.2f at the law handover", worstNz)
	}
}

// TestLawHysteresis: inside the 125..135 m/s band the law must hold whatever
// it was — the old raw 130 m/s comparison flipped it every frame the speed
// wobbled across the line.
func TestLawHysteresis(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 600}, Vec3{X: 1}, 130, 2000)
	m.Step(Inputs{Throttle: 0.55, Gear: true})
	entered := m.pa // whatever the first step derived at exactly 130
	flips := 0
	was := entered
	for i := 0; i < 240*30; i++ { // half a minute hovering about the old boundary
		throttle := 0.45
		if m.State.Velocity.Length() < 130 {
			throttle = 0.75 // crude speed bang-bang about 130 — exactly the chatter case
		}
		m.Step(Inputs{Throttle: throttle, Gear: true})
		if m.pa != was {
			flips++
			was = m.pa
		}
	}
	t.Logf("hysteresis: started pa=%v, %d flips while hovering about 130 m/s", entered, flips)
	if flips > 0 {
		t.Fatalf("law flipped %d times inside the hysteresis band", flips)
	}
}

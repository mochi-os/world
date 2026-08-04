// Mochi world: PA-entry transient
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestPAFlip: entering powered approach must not TOUCH the flight path — the
// pilot decelerating through ~250 KCAS in the turn to final crosses the PA
// threshold mid-bank, and the deck wing-leveler (which must live on the deck
// alone) used to snap the jet from 52 degrees of bank toward wings-level at
// 1.5 rad/s the moment the law flipped: an uncommanded roll at exactly the
// place a pattern is flown.
func TestPAFlip(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 500}, Vec3{X: 1}, 145, Fighter.Mass.Fuel*0.4)
	m.State.Attitude = m.State.Attitude.Multiply(Axis(Vec3{X: 1}, -45*math.Pi/180))
	in := Inputs{Gear: true, Throttle: 0.25, Pitch: 0.25}
	prevBank, wasPA := 0.0, false
	for tick := 0; tick < 60*40; tick++ {
		for s := 0; s < 4; s++ {
			m.Step(in)
		}
		s := &m.State
		up := s.Attitude.Rotate(Vec3{Y: 1})
		right := s.Attitude.Rotate(Vec3{Z: 1})
		bank := math.Atan2(right.Y, up.Y) * 180 / math.Pi
		if m.pa && !wasPA {
			if jump := math.Abs(bank - prevBank); jump > 2 {
				t.Fatalf("PA entry moved the bank %.1f degrees in one tick", jump)
			}
			if math.Abs(s.Omega.X) > 0.2 {
				t.Fatalf("PA entry commanded %.2f rad/s of roll the stick never asked for", s.Omega.X)
			}
			return
		}
		wasPA, prevBank = m.pa, bank
	}
	t.Fatal("the deceleration never reached the PA threshold")
}

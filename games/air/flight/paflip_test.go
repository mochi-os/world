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

// TestPAFlip: entering powered approach must not touch the flight path. The
// deck wing-leveler must live on the deck alone - airborne it snaps the jet
// toward wings-level the moment the law flips, mid-bank in the turn to final.
func TestPAFlip(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 500}, Vec3{X: 1}, 120, Fighter.Mass.Fuel*0.4)
	m.State.Attitude = m.State.Attitude.Multiply(Axis(Vec3{X: 1}, -45*math.Pi/180))
	in := Inputs{Gear: true, Throttle: 0.25, Pitch: 0.25}
	prevBank, wasPA := 0.0, false
	for tick := 0; tick < 60*40; tick++ {
		if tick == 60*4 {
			in.Flap = 2 // the pilot selects flaps mid-bank: THIS is the PA entry now (#86)
		}
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
	t.Fatal("the flap selection never entered PA")
}

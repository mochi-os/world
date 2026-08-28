// Mochi world: The virtual flap switch across a runway takeoff
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestFlapsAuto: raising the gear on the climb-out must not strip the takeoff
// flap - the configuration follows the virtual flap switch (HALF until 180 KCAS
// clean). The observable is alpha pegging the HUD's 10° cage.
func TestFlapsAuto(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, 82, Fighter.Mass.Fuel*0.6)
	m.halfleg = true // the deck latch: this test picks the takeoff leg up already airborne (#86: the law follows the flap switch, and the latch is the deck's selection)
	in := Inputs{Gear: true, Throttle: 1, Reheat: 1, Pitch: 0.1}
	for tick := 0; tick < 60*2; tick++ {
		m.Step(in) // two seconds gear-down: the climb-out established in PA
	}
	if !m.pa {
		t.Fatal("the gear-down climb-out is not in the PA law")
	}
	in.Gear = false // positive rate: gear up
	flipped := -1.0
	for tick := 0; tick < 60*60; tick++ {
		m.Step(in)
		s := &m.State
		speed := s.Velocity.Length()
		density := 1 - 2.2558e-5*s.Position.Y
		calibrated := speed * math.Sqrt(math.Pow(math.Max(density, 0.3), 4.2559))
		body := s.Attitude.Unrotate(s.Velocity)
		alpha := math.Atan2(-body.Y, body.X) * 180 / math.Pi
		second := float64(tick) / 60
		if second > 1 && alpha > 10 {
			t.Fatalf("alpha %.1f° at t=%.1fs (calibrated %.0f m/s) — the velocity vector pegs the HUD's 10° cage", alpha, second, calibrated)
		}
		if m.pa && calibrated < 92 && m.State.Fcs.Flap < 0.02 {
			t.Fatalf("the takeoff droop retracted at %.0f m/s CAS with the flaps still HALF (t=%.1fs)", calibrated, second)
		}
		if flipped < 0 && !m.pa {
			flipped = calibrated
		}
	}
	if flipped < 0 {
		t.Fatal("the burner climb never reached the flaps-AUTO gate")
	}
	if flipped < 92 || flipped > 100 {
		t.Fatalf("flaps went AUTO at %.0f m/s CAS — the virtual switch selects AUTO passing 180 KCAS (92.6)", flipped)
	}
}

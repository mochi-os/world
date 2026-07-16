// Mochi world: Compressibility gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"testing"
)

// TestDragRise: drag at constant small alpha rises sharply through the
// transonic band.
func TestDragRise(t *testing.T) {
	m := calm()
	sound := air(2000, m.Environment).Sound
	_, subsonic, _ := polar(m, 0.70*sound, 0.02, 0)
	_, transonic, _ := polar(m, 1.02*sound, 0.02, 0)
	if transonic < subsonic*2.5 {
		t.Fatalf("no drag divergence: CD %f at M0.70 vs %f at M1.02", subsonic, transonic)
	}
}

// TestTransonicTrim: the section-level aft AC shift is a nose-down moment
// increment proportional to lift, present through the band. (At aircraft
// level and constant alpha it is masked by the PG lift growth, so the gate
// is at the unit level; the FCS trims the aircraft-level effect.)
func TestTransonicTrim(t *testing.T) {
	slow, _, shiftSlow := compress(0.70, 0.4, 0.033)
	fast, _, shiftFast := compress(0.95, 0.4, 0.033)
	if shiftSlow != 0 || shiftFast >= 0 {
		t.Fatalf("AC shift wrong: %f at M0.70, %f at M0.95", shiftSlow, shiftFast)
	}
	if fast <= slow {
		t.Fatalf("no PG plateau: slope factor %f -> %f", slow, fast)
	}
}

// level is the altitude-aware evaluation the compressibility gates need
// (polar() is fixed at 2 km).
func level(m *Model, speed float64, at float64, altitude float64) (float64, float64) {
	return m.Evaluate(speed, at, altitude)
}

// TestNoSupercruise: a static thrust-versus-drag sweep at 9 km — the jet
// must have excess reheat thrust well past M1 (the dash exists) and a dry
// deficit everywhere supersonic (it cannot stay there without burner).
func TestNoSupercruise(t *testing.T) {
	m := calm()
	local := air(9000, m.Environment)
	weight := (m.Airframe.Mass.Empty + m.Airframe.Mass.Fuel) * m.Gravity
	dash := 0.0
	for mach := 0.90; mach <= 1.70; mach += 0.05 {
		speed := mach * local.Sound
		q := 0.5 * local.Density * speed * speed
		// 1g trim: alpha for lift = weight (small-angle search).
		trimAlpha := 0.0
		for a := 0.0; a < 0.2; a += 0.002 {
			cl, _ := level(m, speed, a, 9000)
			if cl*q*m.Airframe.Reference.Area >= weight {
				trimAlpha = a
				break
			}
		}
		_, cd := level(m, speed, trimAlpha, 9000)
		drag := cd * q * m.Airframe.Reference.Area
		wet, dry := 0.0, 0.0
		for i := range m.Airframe.Engines {
			engine := &m.Airframe.Engines[i]
			d, b := output(EngineState{Spool: 1, Reheat: 1}, engine, local.Density, mach)
			wet += d + b
			d, _ = output(EngineState{Spool: 1}, engine, local.Density, mach)
			dry += d
		}
		if wet > drag && mach > dash {
			dash = mach
		}
		if mach >= 1.0 && dry >= drag {
			t.Fatalf("supercruise: dry thrust %.0f >= drag %.0f at M%.2f", dry, drag, mach)
		}
	}
	if dash < 1.35 {
		t.Fatalf("reheat dash tops out at M%.2f — want ~M1.4+", dash)
	}
	if dash > 1.85 {
		t.Fatalf("dash unreasonably fast: M%.2f", dash)
	}
}

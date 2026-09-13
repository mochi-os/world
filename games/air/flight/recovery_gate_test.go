// Mochi world: pitch recovery from high alpha
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestPitchRecovery: full forward stick must arrest a high alpha at fighting
// speed. TestDepartureBoundary asks whether a clean pull departs; this asks
// the other half - once the nose IS up, can the pilot get it down again?
//
// The cell that motivated it came from a scripted opponent in the tier ladder
// (#214). Six attempts to stop that script departing all failed, and a frame
// trace finally showed why: at 256-259 kt it was ALREADY commanding stick
// hard forward - pitch -0.72 running to -0.87 - through 240 consecutive
// frames, and alpha rose from 42.8 to 45.1 degrees regardless. No pull law
// was involved. The script was asking for the right thing and not getting it,
// which is a flight-model question, not a doctrine one.
//
// 250 kt is not slow. At that speed the stabilator has authority, so an alpha
// that keeps climbing under full nose-down is the model declining to fly what
// the pilot commands. The low-speed cells are the honest half of the same
// curve: below about 150 kt a real jet genuinely cannot push out promptly,
// and the gate says so rather than pretending recovery is free everywhere.
func TestPitchRecovery(t *testing.T) {
	// entry drives the jet to a target alpha, then commands full forward stick
	// and reports how long the recovery took and what the stabilator gave.
	entry := func(kcas, want, bank float64) (float64, float64, float64, float64) {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 6000}, Vec3{X: 1}, kcas/1.94384, 3000)
		in := Inputs{Throttle: 1}
		for i := 0; i < 240*2; i++ {
			m.Step(in)
		}
		// Pull to the target alpha (bounded: some cells never reach it).
		in.Pitch = 1
		reached := 0.0
		for i := 0; i < 240*12 && m.Alpha()*180/math.Pi < want; i++ {
			m.Step(in)
			reached = math.Max(reached, m.Alpha()*180/math.Pi)
		}
		peak := m.Alpha() * 180 / math.Pi
		// Now push. Full forward, four seconds, and watch alpha.
		in.Pitch, in.Roll = -1, bank
		over, stab, recovered := peak, 0.0, -1.0
		for i := 0; i < 240*4; i++ {
			m.Step(in)
			a := m.Alpha() * 180 / math.Pi
			over = math.Max(over, a)
			stab = math.Min(stab, (m.State.Fcs.Stabilator.Left+m.State.Fcs.Stabilator.Right)/2)
			if recovered < 0 && a < 20 {
				recovered = float64(i) / 240
			}
		}
		return math.Max(peak, reached), over, recovered, stab * 180 / math.Pi
	}

	// climb is how much alpha may still rise after the stick goes forward. It
	// is not zero anywhere: the wing keeps its incidence for as long as the
	// pitch rate takes to reverse, and that lag lengthens as speed falls. The
	// slow cells get a real allowance for the same reason a real jet does.
	for _, cell := range []struct {
		name             string
		kcas, want, bank float64
		allow, climb     float64
	}{
		{"clean", 350, 30, 0, 1.5, 3},
		{"clean", 300, 40, 0, 2.0, 3},
		{"clean", 260, 43, 0, 2.5, 3}, // the ladder's cell: the script's trace, 256-259 kt
		{"clean", 200, 40, 0, 3.0, 5},
		{"clean", 150, 35, 0, 4.0, 10}, // slow: sluggish here is honest, not a defect
		// The same cells with the stick across, which is what the scripted
		// opponent was actually flying when it departed - its regain commands a
		// residual roll, and pure-pitch recovery is not the input it gives.
		{"rolling", 260, 43, 0.3, 2.5, 3},
		{"rolling", 260, 43, 0.6, 2.5, 3},
		{"rolling", 200, 40, 0.6, 3.0, 5},
	} {
		peak, over, recovered, stab := entry(cell.kcas, cell.want, cell.bank)
		state := "never"
		if recovered >= 0 {
			state = "recovered"
		}
		t.Logf("%-8s %3.0f kt roll %.1f: reached alpha %4.1f, full forward -> peaked %5.1f, %s in %5.2f s (allow %.1f), stabilator %.1f deg",
			cell.name, cell.kcas, cell.bank, peak, over, state, recovered, cell.allow, stab)
		if over > peak+cell.climb {
			t.Errorf("%s %.0f kt roll %.1f: alpha CLIMBED %.1f deg past %.1f under full forward stick (allow %.0f) - the model is refusing a commanded recovery",
				cell.name, cell.kcas, cell.bank, over-peak, peak, cell.climb)
		}
		if recovered < 0 || recovered > cell.allow {
			t.Errorf("%s %.0f kt roll %.1f: full forward stick from alpha %.1f did not recover below 20 deg within %.1f s (took %.2f)",
				cell.name, cell.kcas, cell.bank, peak, cell.allow, recovered)
		}
	}
}

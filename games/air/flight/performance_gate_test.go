// Mochi world: performance envelope gates (#89)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// The AIR_ENERGY probes in envelope_test.go are measurement instruments —
// they print the doghouse and assert nothing, so a thrust or drag regression
// silently re-tunes every fight and bot ladder. These gates pin the points
// that matter against public F/A-18C figures, with bands wide enough for the
// model's honest simplifications (measured 2026-08-28: 660 KCAS / M1.01 on
// the deck, M1.73 at 36,000 ft, 300-600 KCAS in 14.7 s at full reheat,
// sustained 17.5°/s at 400 KCAS on the deck and 13.5°/s at 15,000 ft, all at
// 13,372 kg).

// TestTopSpeedGate: level-held terminal speed, the drag-and-thrust anchor.
func TestTopSpeedGate(t *testing.T) {
	terminal := func(altitude float64) (float64, float64) {
		m := energy()
		m.State = Level(m, Vec3{Y: altitude}, Vec3{X: 1}, 220, 2500)
		var hold leveler
		for i := 0; i < 240*300; i++ {
			m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: hold.pitch(m, altitude)})
		}
		return m.Cas() * 1.94384, m.Mach()
	}
	if kcas, mach := terminal(300); kcas < 630 || kcas > 700 || mach < 0.95 || mach > 1.08 {
		t.Errorf("deck terminal speed %.0f KCAS / M%.2f, want 630-700 / M0.95-1.08", kcas, mach)
	}
	if _, mach := terminal(11000); mach < 1.6 || mach > 1.85 {
		t.Errorf("high terminal speed M%.2f, want M1.60-1.85", mach)
	}
}

// TestAccelerationGate: 300 to 600 KCAS on the deck at full reheat — the
// specific-excess-power anchor for the fight's energy economy.
func TestAccelerationGate(t *testing.T) {
	m := energy()
	m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, 300/1.94384, 2500)
	var hold leveler
	started, finished := -1.0, -1.0
	for i := 0; i < 240*60; i++ {
		m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: clamp(hold.pitch(m, 300), -0.3, 0.3)})
		kcas := m.Cas() * 1.94384
		if started < 0 && kcas >= 300 {
			started = float64(i) * Dt
		}
		if finished < 0 && kcas >= 600 {
			finished = float64(i) * Dt
			break
		}
	}
	if started < 0 || finished < 0 {
		t.Fatal("never reached 600 KCAS at full reheat on the deck")
	}
	span := finished - started
	if span < 10 || span > 25 {
		t.Errorf("300-600 KCAS took %.1f s at full reheat, want 10-25 s", span)
	}
}

// TestSustainedTurnGate: the rate-fight anchor — thrust-limited sustained g
// at the corner-adjacent speeds, on the deck and at altitude. The excess()
// probe returns achieved sustained load at zero specific excess power; rate
// follows from n and TAS.
func TestSustainedTurnGate(t *testing.T) {
	// Bisect the load whose specific excess power is zero — the sustained
	// point the AIR_ENERGY sweep prints — then convert to turn rate.
	rate := func(altitude, kcas float64) float64 {
		low, high := 1.5, 7.5
		tas := 0.0
		for k := 0; k < 8; k++ {
			mid := (low + high) / 2
			power, achieved, speed := excess(altitude, kcas, mid)
			tas = speed
			if achieved < mid-0.3 {
				high = achieved // limiter-capped: the boundary is below the ask
				continue
			}
			if power > 0 {
				low = mid
			} else {
				high = mid
			}
		}
		load := (low + high) / 2
		if load <= 1 {
			return 0
		}
		return math.Sqrt(load*load-1) * 9.80665 / tas * 180 / math.Pi
	}
	if r := rate(300, 400); r < 15.5 || r > 19.5 {
		t.Errorf("sustained %.1f°/s at 400 KCAS on the deck, want 15.5-19.5 (published ~18 light, measured 17.5 mid-weight)", r)
	}
	if r := rate(4600, 400); r < 11 || r > 15 {
		t.Errorf("sustained %.1f°/s at 400 KCAS / 15,000 ft, want 11-15 (measured 12.9)", r)
	}
}

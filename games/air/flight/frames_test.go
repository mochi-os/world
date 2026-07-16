// Mochi world: Instrument accessor tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestCas: calibrated airspeed follows the compressible pitot equations —
// checked against independently computed references (subsonic at altitude,
// the supersonic Rayleigh branch, and the sea-level identity CAS = TAS).
func TestCas(t *testing.T) {
	m := New(Fighter, Environment{}, World{Sea: 0})
	cas := func(tas, altitude float64) float64 {
		m.State.Position = Vec3{Y: altitude}
		m.State.Velocity = Vec3{X: tas}
		return m.Cas()
	}
	references := []struct{ tas, altitude, want float64 }{
		{157, 4572, 126.09}, // 305 KTAS at 15,000 ft — the compressibility term adds ~1 kt over EAS
		{260, 9144, 168.80}, // 505 KTAS at 30,000 ft, M 0.86 — ~20 kt over EAS
		{350, 0, 350.00},    // M 1.03 on the deck: at standard sea level CAS = TAS on both branches
		{80, 0, 80.00},      // low and slow: the incompressible limit
	}
	for _, r := range references {
		if got := cas(r.tas, r.altitude); math.Abs(got-r.want) > 0.15 {
			t.Fatalf("CAS(%v m/s @ %v m) = %.2f, want %.2f", r.tas, r.altitude, got, r.want)
		}
	}
}

// Mochi world: Compressibility
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Transonic corrections, applied per element at its local Mach: a
// Prandtl-Glauert lift-slope rise flattened through the transonic band and
// decaying supersonic (the full supersonic regime is a stub — this is a
// transonic jet); Lock's fourth-power drag divergence above a lift-dependent
// critical Mach; and an aft aerodynamic-centre shift the pitch trim visibly
// works against between M0.85 and M1.05.

package flight

import (
	"math"
)

const (
	critical = 0.85 // zero-lift critical Mach for the thin lifting surfaces
)

// compress returns the lift-slope factor, added wave drag, and pitching
// moment increment for a section at local Mach and lift coefficient.
func compress(mach float64, cl float64, hump float64) (float64, float64, float64) {
	if mach < 0.05 {
		return 1, 0, 0
	}
	// Lift-slope factor: PG below the band, flattened plateau through it,
	// falling supersonic.
	var slope float64
	switch {
	case mach < critical:
		slope = 1 / math.Sqrt(1-mach*mach)
	case mach < 1.05:
		slope = 1 / math.Sqrt(1-critical*critical) // ≈1.90 plateau
	default:
		slope = 1.9 / math.Sqrt(mach*mach-1+0.35)
	}
	if slope > 2.0 {
		slope = 2.0
	}
	// Wave drag above the lift-dependent critical Mach: the classic
	// transonic HUMP — a steep quartic rise peaking just past M1, then a
	// slow supersonic decay (Lock's law is only valid near divergence).
	edge := critical - 0.12*math.Abs(cl)
	wave := 0.0
	if mach > edge {
		rise := clamp((mach-edge)/(1.0-edge), 0, 1)
		rise = rise * rise * rise * rise
		if mach > 1.02 {
			rise = 1 / (1 + (mach-1.02)*2.6) // slow decay to the supersonic shelf
		}
		wave = hump * rise
	}
	// Transonic aft AC shift: ~10% of chord, ramped across the band, felt
	// as a nose-down moment proportional to lift.
	shift := 0.0
	if mach > critical {
		shift = -0.10 * clamp((mach-critical)/0.20, 0, 1) * cl
	}
	return slope, wave, shift
}

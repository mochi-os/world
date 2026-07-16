// Mochi world: Vortex lift
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The Polhamus leading-edge-suction analogy: sharp, highly swept edges (the
// LEX strakes, mildly the inboard wing) shed vortices whose suction acts as
// extra nonlinear lift, sustained far past section stall — the high usable
// alpha of the airframe. Breakdown rolls the benefit off gently past the
// surface's breakdown angle rather than cutting it.

package flight

import (
	"math"
)

const residue = 0.3 // lift fraction surviving vortex breakdown

// vortex is the Polhamus increment for a surface at effective incidence:
// ΔCl = Kv·cosα·sin²α scaled by the breakdown factor; the drag increment is
// the lost leading-edge suction ΔCd = ΔCl·tanα.
func vortex(kv float64, breakdown float64, a float64) (float64, float64) {
	sign := 1.0
	if a < 0 {
		sign = -1
		a = -a
	}
	if a > math.Pi/2 {
		return 0, 0
	}
	cl := kv * math.Cos(a) * math.Sin(a) * math.Sin(a) * fade(a, breakdown) * sign
	return cl, math.Abs(cl) * math.Tan(math.Min(a, 1.4))
}

// fade is the breakdown factor: full vortex below the breakdown angle,
// cosine-ramping to the residue over ~10°.
func fade(a float64, breakdown float64) float64 {
	const span = 10 * math.Pi / 180
	switch {
	case a <= breakdown:
		return 1
	case a >= breakdown+span:
		return residue
	default:
		x := (a - breakdown) / span
		return residue + (1-residue)*0.5*(1+math.Cos(math.Pi*x))
	}
}

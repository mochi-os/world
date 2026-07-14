// Mochi world: Cloud convection
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Turbulence and vertical drafts from the cloud layer, keyed to the SAME
// cells the client renders. The renderer's cell field is worley noise over
// wind-rotated coordinates; its hash is an integer PCG3D shared verbatim
// with the client's noise bake, so both hosts and the visuals agree on
// where the cells are (bit-exact hash; the surrounding float arithmetic
// diverges only at filtering-error level, far below anything a pilot can
// feel). Cell placement carries no time term — the renderer drifts only
// the fill texture, not the cells.
//
// Realism decision (#122): convective cloud (cumulus) is turbulent — each
// cell IS the visible top of a thermal, so the air bumps inside the cell,
// rises beneath it, and gently sinks in the gaps between cells. Stratiform
// decks form in STABLE air and stay deliberately smooth: hiding in the
// concealment layer is serene; its price is the white-out, not chop.

package flight

import (
	"math"
)

// Cloud describes the convective cloud layer the flight model can feel.
// Zero value = no layer (clear, stratiform, or multiplayer where weather
// is not yet session-owned).
type Cloud struct {
	Base       float64 // condensation level, m (0 = no layer)
	Top        float64 // inversion cap of the common cells, m
	High       float64 // vigorous-cell tops, m
	Convective float64 // 1 = cumulus (chop + drafts); 0 = stratiform (smooth)
	Gate       Gate    // vigour band over which cells exist
}

// Gate is the vigour window the renderer gates cells with.
type Gate struct {
	Minimum float64
	Maximum float64
}

// hash is the shared PCG3D integer hash (Jarzynski & Olano). The client's
// noise bake runs the identical sequence in GLSL uints — change one and you
// must change both.
func hash(x uint32, y uint32, z uint32) (uint32, uint32, uint32) {
	x = x*1664525 + 1013904223
	y = y*1664525 + 1013904223
	z = z*1664525 + 1013904223
	x += y * z
	y += z * x
	z += x * y
	x ^= x >> 16
	y ^= y >> 16
	z ^= z >> 16
	x += y * z
	y += z * x
	z += x * y
	return x, y, z
}

// point maps an integer lattice cell to its worley feature point offset in
// [0.075, 0.925)³ — the renderer's h3(p)*0.85+0.075.
func point(x float64, y float64, z float64) (float64, float64, float64) {
	a, b, c := hash(uint32(x+64), uint32(y+64), uint32(z+64))
	scale := 0.85 / 4294967296.0
	return float64(a)*scale + 0.075, float64(b)*scale + 0.075, float64(c)*scale + 0.075
}

// worley evaluates the renderer's F1 worley noise (frequency 6, tiling) at
// texture coordinates (u, v, w) — 1 near a feature point, 0 far away.
func worley(u float64, v float64, w float64) float64 {
	pu, pv, pw := u*6, v*6, w*6
	iu, iv, iw := math.Floor(pu), math.Floor(pv), math.Floor(pw)
	fu, fv, fw := pu-iu, pv-iv, pw-iw
	nearest := 8.0
	for x := -1.0; x <= 1; x++ {
		for y := -1.0; y <= 1; y++ {
			for z := -1.0; z <= 1; z++ {
				cx, cy, cz := point(wrap(iu+x, 6), wrap(iv+y, 6), wrap(iw+z, 6))
				dx, dy, dz := cx+x-fu, cy+y-fv, cz+z-fw
				d := dx*dx + dy*dy + dz*dz
				if d < nearest {
					nearest = d
				}
			}
		}
	}
	return clamp(1-math.Sqrt(nearest), 0, 1)
}

// wrap is GLSL mod for the worley lattice tiling.
func wrap(v float64, size float64) float64 {
	m := math.Mod(v, size)
	if m < 0 {
		m += size
	}
	return m
}

// vigour replicates the renderer's cell field: worley F1 sampled on the
// 0.37 slice of the noise volume over wind-rotated, along-wind-stretched,
// world-scaled coordinates. Cells are stretched ~1.6x along the wind into
// streets; the pattern is fixed in world space.
func vigour(x float64, z float64) float64 {
	u := (x*0.958 + z*0.286) * 0.62 * 2.0833e-5
	v := (-x*0.286 + z*0.958) * 2.0833e-5
	return worley(u-math.Floor(u), v-math.Floor(v), 0.37)
}

// smooth is GLSL smoothstep.
func smooth(low float64, high float64, v float64) float64 {
	t := clamp((v-low)/(high-low), 0, 1)
	return t * t * (3 - 2*t)
}

// convection returns the extra gust intensity (m/s) and the mean vertical
// draft (m/s) the cloud layer contributes at a position.
func convection(position Vec3, cloud Cloud) (float64, float64) {
	if cloud.Convective < 0.5 || cloud.Base <= 0 {
		return 0, 0
	}
	strength := vigour(position.X, position.Z)
	cell := smooth(cloud.Gate.Minimum, cloud.Gate.Maximum, strength)
	tall := smooth(0.52, 0.68, strength) // the renderer's towering-cell ramp
	top := cloud.Top + (cloud.High-cloud.Top)*tall
	if position.Y >= cloud.Base {
		if position.Y >= top {
			return 0, 0
		}
		height := (position.Y - cloud.Base) / math.Max(top-cloud.Base, 1)
		chop := 2.2 * cell * (1 - 0.6*height) * (1 - smooth(0.85, 1, height))
		draft := (1.5 + 1.5*tall) * cell * (1 - height) // the updraft that built the cell, fading toward the cap
		return chop, draft
	}
	// Below the bases: thermals rise under each cell, the air between them
	// gently sinks — the glider pilot's sky.
	profile := smooth(0.15, 0.9, position.Y/cloud.Base)
	sink := 1 - smooth(cloud.Gate.Minimum-0.10, cloud.Gate.Minimum, strength)
	return 0.6 * cell * profile, (1.8*cell - 0.5*sink) * profile
}

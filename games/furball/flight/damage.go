// Mochi world: Damage hooks
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// DamageState is designed now, filled by the damage-model task (#78). The
// aero/propulsion loops consume these multipliers from day one; the zero
// value means pristine, so accessors default to 1.

package flight

type DamageState struct {
	Element []float64  // per-element effectiveness 0..1 (nil = pristine)
	Jam     []float64  // per-channel deflection clamp 0..1 (nil = free)
	Engine  [4]float64 // thrust multiplier offsets: stored as 1-multiplier, so zero = pristine
	Leak    float64    // kg/s fuel loss
	Drag    float64    // added parasitic drag area, m²
	Shift   Vec3       // CG shift from lost structure
	Stress  float64    // accumulated overstress exposure (recorded by the FCS override path)
}

func (d *DamageState) element(i int) float64 {
	if d.Element == nil || i >= len(d.Element) {
		return 1
	}
	return d.Element[i]
}

func (d *DamageState) engine(i int) float64 { return 1 - d.Engine[i] }

func (d *DamageState) jam(c int) float64 {
	if d.Jam == nil || c >= len(d.Jam) {
		return 1
	}
	return d.Jam[c]
}

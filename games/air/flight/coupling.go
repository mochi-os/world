// Mochi world: LEX coupling
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// What keeps the jet flying and pointing at high alpha: the LEX vortex
// re-energises the inboard wing (delaying its stall and raising usable
// lift) and washes over the stabilators and fins, keeping pitch and yaw
// authority alive deep into the buffet. Modelled as a stall-delay applied
// to the affected elements, scaled by how hard the LEX is working.

package flight

import (
	"math"
)

const (
	wingBlend = 0.5 // max attached-flow retention on the inboard wing
	tailBlend = 0.8 // retained stabilator/fin attachment in the vortex wake
)

// loading is how hard the LEX vortex is working: its Polhamus lift relative
// to the pre-breakdown ceiling, from the body angle of attack.
func loading(a float64) float64 {
	a = math.Abs(a)
	breakdown := 35 * math.Pi / 180
	peak := math.Cos(breakdown) * math.Sin(breakdown) * math.Sin(breakdown)
	now := math.Cos(a) * math.Sin(a) * math.Sin(a) * fade(a, breakdown)
	return clamp(now/peak, 0, 1)
}

// retention is the attached-flow blend weight for an element under LEX
// influence: strongest on the inboard wing tapering to nothing at the tip;
// constant on the tail surfaces bathed in the vortex wake.
func retention(surface *Surface, index int, lex float64) float64 {
	switch surface.Kind {
	case Wing:
		taper := 1.0
		if len(surface.Elements) > 1 {
			taper = 1 - float64(index)/float64(len(surface.Elements)-1)
		}
		return wingBlend * lex * taper
	case Stabilator, Fin:
		return tailBlend * lex
	}
	return 0
}

// extended blends the stalled polar toward attached-flow behaviour by the
// retention weight: the energised boundary layer keeps a fraction of the
// section lifting on its pre-stall slope. Attached lift is modelled as
// slope·sinα·cosα (linear at small α, peaking at 45°, bounded), so the
// blend is smooth everywhere, preserves control-surface authority, and
// fades naturally with vortex breakdown via the weight.
func extended(t *Table, a float64, w float64, slope float64) (float64, float64, float64) {
	cl, cd, cm := t.Sample(a)
	if w <= 0 {
		return cl, cd, cm
	}
	sign := 1.0
	at := a
	if at < 0 {
		sign = -1
		at = -at
	}
	if at <= t.Stall || at > math.Pi/2 {
		return cl, cd, cm
	}
	attached := slope * math.Sin(at) * math.Cos(at) / math.Cos(t.Stall) * sign
	drag := t.Sample // keep the analyzer quiet about unused; placeholder
	_ = drag
	cleanCd := 0.006*attached*attached + 0.01
	cl = cl*(1-w) + attached*w
	cd = cd*(1-w) + cleanCd*w
	cm = cm * (1 - 0.5*w) // attachment also softens the CP drift
	return cl, cd, cm
}

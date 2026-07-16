// Mochi world: Aerofoil polars
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// No section data ships on disk; polars are synthesized at package
// initialisation: a thin-aerofoil linear region blended into a Viterna
// post-stall extension covering ±90° and mirrored to ±180°, with the centre
// of pressure drifting aft past the stall so deep-stall pitching moments are
// honest. Lookup is a uniform-grid table interpolation — O(1), allocation
// free, identical native and wasm.

package flight

import (
	"math"
)

// resolution is the table step (0.5°).
const resolution = math.Pi / 360

// entries covers alpha in [-pi, pi].
const entries = 721

// Section is the synthesis input for one aerofoil family.
type Section struct {
	Slope float64 // Cl per rad in the linear region (~5.9 for thin symmetric)
	Stall float64 // rad, 2D stall alpha
	Drag  float64 // Cd0, profile drag at zero lift
	Ratio float64 // aspect ratio of the carrying surface (Viterna Cdmax)
}

// Table is a synthesized polar: uniform in alpha over [-pi, pi].
type Table struct {
	Stall  float64 // the section's break, for stall-delay coupling
	lift   [entries]float64
	drag   [entries]float64
	moment [entries]float64
}

// Synthesize builds the polar for a section.
func Synthesize(s Section) *Table {
	t := &Table{Stall: s.Stall}
	most := 1.11 + 0.018*s.Ratio // Viterna flat-plate drag ceiling
	stall := s.Stall
	cls := s.Slope * stall        // Cl at the stall point
	cds := s.Drag + 0.006*cls*cls // profile drag at the stall point
	a1 := most / 2
	a2 := (cls - most*math.Sin(stall)*math.Cos(stall)) * math.Sin(stall) / (math.Cos(stall) * math.Cos(stall))
	b2 := (cds - most*math.Sin(stall)*math.Sin(stall)) / math.Cos(stall)
	blend := 4 * math.Pi / 180 // smooth the break over ~4°

	for i := 0; i < entries; i++ {
		alpha := -math.Pi + float64(i)*resolution
		// Fold to the forward-flow half plane; reversed flow mirrors.
		a := alpha
		reversed := false
		if a > math.Pi/2 {
			a = math.Pi - a
			reversed = true
		} else if a < -math.Pi/2 {
			a = -math.Pi - a
			reversed = true
		}
		sign := 1.0
		if a < 0 {
			sign = -1
			a = -a
		}
		var cl, cd float64
		linear := s.Slope * a
		viterna := a1*math.Sin(2*a) + a2*math.Cos(a)*math.Cos(a)/math.Max(math.Sin(a), 0.05)
		switch {
		case a < stall-blend:
			cl = linear
			cd = s.Drag + 0.006*linear*linear
		case a < stall+blend:
			// Cubic blend across the break.
			x := (a - (stall - blend)) / (2 * blend)
			w := x * x * (3 - 2*x)
			cl = linear*(1-w) + viterna*w
			cd = (s.Drag+0.006*cl*cl)*(1-w) + (most*math.Sin(a)*math.Sin(a)+b2*math.Cos(a))*w
		default:
			cl = viterna
			cd = most*math.Sin(a)*math.Sin(a) + b2*math.Cos(a)
		}
		cl *= sign
		if reversed {
			cl = -cl * 0.7 // reversed flow lifts poorly
		}
		// Centre of pressure: quarter chord pre-stall, drifting to mid chord
		// at 90°; the moment about quarter chord follows the normal force.
		cp := 0.25
		if a > stall {
			cp = 0.25 + 0.25*(a-stall)/(math.Pi/2-stall)
		}
		normal := cl*math.Cos(a) + cd*math.Sin(a)
		t.lift[i] = cl
		t.drag[i] = cd
		t.moment[i] = -normal * (cp - 0.25) * sign
	}
	return t
}

// Sample interpolates the polar at alpha (rad).
func (t *Table) Sample(alpha float64) (cl float64, cd float64, cm float64) {
	for alpha > math.Pi {
		alpha -= 2 * math.Pi
	}
	for alpha < -math.Pi {
		alpha += 2 * math.Pi
	}
	position := (alpha + math.Pi) / resolution
	i := int(position)
	if i < 0 {
		i = 0
	}
	if i >= entries-1 {
		i = entries - 2
	}
	f := position - float64(i)
	cl = t.lift[i] + (t.lift[i+1]-t.lift[i])*f
	cd = t.drag[i] + (t.drag[i+1]-t.drag[i])*f
	cm = t.moment[i] + (t.moment[i+1]-t.moment[i])*f
	return cl, cd, cm
}

// Effectiveness is the thin-aerofoil flap factor for a control of chord
// fraction cf: the equivalent alpha shift per radian of deflection.
func Effectiveness(cf float64) float64 {
	theta := math.Acos(2*cf - 1)
	return (1 - (theta-math.Sin(theta))/math.Pi) * 0.8 // 0.8 viscous knockdown
}

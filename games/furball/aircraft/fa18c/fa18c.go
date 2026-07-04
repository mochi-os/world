// Mochi world: The F/A-18C airframe
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The legacy F/A-18C Hornet with F404-GE-402 engines: the smaller, lighter,
// livelier stablemate of the F. Planform from published three-views (wing
// 400 sq ft / 37.16 m², span 37.5 ft clean); masses from public C/D data
// (empty ~23,000 lb + 402-era growth, internal fuel 10,860 lb); inertia is
// the NASA HARV F/A-18A set unscaled (our F uses it ×1.25). Geometry the
// drawn model owns (gear stance) is provisional until #91 measures the GLB.
// Validated by the package's own gates, not sourced: anything marked tunable.

package fa18c

import (
	"math"

	"world/games/furball/flight"
)

// Airframe is the F/A-18C dataset.
var Airframe = build()

const cant = 20 * math.Pi / 180 // fin cant from vertical, same family

func build() *flight.Airframe {
	a := &flight.Airframe{Name: "fa18c"}
	a.Reference.Area = 37.16 // m² (400 sq ft)
	a.Reference.Span = 11.43 // m (37.5 ft, clean tips)
	a.Reference.Chord = 3.25 // mean, area/span
	a.Mass.Empty = 10700     // kg — 23,000 lb class plus 402-engine growth
	a.Mass.Fuel = 4900       // kg internal (10,860 lb)
	// NASA HARV F/A-18A inertia, unscaled, axes mapped to our y-up frame
	// (x roll, y yaw, z pitch); x-y off-diagonal is the roll-yaw product.
	a.Inertia = flight.Mat3{{22960, 2960, 0}, {2960, 169900, 0}, {0, 0, 151300}}
	a.Center = flight.Vec3{}
	a.Tank = flight.Vec3{X: -0.33} // fuel slightly aft, CG walks forward burning. Tunable.
	a.Limit.Positive = 7.5
	a.Limit.Negative = -3
	a.Limit.Override = 10
	a.Limit.Alpha = 40 * math.Pi / 180

	// Same F/A-18 CAS family as the F: identical schedules and throws.
	a.Control.Onspeed = 8.1 * math.Pi / 180
	a.Control.Blowdown = 35000
	a.Control.Gearing.Pitch = 0.42
	a.Control.Gearing.Roll = 0.35
	a.Control.Gearing.Yaw = 0.52
	a.Control.Slat.Slope = 0.9
	a.Control.Slat.Offset = 0.05
	a.Control.Slat.Limit = 25 * math.Pi / 180
	a.Control.Droop.Angle = 0.52
	a.Control.Droop.Pressure = 9000
	a.Control.Throw.Down = 0.42
	a.Control.Throw.Up = 0.30
	a.Control.Throw.Flap = 0.60
	a.Control.Throw.Rudder = 0.52
	a.Control.Rate.Stabilator = 40 * math.Pi / 180
	a.Control.Rate.Flaperon = 100 * math.Pi / 180
	a.Control.Rate.Rudder = 75 * math.Pi / 180
	a.Control.Rate.Slat = 0.6
	a.Control.Rate.Brake = 1.0
	a.Wave.Hump = 0.030 // the legacy jet is transonically cleaner than the F (its documented edge). Tunable
	a.Wave.Body = 0.10

	a.Engines = make([]flight.Engine, 2)
	for i := range a.Engines {
		side := float64(i)*2 - 1
		a.Engines[i] = flight.Engine{
			Position: flight.Vec3{X: -5.1, Z: side * 0.55},
			Dry:      48900, // N, F404-GE-402 (11,000 lbf)
			Reheat:   78700, // N (17,700 lbf)
		}
		a.Engines[i].Flow.Dry = 2.3e-5 // kg/(N·s). Tunable.
		a.Engines[i].Flow.Reheat = 5.2e-5
	}

	wing := flight.Section{Slope: 5.9, Stall: 0.19, Drag: 0.006, Ratio: 3.5}
	tail := flight.Section{Slope: 5.7, Stall: 0.21, Drag: 0.007, Ratio: 3.0}
	blade := flight.Section{Slope: 5.7, Stall: 0.21, Drag: 0.008, Ratio: 1.6}
	slender := flight.Section{Slope: 4.5, Stall: 0.35, Drag: 0.010, Ratio: 1.5}

	for _, side := range []float64{-1, 1} {
		// Main wing: the C panel is ~80% of the F's area on 84% of the span.
		a.Surfaces = append(a.Surfaces, strips(flight.Surface{
			Kind: flight.Wing, Side: side, Area: 15.2, Span: 4.7, Ratio: 3.5, Oswald: 0.75,
			Vortex: 0.6, Breakdown: 22 * math.Pi / 180, Channel: flight.Differential,
		}, 8, span{1.0, 5.7, side}, chord{4.0, 1.1}, sweep{0.55, -1.75}, twist{0.017, -0.052}, &wing, 0.25, 0.52))
		// LEX: the C's original strake — the E/F enlarged it by a third.
		a.Surfaces = append(a.Surfaces, strips(flight.Surface{
			Kind: flight.Strake, Side: side, Area: 3.3, Span: 0.95, Ratio: 1.5, Oswald: 0.6,
			Vortex: math.Pi, Breakdown: 35 * math.Pi / 180, Channel: flight.Fixed,
		}, 2, span{0.45, 1.25, side}, chord{3.9, 1.9}, sweep{2.1, 1.5}, twist{}, &slender, 0, 0))
		// Stabilators: all-moving; the E/F grew these 36%.
		a.Surfaces = append(a.Surfaces, strips(flight.Surface{
			Kind: flight.Stabilator, Side: side, Area: 3.5, Span: 2.0, Ratio: 3.0, Oswald: 0.8,
			Channel: flight.Symmetric,
		}, 3, span{0.9, 2.9, side}, chord{2.1, 1.05}, sweep{-5.5, -6.3}, twist{}, &tail, 0, 0.42))
		// Twin canted fins with trailing-edge rudders.
		fin := strips(flight.Surface{
			Kind: flight.Fin, Side: side, Area: 3.1, Span: 1.55, Ratio: 1.6, Oswald: 0.7,
			Channel: flight.Rudder,
		}, 3, span{0, 1.45, side}, chord{2.4, 1.3}, sweep{-4.9, -5.6}, twist{}, &blade, 0.30, 0.52)
		for i := range fin.Elements {
			e := &fin.Elements[i]
			rise := e.Position.Z * side
			e.Position = flight.Vec3{X: e.Position.X, Y: 0.95 + rise*math.Cos(cant), Z: side * (0.85 + rise*math.Sin(cant))}
			e.Axis = flight.Vec3{Y: side * math.Cos(cant), Z: math.Sin(cant)}
			e.Normal = flight.Vec3{Y: math.Sin(cant), Z: -side * math.Cos(cant)}
		}
		a.Surfaces = append(a.Surfaces, fin)
	}
	// The C's dorsal speedbrake between the fins.
	a.Surfaces = append(a.Surfaces, flight.Surface{
		Kind: flight.Brake, Area: 0.8, Span: 1, Ratio: 1, Oswald: 1, Channel: flight.Spoiler,
		Elements: []flight.Element{{
			Position: flight.Vec3{X: -3.5, Y: 0.8}, Area: 0.8, Chord: 1,
			Normal: flight.Vec3{Y: 1}, Axis: flight.Vec3{Z: 1}, Aerofoil: flight.Synthesize(flight.Section{Slope: 0, Stall: 0.3, Drag: 1.2, Ratio: 1}),
		}},
	})
	// Fuselage stations: ~7% shorter, slimmer than the F.
	a.Body = []flight.Station{
		{Position: flight.Vec3{X: 7.0}, Area: 0.9, Plan: 3.2, Drag: 0.09},
		{Position: flight.Vec3{X: 2.8}, Area: 2.3, Plan: 8.2, Drag: 0.09},
		{Position: flight.Vec3{X: -1.4}, Area: 2.7, Plan: 10.0, Drag: 0.09},
		{Position: flight.Vec3{X: -5.6}, Area: 1.6, Plan: 5.4, Drag: 0.09},
	}
	// Undercarriage: track 3.11 m, wheelbase 5.4 m. Stance PROVISIONAL until
	// #91 measures the drawn model's wheel-bottom drop; stiffness scaled to
	// the lighter jet at the F's static-compression ratio. Tunable.
	a.Gear.Nose = flight.Strut{Attach: flight.Vec3{X: 4.9, Y: -2.30}, Travel: 0.45, Stiffness: 4.5e5, Damping: 5.5e4, Steer: 1.2}
	a.Gear.Left = flight.Strut{Attach: flight.Vec3{X: -0.5, Y: -2.30, Z: -1.55}, Travel: 0.5, Stiffness: 9e5, Damping: 1.1e5}
	a.Gear.Right = flight.Strut{Attach: flight.Vec3{X: -0.5, Y: -2.30, Z: 1.55}, Travel: 0.5, Stiffness: 9e5, Damping: 1.1e5}
	a.Hook.Position = flight.Vec3{X: -6.0, Y: -0.55}
	a.Hook.Length = 2.4
	// Crash probes and belly skid points, scaled to the shorter airframe.
	a.Probes = []flight.Vec3{{X: 8.0, Y: -0.4}, {X: -8.0, Y: 0.3}, {X: -1.4, Z: -5.7}, {X: -1.4, Z: 5.7}, {X: -5.6, Y: 2.9, Z: -1.3}, {X: -5.6, Y: 2.9, Z: 1.3}}
	a.Belly = []flight.Vec3{{X: 2.7, Y: -1.0}, {X: -0.9, Y: -1.05}, {X: -4.2, Y: -0.95}}
	return a
}

type span struct{ root, tip, side float64 }
type chord struct{ root, tip float64 }
type sweep struct{ root, tip float64 } // aerodynamic-centre x at root and tip
type twist struct{ root, tip float64 } // built-in incidence, rad

// strips fills a surface with n equal-span trapezoid elements.
func strips(s flight.Surface, n int, sp span, ch chord, sw sweep, tw twist, section *flight.Section, flap float64, limit float64) flight.Surface {
	polar := flight.Synthesize(*section)
	s.Slope = section.Slope
	width := (sp.tip - sp.root) / float64(n)
	total := 0.0
	for i := 0; i < n; i++ {
		f := (float64(i) + 0.5) / float64(n)
		total += ch.root + (ch.tip-ch.root)*f
	}
	for i := 0; i < n; i++ {
		f := (float64(i) + 0.5) / float64(n)
		c := ch.root + (ch.tip-ch.root)*f
		s.Elements = append(s.Elements, flight.Element{
			Position:  flight.Vec3{X: sw.root + (sw.tip-sw.root)*f, Z: sp.side * (sp.root + (float64(i)+0.5)*width)},
			Area:      s.Area * c / total,
			Chord:     c,
			Incidence: tw.root + (tw.tip-tw.root)*f,
			Normal:    flight.Vec3{Y: 1},
			Axis:      flight.Vec3{Z: 1},
			Aerofoil:  polar,
			Flap:      flap,
			Limit:     limit,
		})
	}
	return s
}

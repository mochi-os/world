// Mochi world: The F/A-18F airframe
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The F/A-18F-class airframe as embedded literals (the art model is the
// two-seat F — same wing and engines as the E, ~350 kg heavier empty with
// ~400 kg less internal fuel): planform numbers from the handover doc §6
// and published three-views; element positions computed from them at
// initialisation. Provenance rides each literal; anything marked tunable is
// validated by the flight package's phase gates rather than sourced. The
// identity is internal only — the shipped aircraft is nameless (§19).

package fa18f

import (
	"math"

	"world/games/furball/flight"
)

// Airframe is the F/A-18F dataset.
var Airframe = build()

const cant = 20 * math.Pi / 180 // fin cant from vertical

func build() *flight.Airframe {
	a := &flight.Airframe{Name: "fa18f"}
	a.Reference.Area = 46.45 // m², doc §6
	a.Reference.Span = 13.62 // m
	a.Reference.Chord = 3.41 // mean, area/span
	a.Mass.Empty = 14900     // kg — F two-seater (E-class 14500 + rear cockpit)
	a.Mass.Fuel = 6100       // kg internal — the F trades ~400 kg of fuel for the seat
	// Inertia: NASA HARV F/A-18 set scaled ~1.25 to E-class, axes mapped to
	// our y-up frame (x roll, y yaw, z pitch); the x-y off-diagonal is the
	// aerospace roll-yaw product. Signs validated by the mode tests. Tunable.
	a.Inertia = flight.Mat3{{28700, 3700, 0}, {3700, 212400, 0}, {0, 0, 189100}}
	a.Center = flight.Vec3{}       // datum at the empty CG
	a.Tank = flight.Vec3{X: -0.35} // fuel slightly aft; CG walks forward as it burns. Tunable.
	a.Limit.Positive = 7.5         // g, doc §6
	a.Limit.Negative = -3
	a.Limit.Override = 10 // paddle-switch ceiling
	a.Limit.Alpha = 40 * math.Pi / 180
	a.Limit.Floor = 10 * math.Pi / 180 // negative-alpha protection bound

	// Control-law data: the schedules, throws, and rates the shared law
	// flies this airframe with (F/A-18 CAS family).
	a.Control.Onspeed = 8.1 * math.Pi / 180
	a.Control.Flyaway = 12 * math.Pi / 180
	a.Control.Blowdown = 35000
	a.Control.Gearing.Pitch = 0.42
	a.Control.Gearing.Roll = 0.35
	a.Control.Gearing.Yaw = 0.52
	a.Control.Slat.Slope = 0.9
	a.Control.Slat.Offset = 0.05
	a.Control.Slat.Limit = 25 * math.Pi / 180
	a.Control.Flap.Slope = 0.5
	a.Control.Flap.Offset = 0.03
	a.Control.Flap.Limit = 12 * math.Pi / 180 // AUTO manoeuvring flaps: droop with alpha to ~12°, washed out by ~350 KCAS
	a.Control.Flap.Pressure = 25000
	a.Control.Droop.Angle = 0.52
	a.Control.Droop.Pressure = 9000
	a.Control.Throw.Down = 0.42
	a.Control.Throw.Up = 0.183 // trailing-edge down 10.5° (NATOPS: stabilator +10.5/-24)
	a.Control.Throw.Flap = 0.60
	a.Control.Throw.Rudder = 0.52
	a.Control.Rate.Stabilator = 40 * math.Pi / 180
	a.Control.Rate.Flaperon = 100 * math.Pi / 180
	a.Control.Rate.Rudder = 75 * math.Pi / 180
	a.Control.Rate.Slat = 0.6
	a.Control.Rate.Brake = 1.0
	a.Wave.Hump = 0.033 // the E/F's thicker wing and poorer area-ruling — its documented transonic deficiency
	a.Wave.Body = 0.11

	a.Engines = make([]flight.Engine, 2)
	for i := range a.Engines {
		side := float64(i)*2 - 1
		a.Engines[i] = flight.Engine{
			Position: flight.Vec3{X: -5.5, Z: side * 0.6},
			Dry:      62000, // N, F414-class, doc §6
			Reheat:   97900,
		}
		a.Engines[i].Flow.Dry = 2.4e-5 // kg/(N·s). Tunable.
		a.Engines[i].Flow.Reheat = 5.4e-5
	}

	wing := flight.Section{Slope: 5.9, Stall: 0.19, Drag: 0.006, Ratio: 3.5}    // ~65A005-class thin symmetric
	tail := flight.Section{Slope: 5.7, Stall: 0.21, Drag: 0.007, Ratio: 3.0}    // thinner stabilator section
	blade := flight.Section{Slope: 5.7, Stall: 0.21, Drag: 0.008, Ratio: 1.6}   // fin
	slender := flight.Section{Slope: 4.5, Stall: 0.35, Drag: 0.010, Ratio: 1.5} // LEX: low-AR, high stall

	for _, side := range []float64{-1, 1} {
		// Main wing: 8 spanwise strips, root to tip, linear taper and sweep.
		// Half area ~19 m²; root chord 4.6 m tapering to 1.3 m; the surface
		// aerodynamic centre sits slightly aft of the CG (bare stability).
		a.Surfaces = append(a.Surfaces, strips(flight.Surface{
			Kind: flight.Wing, Side: side, Area: 19, Span: 5.6, Ratio: 3.5, Oswald: 0.75,
			Vortex: 0.6, Breakdown: 22 * math.Pi / 180, Channel: flight.Differential,
		}, 8, span{1.2, 6.8, side}, chord{4.6, 1.3}, sweep{0.6, -1.9}, twist{0.017, -0.052}, &wing, 0.25, 0.52))
		// LEX strakes: slender lifting surfaces ahead of the CG.
		a.Surfaces = append(a.Surfaces, strips(flight.Surface{
			Kind: flight.Strake, Side: side, Area: 4.4, Span: 1.1, Ratio: 1.5, Oswald: 0.6,
			Vortex: math.Pi, Breakdown: 35 * math.Pi / 180, Channel: flight.Fixed,
		}, 2, span{0.5, 1.4, side}, chord{4.4, 2.2}, sweep{2.3, 1.6}, twist{}, &slender, 0, 0))
		// Stabilators: all-moving (Symmetric channel, full authority).
		a.Surfaces = append(a.Surfaces, strips(flight.Surface{
			Kind: flight.Stabilator, Side: side, Area: 4.8, Span: 2.4, Ratio: 3.0, Oswald: 0.8,
			Channel: flight.Symmetric,
		}, 3, span{1.0, 3.4, side}, chord{2.4, 1.2}, sweep{-6.0, -6.9}, twist{}, &tail, 0, 0.42))
		// Twin fins, canted outboard; rudders on the trailing edge.
		fin := strips(flight.Surface{
			Kind: flight.Fin, Side: side, Area: 3.5, Span: 1.7, Ratio: 1.6, Oswald: 0.7,
			Channel: flight.Rudder,
		}, 3, span{0, 1.6, side}, chord{2.6, 1.4}, sweep{-5.3, -6.1}, twist{}, &blade, 0.30, 0.52)
		for i := range fin.Elements {
			e := &fin.Elements[i]
			rise := e.Position.Z * side // distance up the fin
			e.Position = flight.Vec3{X: e.Position.X, Y: 1.0 + rise*math.Cos(cant), Z: side * (0.9 + rise*math.Sin(cant))}
			// Mirrored fin frames with the chord (Axis×Normal) aft on BOTH
			// sides: the y-sign of Axis flips, not the whole normal — a
			// whole-normal flip makes both fins lift the same way.
			e.Axis = flight.Vec3{Y: side * math.Cos(cant), Z: math.Sin(cant)}
			e.Normal = flight.Vec3{Y: math.Sin(cant), Z: -side * math.Cos(cant)}
		}
		a.Surfaces = append(a.Surfaces, fin)
	}
	// Speedbrake: a pure drag panel, deployed by the FCS state.
	a.Surfaces = append(a.Surfaces, flight.Surface{
		Kind: flight.Brake, Area: 0.9, Span: 1, Ratio: 1, Oswald: 1, Channel: flight.Spoiler,
		Elements: []flight.Element{{
			Position: flight.Vec3{X: -2.0, Y: 0.6}, Area: 0.9, Chord: 1,
			Normal: flight.Vec3{Y: 1}, Axis: flight.Vec3{Z: 1}, Aerofoil: flight.Synthesize(flight.Section{Slope: 0, Stall: 0.3, Drag: 1.2, Ratio: 1}),
		}},
	})
	// Fuselage stations: parasitic drag, slender-body normal force, crossflow.
	a.Body = []flight.Station{
		{Position: flight.Vec3{X: 7.5}, Area: 1.0, Plan: 3.5, Drag: 0.09},
		{Position: flight.Vec3{X: 3.0}, Area: 2.6, Plan: 9.0, Drag: 0.09},
		{Position: flight.Vec3{X: -1.5}, Area: 3.0, Plan: 11.0, Drag: 0.09},
		{Position: flight.Vec3{X: -6.0}, Area: 1.8, Plan: 6.0, Drag: 0.09},
	}
	// Undercarriage: mains carry ~2/3 of the static load each side. Tunable.
	// Attach is the uncompressed wheel bottom; -2.52 with ~0.06 m static
	// compression rests the origin 2.46 m above the surface — the drawn
	// model's measured static stance (wheel bottoms 2.457 m below origin).
	a.Gear.Nose = flight.Strut{Attach: flight.Vec3{X: 5.3, Y: -2.52}, Travel: 0.45, Stiffness: 6e5, Damping: 7e4, Steer: 1.2}
	a.Gear.Left = flight.Strut{Attach: flight.Vec3{X: -0.6, Y: -2.52, Z: -1.6}, Travel: 0.5, Stiffness: 1.2e6, Damping: 1.5e5}
	a.Gear.Right = flight.Strut{Attach: flight.Vec3{X: -0.6, Y: -2.52, Z: 1.6}, Travel: 0.5, Stiffness: 1.2e6, Damping: 1.5e5}
	a.Hook.Position = flight.Vec3{X: -6.5, Y: -0.6}
	a.Hook.Length = 2.5
	// Crash probes: any contact = crash (user rule). Belly points skid.
	a.Probes = []flight.Vec3{{X: 8.6, Y: -0.4}, {X: -8.6, Y: 0.3}, {X: -1.5, Z: -6.9}, {X: -1.5, Z: 6.9}, {X: -6.0, Y: 3.1, Z: -1.4}, {X: -6.0, Y: 3.1, Z: 1.4}}
	a.Belly = []flight.Vec3{{X: 3.0, Y: -1.1}, {X: -1.0, Y: -1.15}, {X: -4.5, Y: -1.0}}
	return a
}

type span struct{ root, tip, side float64 }
type chord struct{ root, tip float64 }
type sweep struct{ root, tip float64 } // aerodynamic-centre x at root and tip
type twist struct{ root, tip float64 } // built-in incidence, rad (washout staggers the stall)

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
			Axis:      flight.Vec3{Z: 1}, // +z both sides: chord = Axis×Normal must point aft
			Aerofoil:  polar,
			Flap:      flap,
			Limit:     limit,
		})
	}
	return s
}

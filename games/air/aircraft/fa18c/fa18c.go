// Mochi world: The F/A-18C airframe
// Copyright © 2026 Mochisoft OÜ
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

	"world/games/air/flight"
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
	a.Tank = flight.Vec3{X: -0.33}           // fuel slightly aft, CG walks forward burning. Tunable.
	a.Cockpit = flight.Vec3{X: 5.0, Y: 0.55} // seat under the canopy line, forward fuselage
	a.Limit.Positive = 7.5
	a.Limit.Negative = -3
	a.Limit.Override = 10
	a.Limit.Reference = 14700 // kg — NATOPS writes the 7.5 g placard at 32,357 lb; heavier, Nz schedules down as Reference/mass
	a.Limit.Alpha = 40 * math.Pi / 180
	a.Limit.Floor = 10 * math.Pi / 180 // negative-alpha protection bound

	// Same F/A-18 CAS family as the F: identical schedules and throws.
	a.Control.Onspeed = 8.1 * math.Pi / 180
	a.Control.Flyaway = 16 * math.Pi / 180                // catapult launch trim, stabilator nose up (NATOPS figure 8-1): 16° to 44,000 lb, 17° to 48,000, 19° above
	a.Control.Capture = a.Control.Flyaway - 6*math.Pi/180 // NATOPS 8.2.8: trim settings between 10° and 18° nose up correspond linearly to reference AOAs between 4° and 12°
	a.Control.Blowdown = 35000
	a.Forebody = 4.3 // the widest station (2.7 m²) plus the LEX: with the published fins this lands the HARV basic-F-18 Cy_beta and Cn_beta at once (sideslip_test.go)

	// The M61 port on the nose top - the server's hitscan muzzle and the
	// client's tracer origin - and the M61A1's published 3,818 lbf average
	// recoil at 6,000 rpm.
	a.Gun = flight.Gun{Position: flight.Vec3{X: 6.53, Y: 0.43}, Recoil: 16984}

	a.Control.Gearing.Pitch = 0.42
	a.Control.Gearing.Roll = 0.35
	a.Control.Gearing.Yaw = 0.52
	a.Control.Slat.Slope = 1.4 // the real LEF schedule is steep — full deflection by ~23° alpha, not the 31° the old 0.9 gave. The late slats left the mid-band on the post-stall polar: with the wing Vortex below, trimmed CL across 16-22° (TestLift) moves from ~1.24-1.31 to ~1.31-1.38 against the ~1.33 DCS-class reference
	a.Control.Slat.Offset = 0.05
	a.Control.Slat.Limit = 25 * math.Pi / 180
	// The trailing edge (NATOPS 2.8.4.1). AUTO: the flaps against alpha and
	// Mach, the McDonnell Douglas flight hardware simulator's 8.3.3 schedule
	// (NASA TM-107601, Buttrill et al. 1992, figure 9.2(d): rising to a 17 deg
	// plateau by Mach 0.6, 9 deg at Mach 0.9, none from Mach 1.05) under its
	// impact-pressure ceiling (figure 9.2(e): 17 deg at 600 lb/ft2, 5 at 835),
	// and up above Mach 1.0 whatever the alpha (NATOPS 2.8.4); the ailerons
	// undrooped. HALF and FULL: 30 deg flaps and 30 deg aileron droop, and 45
	// and 42 (NATOPS 2.8.4.1), washed out with dynamic pressure toward the
	// mode's 250 kt limit: NATOPS schedules them "as a function of airspeed",
	// its breakpoints not published, so the washout is this model's (whole
	// below ~4,500 Pa, the approach band, gone at 10,000 Pa, ~250 KIAS).
	deg := math.Pi / 180
	psf := 47.880259 // Pa per lb/ft2
	a.Control.Flaps.Auto = []flight.Sheet{
		{Mach: 0.6, Curve: flight.Curve{{0, 0}, {12.1 * deg, 17 * deg}, {13.5 * deg, 17 * deg}, {25.6 * deg, 0}}},
		{Mach: 0.9, Curve: flight.Curve{{0, 0}, {6.4 * deg, 9 * deg}, {13.2 * deg, 9 * deg}, {19.6 * deg, 0}}},
		{Mach: 1.05, Curve: flight.Curve{{0, 0}}},
	}
	a.Control.Flaps.Ceiling = flight.Curve{{600 * psf, 17 * deg}, {835 * psf, 5 * deg}}
	a.Control.Flaps.Mach = 1.0
	a.Control.Flaps.Half = flight.Setting{Trailing: 30 * deg, Aileron: 30 * deg}
	a.Control.Flaps.Full = flight.Setting{Trailing: 45 * deg, Aileron: 42 * deg}
	a.Control.Flaps.Pressure = 10000
	a.Control.Flaps.Lift = 0.507 // the lift the PA law's blend into its level-flight trim prices FULL at (its anchor, the on-speed CL): the share of FULL's trimmed zero-alpha lift over the clean wing (0.664, TestFlapsLift) the law was tuned with when the flaps were one 26 deg flaperon (0.48 of its 0.629), so the blend begins where it did
	// AUTO's differential flaps (TM-107601 figure 9.4(g): the whole roll
	// command from 0 to 5 deg alpha, none from 10; figure 9.3(c): 18 deg).
	a.Control.Flaps.Differential.Gain = flight.Curve{{-5 * deg, 0}, {0, 1}, {5 * deg, 1}, {10 * deg, 0}}
	a.Control.Flaps.Differential.Limit = 18 * deg
	// Rudder toe-in in the takeoff and landing modes (NATOPS 2.8.2.8): 30 deg
	// with weight on the wheels or below 2 deg alpha, through 0 to 15 deg
	// toe-out at 11.
	a.Control.Toe = flight.Curve{{2 * deg, 30 * deg}, {11 * deg, -15 * deg}}
	a.Control.Throw.Down = 0.42
	a.Control.Throw.Up = 0.183 // trailing-edge down 10.5° (NATOPS: stabilator +10.5/-24)
	// The stabilators' differential for roll: 10 deg each side at full
	// lateral stick (NATOPS III-7-20, the before-taxi control check's "20 units
	// differential stabilator"; NASA's HARV lateral law limits it to +-10.0).
	a.Control.Throw.Differential = 10 * deg
	// The ailerons and flaps' travel and rates (TM-107601 table 8.7): ailerons
	// 25 up and 45 down at 100 deg/s, flaps 8 up and 45 down at 18 deg/s.
	a.Control.Throw.Aileron.Down = 45 * deg
	a.Control.Throw.Aileron.Up = 25 * deg
	a.Control.Throw.Trailing.Down = 45 * deg
	a.Control.Throw.Trailing.Up = 8 * deg
	a.Control.Throw.Rudder = 30 * math.Pi / 180 // ±30° rudder travel (NATOPS/HARV)
	a.Control.Throw.Brake = 60 * math.Pi / 180  // the speed brake panel's travel (HARV simulation model: 0-60°)
	a.Control.Rate.Stabilator = 40 * math.Pi / 180
	a.Control.Rate.Aileron = 100 * deg
	a.Control.Rate.Trailing = 18 * deg
	a.Control.Rate.Rudder = 75 * math.Pi / 180
	a.Control.Rate.Slat = 0.6
	a.Control.Rate.Brake = 0.4 // the panel takes 2.5 s to full travel (NASA TM-110216: 60/2.5 deg/s no-load rate limit; NATOPS: 3 s maximum)
	a.Wave.Hump = 0.025        // the legacy jet is transonically cleaner than the F (its documented edge). Tunable
	a.Wave.Body = 0.085

	// The full fitment catalog (#17). Bits 0 and 1 must stay the wingtip AIM-9Ms:
	// every count-to-mask mapping and the calibrated bot rely on those indices.
	// Mutually exclusive fixtures (a station's rail versus its twin) never pair.
	a.Stores = []flight.Store{
		{Name: "tip1", Station: 1, Position: flight.Vec3{X: 0.2, Y: -0.77, Z: -5.94}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "tip9", Station: 9, Position: flight.Vec3{X: 0.2, Y: -0.77, Z: 5.94}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "rail2", Station: 2, Position: flight.Vec3{X: 0.3, Y: -0.85, Z: -3.35}, Mass: 179, Area: 0.04},
		{Name: "twin2", Station: 2, Position: flight.Vec3{X: 0.3, Y: -0.85, Z: -3.35}, Mass: 290, Area: 0.06},
		// A single AIM-9 on a wing pylon rides the LAU-115C/A's outboard LAU-7, at
		// the twin's outboard point, as the model hangs it (hang.ts).
		{Name: "9m2", Station: 2, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: -3.50}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m2a", Station: 2, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: -3.50}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m2b", Station: 2, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: -3.20}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "rail8", Station: 8, Position: flight.Vec3{X: 0.3, Y: -0.85, Z: 3.35}, Mass: 179, Area: 0.04},
		{Name: "twin8", Station: 8, Position: flight.Vec3{X: 0.3, Y: -0.85, Z: 3.35}, Mass: 290, Area: 0.06},
		{Name: "9m8", Station: 8, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: 3.50}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m8a", Station: 8, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: 3.50}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m8b", Station: 8, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: 3.20}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "pylon3", Station: 3, Position: flight.Vec3{X: 0.3, Y: -0.90, Z: -2.24}, Mass: 136, Area: 0.03},
		{Name: "tank3", Station: 3, Position: flight.Vec3{X: -0.2, Y: -1.25, Z: -2.24}, Mass: 158, Area: 0.07, Fuel: 1010},
		{Name: "pylon7", Station: 7, Position: flight.Vec3{X: 0.3, Y: -0.90, Z: 2.24}, Mass: 136, Area: 0.03},
		{Name: "tank7", Station: 7, Position: flight.Vec3{X: -0.2, Y: -1.25, Z: 2.24}, Mass: 158, Area: 0.07, Fuel: 1010},
		{Name: "pylon5", Station: 5, Position: flight.Vec3{X: -0.2, Y: -1.15, Z: 0}, Mass: 120, Area: 0.03},
		{Name: "tank5", Station: 5, Position: flight.Vec3{X: -0.6, Y: -1.45, Z: 0}, Mass: 158, Area: 0.07, Fuel: 1010},
		// The cheek stations (#27): LAU-116 ejector 31 kg, AIM-120C-5 round
		// 156 kg (344 lb). Appended so every existing mask bit keeps its index.
		// Buttline ±0.55 m — the rounds sit against the nacelle sides, slightly
		// forward of the wing (the arm is a plausible figure, not a survey).
		{Name: "rail4", Station: 4, Position: flight.Vec3{X: 1.0, Y: -0.70, Z: -0.55}, Mass: 31, Area: 0.01},
		{Name: "120c4", Station: 4, Position: flight.Vec3{X: 1.0, Y: -0.80, Z: -0.55}, Mass: 156, Area: 0.04, Warhead: 22.0},
		{Name: "rail6", Station: 6, Position: flight.Vec3{X: 1.0, Y: -0.70, Z: 0.55}, Mass: 31, Area: 0.01},
		{Name: "120c6", Station: 6, Position: flight.Vec3{X: 1.0, Y: -0.80, Z: 0.55}, Mass: 156, Area: 0.04, Warhead: 22.0},
		// Wing-rail AMRAAMs (#27): the LAU-127 positions the 9M already models.
		{Name: "120c2", Station: 2, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: -3.35}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c8", Station: 8, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: 3.35}, Mass: 156, Area: 0.05, Warhead: 22.0},
		// Inboard-pylon AMRAAMs (#27): the Sparrow-capable 3/7 via LAU-115C,
		// at the 2.24 m buttline the tanks share.
		{Name: "120c3", Station: 3, Position: flight.Vec3{X: 0.0, Y: -1.10, Z: -2.24}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c7", Station: 7, Position: flight.Vec3{X: 0.0, Y: -1.10, Z: 2.24}, Mass: 156, Area: 0.05, Warhead: 22.0},
		// Dual AMRAAM carriage (#27): the LAU-115 with a LAU-127 on each
		// side on all four wing pylons — the ten-round fit. Inboard twins at
		// the pylon's own arm; round pairs spread ±0.15 m like the heater
		// twins, a slightly higher hang than the single rail round.
		{Name: "twin3", Station: 3, Position: flight.Vec3{X: 0.3, Y: -0.90, Z: -2.24}, Mass: 290, Area: 0.06},
		{Name: "twin7", Station: 7, Position: flight.Vec3{X: 0.3, Y: -0.90, Z: 2.24}, Mass: 290, Area: 0.06},
		{Name: "120c2a", Station: 2, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: -3.50}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c2b", Station: 2, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: -3.20}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c8a", Station: 8, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: 3.50}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c8b", Station: 8, Position: flight.Vec3{X: 0.0, Y: -1.00, Z: 3.20}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c3a", Station: 3, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: -2.39}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c3b", Station: 3, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: -2.09}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c7a", Station: 7, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: 2.39}, Mass: 156, Area: 0.05, Warhead: 22.0},
		{Name: "120c7b", Station: 7, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: 2.09}, Mass: 156, Area: 0.05, Warhead: 22.0},
		// Inboard heaters (#27 follow-up): the same LAU-115C points the
		// AMRAAMs hang on — singles on the pylon arm, pairs on the twin.
		{Name: "9m3", Station: 3, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: -2.39}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m3a", Station: 3, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: -2.39}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m3b", Station: 3, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: -2.09}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m7", Station: 7, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: 2.39}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m7a", Station: 7, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: 2.39}, Mass: 86, Area: 0.05, Warhead: 9.4},
		{Name: "9m7b", Station: 7, Position: flight.Vec3{X: 0.0, Y: -1.05, Z: 2.09}, Mass: 86, Area: 0.05, Warhead: 9.4},
		// The empty inboard LAU-7 beside a single AIM-9: the LAU-115C/A carries
		// its side launchers in pairs (NAVEDTRA 14313 fig 15-20), so the other
		// shoulder keeps its rail. 90 lb (the LAU-7 data sheet); at the twin's
		// inboard point raised by the round's radius and the rail's half
		// height; drag about its 4.2 by 5.4 in face, faired.
		{Name: "shoulder2", Station: 2, Position: flight.Vec3{X: 0.0, Y: -0.87, Z: -3.20}, Mass: 41, Area: 0.01},
		{Name: "shoulder8", Station: 8, Position: flight.Vec3{X: 0.0, Y: -0.87, Z: 3.20}, Mass: 41, Area: 0.01},
		{Name: "shoulder3", Station: 3, Position: flight.Vec3{X: 0.0, Y: -0.92, Z: -2.09}, Mass: 41, Area: 0.01},
		{Name: "shoulder7", Station: 7, Position: flight.Vec3{X: 0.0, Y: -0.92, Z: 2.09}, Mass: 41, Area: 0.01},
	}
	a.Default = 0b11 // the bare jet flies wingtips only, exactly as before the catalog grew
	// R-LIM (NATOPS 2.8.2.8): the wing-pylon tanks engage the FCS roll-rate
	// reduction while aboard. The centreline tank is not a wing-pylon store —
	// the manual's condition excludes it, and it adds almost no roll inertia.
	for i := range a.Stores {
		if name := a.Stores[i].Name; name == "tank3" || name == "tank7" {
			a.Stores[i].Limit.Roll = true
		}
	}
	a.Engines = make([]flight.Engine, 2)
	for i := range a.Engines {
		side := float64(i)*2 - 1
		a.Engines[i] = flight.Engine{
			Position: flight.Vec3{X: -5.1, Z: side * 0.55},
			Dry:      48900, // N, F404-GE-402 (11,000 lbf)
			Reheat:   78700, // N (17,700 lbf)
		}
		a.Engines[i].Flow.Dry = 2.3e-5    // kg/(N·s): mil SFC ~0.81 lb/lbf·hr — both engines ≈ 2.25 kg/s at full mil
		a.Engines[i].Flow.Reheat = 9.3e-5 // kg/(N·s) on the BOOST thrust: AB SFC ~1.74 lb/lbf·hr total — both in full reheat ≈ 7.8 kg/s (a 3,000 kg load is ~6½ minutes of pure burner)
	}

	wing := flight.Section{Slope: 5.9, Stall: 0.19, Drag: 0.006, Ratio: 3.5}
	tail := flight.Section{Slope: 5.7, Stall: 0.21, Drag: 0.007, Ratio: 3.0}
	blade := flight.Section{Slope: 5.7, Stall: 0.21, Drag: 0.008, Ratio: 1.6}
	slender := flight.Section{Slope: 4.5, Stall: 0.35, Drag: 0.010, Ratio: 1.5}

	for _, side := range []float64{-1, 1} {
		// Main wing: the C panel is ~80% of the F's area on 84% of the span.
		a.Surfaces = append(a.Surfaces, hinged(strips(flight.Surface{
			Kind: flight.Wing, Side: side, Area: 15.2, Span: 4.7, Ratio: 3.5, Oswald: 0.75, Induced: 0.16, // recalibrated 2026-11-12 for the real flaps: AUTO's schedule (TM-107601) runs about 10 deg of flap at 7 g / 450 KCAS on the deck where the old washout ran 3.5, and with DATCOM's slotted-flap drag priced in, 0.14 left that pull Ps +18 m/s against the doghouse gate's +8 (performance_gate_test.go); 0.16 closes it and keeps the envelope map's windows. Before that, recalibrated 2026-07-21 with the fixed control law: the #131 value (0.19) was fitted while the C* law parked sustained pulls ~1 g short, so it priced the law's deficit into the drag. At 0.14 the SL sustained map is real-jet-shaped: a flat 17.5/18.0/16.9 deg/s plateau across 350-450 KCAS at 29k lb with the bucket at 400 and the limiter reached only by ~450, and 15k ft holds the 12-14 deg/s band. Residual: the 350 station sits ~0.4 g under the scaled EM anchor (the high-cl drag stack — vortex suction, post-stall blending — not this parabola) so the bucket reads ~40 kt fast
			Vortex: 1.2, Breakdown: 26 * math.Pi / 180, Channel: flight.Differential, // the wing's share of the LEX vortex system riding over the inboard panel. At 0.6/22° it was worth ~0.05 CL at combat alpha and broke down right where the fights live; with the steeper slat schedule this lifts the trimmed mid-band CL (TestLift, 16-22°) from ~1.24-1.31 to ~1.31-1.38 — the DCS-class reference sits near 1.33 — bringing corner to ~345 KCAS at 29k lb. ~sin², so negligible at approach alpha
		}, 8, span{1.0, 5.7, side}, chord{4.0, 1.1}, sweep{0.55, -1.75}, twist{0.017, -0.052}, &wing, 0.25, 0.52), 1.0, 5.7))
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
		// Twin canted fins with trailing-edge rudders: the published 9.68 m² of
		// fin between them, and at 30% chord the published 1.45 m² of rudder;
		// the height and chords follow the area at the same taper. Ratio stays
		// the calibrated effective aspect ratio (the fuselage end-plates a fin).
		// Sized 2026-09-14 against the HARV basic-F-18 sideslip derivatives.
		fin := strips(flight.Surface{
			Kind: flight.Fin, Side: side, Area: 4.84, Span: 2.4, Ratio: 1.6, Oswald: 0.7,
			Channel: flight.Rudder,
		}, 3, span{0, 2.3, side}, chord{2.75, 1.45}, sweep{-4.9, -5.6}, twist{}, &blade, 0.30, 0.52)
		for i := range fin.Elements {
			e := &fin.Elements[i]
			rise := e.Position.Z * side
			e.Position = flight.Vec3{X: e.Position.X, Y: 0.3 + rise*math.Cos(cant), Z: side * (0.85 + rise*math.Sin(cant))} // the fin roots sit a little above the wing plane the datum and CG share (the shoulder wing tops the fuselage, the fins stand on the fillet behind it); at 0.95 they carried the whole dihedral effect the unswept wing gave none of, and overshot by half once it did
			e.Axis = flight.Vec3{Y: side * math.Cos(cant), Z: math.Sin(cant)}
			e.Normal = flight.Vec3{Y: math.Sin(cant), Z: -side * math.Cos(cant)}
			e.Aerofoil = flight.Synthesize(blade)
		}
		// The fins keep unswept axes and the plain section polar: a low-aspect
		// fin is no simple-sweep surface, and swept it weathercocked a tenth
		// harder at low alpha and a fifth harder in the landing configuration,
		// outside the flight scatter the fins were sized against.
		fin.Sweep = 0
		a.Surfaces = append(a.Surfaces, fin)
	}
	// The C's dorsal speedbrake between the fins: the panel the airframe's
	// own model carries, 0.84 m across by 2.13 m long, hinged out to 60°, so
	// its drag is the plate's at sin² of the travel (aero.go) - 1.5 m² of drag
	// area fully out. It was an 0.8 m² plate deployed in a second before
	// 2026-09-15; the jet's actuator takes 2.5 s (NASA TM-110216's 60/2.5
	// deg/s no-load rate limit; NATOPS: 3 s maximum).
	a.Surfaces = append(a.Surfaces, flight.Surface{
		Kind: flight.Brake, Area: 1.7, Span: 1, Ratio: 1, Oswald: 1, Channel: flight.Spoiler,
		Elements: []flight.Element{{
			Position: flight.Vec3{X: -3.5, Y: 0.8}, Area: 1.7, Chord: 1,
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
	// Undercarriage: track 3.11 m, wheelbase 5.4 m. Stance measured from the drawn
	// model's DEPLOYED gear so the drawn mains land within 5 cm of these struts.
	// Stiffness scaled to the lighter jet at the F's ratio. Tunable.
	a.Gear.Nose = flight.Strut{Attach: flight.Vec3{X: 4.9, Y: -2.63}, Travel: 0.45, Stiffness: 4.5e5, Damping: 5.5e4, Steer: 75 * math.Pi / 180} // NWS HI 75°; LOW's 16° is the core's (Strut.limit)
	a.Gear.Left = flight.Strut{Attach: flight.Vec3{X: -0.5, Y: -2.63, Z: -1.55}, Travel: 0.5, Stiffness: 9e5, Damping: 1.1e5}
	a.Gear.Right = flight.Strut{Attach: flight.Vec3{X: -0.5, Y: -2.63, Z: 1.55}, Travel: 0.5, Stiffness: 9e5, Damping: 1.1e5}
	a.Hook.Position = flight.Vec3{X: -6.0, Y: -0.55}
	a.Hook.Length = 2.4
	// Crash probes and belly skid points, scaled to the shorter airframe.
	a.Probes = []flight.Vec3{{X: 8.0, Y: -0.4}, {X: -8.0, Y: 0.3}, {X: -1.4, Z: -5.7}, {X: -1.4, Z: 5.7}, {X: -5.6, Y: 2.9, Z: -1.3}, {X: -5.6, Y: 2.9, Z: 1.3}}
	a.Belly = []flight.Vec3{{X: 6.3, Y: -1.2}, {X: 2.7, Y: -1.3}, {X: -0.9, Y: -1.35}, {X: -4.2, Y: -1.3}, // fuselage keel MEASURED from the rendered mesh (dev_measure, #72): deepest -1.35 mid, -1.32 at the ventral fins, -1.26 at the nose barrel — eyeballed shallower values sank the model visibly into the runway. The nose point exists because with only aft points the decelerating slide dropped the nose onto the crash probe
		{X: 0.2, Y: -0.77, Z: -6.26}, {X: 0.2, Y: -0.77, Z: 6.26}} // wingtip rests at the mesh's MEASURED widest tip vertex (y -0.77, z 6.26 — the drawn mesh spans ~5% wide of the figure 4-4 buttline the tip store elements use): an inboard/shallow point lets a topple lean ~8 degrees and dip the stabilators into the runway before physics catches what the eye already sees touching; at the mesh extremity it catches at ~5 and the stabs stay clear (gear.go gates the tips off for unsupported flying-speed arrivals, where the crash probes own them)
	return a
}

type span struct{ root, tip, side float64 }
type chord struct{ root, tip float64 }
type sweep struct{ root, tip float64 } // aerodynamic-centre x at root and tip
type twist struct{ root, tip float64 } // built-in incidence, rad

// strips fills a surface with n equal-span trapezoid elements.
// fade is the flaps' and ailerons' lift effectiveness against their deflection,
// as a share of the small-deflection value: both are single-slotted (NASA
// TM-107601 table 3.3), and a single-slotted flap of 30% chord loses its
// effectiveness past 20 deg as USAF DATCOM figure 6.1.1.1-41 correlates it
// (its section lift-effectiveness parameter at each deflection over its value
// at none).
var fade = func() flight.Curve {
	deg := math.Pi / 180
	at := []float64{0, 10, 20, 25, 30, 35, 40, 45, 50, 55, 60, 70, 80}
	share := []float64{1, 1, 0.964, 0.945, 0.909, 0.858, 0.8, 0.72, 0.618, 0.509, 0.455, 0.407, 0.382}
	c := make(flight.Curve, len(at))
	for i := range at {
		c[i] = [2]float64{at[i] * deg, share[i]}
	}
	return c
}()

// hinged gives each strip of a wing laid from root to tip, m, the surface
// hinged across it: the trailing-edge flap from BL 50 to 153, the aileron from
// BL 156 to 224.5 (tp209033 figure 2, the flap's outer end and the aileron's
// inner by photo match: apps/air/tools/fa18c facts.json layout.surfaces), each
// covering its share of the strip's span. The flap is 30% of the chord (NASA
// TM-107601 table 3.3); the aileron's chord is the fraction its area
// (cr201687 table 1: 24.4 ft2 for both sides) takes of the wing's cited
// trapezoid there (190.29 in at the centreline to 66.28 at BL 222.1). Both
// lose lift at large deflections (fade). Each is scaled to what the F/A-18
// measured (NASA TP-97-206539, Iliff and Wang 1997, figures 12 and 13). The
// ailerons lift and pitch 15% more than the strips' theory gives them: 0.0065
// normal force and -0.0022 pitching moment per degree in flight at 10 deg
// alpha, the wind-tunnel database 0.007 and -0.0023 below it, against the
// strips' 0.0058 and -0.0020. The flaps lift 12% less: 0.88 brings their
// 0.0163 per degree at low alpha to the wind tunnel's 0.013-0.015 and their
// 0.0093-0.0119 at 14-20 deg to the flight values' 0.007-0.0095, and with it
// the FULL approach to NATOPS's on-speed anchor, 134 kt at 8.1 deg with 2,500
// kg of fuel (envelope_test.go), where 1 flew it at 129 kt. Between 8 and 12
// deg alpha the wing's own polar steepens past its stall and carries the
// flaps' lift per degree to twice the measured.
func hinged(s flight.Surface, root, tip float64) flight.Surface {
	const inch = 0.0254
	chord := func(bl float64) float64 { return 190.29 - (190.29-66.28)*bl/222.1 }
	fraction := func(area, from, to float64) float64 {
		return area / 2 * 144 / ((chord(from) + chord(to)) / 2 * (to - from))
	}
	controls := []struct {
		from, to, flap float64
		aileron        bool
		gain, centre   float64
	}{
		{50, 153, 0.30, false, 0.88, 0.07},
		{156, 224.5, fraction(24.4, 156, 224.5), true, 1.15, 0},
	}
	width := (tip - root) / float64(len(s.Elements))
	for i := range s.Elements {
		e := &s.Elements[i]
		lo, hi := root+float64(i)*width, root+float64(i+1)*width
		e.Flap, e.Hinged, e.Fade = 0, 0, fade
		best := 0.0
		for _, c := range controls {
			covered := math.Max(0, math.Min(hi, c.to*inch)-math.Max(lo, c.from*inch)) / width
			if covered > best {
				best, e.Flap, e.Aileron, e.Hinged, e.Gain, e.Centre = covered, c.flap, c.aileron, covered, c.gain, c.centre
			}
		}
	}
	return s
}

func strips(s flight.Surface, n int, sp span, ch chord, sw sweep, tw twist, section *flight.Section, flap float64, limit float64) flight.Surface {
	// The elements' span axes lie along the swept aerodynamic-centre line, so
	// each section sees the flow normal to it: at sideslip the leading panel
	// unsweeps and lifts more, the trailing one less, and the swept wing's
	// dihedral effect, its share of the weathercock and its earlier stall on
	// the trailing side all emerge. Unswept axes (before 2026-09-15) gave the
	// wing no sideslip effects at all; the fins carried the whole dihedral
	// effect and it could not grow with lift the way the flight data does.
	// The section polar is the calibrated body-frame one mapped into the
	// section frame (flight.Swept), so the surface keeps its lift curve, stall
	// and all, at zero sideslip.
	s.Sweep = math.Atan2(sw.root-sw.tip, sp.tip-sp.root)
	across := math.Cos(s.Sweep)
	polar := flight.Swept(*section, s.Sweep)
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
			Axis:      flight.Vec3{X: -sp.side * math.Sin(s.Sweep), Z: across}, // +Z on both sides, so flow × axis is up for both
			Aerofoil:  polar,
			Flap:      flap,
			Limit:     limit,
		})
	}
	return s
}

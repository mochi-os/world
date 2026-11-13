// Mochi world: Airframe definition types
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

// Attachments is the largest store catalogue the attach mask can carry; New
// refuses an airframe with more. The mask is a uint64, but the binding limit is
// narrower: it crosses the wasm boundary as a JavaScript number, and the client
// composes it by adding powers of two, so a set spanning more than a double's
// 53-bit mantissa loses its low bits to rounding. Beyond 64, Go's own shift
// yields zero for every further index. Both failures are silent - the store
// never attaches, weighs, drags or appears in a mask, and no test notices -
// which is why the ceiling is a panic at construction rather than a comment.
const Attachments = 53

// Airframe is the immutable aircraft definition: geometry, mass, engines,
// limits. One embedded instance (fighter.go) exists in v1.
type Airframe struct {
	Name      string // internal only — never shown in game
	Reference struct{ Area, Span, Chord float64 }
	Surfaces  []Surface
	Body      []Station
	Engines   []Engine                                                                // 0..4; State carries four slots regardless
	Stores    []Store                                                                 // the full catalog of mountable fixtures and stores, wingtips first — mass and flat-plate drag while attached (Model.Stores masks the flown subset)
	Default   uint64                                                                  // the attach mask New arms (the catalog holds every legal fitment; a bare New flies this subset)
	Mass      struct{ Empty, Fuel float64 }                                           // kg; Fuel = internal capacity
	Control   Control                                                                 // control-law data the shared law flies with
	Gun       Gun                                                                     // the cannon: where it fires from and what it kicks back with
	Forebody  float64                                                                 // m²: the cross-section the slender-body side force at sideslip works on - the widest station plus what the LEX add, calibrated to the flight-measured Cy_beta; 0 falls back to the nose station's own area
	Wave      struct{ Hump, Body float64 }                                            // transonic wave-drag character: per-element hump peak, body peak (area-ruling quality)
	Inertia   Mat3                                                                    // empty aircraft, about empty CG (frames.go axis mapping)
	Center    Vec3                                                                    // empty CG, body, from datum
	Tank      Vec3                                                                    // fuel CG, body
	Cockpit   Vec3                                                                    // pilot seat, body (battle: the pilot-kill target)
	Limit     struct{ Positive, Negative, Override, Alpha, Floor, Reference float64 } // Reference: gross mass (kg) the symmetric-g placard is written at; 0 = no weight scheduling
	Gear      struct{ Nose, Left, Right Strut }
	Hook      struct {
		Position Vec3
		Length   float64
	}
	Probes []Vec3 // crash probes: nose, tail, wingtips, fins (any contact = crash)
	Belly  []Vec3 // permitted skid contacts for gear-up arrivals
}

// Surface is one lifting/control surface, split spanwise into elements.
type Surface struct {
	Kind      Kind
	Side      float64 // -1 left, +1 right, 0 centre
	Area      float64 // m²
	Span      float64 // m
	Ratio     float64 // aspect ratio
	Oswald    float64 // span-efficiency for induced flow
	Induced   float64 // supplementary drag-due-to-lift K (cd += K·cl²): the lifting-line tilt only prices the linear lift; the classic F-18 polar K≈0.19 includes the nonlinear/trim costs
	Slope     float64 // section lift slope (Cl per rad) — downwash consistency
	Vortex    float64 // Polhamus Kv (0 = no vortex lift)
	Breakdown float64 // vortex breakdown alpha, rad
	Channel   Channel
	Sweep     float64 // rad, aft sweep of the aerodynamic-centre line the elements' span axes lie along
	Elements  []Element
}

type Kind int

const (
	Wing Kind = iota
	Strake
	Stabilator
	Fin
	Brake
)

type Channel int

const (
	Fixed        Channel = iota
	Symmetric            // stabilator pair
	Differential         // flaperons
	Rudder
	Spoiler
)

// Element is a spanwise strip with its own local airflow each step.
type Element struct {
	Position  Vec3 // aerodynamic centre, body, from datum
	Area      float64
	Chord     float64
	Incidence float64 // rad, built-in twist
	Normal    Vec3    // body: local lift reference direction
	Axis      Vec3    // body: span axis
	Aerofoil  *Table  // synthesized section polar (aerofoil.go)
	Flap      float64 // control chord fraction cf/c; 0 = no control surface here
	Limit     float64 // max deflection, rad
	Aileron   bool    // a Differential surface's control here is the aileron, not the trailing-edge flap
	Hinged    float64 // the share of the element's span its control covers; 0 reads as all of it
	Fade      Curve   // the control's lift effectiveness against the size of its deflection, rad, as a share of the small-deflection value; empty holds it whole
	Gain      float64 // the control's small-deflection lift effectiveness over the thin-aerofoil theory Effectiveness gives, as measured on the airframe; 0 reads as 1
	Centre    float64 // chords behind the strip's quarter chord at which the control's lift acts, as measured on the airframe: its pitching moment over its lift, beyond what the strip's place and the tail's downwash give
}

// Station is a fuselage segment for parasitic drag, slender-body normal
// force, and crossflow.
type Station struct {
	Position Vec3
	Area     float64 // frontal, m²
	Plan     float64 // planform, m²
	Drag     float64 // Cd on frontal area
}

// Store is one catalog entry - a fixture or a store at its station, carrying
// mass and flat-plate drag while its mask bit is set. Mutually exclusive
// fitments are distinct bits; a fuel-bearing entry extends State.External, its wing or centreline group by where it hangs.
type Store struct {
	Name     string // catalog identity — the wire and client vocabulary
	Station  int    // NATOPS station 1..9 (port tip to starboard tip)
	Position Vec3
	Mass     float64             // kg while attached (dry mass for tanks)
	Area     float64             // m² flat plate while attached
	Fuel     float64             // kg usable capacity; 0 = dry store
	Warhead  float64             // kg of high explosive aboard; 0 = inert (a rail, a pylon, a tank). A hit here can cook the round off — which is why a loaded jet is a more explosive target than a Winchester one, and why crews jettison stores when they are hit.
	Limit    struct{ Roll bool } // FCS restrictions while attached — Roll: the store engages R-LIM (NATOPS 2.8.2.8, wing-pylon tanks and air-to-ground stores)
}

// Engine is one powerplant.
type Engine struct {
	Position Vec3
	Dry      float64                       // N, sea-level static
	Reheat   float64                       // N, sea-level static max
	Flow     struct{ Dry, Reheat float64 } // TSFC, kg/(N·s)
}

// Strut is one landing-gear leg.
type Strut struct {
	Position  float64 // reserved layout slot (geometry lands in phase F)
	Attach    Vec3    // body attachment of the contact point at full extension
	Travel    float64 // m of compression
	Stiffness float64 // N/m
	Damping   float64 // N·s/m
	Steer     float64 // max steering angle, rad (nosewheel): the HI mode's throw
}

// Gun is the cannon's place and its recoil: the average force it kicks back
// with while firing, aft along the body axis at the port, from published gun
// data (the M61A1's 3,818 lbf at 6,000 rpm). Zero: no gun, or one whose kick
// is not modelled.
type Gun struct {
	Position Vec3    // the port, body, from datum
	Recoil   float64 // N, average while firing
}

// Control is the airframe-specific control-law data: schedules, throws, and
// actuator rates. The law itself (loop shaping, limiter structure) is shared
// across aircraft; everything a different airframe would change lives here.
type Control struct {
	Onspeed  float64                                // PA on-speed alpha, rad
	Blowdown float64                                // deflection·dynamic-pressure ceiling, Pa
	Gearing  struct{ Pitch, Roll, Yaw float64 }     // Direct-mode stick to surface, rad
	Slat     struct{ Slope, Offset, Limit float64 } // leading-edge schedule: Slope·(alpha−Offset) up to Limit
	Flaps    Flaps                                  // the trailing-edge flaps and aileron droop, by flap switch position
	Flyaway  float64                                // catapult trim board setting, rad: the stabilator's takeoff trim off the shuttle
	Capture  float64                                // reference alpha that trim commands hands-off after the shot, rad
	Toe      Curve                                  // rudder toe-in against alpha in the takeoff and landing modes, rad (+ both trailing edges inboard; canted fins turn it into tail downforce for rotation); weight on the wheels holds the low-alpha end
	Throw    struct {                               // surface limits, rad
		Down, Up     float64                    // stabilator (Down clamps the trailing-edge-UP side — core negative = nose-up; Up clamps trailing-edge-down)
		Differential float64                    // the stabilators' differential at the full roll command, each side
		Aileron      struct{ Down, Up float64 } // aileron travel (positive = trailing edge down)
		Trailing     struct{ Down, Up float64 } // trailing-edge flap travel (positive = trailing edge down)
		Rudder       float64
		Brake        float64 // speed brake panel travel, rad: its drag goes with sin² of the deflection, so the fraction the FCS reports is not the fraction of the drag
	}
	Rate struct { // actuator slew, rad/s (Brake in fraction/s)
		Stabilator, Aileron, Trailing, Rudder, Slat, Brake float64
	}
}

// Flaps is the trailing edge's schedule. AUTO (flaps up) moves the flaps
// with alpha and Mach and holds the ailerons undrooped; HALF and FULL set
// both to a fixed angle washed out with dynamic pressure toward the mode's
// airspeed limit.
type Flaps struct {
	Auto         []Sheet // AUTO: the flaps against alpha at each Mach, rad, in ascending Mach
	Ceiling      Curve   // AUTO: the flaps' limit against impact pressure, Pa to rad
	Mach         float64 // AUTO: the flaps stay up above this Mach
	Half, Full   Setting // HALF and FULL: the flaps and the ailerons' droop, rad
	Pressure     float64 // HALF and FULL wash out with dynamic pressure: whole below 0.45·Pressure, gone at Pressure, Pa
	Lift         float64 // the FULL configuration's trimmed zero-alpha lift coefficient (flaps, droop and slats), for the PA law's level-flight cap
	Differential struct {
		Gain  Curve   // AUTO: the flaps' share of the roll command against alpha
		Limit float64 // rad
	}
}

// Setting is one flap switch position's trailing edge, rad.
type Setting struct{ Trailing, Aileron float64 }

// Sheet is one Mach's schedule.
type Sheet struct {
	Mach  float64
	Curve Curve
}

// Curve is a piecewise-linear schedule, (input, output) in ascending input,
// held at its ends.
type Curve [][2]float64

// At reads the curve at x.
func (c Curve) At(x float64) float64 {
	if len(c) == 0 {
		return 0
	}
	if x <= c[0][0] {
		return c[0][1]
	}
	for i := 1; i < len(c); i++ {
		if x <= c[i][0] {
			a, b := c[i-1], c[i]
			return a[1] + (b[1]-a[1])*(x-a[0])/(b[0]-a[0])
		}
	}
	return c[len(c)-1][1]
}

// Automatic reads the AUTO flap schedule at an alpha and Mach, between its sheets
// linearly in Mach and held past the first and last.
func (f *Flaps) Automatic(alpha, mach float64) float64 {
	n := len(f.Auto)
	if n == 0 || mach > f.Mach {
		return 0
	}
	if mach <= f.Auto[0].Mach {
		return f.Auto[0].Curve.At(alpha)
	}
	for i := 1; i < n; i++ {
		if mach <= f.Auto[i].Mach {
			a, b := f.Auto[i-1], f.Auto[i]
			t := (mach - a.Mach) / (b.Mach - a.Mach)
			return a.Curve.At(alpha)*(1-t) + b.Curve.At(alpha)*t
		}
	}
	return f.Auto[n-1].Curve.At(alpha)
}

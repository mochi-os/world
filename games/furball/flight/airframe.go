// Mochi world: Airframe definition types
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

// Airframe is the immutable aircraft definition: geometry, mass, engines,
// limits. One embedded instance (fighter.go) exists in v1.
type Airframe struct {
	Name      string // internal only — never shown in game
	Reference struct{ Area, Span, Chord float64 }
	Surfaces  []Surface
	Body      []Station
	Engines   []Engine                      // 0..4; State carries four slots regardless
	Mass      struct{ Empty, Fuel float64 } // kg; Fuel = internal capacity
	Control   Control                       // control-law data the shared law flies with
	Wave      struct{ Hump, Body float64 }  // transonic wave-drag character: per-element hump peak, body peak (area-ruling quality)
	Inertia   Mat3                          // empty aircraft, about empty CG (frames.go axis mapping)
	Center    Vec3                          // empty CG, body, from datum
	Tank      Vec3                          // fuel CG, body
	Limit     struct{ Positive, Negative, Override, Alpha, Floor float64 }
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
}

// Station is a fuselage segment for parasitic drag, slender-body normal
// force, and crossflow.
type Station struct {
	Position Vec3
	Area     float64 // frontal, m²
	Plan     float64 // planform, m²
	Drag     float64 // Cd on frontal area
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
	Steer     float64 // max steering angle, rad (nosewheel)
}

// Control is the airframe-specific control-law data: schedules, throws, and
// actuator rates. The law itself (loop shaping, limiter structure) is shared
// across aircraft; everything a different airframe would change lives here.
type Control struct {
	Onspeed  float64                                                     // PA on-speed alpha, rad
	Blowdown float64                                                     // deflection·dynamic-pressure ceiling, Pa
	Gearing  struct{ Pitch, Roll, Yaw float64 }                          // Direct-mode stick to surface, rad
	Slat     struct{ Slope, Offset, Limit float64 }                      // leading-edge schedule: Slope·(alpha−Offset) up to Limit
	Flap     struct{ Slope, Offset, Limit, Pressure float64 }            // AUTO manoeuvring flaps: trailing edge droops with alpha, washing out with q̄/Pressure
	Flyaway  float64                                                     // PA-mode pitch-attitude capture datum, rad (hands-off catapult flyaway)
	Droop    struct{ Angle, Pressure float64 }                           // PA trailing-edge droop, rad, washed out by q̄/Pressure
	Throw    struct{ Down, Up, Flap, Rudder float64 }                    // surface limits, rad (stabilator: Down clamps the trailing-edge-UP side — core negative = nose-up; Up clamps trailing-edge-down)
	Rate     struct{ Stabilator, Flaperon, Rudder, Slat, Brake float64 } // actuator slew, rad/s (Brake in fraction/s)
}

// Mochi world: Dynamic state and inputs
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

// Inputs is one control sample. Pitch/Roll/Yaw are COMMANDS the FCS
// interprets (g/rate/beta demands), not surface deflections; the host
// applies any sensitivity scaling before the wire and clamps to ±1.
type Inputs struct {
	Pitch      float64 // -1..1, +1 = full aft = nose-up demand
	Roll       float64 // -1..1, +1 = roll right
	Yaw        float64 // -1..1, +1 = nose right
	Throttle   float64 // 0..1; reheat only at 1.0 (no detent)
	Speedbrake float64 // 0..1 commanded
	Reheat     float64 // commanded reheat fraction 0..1: the throttle's position in the afterburner range (0 = dry); the fuel control quantizes to the F404's five zones
	Brake      bool    // wheel brakes, held, both mains
	Bypass     bool    // the ANTI SKID switch OFF (NATOPS 2.10.3.2): the brakes take full pressure on the pedal alone; the zero value is ON, so a host that never sends it keeps protected brakes
	Steering   int     // nosewheel steering (NATOPS 2.10.2): -1 off, the nosewheel castors; 0 LOW, ±16°; +1 HI, the leg's full throw (±75°); the zero value is LOW, so a host that never sends it keeps taxi steering
	Gear       bool    // commanded position, true = down
	Emergency  bool    // the gear handle turned 90° and pulled (NATOPS 2.10.1.6): the gear free-falls down and locks whatever the hydraulics, and stays down while the host holds it
	Mechanical bool    // MECH ON: all electrical power gone, the host judging when (NATOPS 15.17): the flight control computers drop out and the stick drives the stabilators through the mechanical linkage (2.8.2.10)
	Hook       bool    // true = deployed
	Probe      bool    // refuelling probe out (drag + the real ~300 KCAS limit stays procedural)
	Launch     bool    // catapult fire edge, while attached
	Override   bool    // paddle switch: raises the g ceiling, records overstress
	Trim       float64 // -1..1 held pitch-trim rate, +1 = nose-up: UA nudges the attitude datum, PA biases the alpha datum
	Lean       float64 // -1..1 held roll-trim rate, +1 = right wing down: walks the differential-flaperon datum
	Reset      bool    // one-shot trim reset: zero the alpha and roll datums, re-datum the attitude hold
	Onspeed    bool    // one-shot: the pitch trim alone back to on-speed, the roll trim kept (NATOPS 2.9.2.1: the autopilot disengaged in the landing configuration)
	Reverted   bool    // mission computer 1 lost, the host judging when (NATOPS 25.1): the flight control computer gets no g limiter or stores data and reverts to a 7.5 g aircraft with no roll rate limiting for stores
	Held       float64 // internal wing fuel the INTR WING switch at INHIBIT holds out of the feed, kg (NATOPS 2.2.3.3): aboard and weighed, and not the engines' to burn; the host apportions the tanks, and the zero value holds none
	Flap       float64 // flap switch: 0 = AUTO (the virtual schedule), 1 = HALF, 2 = FULL
	Dump       bool    // fuel dump switch: burn() drains toward the bingo floor at the NATOPS rate while held on
	Secure     [2]bool // per-engine fuel OFF (the fire drill and the runaway shutdown, NATOPS 15.1); clearing the switch relights
	Transfer   [2]int  // the EXT TANKS switches (NATOPS 2.2.4.1), [0] WING and [1] CTR: 0 NORM, -1 STOP, +1 ORIDE; the zero value is NORM, so a host that never sends them keeps the normal transfer
	Eject      bool    // ejection handle: flight ignores it; the host judges
	Fire       bool    // weapons flags ride the wire; flight ignores them
	Flare      bool
	Chaff      bool // the dispense switch forward (NATOPS 2.1.1.7.3): chaff singles, an edge like the flare's
	Solo       bool // the dispenser at BYPASS: a flare edge releases the flare alone, with no chaff bundle beside it
	Extinguish bool // the FIRE EXTGH pushbutton (NATOPS 2.14.2): an edge the host judges; flight ignores it
	Missile    bool
	Radar      bool // the radar missile's own trigger (#27): a separate magazine and a separate edge from the heater's
	Visual     bool // that trigger edge is a VISUAL shot (#155): no lock, the round's own seeker live off the rail
	Jammer     bool // the jammer's ARMED state (#31): a level, not an edge — the server judges when it actually radiates
	Sequence   uint32
}

// State is the complete integrated dynamic state — what the server
// snapshots, the client rewinds to, and Encode/Decode serialise. Mass is
// derived (empty + fuel + damage), never stored. All contact bookkeeping
// that geometry can re-derive (strut compression, cable payout) is not here.
type State struct {
	Position Vec3 // world, m
	Velocity Vec3 // world, m/s
	Attitude Quat // body->world
	Omega    Vec3 // body, rad/s
	Fuel     float64
	External Tanks          // external-tank fuel, kg, by group; it transfers into the internal tanks as the EXT TANKS switches allow (burn) and rides at the tank positions in weigh()
	Engine   [4]EngineState // one per Airframe.Engines entry (0..4); unused slots stay zero
	Fcs      FcsState
	Gear     GearState
	Damage   DamageState
	Buffet   float64 // aerodynamic buffet intensity 0..1 — the LEX/stall shake the airframe feels, for the client's seat-of-pants cue
	Time     float64 // sim time, s — drives turbulence and the carrier pose

	// Retracted is the inflight IDLE stop's latch (NATOPS 2.1.1.7.2): airborne,
	// the stop holds the throttles at flight idle, and pulling them to it at 5 g
	// or more retracts it to ground idle until they come off it or the jet lands.
	Retracted bool
}

// Tanks is the external fuel by group, kg: the wing pylon tanks (stations 3
// and 7) and the centreline tank (5), which the EXT TANKS switches stop and
// start separately (NATOPS 2.2.4.1). Within a group the tanks drain in step.
type Tanks struct {
	Wing   float64
	Centre float64
}

// Total is all the external fuel aboard, kg.
func (t Tanks) Total() float64 { return t.Wing + t.Centre }

// EngineState is the achieved thrust condition of one engine.
type EngineState struct {
	Spool  float64 // 0..1 achieved dry-thrust fraction
	Reheat float64 // 0..1 achieved reheat stage
}

// Pair is a left/right actuator pair.
type Pair struct {
	Left, Right float64
}

// FcsState is the achieved control-system state: actuator positions and
// controller memories.
type FcsState struct {
	Stabilator Pair    // rad, + trailing edge down
	Flaperon   Pair    // rad, + trailing edge down
	Rudder     float64 // rad, ganged pair
	Slat       float64 // leading-edge flap, rad (scheduled)
	Flap       float64 // trailing-edge droop, rad (PA configuration)
	Speedbrake float64 // 0..1 achieved
	Integral   float64 // outer-loop g-trim integrator (rad/s of rate demand)
	Trim       float64 // inner-loop surface trim integrator (rad of stabilator)
	Washout    float64 // yaw-damper washout filter state
	Pitchwash  float64 // pitch-damper washout filter state: the slow tracker of excess pitch rate the low-q tracking damper subtracts, so steady g-builds pass and only oscillation is damped
	Demand     float64 // onset-shaped g demand (12 g/s slew — no slam transients)
	Normal     float64 // sensed load factor (body up) from the last step — the g meter
	Reference  float64 // attitude-hold datum, rad of pitch (the stick-free hold in both laws; an early design stored trimmed airspeed here and this comment outlived it)
	Datum      float64 // PA trim bias, rad of alpha about the law's own datum — the pitch trim switch in the landing configuration
	Bank       float64 // roll-trim datum: a standing differential-flaperon command (stick fraction), the roll half of the trim hat
	Recovery   bool    // spin recovery mode engaged (NATOPS 2.8.2.6): latched from the stick placed as the display asks until it goes prospin, airspeed passes about 245 kt or the yaw rate falls under 15°/s
}

// GearState is the undercarriage, catapult, and arrestor condition, plus
// the contact events the host reads.
type GearState struct {
	Extension float64 // 0 up .. 1 down
	Catapult  int     // attached catapult index, -1 free
	Stroke    float64 // m travelled along the stroke; -1 = not fired, -2 = unhooked (re-arms once clear of the shuttle)
	Wire      int     // engaged wire index, -1 free
	Wow       bool    // weight on wheels
	Touch     Touch
	Contact   int // crash-probe index that touched, -1 none (host judges)
}

// Touch records the first surface contact of an arrival for the host's
// verdict gates (land / bounce / crash). The host clears it after reading.
type Touch struct {
	Occurred bool
	Sink     float64 // m/s downward at contact
	Bank     float64 // rad at contact
	Kind     int     // surface kind (host vocabulary)
}

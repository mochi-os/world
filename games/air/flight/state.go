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
	Gear       bool    // commanded position, true = down
	Hook       bool    // true = deployed
	Probe      bool    // refuelling probe out (drag + the real ~300 KCAS limit stays procedural)
	Launch     bool    // catapult fire edge, while attached
	Override   bool    // paddle switch: raises the g ceiling, records overstress
	Trim       float64 // -1..1 held pitch-trim rate, +1 = nose-up: UA nudges the attitude datum, PA biases the alpha datum
	Lean       float64 // -1..1 held roll-trim rate, +1 = right wing down: walks the differential-flaperon datum
	Reset      bool    // one-shot trim reset: zero the alpha and roll datums, re-datum the attitude hold
	Flap       float64 // flap switch: 0 = AUTO (the virtual schedule), 1 = HALF, 2 = FULL
	Dump       bool    // fuel dump switch: burn() drains toward the bingo floor at the NATOPS rate while held on
	Secure     [2]bool // per-engine fuel OFF (the fire drill and the runaway shutdown, NATOPS 15.1); clearing the switch relights
	Eject      bool    // ejection handle: flight ignores it; the host judges
	Fire       bool    // weapons flags ride the wire; flight ignores them
	Flare      bool
	Missile    bool
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
	External float64        // external-tank fuel, kg, summed over attached tanks; burns before internal (the real transfer order) and rides at the tank positions in weigh()
	Engine   [4]EngineState // one per Airframe.Engines entry (0..4); unused slots stay zero
	Fcs      FcsState
	Gear     GearState
	Damage   DamageState
	Buffet   float64 // aerodynamic buffet intensity 0..1 — the LEX/stall shake the airframe feels, for the client's seat-of-pants cue
	Time     float64 // sim time, s — drives turbulence and the carrier pose
}

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

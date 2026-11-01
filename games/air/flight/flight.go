// Mochi world: Flight simulation core — contract
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Package flight is the vehicle-neutral simulation core: a blade-element
// F/A-18E-class flight model with contact physics, compiled both native (the
// authoritative world server) and to WebAssembly (the browser client, for
// single-player and multiplayer prediction). Pure and deterministic: fixed
// timestep, stdlib math only, no I/O, no clock, no global randomness, no
// allocation in the hot path. Frames: body x-forward / y-up / z-right, world
// y-up (see frames.go — a deliberate deviation from aerospace z-down,
// matching every existing boundary of the game).
package flight

// Version identifies the model's behaviour and state layout. It travels in the
// multiplayer join payload and the server listing; hosts on different versions
// disable prediction rather than mispredict. Held at 1: it changes only on the
// project owner's instruction, never as a side effect of a behavioural change.
const Version = 1

// Dt is the simulation timestep. Hosts choose how many steps to run, not how
// long a step is, with one exception: single player's time compression sets
// Model.Interval to as much as 1/60 s, which costs about 1.6 degrees of
// attitude over half a minute of hard turning where Dt costs nothing. The
// server and multiplayer prediction always step at Dt.
const Dt = 1.0 / 240

// Environment is the per-match world configuration outside the aircraft.
type Environment struct {
	Seed        uint64  // drives turbulence and weather-of-the-day; per match
	Wind        Vec3    // mean surface (10 m) wind, world m/s
	Turbulence  float64 // gust intensity σ, m/s (0 = calm)
	Temperature float64 // sea-level offset from ISA, K (Midway climatology ≈ +9)
	Pressure    float64 // sea-level pressure, Pa (0 = ISA standard 101325)
	Frame       Frame   `json:"-"` // the frame the match's coordinates are in (earth.go); the zero Frame is the world frame, Earth. A host sets it; it never travels
	Chart       Frame   `json:"-"` // the frame the map is charted in, and the wakes and the weather's pattern are fixed in (earth.go); the zero Frame is Midway. A host sets it; it never travels
	Cloud       Cloud   // convective cloud layer (zero = clear/stratiform/smooth)
	Cheat       struct {
		Fuel bool // mission cheat: the tank never depletes — burn() leaves State.Fuel exactly where the spawn set it, so mass stays constant and the same flag drives the server and the client's wasm core identically
	}
}

// Fighter is the airframe the package's own tests fly, wired by the test
// bootstrap. Hosts resolve airframes through the aircraft catalogue instead.
var Fighter *Airframe

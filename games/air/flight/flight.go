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

// Version identifies the model's behaviour and state layout. It travels in
// the multiplayer join payload; hosts on different versions disable
// prediction rather than mispredict. Bump on ANY behavioural change.
const Version = 24

// Dt is the fixed simulation timestep. Hosts never choose a timestep; they
// choose how many steps to run.
const Dt = 1.0 / 240

// Environment is the per-match world configuration outside the aircraft.
type Environment struct {
	Seed        uint64  // drives turbulence and weather-of-the-day; per match
	Wind        Vec3    // mean surface (10 m) wind, world m/s
	Turbulence  float64 // gust intensity σ, m/s (0 = calm)
	Temperature float64 // sea-level offset from ISA, K (Midway climatology ≈ +9)
	Pressure    float64 // sea-level pressure, Pa (0 = ISA standard 101325)
	Wrap        float64 // toroidal world size, m; 0 = none
	Cloud       Cloud   // convective cloud layer (zero = clear/stratiform/smooth)
	Cheat       struct {
		Fuel bool // mission cheat: the tank never depletes — burn() leaves State.Fuel exactly where the spawn set it, so mass stays constant and the same flag drives the server and the client's wasm core identically
	}
}

// Shortest returns the minimum-image difference b-a along one axis of the
// toroidal world.
func Shortest(a float64, b float64, size float64) float64 {
	d := b - a
	if size <= 0 {
		return d
	}
	half := size / 2
	for d > half {
		d -= size
	}
	for d < -half {
		d += size
	}
	return d
}

// Fighter is the airframe the package's own tests fly; the test bootstrap
// (bootstrap_test.go, package flight_test) wires it to the reference dataset
// in fighter_test.go — a fixed F/A-18F-class airframe kept out of the
// shipping catalogue. Hosts never read it — they resolve airframes through
// the aircraft catalogue and pass them to New.
var Fighter *Airframe

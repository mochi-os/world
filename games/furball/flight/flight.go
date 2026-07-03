// Mochi world: Furball flight model
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Package flight is the vehicle-neutral simulation core, keeping the plan
// document's §10 contract: pure fixed-timestep Step over (State, Inputs,
// Environment), toroidal wrap inside the integrator, SI units, body axes
// x-forward / y-up / z-right matching the web client.
//
// The current internals are the deliberate placeholder: a faithful port of
// the client's interim kinematics (engine.ts fly_player, airborne branch) so
// the server is authoritative from day one. The real blade-element model
// replaces these internals in the flight-model phase without changing the
// contract.
package flight

import (
	"math"
)

// Inputs is one normalised control sample.
type Inputs struct {
	Pitch      float64 // -1..2 effective deflection (client sensitivity applied)
	Roll       float64
	Yaw        float64
	Throttle   float64 // 0..1
	Speedbrake float64 // 0..1
	Reheat     bool
	Brake      bool
	Gear       bool
	Hook       bool
	Fire       bool
	Flare      bool
	Missile    bool
	Sequence   uint32
}

// State is the complete integrated dynamic state.
type State struct {
	Position  Vec3
	Direction Vec3 // unit velocity direction (the client's vel_dir)
	Speed     float64
	Attitude  Quat // body->world
	Time      float64
}

// Environment is per-match configuration.
type Environment struct {
	Seed uint64
	Wind Vec3
	Wrap float64 // toroidal world size (m); 0 = no wrap
}

// Rates of the placeholder model (per second, matching engine.ts).
const (
	rate_roll  = 3.0
	rate_pitch = 1.3
	rate_yaw   = 0.6
)

// Step advances the state one fixed timestep. Pure: no I/O, no clock, no
// global randomness.
func Step(s State, in Inputs, dt float64, env Environment) State {
	forward := s.Attitude.Rotate(Vec3{1, 0, 0})
	up := s.Attitude.Rotate(Vec3{0, 1, 0})
	right := s.Attitude.Rotate(Vec3{0, 0, 1})

	// Attitude rates from control deflections.
	s.Attitude = axis(right, clamp(in.Pitch, -2, 2)*rate_pitch*dt).multiply(s.Attitude)
	s.Attitude = axis(up, -clamp(in.Yaw, -2, 2)*rate_yaw*dt).multiply(s.Attitude)
	s.Attitude = axis(forward, clamp(in.Roll, -2, 2)*rate_roll*dt).multiply(s.Attitude)
	s.Attitude = s.Attitude.normalize()
	forward = s.Attitude.Rotate(Vec3{1, 0, 0})
	up = s.Attitude.Rotate(Vec3{0, 1, 0})
	right = s.Attitude.Rotate(Vec3{0, 0, 1})

	// Coordinated turn: bank angle drives a level-turn heading change.
	bank := math.Atan2(-right.Y, math.Max(0.25, up.Y))
	omega := clamp(9.81*math.Tan(clamp(bank, -1.3, 1.3))/math.Max(s.Speed, 70), -0.7, 0.7)
	s.Attitude = axis(Vec3{0, 1, 0}, -omega*dt).multiply(s.Attitude).normalize()
	forward = s.Attitude.Rotate(Vec3{1, 0, 0})

	// Speed: approach the throttle target, bleed for climb, clamp.
	target := 70 + clamp(in.Throttle, 0, 1)*290 - clamp(in.Speedbrake, 0, 1)*90
	pitch := math.Asin(clamp(forward.Y, -1, 1))
	s.Speed += (target - s.Speed) * math.Min(1, dt*0.5)
	s.Speed -= 9.81 * math.Sin(pitch) * dt * 1.6
	s.Speed = clamp(s.Speed, 0, 360)

	// Velocity tracks the nose.
	s.Direction = s.Direction.lerp(forward, math.Min(1, dt*2.5)).normalize()
	s.Position = s.Position.add(s.Direction.scale(s.Speed * dt))
	s.Position = wrap(s.Position, env.Wrap)
	s.Time += dt
	return s
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

// wrap applies the toroidal world inside the integrator so every
// implementation wraps identically (plan doc §7).
func wrap(p Vec3, size float64) Vec3 {
	if size <= 0 {
		return p
	}
	half := size / 2
	for _, axis := range []int{0, 2} {
		v := p.component(axis)
		for v > half {
			v -= size
		}
		for v < -half {
			v += size
		}
		p = p.replace(axis, v)
	}
	return p
}

// Shortest returns the minimum-image difference b-a along one axis.
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

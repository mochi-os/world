// Mochi world: Battle gunnery
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"

	"world/games/furball/flight"
)

const (
	reach      = 1500  // m, useful gun range (tracer-burnout class)
	dispersion = 0.003 // rad, one-sigma round scatter (M61 spec: 80% inside 8 mil)
)

// Pose is the shooter's muzzle state in world coordinates.
type Pose struct {
	Position flight.Vec3 // muzzle
	Forward  flight.Vec3 // bore line, unit
	Up       flight.Vec3 // unit, for the dispersion basis
	Right    flight.Vec3 // unit
}

// Burst fires rounds from the shooter at a target body posed at position
// with attitude, and applies every hit. Wrap is the toroidal world size.
// Returns the hit count and the events the strikes raised.
func Burst(shooter Pose, position flight.Vec3, attitude flight.Quat, body *Body, rounds int, wrap float64, seed uint64, slot uint64, tick uint64) (int, []Event) {
	// Target-relative muzzle, wrap-aware, rotated into the target's body frame.
	relative := flight.Vec3{
		X: flight.Shortest(position.X, shooter.Position.X, wrap),
		Y: shooter.Position.Y - position.Y,
		Z: flight.Shortest(position.Z, shooter.Position.Z, wrap),
	}
	origin := attitude.Unrotate(relative)
	hits := 0
	var events []Event
	for r := 0; r < rounds; r++ {
		round := uint64(r)
		// Gaussian dispersion via Box-Muller on the deterministic hash.
		radius := dispersion * math.Sqrt(-2*math.Log(math.Max(roll(seed, slot, tick, round, 20), 1e-12)))
		angle := 2 * math.Pi * roll(seed, slot, tick, round, 21)
		direction := shooter.Forward.
			Add(shooter.Right.Scale(radius * math.Cos(angle))).
			Add(shooter.Up.Scale(radius * math.Sin(angle)))
		direction = attitude.Unrotate(direction).Normalize()
		part, _ := trace(body.Parts, origin, direction, reach)
		if part < 0 {
			continue
		}
		hits++
		events = append(events, strike(body, &body.Parts[part], 1, seed, slot, tick, round)...)
	}
	if hits > 0 {
		events = append(events, Event{Kind: "hit", Engine: -1, Surface: -1, Count: hits})
	}
	return hits, events
}

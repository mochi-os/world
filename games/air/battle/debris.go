// Mochi world: Battle debris
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"

	"world/games/air/flight"
)

// A kill leaves its wreckage in the air: pieces thrown apart at tens of metres
// a second from where the jet broke up, carried on at its speed, slowing and
// falling. For a few seconds the cloud is dense enough that a jet flying
// through it takes hits, more the deeper and faster it goes; after that the
// pieces have spread too thin to matter. The cloud is one expanding sphere
// with a rate of strikes, not pieces tracked one by one, so the server and the
// single-player core decide it alike without a piece ever crossing the wire.
const (
	debris_life   = 5.0   // s the cloud can strike: the drawn chunks live 3.5-6.5 s
	debris_core   = 8.0   // m: its radius at the break-up, the airframe's own size
	debris_spread = 25.0  // m/s the pieces fly apart (the drawn chunks 12-42 m/s)
	debris_drag   = 0.6   // 1/s: how fast the pieces lose the wreck's speed
	debris_pieces = 40.0  // pieces heavy enough to wound
	debris_area   = 20.0  // m²: the airframe a piece can meet, across the path
	debris_speed  = 150.0 // m/s: the relative speed at which a piece strikes at gun severity
)

// Debris is a kill's cloud: where the jet broke up, its velocity then, and
// when, on the clock the cloud is met by.
type Debris struct {
	Origin   flight.Vec3
	Velocity flight.Vec3
	Time     float64
}

// Cloud is the debris at a time: its centre, its radius and the velocity its
// pieces share, and whether it can still strike.
func (d Debris) Cloud(time float64) (flight.Vec3, float64, flight.Vec3, bool) {
	age := time - d.Time
	if age < 0 || age > debris_life {
		return flight.Vec3{}, 0, flight.Vec3{}, false
	}
	carried := (1 - math.Exp(-debris_drag*age)) / debris_drag
	centre := d.Origin.Add(d.Velocity.Scale(carried))
	centre.Y -= 0.5 * 9.8 * age * age
	velocity := d.Velocity.Scale(math.Exp(-debris_drag * age))
	velocity.Y -= 9.8 * age
	return centre, debris_core + debris_spread*age, velocity, true
}

// Meet rolls the debris against one aircraft over a step of dt seconds ending
// at time: the chance of a strike is the pieces' density times the airframe
// they can meet times the distance flown through them, and a strike is one
// piece traced into the airframe along the relative motion, at a severity
// that goes with the square of that speed. Returns whether a piece struck,
// the events it raised, and where it landed in the body frame.
func (d Debris) Meet(time float64, dt float64, position flight.Vec3, velocity flight.Vec3, attitude flight.Quat, body *Body, wrap float64, seed uint64, slot uint64, tick uint64) (bool, []Event, []flight.Vec3) {
	centre, radius, moving, alive := d.Cloud(time)
	if !alive {
		return false, nil, nil
	}
	offset := flight.Vec3{
		X: flight.Shortest(centre.X, position.X, wrap),
		Y: position.Y - centre.Y,
		Z: flight.Shortest(centre.Z, position.Z, wrap),
	}
	if offset.Length() > radius {
		return false, nil, nil
	}
	relative := velocity.Subtract(moving)
	speed := relative.Length()
	if speed < 1 {
		return false, nil, nil
	}
	density := debris_pieces / (4.0 / 3.0 * math.Pi * radius * radius * radius)
	if roll(seed, slot, tick, 40) >= density*debris_area*speed*dt {
		return false, nil, nil
	}
	// The piece comes at the airframe against its relative motion, somewhere
	// across the disc the airframe presents to it.
	toward := attitude.Unrotate(relative.Scale(-1 / speed))
	extent := Extent(body.Parts)
	across := scatter(toward, (roll(seed, slot, tick, 41)-0.5)*0.8, (roll(seed, slot, tick, 42)-0.5)*0.8)
	origin := across.Scale(-(extent + 1))
	part, along := trace(body.Parts, origin, across, 2*extent+2)
	if part < 0 {
		return false, nil, nil // it passed between the tails and the wing
	}
	point := origin.Add(across.Scale(along))
	severity := clamp((speed/debris_speed)*(speed/debris_speed), 0.3, 2)
	return true, strike(body, &body.Parts[part], severity, false, seed, slot, tick, 300), []flight.Vec3{point}
}

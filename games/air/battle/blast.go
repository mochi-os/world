// Mochi world: Battle warhead
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"

	"world/games/air/flight"
)

const (
	lethal    = 5.5  // m, inside this the annular blast is a structural kill
	fringe    = 12.0 // m, fragment envelope
	fragments = 10   // fragment rays thrown across the fringe
)

// Warhead classes scale the radii above, which are the AIM-9M's 9.4 kg annular
// blast. `lethal` is a CERTAINTY radius, not the published ~10 m probability
// contour; the fragment band supplies the rest. Radar is the 22 kg charge
// scaling.
const (
	Heater = 1.0 // AIM-9M, 9.4 kg — the reference, 5.5 m lethal / 12 m fringe
	Radar  = 1.4 // AIM-120C, 22 kg directed fragmentation — 7.7 m lethal / 16.8 m fringe
)

// Blast detonates an AIM-9M-class warhead at a world point against a target
// body. Returns whether the blast was an outright structural kill, and the
// events the fragment strikes raised.
func Blast(point flight.Vec3, position flight.Vec3, attitude flight.Quat, body *Body, wrap float64, seed uint64, slot uint64, tick uint64) (bool, []Event) {
	return Warhead(Heater, point, position, attitude, body, wrap, seed, slot, tick)
}

// Warhead is Blast with an explicit charge class (Heater, Radar): the radii
// scale with the cube root of the charge mass.
func Warhead(class float64, point flight.Vec3, position flight.Vec3, attitude flight.Quat, body *Body, wrap float64, seed uint64, slot uint64, tick uint64) (bool, []Event) {
	lethal, fringe := lethal*class, fringe*class
	relative := flight.Vec3{
		X: flight.Shortest(position.X, point.X, wrap),
		Y: point.Y - position.Y,
		Z: flight.Shortest(position.Z, point.Z, wrap),
	}
	burst := attitude.Unrotate(relative)
	miss := burst.Length()
	if miss < lethal {
		return true, []Event{{Kind: "explode", Engine: -1, Surface: -1}}
	}
	if miss > fringe {
		return false, nil
	}
	// Fragment rays from the burst point, deterministically scattered toward
	// the airframe, each striking at twice gun severity.
	var events []Event
	toward := burst.Scale(-1).Normalize()
	for f := 0; f < fragments; f++ {
		ray := uint64(f)
		pitch := (roll(seed, slot, tick, ray, 30) - 0.5) * 1.2
		yaw := (roll(seed, slot, tick, ray, 31) - 0.5) * 1.2
		direction := scatter(toward, pitch, yaw)
		part, _ := trace(body.Parts, burst, direction, fringe*2)
		if part < 0 {
			continue
		}
		events = append(events, strike(body, &body.Parts[part], 2, false, seed, slot, tick, ray+100)...)
	}
	return false, events
}

// scatter tilts a unit direction by small pitch/yaw angles using any
// stable perpendicular basis.
func scatter(direction flight.Vec3, pitch float64, yaw float64) flight.Vec3 {
	reference := flight.Vec3{Y: 1}
	if math.Abs(direction.Y) > 0.9 {
		reference = flight.Vec3{X: 1}
	}
	right := direction.Cross(reference).Normalize()
	up := right.Cross(direction)
	return direction.Add(right.Scale(yaw)).Add(up.Scale(pitch)).Normalize()
}

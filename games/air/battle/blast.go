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
func Blast(point flight.Vec3, closure float64, position flight.Vec3, attitude flight.Quat, body *Body, wrap float64, seed uint64, slot uint64, tick uint64) (bool, []Event, []flight.Vec3) {
	return Warhead(Heater, point, closure, position, attitude, body, wrap, seed, slot, tick)
}

// Warhead is Blast with an explicit charge class (Heater, Radar): the radii
// scale with the cube root of the charge mass. The final return is where the
// fragments landed, in the target's body frame — the visible evidence of a
// connecting burst, as against a fireball the target flies through unmarked.
func Warhead(class float64, point flight.Vec3, closure float64, position flight.Vec3, attitude flight.Quat, body *Body, wrap float64, seed uint64, slot uint64, tick uint64) (bool, []Event, []flight.Vec3) {
	lethal, fringe := lethal*class, fringe*class
	// Closure scales the fragments, not the blast (#57): they leave the case
	// at ~2 km/s and the engagement's relative speed adds head-on, subtracts
	// astern — energy goes with the square of the ARRIVAL speed, in which the
	// case velocity dominates and the closure modulates. Anchored at 650 m/s,
	// the measured chase fusing, so everything tuned before this keeps its
	// lethality there; a 1,200 m/s head-on fusing lands ~1.5x the punch and a
	// slow overtake on a fleeing target ~0.75x. (A first cut scaled by the
	// closure ratio alone, which halved slow-chase fusings and left a duck
	// alive through six of them — the case velocity is most of the energy.)
	sway := 1.0
	if closure > 0 {
		arrival := (2000 + closure) / 2650
		sway = clamp(arrival*arrival, 0.6, 2.2)
	}
	relative := flight.Vec3{
		X: flight.Shortest(position.X, point.X, wrap),
		Y: point.Y - position.Y,
		Z: flight.Shortest(position.Z, point.Z, wrap),
	}
	burst := attitude.Unrotate(relative)
	miss := burst.Length()
	if miss < lethal {
		return true, []Event{{Kind: "explode", Engine: -1, Surface: -1}}, []flight.Vec3{burst}
	}
	if miss > fringe {
		return false, nil, nil
	}
	// Fragment rays from the burst point, deterministically scattered toward
	// the airframe, each striking at twice gun severity. A body station is
	// skin over mostly empty volume, so a fragment that meets one takes its
	// transit toll there and carries on to whatever the station shadows — the
	// tail-cone capsule must not armour the engines and tails behind it (#78:
	// a 7.8 m dead-astern fusing, the stern chase's best aim, wounded nothing
	// while a burst 1 m off the axis wounded heavily).
	var events []Event
	var hits []flight.Vec3
	toward := burst.Scale(-1).Normalize()
	for f := 0; f < fragments; f++ {
		ray := uint64(f)
		pitch := (roll(seed, slot, tick, ray, 30) - 0.5) * 1.2
		yaw := (roll(seed, slot, tick, ray, 31) - 0.5) * 1.2
		direction := scatter(toward, pitch, yaw)
		origin, budget := burst, fringe*2
		var mark *flight.Vec3
		for hop := 0; hop < 4 && budget > 0; hop++ {
			part, along := trace(body.Parts, origin, direction, budget)
			if part < 0 {
				break
			}
			p := &body.Parts[part]
			point := origin.Add(direction.Scale(along))
			if p.Kind == Fuselage {
				if mark == nil { // the toll is paid once: one hole in, and the mark stays here if nothing lies beyond
					mark = &point
					events = append(events, strike(body, p, 2*sway, false, seed, slot, tick, ray+100)...)
				}
				step := along + p.Radius*2.2
				origin = origin.Add(direction.Scale(step))
				budget -= step
				continue
			}
			mark = &point
			events = append(events, strike(body, p, 2*sway, false, seed, slot, tick, ray+200)...)
			break
		}
		if mark != nil { // one visible mark per fragment — the wasm bridge carries at most `fragments` impact points
			hits = append(hits, *mark)
		}
	}
	return false, events, hits
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

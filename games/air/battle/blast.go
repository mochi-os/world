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

// Warhead classes: the radii above are the AIM-9M's 9.4 kg annular blast.
// Callers pass the class; Blast keeps the 9M as its default so every
// existing call is unchanged.
//
// This radius is a CERTAINTY, not the published figure. The ~10 m credited
// to a 9M-class round is a probability contour — the distance within which
// a fighter-sized target is likely to take mission-killing damage — while
// `lethal` here means destroyed outright with no roll. Blast overpressure
// alone only breaks an airframe within a very few metres of a 9 kg
// warhead; past that it is fragments, and whether a fragment finds a spar,
// a tank, an engine or just skin is chance. So the certainty radius sits
// where destruction really is near-certain (5.5 m), and the frequent,
// VISIBLE explosions come from the stochastic paths in the fragment band
// instead — a splinter into the vapour space or a warhead on the rail.
// That is also how real aircraft blow up at six to ten metres, and it is
// what leaves the graded middle the fringe tests guard: wounded and
// flying, sometimes burning down, occasionally coming apart.
//
// Radar is the AIM-120's 22 kg blast-fragmentation warhead, a DIRECTED
// design built for exactly this endgame — a high-closure pass where the
// round has metres, not a stern chase where it has seconds. Its class is
// now simply the charge scaling: the cube root of 22 over 9.4 is 1.33, and
// 1.4 allows a little for the directed pattern. That is the physically
// honest number, and it becomes usable only once the base radius means
// what it says — the class used to be 2.0 because the multiplier was
// carrying the heater's conservatism, which inflated the fragment envelope
// to 24 m as a side effect. A well-flown BVR shot stays lethal because our
// terminal geometry passes a defending fighter at 6-10 m: inside the
// fragment band, where the stochastic paths live even when the burst is
// outside the certainty radius.
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

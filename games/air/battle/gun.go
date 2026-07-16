// Mochi world: Battle gunnery
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
	reach       = 1500  // m, useful gun range (tracer-burnout class)
	dispersion  = 0.003 // rad, one-sigma round scatter (M61 spec: 80% inside 8 mil)
	penetration = 0.45  // severity retained through each part pierced (#144)
	through     = 3     // parts one round can reach
	spent       = 0.15  // severity below which the round has nothing left
)

// Muzzle is the M61 barrel speed, exported for the fire-control solutions
// (the bot's lead point, the HUD's director pipper) that must match the
// gunnery here.
const Muzzle = 1050.0 // m/s

// Pose is the shooter's muzzle state in world coordinates.
type Pose struct {
	Position flight.Vec3 // muzzle
	Forward  flight.Vec3 // bore line, unit
	Up       flight.Vec3 // unit, for the dispersion basis
	Right    flight.Vec3 // unit
	Velocity flight.Vec3 // the shooter's velocity rides on every round
}

// Burst fires rounds from the shooter at a target body posed at position
// with attitude, moving at velocity, and applies every hit. Wrap is the
// toroidal world size. Returns the hit count and the events raised.
//
// Rounds fly real time of flight: each one inherits the shooter's velocity,
// the target's velocity carries it away across the flight, and gravity pulls
// the round (not the lift-borne target) — so the correct bore is the LED one,
// and pipper-on-target only kills when the pipper computes the same lead.
// (The judgment is a straight ray in the target-relative frame with the mean
// gravity kick folded in — exact enough over gun ranges.)
func Burst(shooter Pose, position flight.Vec3, attitude flight.Quat, velocity flight.Vec3, body *Body, rounds int, wrap float64, seed uint64, slot uint64, tick uint64) (int, []Event) {
	// Target-relative muzzle, wrap-aware, rotated into the target's body frame.
	relative := flight.Vec3{
		X: flight.Shortest(position.X, shooter.Position.X, wrap),
		Y: shooter.Position.Y - position.Y,
		Z: flight.Shortest(position.Z, shooter.Position.Z, wrap),
	}
	origin := attitude.Unrotate(relative)
	// One flight-time solution per burst: the bore round's target-relative
	// velocity closes the range; gravity's mean kick over that flight bends
	// every round's relative path the same way.
	flat := shooter.Forward.Scale(Muzzle).Add(shooter.Velocity).Subtract(velocity)
	time := relative.Length() / math.Max(flat.Length(), 1)
	kick := flight.Vec3{Y: -0.5 * 9.8 * time}
	hits := 0
	var events []Event
	for r := 0; r < rounds; r++ {
		round := uint64(r)
		// Gaussian dispersion via Box-Muller on the deterministic hash.
		radius := dispersion * math.Sqrt(-2*math.Log(math.Max(roll(seed, slot, tick, round, 20), 1e-12)))
		angle := 2 * math.Pi * roll(seed, slot, tick, round, 21)
		bore := shooter.Forward.
			Add(shooter.Right.Scale(radius * math.Cos(angle))).
			Add(shooter.Up.Scale(radius * math.Sin(angle)))
		direction := bore.Scale(Muzzle).Add(shooter.Velocity).Subtract(velocity).Add(kick)
		direction = attitude.Unrotate(direction).Normalize()
		chain := pierce(body.Parts, origin, direction, reach)
		if len(chain) == 0 {
			continue
		}
		hits++
		// Penetration: SAPHEI keeps killing behind the first thing it meets —
		// severity decays per part, and the round word is spread by depth so
		// the chain's rolls stay independent.
		severity := 1.0
		for depth, part := range chain {
			if depth >= through || severity < spent {
				break
			}
			events = append(events, strike(body, &body.Parts[part], severity, seed, slot, tick, round*uint64(through)+uint64(depth))...)
			severity *= penetration
		}
	}
	if hits > 0 {
		events = append(events, Event{Kind: "hit", Engine: -1, Surface: -1, Count: hits})
	}
	return hits, events
}

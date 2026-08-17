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
	through     = 3     // parts one round can reach at full striking speed
	spent       = 0.15  // severity below which the round has nothing left
	// A 20 mm round is a SHELL, not a slug: the M56 and PGU-28 this model is
	// built around are high-explosive incendiary with a point-detonating fuze,
	// so the burst does not care how fast the shell arrived. What the striking
	// speed buys is DEPTH — a fast shell punches through the skin and functions
	// against a spar or a fuel cell, a slow one bursts on the surface.
	striking = 700.0 // m/s at which a shell still reaches everything it would at the muzzle
	graze    = 250.0 // and below this it functions on the skin and goes no further
)

// Muzzle is the M61 barrel speed, exported for the fire-control solutions
// (the bot's lead point, the HUD's director pipper) that must match the
// gunnery here.
const Muzzle = 1050.0 // m/s

// Length is the round's drag length at sea level: quadratic drag makes the
// speed decay exponentially with DISTANCE, v(x) = v0*exp(-x/Length), which
// fits the published M56/PGU-28 tables (about 1,050 m/s at the barrel, about
// 700 m/s and 1.16 s at a thousand metres). The length stretches with
// altitude as the air thins — a high fight genuinely shoots further, which
// is real and which the no-drag model hid by shooting infinitely far
// everywhere.
const Length = 2600.0 // m

// stretched is the drag length at an altitude, on an 8.5 km density scale.
func stretched(altitude float64) float64 {
	return Length * math.Exp(math.Max(altitude, 0)/8500)
}

// Average is the round's mean speed over a flight of span metres starting at
// an altitude — the number every fire-control solution divides range by. It
// tends to Muzzle as the span shortens.
func Average(span, altitude float64) float64 {
	if span < 1 {
		return Muzzle
	}
	l := stretched(altitude)
	return span * Muzzle / (l * (math.Expm1(span / l)))
}

// Pose is the shooter's muzzle state in world coordinates.
type Pose struct {
	Position flight.Vec3 // muzzle
	Forward  flight.Vec3 // bore line, unit
	Up       flight.Vec3 // unit, for the dispersion basis
	Right    flight.Vec3 // unit
	Velocity flight.Vec3 // the shooter's velocity rides on every round
}

// ImpactPoints caps how many per-round strike positions one report carries.
const ImpactPoints = 8

// Round is one 20 mm round in flight: real position, real velocity, no
// knowledge of any target. Rounds RESOLVE ON ARRIVAL against wherever the
// target actually is by then — the old Burst judged the whole flight in the
// target-relative frame at the trigger, which auto-led every shooter and made
// manoeuvre-during-flight (the real last-ditch guns defence) impossible.
type Round struct {
	Position flight.Vec3
	Velocity flight.Vec3
	Shooter  int    // slot identity, for kill credit
	Born     uint64 // tick fired: keys the damage rolls
	Index    uint64 // position within its volley: keys the damage rolls
	Age      float64
}

// Depth is how many parts a shell reaches, from its striking speed. One is
// always guaranteed: a high-explosive round that arrives at all functions on
// what it touches. Beyond that it is momentum that carries the shell inward.
func Depth(speed float64) int {
	if speed >= striking {
		return through
	}
	if speed <= graze {
		return 1
	}
	reach := 1 + int(float64(through-1)*(speed-graze)/(striking-graze)+0.5)
	if reach > through {
		reach = through
	}
	return reach
}

// Life is how long a round stays dangerous, seconds. Raised from 2 s to 4 on
// 2026-08-17: the shell's damage does not decay with speed, so cutting it off
// while it was still doing 650 m/s was an arbitrary wall a pilot could feel —
// he could see the pipper on a target his rounds silently could not reach. Four
// seconds carries it past 2.5 km, beyond any range where the lead solution is
// worth taking, and the depth model above is what makes a long shot weak rather
// than the round vanishing.
const Life = 4.0

// Volley spawns one burst's rounds from the shooter with the same
// deterministic Gaussian dispersion the instant model rolled.
func Volley(shooter Pose, rounds int, seed uint64, slot uint64, tick uint64) []Round {
	born := make([]Round, 0, rounds)
	for r := 0; r < rounds; r++ {
		round := uint64(r)
		radius := dispersion * math.Sqrt(-2*math.Log(math.Max(roll(seed, slot, tick, round, 20), 1e-12)))
		angle := 2 * math.Pi * roll(seed, slot, tick, round, 21)
		bore := shooter.Forward.
			Add(shooter.Right.Scale(radius * math.Cos(angle))).
			Add(shooter.Up.Scale(radius * math.Sin(angle)))
		born = append(born, Round{
			Position: shooter.Position,
			Velocity: bore.Scale(Muzzle).Add(shooter.Velocity),
			Shooter:  int(slot),
			Born:     tick,
			Index:    round,
		})
	}
	return born
}

// Fly advances a round one step of dt: ballistic, gravity only (20 mm drag
// over these ranges is a second-order correction the fire control never
// modelled either). Returns true once the round is spent.
func Fly(r *Round, dt float64) bool {
	// Quadratic drag on the whole vector: the round flies through the air
	// mass, and the air does not care which part of the speed was barrel
	// and which was inherited from the shooter.
	if speed := r.Velocity.Length(); speed > 1 {
		r.Velocity = r.Velocity.Scale(1 / (1 + speed*dt/stretched(r.Position.Y)))
	}
	r.Velocity.Y -= 9.8 * dt
	r.Position = r.Position.Add(r.Velocity.Scale(dt))
	r.Age += dt
	return r.Age > Life || r.Position.Y < 0
}

// Strike tests one round's NEXT step of dt against a body posed at position
// with attitude, moving at velocity — the segment is judged in the target's
// frame at THIS tick, which is what makes a jink after the trigger a real
// defence. On a hit the damage cascade applies exactly as the instant model's
// did, and the round is spent. The impact point is in the target's body frame.
func Strike(r *Round, position flight.Vec3, attitude flight.Quat, velocity flight.Vec3, body *Body, dt float64, wrap float64, seed uint64) (bool, []Event, flight.Vec3) {
	relative := flight.Vec3{
		X: flight.Shortest(position.X, r.Position.X, wrap),
		Y: r.Position.Y - position.Y,
		Z: flight.Shortest(position.Z, r.Position.Z, wrap),
	}
	origin := attitude.Unrotate(relative)
	step := r.Velocity.Subtract(velocity).Scale(dt)
	span := step.Length()
	if span < 1e-9 {
		return false, nil, flight.Vec3{}
	}
	direction := attitude.Unrotate(step.Scale(1 / span))
	chain, along := pierce(body.Parts, origin, direction, span)
	if len(chain) == 0 {
		return false, nil, flight.Vec3{}
	}
	impact := origin.Add(direction.Scale(along[0]))
	var events []Event
	severity := 1.0
	// The fuze functions whatever the speed, so the first part always takes the
	// full burst; the shell's remaining momentum decides how much further in it
	// gets. Scaling SEVERITY with impact energy instead would have modelled a
	// solid shot, which is not what this gun fires.
	depth := Depth(r.Velocity.Subtract(velocity).Length())
	for reached, part := range chain {
		if reached >= depth || severity < spent {
			break
		}
		events = append(events, strike(body, &body.Parts[part], severity, true, seed, uint64(r.Shooter), r.Born, r.Index*uint64(through)+uint64(reached))...)
		severity *= penetration
	}
	events = append(events, Event{Kind: "hit", Engine: -1, Surface: -1, Count: 1})
	return true, events, impact
}

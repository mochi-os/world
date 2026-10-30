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
	// A 20 mm round is a SHELL, not a slug: the burst does not care how fast it
	// arrived. What the striking speed buys is DEPTH.
	striking = 700.0 // m/s at which a shell still reaches everything it would at the muzzle
	graze    = 250.0 // and below this it functions on the skin and goes no further
)

// Muzzle is the M61 barrel speed, exported for the fire-control solutions
// (the bot's lead point, the HUD's director pipper) that must match the
// gunnery here.
const Muzzle = 1050.0 // m/s

// Length is the round's drag length at sea level: v(x) = v0*exp(-x/Length),
// fitted to the published M56/PGU-28 tables (1,050 m/s at the barrel, 700 m/s
// at a thousand metres). It stretches with altitude as the air thins.
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

// Pose is the shooter's muzzle state in the host's frame.
type Pose struct {
	Position flight.Vec3 // muzzle
	Forward  flight.Vec3 // bore line, unit
	Up       flight.Vec3 // unit, for the dispersion basis
	Right    flight.Vec3 // unit
	Velocity flight.Vec3 // the shooter's velocity rides on every round
	Wind     flight.Vec3 // the air's velocity at the muzzle, which the volley flies through
	Level    flight.Vec3 // the level up at the muzzle, which the volley falls along (zero: the frame's own y)
	Altitude float64     // the muzzle's height above the sea
}

// ImpactPoints caps how many per-round strike positions one report carries.
const ImpactPoints = 8

// Round is one 20 mm round in flight: real position, real velocity, no
// knowledge of any target. Rounds RESOLVE ON ARRIVAL against wherever the
// target actually is by then, so manoeuvre during flight is a real defence.
type Round struct {
	Position flight.Vec3
	Velocity flight.Vec3
	Shooter  int    // slot identity, for kill credit
	Born     uint64 // tick fired: keys the damage rolls
	Index    uint64 // position within its volley: keys the damage rolls
	Age      float64
	Wind     flight.Vec3 // the air it flies through, taken at the muzzle and every Sampled seconds after (Feel)
	Felt     float64     // its age when it last took the wind
	Up       flight.Vec3 // the level up at the muzzle: a round's whole flight lies within a few kilometres of it, where the sea's curve departs from that level by under a metre (zero: the frame's own y)
	Sea      float64     // the sea along Up: a round's height is Position·Up less this
}

// level is the up a round falls along and the sea's place along it.
func (r *Round) level() (flight.Vec3, float64) {
	if r.Up == (flight.Vec3{}) {
		return flight.Vec3{Y: 1}, 0
	}
	return r.Up, r.Sea
}

// Height is the round's height above the sea.
func (r *Round) Height() float64 {
	up, sea := r.level()
	return r.Position.Dot(up) - sea
}

// Carry moves the round into another frame: m is the motion there.
func (r *Round) Carry(m flight.Rigid) {
	height := r.Height()
	up, _ := r.level()
	r.Position, r.Velocity, r.Wind, r.Up = m.Point(r.Position), m.Vector(r.Velocity), m.Vector(r.Wind), m.Vector(up)
	r.Sea = r.Position.Dot(r.Up) - height
}

// Sampled is how often a round in flight takes the wind where it is. The wind
// grows and veers with height and wanders across the map: a round fired
// steeply crosses air moving over ten metres a second differently from the
// muzzle's, and the drag carries it metres off a round that kept the muzzle's
// air, within two kilometres. Taken every quarter second it stays within a few
// decimetres of one that takes the wind every step, at a sixtieth of the cost
// when tens of thousands of rounds are in the air.
const Sampled = 0.25 // s

// Feel takes the wind where the round is, once Sampled seconds have passed
// since it last did - at the muzzle, when it was fired.
func (r *Round) Feel(environment flight.Environment, clock float64) {
	if r.Age-r.Felt >= Sampled {
		r.Wind, r.Felt = flight.Wind(r.Position, clock, environment), r.Age
	}
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

// Life is how long a round stays dangerous, seconds. Damage does not decay with
// speed, so this is a cost cap, not a lethality one: four seconds carries it
// past 2.5 km, beyond any worthwhile lead solution.
const Life = 4.0

// Volley spawns one burst's rounds from the shooter with the same
// deterministic Gaussian dispersion the instant model rolled.
func Volley(shooter Pose, rounds int, seed uint64, slot uint64, tick uint64) []Round {
	born := make([]Round, 0, rounds)
	up, sea := shooter.Level, 0.0
	if up != (flight.Vec3{}) {
		sea = shooter.Position.Dot(up) - shooter.Altitude
	}
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
			Wind:     shooter.Wind,
			Up:       up,
			Sea:      sea,
		})
	}
	return born
}

// Graze is how far outside the skin a round still counts as a near miss. The
// gate is a broad phase, not a judgement: anything further out than this is
// not worth the narrow-phase sweep, and a stream this wide is already a miss
// no pilot needs the metres for.
const Graze = 60.0

// Near is how close a round that did NOT strike came, and where. It returns
// the gap from the airframe's SKIN in metres (a part's radius is subtracted,
// so zero is a graze), the point of closest approach in the TARGET's body
// frame, and whether the round came near enough to measure at all.
//
// This exists because Strike returns nothing whatever on a miss. A burst that
// connects is fully described by its hit count; a burst that misses — the one
// a pilot actually needs to learn from — was silent, and the debrief could
// only guess at it from the recorded tracks, against the body ORIGIN rather
// than the structure. Measuring it here is exact, because this is the same
// sweep and the same capsules that decide a hit.
//
// It runs on the hot path for every missing round, so the bounding sphere
// gates it before any per-part work: the step is one segment and the body is
// one sphere, which is a dot product and a length against a few dozen capsule
// sweeps.
func Near(r *Round, position flight.Vec3, attitude flight.Quat, velocity flight.Vec3, body *Body, dt float64) (float64, flight.Vec3, bool) {
	origin := attitude.Unrotate(r.Position.Subtract(position))
	step := r.Velocity.Subtract(velocity).Scale(dt)
	span := step.Length()
	if span < 1e-9 {
		return 0, flight.Vec3{}, false
	}
	direction := attitude.Unrotate(step.Scale(1 / span))
	// Broad phase: the closest the step comes to the body's own origin. The
	// body frame puts that origin at zero, so this is the distance from a
	// segment to a point.
	along := clamp(-origin.Dot(direction), 0, span)
	if origin.Add(direction.Scale(along)).Length() > Extent(body.Parts)+Graze {
		return 0, flight.Vec3{}, false
	}
	finish := origin.Add(direction.Scale(span))
	best, at, found := math.Inf(1), flight.Vec3{}, false
	for pi := range body.Parts {
		part := &body.Parts[pi]
		if !carried(part, body.Stores) {
			continue
		}
		gap, fraction := nearest(origin, finish, part.A, part.B)
		gap -= part.Radius
		if gap < best {
			best, at, found = gap, origin.Add(direction.Scale(fraction*span)), true
		}
	}
	if !found || best > Graze {
		return 0, flight.Vec3{}, false
	}
	if best < 0 {
		best = 0 // inside a capsule's radius without Strike calling it: a graze, not a negative distance
	}
	return best, at, true
}

// Fly advances a round one step of dt: drag and gravity, which pulls it down
// the level up where it was fired. Returns true once the round is spent.
func Fly(r *Round, dt float64) bool {
	// Quadratic drag on the whole velocity through the air: the round flies
	// through the air mass, and the air does not care which part of the speed
	// was barrel and which was inherited from the shooter. The wind is the
	// air's own motion, so the round slows toward it, not toward the sea.
	up, sea := r.level()
	through := r.Velocity.Subtract(r.Wind)
	if speed := through.Length(); speed > 1 {
		r.Velocity = r.Wind.Add(through.Scale(1 / (1 + speed*dt/stretched(r.Position.Dot(up)-sea))))
	}
	r.Velocity = r.Velocity.Subtract(up.Scale(9.8 * dt))
	r.Position = r.Position.Add(r.Velocity.Scale(dt))
	r.Age += dt
	return r.Age > Life || r.Position.Dot(up) < sea
}

// Strike tests one round's next step of dt against a body posed at position
// with attitude - judged in the target's frame at THIS tick, which is what
// makes a jink after the trigger a real defence. The impact point is in the
// body frame.
func Strike(r *Round, position flight.Vec3, attitude flight.Quat, velocity flight.Vec3, body *Body, dt float64, seed uint64) (bool, []Event, flight.Vec3) {
	origin := attitude.Unrotate(r.Position.Subtract(position))
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

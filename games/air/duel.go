// Mochi world: The duel arbiter — manoeuvre selection by forward simulation
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A teamless bot does not pick its manoeuvre from a ladder of hand-tuned
// gates. It REHEARSES: each candidate manoeuvre is flown forward through the
// real flight model against the opponent's estimated turn circle, the outcome
// geometry is scored, and the winner is committed until its horizon expires.
// Offence and defence are the same decision — a break turn and a reversal
// into attack are just candidates, so the moment an attacker overshoots, the
// attacking line starts outscoring the defensive one with no hand-written
// bridge between "modes". Skill shapes the machinery, not the outcome list:
// the library gates which candidates exist, wander adds selection noise, and
// commitment sets how long a chosen line is flown before re-judging.

package air

import (
	"math"

	"world/games/air/battle"
	"world/games/air/flight"
)

// moment is the geometry one candidate law reads: my live (or simulated)
// state against the opponent's predicted position and velocity.
type moment struct {
	me        *flight.State
	prey      flight.Vec3 // his predicted position
	velocity  flight.Vec3 // his predicted velocity
	ring      orbit       // his estimated turning circle
	pace      float64     // my corner speed
	pull      float64     // the skill's g ceiling
	direction flight.Vec3 // unit, me toward him
	distance  float64
	closure   float64
}

// derive fills the dependent fields from the endpoints.
func (m *moment) derive() {
	los := m.prey.Subtract(m.me.Position)
	m.distance = math.Max(los.Length(), 1)
	m.direction = los.Scale(1 / m.distance)
	m.closure = m.me.Velocity.Subtract(m.velocity).Dot(m.direction)
}

// flat returns my normalized horizontal flight path.
func (m *moment) flat() flight.Vec3 {
	f := flight.Vec3{X: m.me.Velocity.X, Z: m.me.Velocity.Z}
	if f.Length() < 1 {
		n := m.me.Attitude.Rotate(flight.Vec3{X: 1})
		f = flight.Vec3{X: n.X, Z: n.Z}
	}
	return f.Normalize()
}

// swing rotates my horizontal flight path by an angle (radians, + toward his
// side when signed by the caller) and lifts it just off the horizon.
func (m *moment) swing(angle float64) flight.Vec3 {
	f := m.flat()
	sin, cos := math.Sin(angle), math.Cos(angle)
	return flight.Vec3{X: f.X*cos - f.Z*sin, Y: 0.05, Z: f.X*sin + f.Z*cos}.Normalize()
}

// lead is the ballistic intercept point the gunnery flies: his position at
// bullet arrival, in the shooter's accelerating frame, with gravity drop.
func (m *moment) lead() flight.Vec3 {
	transit := m.distance / math.Max(battle.Muzzle+m.closure, 200)
	return m.prey.Add(m.velocity.Scale(transit)).
		Subtract(m.me.Velocity.Scale(transit)).
		Add(flight.Vec3{Y: 4.9 * transit * transit})
}

// lag is the control point behind him on his own circle — flight-path lag
// when the circle is not readable.
func (m *moment) lag() flight.Vec3 {
	if m.ring.valid {
		return m.ring.behind(m.prey, 0.85)
	}
	back := m.velocity
	if back.Length() > 1 {
		back = back.Normalize()
	}
	return m.prey.Subtract(back.Scale(math.Max(250, m.distance*0.3)))
}

// side reports which side of my flight path he sits: +1 right, -1 left.
func (m *moment) side() float64 {
	v := m.me.Velocity
	if v.Length() < 1 {
		v = m.me.Attitude.Rotate(flight.Vec3{X: 1})
	}
	cross := v.Cross(m.direction)
	if cross.Y >= 0 {
		return -1
	}
	return 1
}

// aloft aims from my position toward a world point.
func (m *moment) aloft(point flight.Vec3) flight.Vec3 {
	to := point.Subtract(m.me.Position)
	if to.Length() < 1 {
		return m.direction
	}
	return to.Normalize()
}

// order is one candidate command set.
type order struct {
	aim      flight.Vec3
	g        float64
	throttle float64
	reheat   float64
	brake    float64
}

// play is one candidate manoeuvre: a closed-loop control law, recomputed from
// fresh geometry at every decision while committed — the rollout flies the
// same law it judges.
type play struct {
	name string
	tier int // library tier that unlocks it
	law  func(m *moment) order
}

// plays is the candidate library, in fixed order (map iteration would
// re-litigate #225). Tiers: a novice owns pursuit, the breaks, and running
// away; the circle work, the vertical, and the reversal arrive with skill.
var plays = []play{
	{"press", 1, func(m *moment) order {
		o := order{aim: m.direction, g: m.pull, throttle: 1, reheat: 1}
		speed := m.me.Velocity.Length()
		if m.distance < 1200 {
			o.aim = m.aloft(m.lead()) // in range: fly the gun solution, not the jet
		}
		if m.distance < 900 {
			// Terminal closure discipline: park at the finishing gap with the
			// overtake spent — arriving hot blows through the saddle and turns
			// the kill into another run-in from a mile out.
			goal := clamp((m.distance-250)*0.15, 0, 45)
			o.throttle = clamp(0.7-(m.closure-goal)*0.006, 0.2, 1)
			o.reheat = 0
			if m.closure > goal+50 {
				o.brake = 1
			}
		} else if m.distance < 1500 {
			o.throttle = clamp(1-(m.closure-40)/200, 0.35, 1)
			o.reheat = 0
			if speed < m.pace-60 {
				o.reheat = 1
			}
		}
		return o
	}},
	{"left", 1, func(m *moment) order {
		return order{aim: m.swing(-1.6), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"right", 1, func(m *moment) order {
		return order{aim: m.swing(1.6), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"extend", 1, func(m *moment) order {
		away := level(m.direction.Scale(-1))
		return order{aim: away, g: 2.5, throttle: 1, reheat: 1}
	}},
	{"lag", 2, func(m *moment) order {
		o := order{aim: m.aloft(m.lag()), g: m.pull * 0.92, throttle: 1}
		if m.me.Velocity.Length() < m.pace {
			o.reheat = 1
		}
		return o
	}},
	{"low", 2, func(m *moment) order {
		point := m.prey // the gun-frame lead point is a phantom beyond gun range: it swings with my own velocity and the jet chases it in circles
		if m.distance < 1400 {
			point = m.lead()
		}
		return order{aim: m.aloft(point.Subtract(flight.Vec3{Y: 300})), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"high", 3, func(m *moment) order {
		o := order{aim: m.aloft(m.lag().Add(flight.Vec3{Y: 450})), g: m.pull * 0.85, throttle: 0.9}
		if m.closure > 90 {
			o.brake = 1
		}
		return o
	}},
	{"reverse", 3, func(m *moment) order {
		o := order{aim: m.swing(m.side() * 1.9), g: m.pull, throttle: 0.35}
		if m.me.Velocity.Length() > m.pace {
			o.brake = 1
		}
		return o
	}},
	{"climb", 4, func(m *moment) order {
		f := m.flat()
		return order{aim: flight.Vec3{X: f.X, Y: 0.6, Z: f.Z}.Normalize(), g: 3, throttle: 1, reheat: 1}
	}},
}

// evolve advances the opponent's track by dt seconds: around his estimated
// circle when one is readable, straight otherwise. The arc is capped — a
// half-circle of extrapolation is prediction, a full one is fantasy.
func evolve(t *track, ring orbit, dt float64) (flight.Vec3, flight.Vec3) {
	if !ring.valid {
		return t.position.Add(t.velocity.Scale(dt)), t.velocity
	}
	arc := clamp(ring.omega*dt, 0, 2.6)
	radial := t.position.Subtract(ring.centre)
	planar := radial.Subtract(ring.normal.Scale(radial.Dot(ring.normal)))
	if planar.Length() < 1 {
		return t.position.Add(t.velocity.Scale(dt)), t.velocity
	}
	u := planar.Normalize()
	w := ring.normal.Cross(u)
	turned := u.Scale(math.Cos(arc)).Add(w.Scale(math.Sin(arc)))
	position := ring.centre.Add(turned.Scale(planar.Length())).Add(ring.normal.Scale(radial.Dot(ring.normal)))
	speed := t.velocity.Length()
	velocity := ring.normal.Cross(turned).Normalize().Scale(speed)
	return position, velocity
}

// appraise scores one instant of a rehearsed future. Positive is a fight
// being won: his tail toward my pointed nose inside the gun band. Negative is
// a fight being lost: his nose behind my tail, or the sea arriving.
func appraise(s *flight.State, hisP, hisV flight.Vec3, pace float64) float64 {
	los := hisP.Subtract(s.Position)
	r := math.Max(los.Length(), 1)
	lhat := los.Scale(1 / r)
	nose := s.Attitude.Rotate(flight.Vec3{X: 1})
	speed := math.Max(s.Velocity.Length(), 1)
	vhat := s.Velocity.Scale(1 / speed)
	hv := hisV
	if hv.Length() > 1 {
		hv = hv.Normalize()
	}
	rear := hv.Dot(lhat)    // 1: I look up his tailpipes
	point := nose.Dot(lhat) // 1: my nose is on him
	ahead := lhat.Dot(vhat) // 1: he is ahead of my path, -1: behind me
	band := clamp((r-60)/190, 0, 1) * clamp((1500-r)/800, 0, 1)
	near := clamp((2500-r)/1500, 0, 1)
	offence := clamp(rear, 0, 1) * clamp(point, 0, 1) * clamp(point, 0, 1) * band
	threat := clamp(-rear, 0, 1) * clamp(-ahead, 0, 1) * near
	energy := 0.25*clamp((speed-0.8*pace)/pace, -1, 0.5) + 0.1*clamp((s.Position.Y-800)/2200, 0, 1)
	// The chase gradient: outside the gun band the geometry terms flatten to
	// zero and a short rehearsal barely moves the range, so without this the
	// choice at 3 km is decided by the selection noise while the target sails
	// away. Closure is what a pursuit buys; it fades out approaching the band,
	// where arriving hot is the classic blown pass.
	closing := s.Velocity.Subtract(hisV).Dot(lhat)
	score := offence - 1.3*threat + energy - r/12000 +
		0.35*clamp(closing/400, -1, 1)*clamp((r-500)/1200, 0, 1) +
		0.15*point - // nose toward him is progress at any range: a reversal's value shows as swing long before it shows as a gun band
		0.5*clamp((closing-70)/150, 0, 1)*clamp((900-r)/600, 0, 1) // the blown pass, priced: arriving hot inside the merge cannot be stopped by any law (stopping distance alone exceeds the range), and without this the incumbent full-burner play tied the disciplined one and zero-noise argmax never escaped it — the machine overshot every pass it flew
	if s.Position.Y < 400 {
		score -= 2
	}
	if speed < 80 {
		score -= 1
	}
	return score
}

// rehearse flies one candidate law forward through the real flight model —
// the same airframe, FCS, and executor imperfections the live bot flies —
// against the opponent's evolved track, and returns the weighted score.
// Later instants weigh more: BFM is judged by where it ends up.
func (i *instance) rehearse(a *craft, b *brain, sim *flight.Model, chosen play, prey *track, tick uint64, horizon int) float64 {
	sim.State = a.model.State
	shadow := *b // the executor's scalar state rides along; maps are untouched
	shadow.shoot = false
	pace := corner(a.model)
	age := float64(tick-prey.when) / 60
	score, weight := 0.0, 0.0
	for k := 1; k <= horizon; k++ {
		t := float64(k) / 60
		hisP, hisV := evolve(prey, b.ring, age+t)
		m := moment{me: &sim.State, prey: hisP, velocity: hisV, ring: b.ring, pace: pace, pull: b.skill.pull}
		m.derive()
		o := chosen.law(&m)
		shadow.aim, shadow.g, shadow.throttle, shadow.reheat, shadow.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
		in := shadow.steer(sim, tick+uint64(k))
		for sub := 0; sub < 4; sub++ {
			sim.Step(in) // the flight core steps at 240 Hz: four substeps per 60 Hz rollout tick, exactly like the live path. One Step per tick ran the WHOLE rehearsal at quarter time — the phantom moved four times faster than the jet, every candidate scored against that fiction, and the rollouts barely discriminated (the press-versus-extend trace that exposed it flew identical paths)
		}
		if sim.State.Position.Y < 120 {
			return -100 // flew it into the sea: veto, whatever else it bought
		}
		if k%30 == 0 || k == horizon {
			w := 0.4 + 0.6*float64(k)/float64(horizon)
			score += w * appraise(&sim.State, hisP, hisV, pace)
			weight += w
		}
	}
	return score / weight
}

// duel is the teamless fight brain: rehearse, commit, fly. Replaces the mode
// ladder (which remains the section doctrine) from the moment a target is
// held. The caller runs the shared tail (aero cap, missile request, guard,
// fuel, wander) after it.
func (i *instance) duel(slot int, a *craft, tick uint64, prey *track, direction flight.Vec3, distance, tail float64, menace int, gap float64) {
	b := a.brain
	me := &a.model.State
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})

	// The press clock (the finisher's deliberate looseness, #144): held
	// advantage in range starts it, losing the range stops it.
	if distance < b.tactics.press.span && tail > 0.35 && nose.Dot(direction) > 0.5 {
		if b.press == 0 {
			b.press = tick + 1
		}
	} else if distance > b.tactics.press.span || tail < 0 {
		b.press = 0
	}

	// Re-judge when the committed line expires — or the picture breaks it: an
	// attacker arriving close behind invalidates any offensive line now.
	offensive := b.play == "press" || b.play == "lag" || b.play == "low" || b.play == "high" || b.play == "climb"
	if b.play == "" || tick >= b.until || (offensive && menace >= 0 && gap < 700 && tick-b.picked >= 15) {
		sim := flight.New(a.model.Airframe, a.model.Environment, a.model.World)
		// The horizon must outlive the manoeuvres it judges — in REAL seconds,
		// now that the rollout clock is honest: 2.5 s for the novice up to 4 s
		// for the top tiers, enough for a reversal's payoff to show through the
		// point-progress term without quadrupling the rehearsal budget.
		horizon := 60*2 + 30*b.skill.library
		best, top, n := b.play, math.Inf(-1), 0
		for _, p := range plays {
			if p.tier > b.skill.library {
				continue
			}
			if p.name == "extend" && distance > 2500 {
				continue // already separated: rehearsing more separation buys nothing
			}
			score := i.rehearse(a, b, sim, p, prey, tick, horizon)
			// Selection noise is the skill's wander: the ace nearly argmaxes,
			// the novice sometimes picks the second-best line and flies it well.
			score += (battle.Roll(i.environment.Seed, uint64(slot)+57, tick, uint64(n)) - 0.5) * b.skill.wander * 2
			if p.name == b.play {
				score += 0.02 // ties keep the committed line: churn is its own cost (a larger incumbency rode broken lines past the moment press should take over — measured 6/6 kills falling to 3/6 on the conversion referendum)
			}
			if score > top {
				best, top = p.name, score
			}
			n++
		}
		b.play = best
		b.picked = tick
		b.until = tick + uint64(math.Max(54, b.skill.commit*24)) // the commitment: ~0.9 s floor, 1.6 s at the top — the machine included, whose edge is reflex and precision, not strategy churn
	}

	// Fly the committed law against the LIVE geometry — commitment holds the
	// line, never a stale aim point.
	pace := corner(a.model)
	m := moment{me: me, ring: b.ring, pace: pace, pull: b.skill.pull}
	age := float64(tick-prey.when) / 60
	// The LIVE aim predicts with the track's measured swing, not the fitted
	// circle: gunnery needs local precision, and a weaving target breaks any
	// circle fit — the lead point wandered and the trigger starved (drone
	// ladder 0/0/3/5). The circle keeps its place in the rollouts, where
	// multi-second arcs are the question and a parabola diverges.
	m.prey = predict(prey, age, b.skill.library >= 3)
	m.velocity = prey.velocity.Add(prey.swing.Scale(age))
	m.derive()
	for _, p := range plays {
		if p.name == b.play {
			o := p.law(&m)
			b.aim, b.g, b.throttle, b.reheat, b.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
			break
		}
	}
	b.mode = b.play
	// The trigger is ALWAYS live in a duel: the led solution gate and the
	// burst governor decide each round. Doctrine safing the gun was the #206
	// offensive deficit, found one wired-shut window at a time.
	b.shoot = true
}

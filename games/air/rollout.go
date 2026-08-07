// Mochi world: Rehearsal fidelity
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"

	"world/games/air/flight"
)

// The arbiter rehearses every candidate play through the flight model, which
// costs about 20 microseconds per step and ~19,000 steps per re-plan — 380 ms
// of CPU for one bot's decision, and a remotely triggerable way to wedge a
// session (#256). This file holds the fidelity knob the fix is measured
// against: the same rollout, advanced four different ways.
//
// The danger is specific and this codebase has already paid for it once. The
// rehearsal ran at QUARTER TIME from the day duel.go was born — the phantom
// opponent moved four times faster than the rehearsed jet — and the bots did
// not look broken, they just quietly stopped discriminating between plays.
// Any reduced fidelity is a controlled version of that same divergence, so it
// is never shipped on plausibility: agreement with the full model is measured
// (TestFidelity), because ranking plays is a far weaker requirement than
// simulating them and the failure mode is silent.
type fidelity int

const (
	full      fidelity = iota // the live path: four 240 Hz substeps per rollout tick, blade element and FCS
	coarse                    // the same model at 60 Hz: one substep per rollout tick, quarter the work
	surrogate                 // point-mass energy model at 240 Hz: no blade element, no FCS
	both                      // point-mass at 60 Hz
)

// rehearsal selects how rollouts advance. The live server flies `both` — the
// point-mass surrogate at 60 Hz — since 2026-08-08 (#256), and the choice is
// measured, not assumed:
//
//	full 240 Hz     436.7 ms per re-plan   the old live path
//	surrogate 60 Hz   1.4 ms               313x cheaper
//
// Paired over 360 decision points it picks the same play 57% of the time, and
// the disagreements are cheap: judged by the FULL model's own scores it gives
// up 11% of the candidate spread on average, and only 5% of all decisions cost
// more than a fifth of it. Ranking candidates is a far weaker requirement than
// simulating them, which is why this works at all. What matters is that the
// FIGHTS did not degrade: TestConvert, TestGunnery, TestOffence, TestFlounder
// and the guns ladder all hold or improve (the superhuman kills the flounder
// in 50.0 s against the full model's 50.8 s).
//
// `full` remains, and TestFidelity keeps measuring against it — a surrogate
// that drifts from the jet it stands in for is the quarter-time bug wearing a
// different hat.
var rehearsal = both

// substeps is how many surrogate/model steps one 60 Hz rollout tick takes.
func (f fidelity) substeps() int {
	if f == coarse || f == both {
		return 1
	}
	return 4
}

// span is the timestep each of those substeps advances.
func (f fidelity) span() float64 {
	if f == coarse || f == both {
		return 4 * flight.Dt
	}
	return flight.Dt
}

func (f fidelity) reduced() bool { return f == surrogate || f == both }

// glide is the point-mass surrogate: the jet as energy and a turn rate,
// advanced straight from the play's ORDER rather than through the stick, the
// FCS and the blade-element aero. It keeps what ranks plays — thrust against
// drag, induced drag paid for every g, the aero g ceiling the wing can
// actually deliver at this speed — and drops what merely renders them.
//
// Thrust comes from the real engine lapse (flight.Output) and the atmosphere
// from the real table, so the surrogate cannot drift from the jet on the two
// terms an energy fight is decided by.
func glide(m *flight.Model, o order, dt float64) {
	s := &m.State
	speed := s.Velocity.Length()
	if speed < 1 {
		speed = 1
	}
	local := flight.Atmosphere(s.Position.Y, m.Environment)
	mass := m.Mass()
	if mass <= 0 {
		mass = m.Airframe.Mass.Empty + s.Fuel
	}

	// Thrust: the same lapse the real engines fly, at the commanded lever.
	// Spool lag is deliberately dropped — it is a sub-second transient on a
	// multi-second rollout, and modelling it would need the engine states.
	thrust := 0.0
	mach := speed / local.Sound
	for i := range m.Airframe.Engines {
		dry, boost := flight.Output(flight.EngineState{Spool: o.throttle, Reheat: o.reheat},
			&m.Airframe.Engines[i], local.Density, mach)
		thrust += dry + boost
	}

	area := m.Airframe.Reference.Area
	pressure := 0.5 * local.Density * speed * speed
	// The g actually available: the commanded pull, capped by structure and
	// by the wing at this speed. This is the term that makes a slow jet a
	// balloon, so the surrogate must keep it or every energy judgement is a
	// fiction.
	lift := clamp(o.g, -3, m.Airframe.Limit.Positive)
	ceiling := pressure * area * 1.55 / (mass * 9.81)
	if lift > ceiling {
		lift = ceiling
	}
	// Drag: parasitic plus induced. The induced term is what a turn costs,
	// and it scales with the square of the load — the whole currency of BFM.
	coefficient := lift * mass * 9.81 / math.Max(pressure*area, 1)
	span := m.Airframe.Reference.Span
	ratio := span * span / math.Max(area, 1)
	drag := pressure * area * (0.021 + coefficient*coefficient/(math.Pi*ratio*0.78))
	if o.brake > 0.5 {
		drag *= 1.35
	}

	// Turn: rotate the velocity toward the aim at the rate the available
	// lateral g gives, never past the aim itself. `heave` is where lift acts
	// — the turn it is buying plus the gravity it is holding — because the
	// nose sits alpha degrees from the flight path IN THAT DIRECTION, and
	// the nose is what every offensive term in appraise() is scored on.
	direction := s.Velocity.Scale(1 / speed)
	lateral := 9.81 * math.Sqrt(math.Max(lift*lift-1, 0))
	skyward := flight.Vec3{Y: 1}.Subtract(direction.Scale(direction.Y))
	heave := skyward
	if aim := o.aim; aim.Length() > 0.5 {
		want := aim.Normalize()
		if angle := math.Acos(clamp(direction.Dot(want), -1, 1)); angle > 1e-4 {
			turn := math.Min(lateral/speed*dt, angle)
			axis := direction.Cross(want)
			if axis.Length() > 1e-6 {
				axis = axis.Normalize()
				pull := axis.Cross(direction) // the direction the flight path is being bent
				heave = pull.Scale(lateral).Add(skyward.Scale(9.81))
				// Rodrigues about the turn axis: the velocity swings.
				sin, cos := math.Sin(turn), math.Cos(turn)
				direction = direction.Scale(cos).
					Add(axis.Cross(direction).Scale(sin)).
					Add(axis.Scale(axis.Dot(direction) * (1 - cos)))
				direction = direction.Normalize()
			}
		}
	}

	// Energy: thrust less drag along the path, less the climb.
	speed += ((thrust-drag)/mass - 9.81*direction.Y) * dt
	if speed < 30 {
		speed = 30 // a surrogate that stops flying stops ranking; the aero ceiling above already prices the mush
	}
	s.Velocity = direction.Scale(speed)
	s.Position = s.Position.Add(s.Velocity.Scale(dt))
	// The NOSE, not the flight path (#256): a jet carries alpha between the
	// two, and the whole conversion machinery — the pipper takeover, the led
	// solution the offensive term scores — lives in exactly that gap. The
	// first surrogate pointed the nose down the velocity vector and the
	// superhuman, whose margin is the thinnest on the ladder, lost forty
	// seconds on the flounder gate for it.
	alpha := clamp(coefficient/5.9, 0, m.Airframe.Limit.Alpha)
	nose := direction
	if heave.Length() > 1e-6 && alpha > 1e-4 {
		lifting := heave.Subtract(direction.Scale(heave.Dot(direction)))
		if lifting.Length() > 1e-6 {
			lifting = lifting.Normalize()
			nose = direction.Scale(math.Cos(alpha)).Add(lifting.Scale(math.Sin(alpha))).Normalize()
		}
	}
	s.Attitude = flight.Look(nose)
	s.Fcs.Normal = lift
	s.Time += dt
}

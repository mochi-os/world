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
//	full 240 Hz     481.3 ms per re-plan   the old live path
//	surrogate 60 Hz   1.7 ms               ~243x cheaper
//
// Paired over 360 decision points it picks the same play 66% of the time, and
// the disagreements are cheap: judged by the FULL model's own scores it gives
// up 12% of the candidate spread on average, and only 7% of all decisions cost
// more than a fifth of it. (Before the surrogate learned to roll and to store
// a full attitude, those read 48% / 21% / 19%.) Ranking candidates is a far
// weaker requirement than simulating them, which is why this works at all.
// What matters is that the FIGHTS moved TOWARD the full model, not just the
// metric: the superhuman guns edge the ladder used to show was surrogate
// fiction (0-0 at full fidelity), and the honest rollout plays it even.
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
//
// A NOSE-SETTLING mirror of the executor's pipper takeover was tried here
// and rejected by measurement (2026-08-13, three forms: parked instantly,
// parked with the tier's wander as the floor, and driven at a 0.4 s time
// constant — all scoped to the gun band). Each closed some of the floater
// conversion gap (stock kills 2/1/1 of twelve against the full model's
// 5/4/3) but every form INVERTED the drone ladder (machine 9/12 under the
// ace's 11): against a compliant target every rehearsed approach holds the
// solution window, so a settling nose scores every play as converting and
// the machine's argmax loses its gradient exactly where it discriminates.
// The floater gap is real but it is not worth that trade; whatever closes
// it must discriminate BETTER close-in, not saturate.
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
	// lateral g gives — in the plane the WINGS span this instant, not the
	// ideal one. The lift direction is carried tick to tick through the
	// attitude and slews at the FCS roll-rate law, so a rehearsed reversal
	// costs the roll the live jet pays. The first surrogate rolled instantly
	// at full g, which flattered every close-in escape: the rehearsed
	// defender was always cleaner than the jet could fly, and the superhuman
	// guns edge the ladder gated on turned out to be that fiction (0-0 at
	// full fidelity). The limiter's rolling-pull reduction is deliberately
	// NOT mirrored here: modelling it on the standing bank error was
	// measured and rejected — it over-taxed reversal-heavy missile defence
	// and inverted the superhuman-ace arm 0-6 where the full model plays it
	// near even. The slew alone prices reorientation time.
	direction := s.Velocity.Scale(1 / speed)
	skyward := flight.Vec3{Y: 1}.Subtract(direction.Scale(direction.Y))
	lifting := s.Attitude.Rotate(flight.Vec3{Y: 1})
	lifting = lifting.Subtract(direction.Scale(lifting.Dot(direction)))
	if lifting.Length() < 1e-6 {
		lifting = skyward
	}
	if lifting.Length() < 1e-6 { // flying straight up or down: any lateral serves
		lifting = flight.Vec3{Z: 1}.Subtract(direction.Scale(direction.Z))
	}
	lifting = lifting.Normalize()

	// Where the play wants the lift: bending the path toward the aim while
	// holding gravity; with no aim, holding the sky.
	desired := skyward
	angle := 0.0
	if aim := o.aim; aim.Length() > 0.5 {
		want := aim.Normalize()
		angle = math.Acos(clamp(direction.Dot(want), -1, 1))
		if axis := direction.Cross(want); angle > 1e-4 && axis.Length() > 1e-6 {
			pull := axis.Normalize().Cross(direction)
			desired = pull.Scale(9.81 * math.Sqrt(math.Max(lift*lift-1, 0))).Add(skyward.Scale(9.81))
		}
	}

	// Roll toward the desired plane at the rate the FCS grants (KEEP IN
	// SYNC with fcs.go's roll-rate command; the alpha taper and store
	// limits are dropped as sub-dominant here).
	roll := 0.0
	if d := desired.Subtract(direction.Scale(desired.Dot(direction))); d.Length() > 1e-6 {
		want := d.Normalize()
		roll = math.Atan2(direction.Dot(lifting.Cross(want)), lifting.Dot(want))
	}
	rate := 3.8 * clamp(speed/200, 0.35, 1)
	step := clamp(roll, -rate*dt, rate*dt)
	if math.Abs(step) > 1e-9 {
		sin, cos := math.Sin(step), math.Cos(step)
		lifting = lifting.Scale(cos).Add(direction.Cross(lifting).Scale(sin)).Normalize()
	}
	// Bend the path along the current lift direction, never past the aim.
	lateral := 9.81 * math.Sqrt(math.Max(lift*lift-1, 0))
	heave := skyward
	if angle > 1e-4 && lateral > 1e-6 {
		turn := math.Min(lateral/speed*dt, angle)
		heave = lifting.Scale(lateral).Add(skyward.Scale(9.81))
		sin, cos := math.Sin(turn), math.Cos(turn)
		direction = direction.Scale(cos).Add(lifting.Scale(sin))
		direction = direction.Normalize()
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
	// seconds on the flounder gate for it. The attitude stores the FULL
	// frame — Look() flattened it to a wings-level horizontal heading, which
	// handed appraise() a pitchless nose in every vertical fight.
	//
	// The nose SETTLES ON THE AIM when the aim is within the alpha budget —
	// the executor's pipper takeover does exactly that in the full rollout,
	// and appraise() scores the nose, so a surrogate whose nose only ever
	// tilts into the pull plane cannot tell a converting approach from a
	// near miss: the full model killed the floater 5/4/3 of twelve while
	// stock managed 2/1/1 on the SAME play distribution, because the
	// fine-grained choices never saw the conversion coming.
	alpha := clamp(coefficient/5.9, 0, m.Airframe.Limit.Alpha)
	nose := direction
	if heave.Length() > 1e-6 && alpha > 1e-4 {
		if plane := heave.Subtract(direction.Scale(heave.Dot(direction))); plane.Length() > 1e-6 {
			nose = direction.Scale(math.Cos(alpha)).Add(plane.Normalize().Scale(math.Sin(alpha))).Normalize()
		}
	}
	up := lifting
	if lift < 0 {
		up = lifting.Scale(-1) // a push carries the canopy away from the pull
	}
	s.Attitude = flight.Basis(nose, up)
	s.Fcs.Normal = lift
	s.Time += dt
}

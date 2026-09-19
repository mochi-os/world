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

// The arbiter rehearses every candidate play through the flight model - about
// 380 ms of CPU per bot decision at full fidelity (#256). This file holds the
// fidelity knob: the same rollout, advanced four ways. Reduced fidelity ships
// only on measured agreement (TestFidelity); its failure mode is silent.
type fidelity int

const (
	full      fidelity = iota // the live path: four 240 Hz substeps per rollout tick, blade element and FCS
	coarse                    // the same model at 60 Hz: one substep per rollout tick, quarter the work
	surrogate                 // point-mass energy model at 240 Hz: no blade element, no FCS
	both                      // point-mass at 60 Hz
)

// rehearsal selects how rollouts advance. The live server flies both - the
// point-mass surrogate at 60 Hz, ~243x cheaper than full 240 Hz, picking the
// same play 66% of the time and giving up 12% of the candidate spread when it
// differs. full remains, and TestFidelity keeps measuring against it.
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
// advanced from the play's ORDER rather than through the stick, FCS and
// blade-element aero. It keeps what ranks plays - thrust against drag, induced
// drag per g, the aero g ceiling - and takes thrust and atmosphere from the
// real tables.
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
	stalled := o.alpha
	if stalled {
		// Past the lift peak, for the one play licensed to go there (bleed, stage
		// 9). The blade-element jet does not stop at 1.55: full aft stick below
		// corner rides on to the 40 degree alpha limit, and the load ACROSS the
		// path keeps rising as the body, the LEX and the tilted thrust join the
		// wing. Measured on the bandit's own jet (TestStallProbe, 2026-09-18,
		// reheat off / on alike):
		//
		//     alpha     lift   drag   lift/drag
		//     20-25     1.50   0.55     2.7
		//     25-30     1.81   0.80     2.3
		//     30-35     2.24   1.16     1.9
		//     35-40     2.60   1.40     1.9
		//
		// The top bin is mostly thrust at almost no airspeed, so the branch stops
		// at 2.3. Drag and alpha are the straight lines through the other three,
		// and they are what make this a purchase rather than a gift: four times
		// the pre-stall drag for half again the load.
		//
		// The jet does not ARRIVE there at once. Alpha walks up at eleven or
		// twelve degrees a second from the moment the stick comes back (the same
		// jet, half-second steps: 5.9, 11.8, 16.7, 22.5, 29.4, 32.6 degrees),
		// which is 1.2 of lift coefficient a second below the peak and 0.75 above
		// it. The load glide stored on the last tick is the memory - on the first
		// tick of a rollout that is the live jet's own - and without it a
		// rehearsed bleed paid the whole post-stall drag from its first instant:
		// 20 m/s gone in the first half second, where the real jet loses one.
		held := clamp(s.Fcs.Normal*mass*9.81/math.Max(pressure*area, 1), 0, 2.3)
		rate := 1.2
		if held >= 1.55 {
			rate = 0.75
		}
		ceiling = pressure * area * math.Min(2.3, held+rate*dt) / (mass * 9.81)
	}
	if lift > ceiling {
		lift = ceiling
	}
	// Drag: parasitic plus induced. The induced term is what a turn costs,
	// and it scales with the square of the load — the whole currency of BFM.
	coefficient := lift * mass * 9.81 / math.Max(pressure*area, 1)
	span := m.Airframe.Reference.Span
	ratio := span * span / math.Max(area, 1)
	drag := pressure * area * (0.021 + coefficient*coefficient/(math.Pi*ratio*0.78))
	if stalled && coefficient > 1.5 {
		drag = pressure * area * (0.55 + (coefficient-1.5)*0.8)
		if o.brake > 0.5 {
			drag += pressure * area * 0.1 // the boards add what they add at the lift peak; scaling a post-stall drag by them would charge for a panel the wake has already swallowed
		}
	} else if o.brake > 0.5 {
		drag *= 1.35
	}

	// Turn: rotate the velocity toward the aim at the rate the available lateral g
	// gives, in the plane the WINGS span this instant. The lift direction slews at
	// the FCS roll-rate law, so a rehearsed reversal costs the roll the live jet
	// pays. The limiter's rolling-pull reduction is deliberately not mirrored.
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
	if stalled || o.gravity {
		// The licensed turn bends the path along what is LEFT of the lift once
		// gravity across the path is paid. The branch below turns toward the lift
		// vector itself, which the roll law has tilted up to hold gravity, so every
		// turn it flies also climbs at about one g; a bleed rehearsed that way
		// spent its speed on height the real jet never gains.
		net := lifting.Scale(lift * 9.81).Subtract(skyward.Scale(9.81))
		if size := net.Length(); size > 1e-6 && angle > 1e-4 {
			turn := math.Min(size/speed*dt, angle)
			heave = lifting.Scale(lateral).Add(skyward.Scale(9.81))
			direction = direction.Scale(math.Cos(turn)).Add(net.Scale(math.Sin(turn) / size)).Normalize()
		}
	} else if angle > 1e-4 && lateral > 1e-6 {
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
	// The NOSE, not the flight path (#256): the conversion machinery lives in the
	// alpha gap between them and appraise() scores the nose. Store the full frame
	// (Look() flattens it to a wings-level heading) and settle the nose on the aim
	// when the aim is inside the alpha budget, as the pipper takeover does.
	alpha := clamp(coefficient/5.9, 0, m.Airframe.Limit.Alpha)
	if stalled && coefficient > 1.5 {
		alpha = clamp(0.39+(coefficient-1.5)*0.237, 0, m.Airframe.Limit.Alpha) // 22.5 degrees at the peak, 33 at the branch's ceiling: the measured line above
	}
	nose := direction
	if stalled || o.gravity {
		// Alpha lives in the jet's plane of symmetry, which holds the LIFT vector,
		// not the net heave. The branch below tilts the nose toward the heave, and
		// the frame it stores then hands the next tick a lift vector pushed off by
		// sin^2(alpha) of the difference: a nudge at 15 degrees that the roll law
		// absorbs, and at 33 a roll onto its back inside a second (measured: the
		// licensed turn inverted, dived at 67 m/s and turned 22 degrees in six
		// seconds where the real jet turns 68).
		nose = direction.Scale(math.Cos(alpha)).Add(lifting.Scale(math.Sin(alpha))).Normalize()
	} else if heave.Length() > 1e-6 && alpha > 1e-4 {
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

// Mochi world: Flight control system
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// An augmenter and limiter on an airframe that flies honestly bare-handed:
// C*-style pitch command with integral auto-trim, roll-rate command through
// differential flaperon and stabilator, yaw damping and turn coordination,
// scheduled leading-edge flaps, a powered-approach mode, and the carefree
// limiter — full aft stick parks at the alpha or g limit and pro-spin input
// is refused. The paddle switch (Inputs.Override) raises the g ceiling and
// records overstress exposure into DamageState for the damage model.
// Model.Direct bypasses everything: stick drives geared surfaces (validation).

package flight

import (
	"math"
)

const (
	crossover   = 60.0               // m/s: the C* crossover (~120 kt, the classic blend point)
	stab_rate   = 40 * math.Pi / 180 // actuator rates, rad/s
	flap_rate   = 100 * math.Pi / 180
	rudder_rate = 75 * math.Pi / 180
	brake_rate  = 1.0                 // speedbrake: full travel per second
	blowdown    = 35000.0             // deflection·dynamic-pressure ceiling — bites near limit q only
	onspeed     = 8.1 * math.Pi / 180 // PA approach alpha
)

// fcs turns stick commands into surface commands and slews the actuators.
func (m *Model) fcs(in Inputs, local Air) {
	f := &m.State.Fcs
	v := m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust))
	speed := v.Length()
	pressure := 0.5 * local.Density * speed * speed
	a := alpha(v)
	b := beta(v)
	p, q, r := rates(m.State.Omega)

	stick := clamp(in.Pitch, -1, 1)
	lateral := clamp(in.Roll, -1, 1)
	pedal := clamp(in.Yaw, -1, 1)

	var stabTarget, flapTarget, rudderTarget, droopTarget float64

	if m.Direct {
		// Geared surfaces, no augmentation — the bare-airframe validation path.
		stabTarget = -stick * 0.42
		flapTarget = lateral * 0.35
		rudderTarget = -pedal * 0.52
		f.Integral = 0
	} else if in.Gear && speed < 130 {
		// Powered approach: the stick commands alpha about on-speed, and the
		// trailing edge droops for lift at approach speed. The neutral demand
		// is capped at the alpha LEVEL FLIGHT needs at this dynamic pressure —
		// snapping straight to on-speed alpha at gear-limit speed is a 2.5 g
		// uncommanded zoom that full forward stick cannot push out of; as the
		// jet decelerates toward on-speed the cap rises to meet it.
		level := clamp(m.mass*gravity/math.Max(pressure*m.Airframe.Reference.Area*5.0, 1), 0, onspeed)
		demand := math.Min(onspeed, level) + stick*(6*math.Pi/180)
		errorTerm := (demand-a)*2.2 - q*1.8
		f.Integral = clamp(f.Integral+errorTerm*0.45*Dt, -0.3, 0.3)
		stabTarget = -(errorTerm*0.28 + f.Integral)
		droopTarget = 0.52 * clamp(1-pressure/9000, 0, 1)
		flapTarget = lateral * 0.30
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
	} else {
		// Up and away: C* command with the carefree limiter.
		ceiling := m.Airframe.Limit.Positive
		if in.Override {
			ceiling = m.Airframe.Limit.Override
		}
		floor := m.Airframe.Limit.Negative
		demand := 1.0
		if stick >= 0 {
			demand = 1 + stick*(ceiling-1)
		} else {
			demand = 1 + stick*(1-floor)
		}
		// Onset shaping: the demand slews at ~15 g/s, so a stick slam builds
		// load the loops can track instead of a step they chase. (No zero-
		// means-fresh sentinel here: a full push slews THROUGH exactly zero,
		// and a sentinel reset turns it into a 1→0→1 loop that silently
		// refuses every negative-g command.)
		f.Demand += clamp(demand-f.Demand, -25*Dt, 25*Dt)
		demand = f.Demand
		_ = floor
		// Cascaded pitch: the g error commands a PITCH RATE, and the carefree
		// limits shape that rate demand — it fades to zero approaching the g
		// and alpha boundaries and goes negative beyond them, so the limiter
		// is a smooth property of the command path, not a switched override.
		// A fast inner rate loop owns the (very powerful) stabilator.
		star := demand - m.State.Fcs.Normal
		rateBound := math.Min(1.0, 150/math.Max(speed, 60)) // ~0.58 rad/s at 260 m/s, opening up low and slow
		// The g error commands the rate that closes it at a fixed loop
		// bandwidth: a rad/s of pitch rate yields V/g g's, so the gain must
		// carry g/V or the loop crossover climbs with speed past the alpha
		// lag and limit-cycles about 1g.
		gain := 30 / math.Max(speed, 60)
		f.Integral = clamp(f.Integral+star*gain*0.3*Dt, -0.5, 0.5)
		// Stick feedforward: the real CAS has a direct forward path, so a
		// slam bites the surface immediately while the g loop trims behind
		// it — without it the response is bandwidth-limited and reads mushy.
		rateDemand := clamp(star*gain+f.Integral+stick*0.35*rateBound, -rateBound, rateBound)
		// Rate anticipation on the EXCESS pitch rate only: q above the steady
		// turn rate n·g/V is what is still building g. Penalising total q made
		// the limiter park a full g below the ceiling in a sustained pull.
		excess := q - m.State.Fcs.Normal*gravity/math.Max(speed, 60)
		capG := (ceiling-m.State.Fcs.Normal)*0.9 - excess*(pressure/14000)
		capA := (m.Airframe.Limit.Alpha - a) * 2.2
		capFloor := (m.Airframe.Limit.Negative-m.State.Fcs.Normal)*0.9 - excess*(pressure/14000) // mirrored anticipation: without it the negative boundary chatters
		shaped := clamp(rateDemand, capFloor, math.Min(capG, capA))
		// Boundary-recovery demands are proportional to the violation, so a
		// large external upset (transonic pitch-up, gust) can ask for tens of
		// rad/s — far beyond the airframe. Unbounded, those slams pump the
		// upset instead of damping it.
		envelope := math.Min(3*rateBound, 1.2)
		shaped = clamp(shaped, -envelope, envelope)
		// Back-calculation anti-windup: pull the g-trim integral toward what
		// the limits actually allow. (A blanket decay here oscillates at a
		// sustained boundary: bind → bleed → g sags → unbind → rebuild.)
		f.Integral += (shaped - rateDemand) * 3 * Dt
		inner := shaped - q
		// Air-data gain scheduling: stabilator power grows with dynamic
		// pressure, so a fixed inner gain that is crisp at 20 kPa rings past
		// ~60 kPa (a supersonic dive on the deck). Scale the surface loop
		// down as q̄ rises, exactly as the real jet's control law does.
		authority := clamp(20000/math.Max(pressure, 1), 0.25, 1)
		saturated := math.Abs(f.Stabilator.Left) > 0.40*clamp(blowdown/math.Max(pressure, 1), 0, 1)
		if !saturated {
			f.Trim = clamp(f.Trim+inner*0.25*authority*Dt, -0.35, 0.35)
		}
		command := -(inner*0.30*authority + f.Trim)
		// Overstress accounting for the damage model.
		if m.State.Fcs.Normal > m.Airframe.Limit.Positive {
			m.State.Damage.Stress += (m.State.Fcs.Normal - m.Airframe.Limit.Positive) * Dt
		}
		stabTarget = command
		// Roll-rate command, tempered at low speed and high alpha.
		limit := 3.8 * clamp(speed/200, 0.35, 1) * clamp(1-0.9*a/m.Airframe.Limit.Alpha, 0.08, 1)
		limit *= clamp(1-math.Abs(b)/0.30, 0.05, 1) // sideslip strips roll authority: no spin fuel
		flapTarget = (lateral*limit - p) * 0.22
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
	}

	// Leading-edge flaps schedule with alpha.
	slatTarget := clamp(0.9*(a-0.05), 0, 25*math.Pi/180)
	if m.Direct {
		slatTarget = 0
	}

	// Blowdown: available deflection falls with dynamic pressure.
	available := clamp(blowdown/math.Max(pressure, 1), 0, 1)

	// Actuators: rate-limited slew toward the commanded positions.
	slew := func(current float64, target float64, rate float64, limit float64) float64 {
		bound := limit * math.Min(available, 1)
		target = clamp(target, -bound, bound)
		return current + clamp(target-current, -rate*Dt, rate*Dt)
	}
	symmetric := clamp(stabTarget, -0.42, 0.30)
	differential := clamp(flapTarget, -0.35, 0.35)
	f.Stabilator.Left = slew(f.Stabilator.Left, symmetric+0.25*differential, stab_rate, 0.42)
	f.Stabilator.Right = slew(f.Stabilator.Right, symmetric-0.25*differential, stab_rate, 0.42)
	f.Flaperon.Left = slew(f.Flaperon.Left, droopTarget+differential, flap_rate, 0.60)
	f.Flaperon.Right = slew(f.Flaperon.Right, droopTarget-differential, flap_rate, 0.60)
	f.Rudder = slew(f.Rudder, rudderTarget, rudder_rate, 0.52)
	f.Slat += clamp(slatTarget-f.Slat, -0.6*Dt, 0.6*Dt)
	f.Flap = f.Flaperon.Left*0 + droopTarget // droop is carried inside the flaperon targets; keep the readout
	f.Speedbrake += clamp(clamp(in.Speedbrake, 0, 1)-f.Speedbrake, -brake_rate*Dt, brake_rate*Dt)
}

// yaw is the directional law: a washed-out yaw damper, sideslip suppression
// with a touch of pedal-commanded beta, and an aileron-rudder interconnect
// that grows with alpha (pro-spin input ends up refused because the rudder
// is busy coordinating).
func (m *Model) yaw(pedal float64, lateral float64, a float64, b float64, r float64, f *FcsState) float64 {
	f.Washout += (r - f.Washout) * Dt / 1.0
	damped := r - f.Washout
	interconnect := lateral * clamp(a/0.35, 0, 1) * 0.35
	pedal *= clamp(1-a/0.7, 0.1, 1) // pedals fade at high alpha
	// Signs under the -side rudder geometry (positive rudder pushes the
	// tail right, yawing the nose left): opposing r means following it with
	// the rudder (+damped), killing beta means steering away from it (-b),
	// and coordination follows the roll (-interconnect).
	return clamp(damped*1.2-(b-pedal*0.06)*3.4-interconnect, -0.52, 0.52)
}

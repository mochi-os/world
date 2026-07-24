// Mochi world: Flight control system
// Copyright © 2026 Mochisoft OÜ
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

// fcs turns stick commands into surface commands and slews the actuators.
func (m *Model) fcs(in Inputs, local Air) {
	c := &m.Airframe.Control
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

	var stabTarget, flapTarget, rudderTarget, droopTarget, slatFloor float64
	brakeTarget := clamp(in.Speedbrake, 0, 1)

	// Law selection with hysteresis (#203). The powered-approach condition was
	// the raw `gear && speed < 130`, so a gear-down speed crossing (a bolter or
	// waveoff cleaning up late) flipped laws instantly — and the trim integral
	// means different things in the two laws (up-and-away: a pitch-RATE bias;
	// powered approach: a direct stabilator add), so the flip mis-trimmed by up
	// to half a radian of stabilator: the gear-cycle trim jump, back on the
	// unpatched speed path. Enter PA below 125 m/s, leave above 135 (no
	// boundary chatter), and launder the integral across ANY law change — the
	// old gear-transit-only decay was a special case of this rule.
	pa := m.pa
	if !m.lawInit {
		pa = in.Gear && speed < 130
		m.pa = pa // initialisation is NOT a law change: leaving m.pa at its zero value made the first step of every fresh model read as a flip and launder the trim for its first two seconds (TestTrap's scripted pass missed the wires)
		m.lawInit = true
	} else if pa {
		if !in.Gear || speed > 135 {
			pa = false
		}
	} else if in.Gear && speed < 125 {
		pa = true
	}
	if pa != m.pa {
		m.launder = 2 // seconds of decay: each law re-learns its own trim behind the demand faders
	}
	m.pa = pa
	if extension := m.State.Gear.Extension; extension > 0.02 && extension < 0.98 {
		m.launder = math.Max(m.launder, Dt) // gear in transit keeps the laundering alive step by step — expiring WITH the transit, exactly like the old transit-gated decay, and without stacking a second rate on top of a law-flip launder
	}
	if m.launder > 0 {
		m.launder -= Dt
		f.Integral *= 1 - 1.2*Dt // gentle: fully laundered over a couple of seconds (the units change between laws), but slow enough that the attitude hold keeps most of its trim — 3/s sagged the nose ~2 deg at gear-up
	}

	if m.Direct {
		// Geared surfaces, no augmentation — the bare-airframe validation path.
		stabTarget = -stick * c.Gearing.Pitch
		flapTarget = lateral * c.Gearing.Roll
		rudderTarget = -pedal * c.Gearing.Yaw
		f.Integral = 0
	} else if m.pa {
		// Powered approach: the stick commands alpha about on-speed, and the
		// trailing edge droops for lift at approach speed. The neutral demand
		// is capped at the alpha LEVEL FLIGHT needs at this dynamic pressure —
		// snapping straight to on-speed alpha at gear-limit speed is a 2.5 g
		// uncommanded zoom that full forward stick cannot push out of; as the
		// jet decelerates toward on-speed the cap rises to meet it.
		level := clamp(m.mass*gravity/math.Max(pressure*m.Airframe.Reference.Area*5.0, 1), 0, c.Onspeed)
		demand := math.Min(c.Onspeed, level) + stick*(9*math.Pi/180)
		// Flyaway attitude capture: hands-off after a catapult shot the real
		// FCS settles at the trim-board flyaway datum (c.Flyaway, 16°) rather than riding approach alpha
		// into a full-burner zoom. Binds only when pitch exceeds the datum;
		// the approach (low attitude, low power) never feels it.
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		pitch := math.Asin(clamp(forward.Y, -1, 1))
		f.Reference = pitch // keep the attitude-hold datum CURRENT: crossing the 130 m/s law boundary otherwise handed the UA hold a stale deck pitch, and it flew the nose back down from the flyaway attitude ("suddenly pitches down" a few seconds after launch)
		capture := a + (c.Flyaway - pitch)
		if in.Throttle > 0.85 {
			// Launch/waveoff power: the flyaway is an ATTRACTION, not just a cap —
			// with honest droop lift the hands-off climb tops out below the datum
			// and never used to reach the flyaway datum.
			demand = math.Max(demand, math.Min(capture, c.Onspeed+2*math.Pi/180))
		}
		demand = math.Min(demand, math.Max(capture, 0)+stick*(22*math.Pi/180)) // the capture yields to a DELIBERATE pull: at neutral stick it pins the flyaway attitude, but its stick opening (22°) outruns the main demand's (9°), so pulling past ~half stick clears the cap entirely — it no longer fought the climb-out (post-launch "unresponsive then suddenly alive")
		if m.State.Gear.Wow {
			// Ground mode: the alpha law would wind the stabilator full
			// nose-up during the catapult stroke (deck alpha is far below
			// approach alpha) and rotate the jet off the shuttle mid-stroke.
			// Follow the current alpha instead — no error, no windup; the
			// stick passes through for checks and early rotation stays manual.
			demand = a + stick*(12*math.Pi/180) // full aft stick rotates ~12° above deck alpha — field takeoffs need real rotation authority
			if in.Throttle < 0.3 && m.State.Gear.Wire < 0 {
				// Rollout derotation: pure alpha-follow is a RATCHET — every
				// nose-up disturbance becomes the new setpoint, deceleration
				// trims nose-up, and by ~11° the wing re-flies (the touchdown
				// bounce, live-traced at #72). At idle the nose flies gently
				// down instead; the catapult and takeoffs run power and are
				// untouched.
				demand -= 2.5 * math.Pi / 180
			}
			f.Integral = 0
			f.Reference = pitch // leave the deck holding the deck attitude
		}
		errorTerm := (demand-a)*2.2 - q*1.8
		f.Integral = clamp(f.Integral+errorTerm*0.45*Dt, -0.45, 0.45) // clamp re-sized for the honest single-count droop moment (the old ±0.3 pinned alpha 2.5° shy of on-speed)
		stabTarget = -(errorTerm*0.34 + f.Integral) - stick*0.10      // direct stick path, like the UA feedforward: the surface bites immediately while the alpha loop trims behind it — without it PA full stick moved the stabilator ~2° and read as dead elevators
		// Hold-then-washout: the real TEF schedule HOLDS the commanded setting
		// through the approach band and retracts approaching the flap limit —
		// the old linear fade left only ~2/3 droop at on-speed ("flaps up" on
		// a slightly fast approach) and nothing by 250 kt.
		schedule := clamp((c.Droop.Pressure-pressure)/(c.Droop.Pressure*0.55), 0, 1) // full below ~0.45·P, gone at P
		droopTarget = c.Droop.Angle * schedule
		slatFloor = 12 * math.Pi / 180 * schedule // NATOPS flaps HALF droops the LEADING edge too (12°)
		brakeTarget = 0                           // the landing configuration auto-retracts the speedbrake (NATOPS: flap extension retracts the board)
		// Wing leveler on deck: as lift builds down the stroke the wheels
		// unload and the crosswind's rolling moment grows — with no roll
		// channel the jet left the catapult at 17° bank, 1 rad/s (measured).
		// The real FCS wing-levels on the cat; stick can still command roll.
		up := m.State.Attitude.Rotate(Vec3{Y: 1})
		starboard := m.State.Attitude.Rotate(Vec3{Z: 1})
		bank := math.Atan2(starboard.Y, up.Y)                                  // heading-independent roll: the old atan2(-up.Z, up.Y) is world-frame and reads pitch as PHANTOM BANK on any off-axis heading — on the carrier strip (~30 deg off world X) at the trap runout's -8 deg pitch the leveler chased ~4 deg of fiction at gain 2.5 and ground-looped the rollout (#72 scenario 9)
		flapTarget = clamp(lateral+bank*2.5-m.State.Omega.X*1.2, -1, 1) * 0.30 // +bank: right roll gives bank<0 and needs a left (negative) command
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
	} else {
		// Up and away: C* command with the carefree limiter.
		ceiling := m.Airframe.Limit.Positive
		if in.Override {
			ceiling = m.Airframe.Limit.Override
		}
		floor := m.Airframe.Limit.Negative
		// Neutral-stick feedforward: the load that holds the current flight
		// path (cos γ); the attitude-hold below owns the actual behaviour.
		gamma := math.Asin(clamp(m.State.Velocity.Y/math.Max(speed, 1), -1, 1))
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		theta := math.Asin(clamp(forward.Y, -1, 1))
		up := m.State.Attitude.Rotate(Vec3{Y: 1})
		upright := clamp(up.Y, -1, 1) // gravity's share of the sensed load: the steady-manoeuvre pitch rate is (g/V)·(n − upright) in ANY attitude (upright ≈ 1 wings level, ≈ 1/n in a level turn, −1 inverted)
		level := math.Cos(gamma)
		level -= clamp((a-0.15)*5, 0, 0.8) // alpha backstop: the nose falls rather than mushing when too slow
		// Stick-free = ATTITUDE HOLD: the one coherent neutral-stick concept.
		// While the stick is displaced, the held reference follows the jet;
		// on release (once the pitch rate settles) it freezes, and the error
		// feeds the rate loop below. This replaced a tower of stacked bias
		// terms (path-hold, level-seek, trim-speed) whose interactions drifted
		// the nose and wandered on an undamped phugoid.
		// Peak ratchet: the reference follows the nose while it moves AWAY
		// (stick in, or coasting after release), and freezes the instant the
		// motion reverses — the nose stops exactly where it peaks.
		flying := clamp(math.Abs(stick)*3.3, 0, 1)
		if flying > 0 {
			f.Reference = theta
		} else {
			// After release the reference CHASES the nose at 85% of the pitch
			// rate (deadbanded): it rides the coast and pins where motion
			// dies — no fixed lead to over- or under-predict the stop. A
			// powered pitch-up outruns the chase, the gap grows, and the
			// hold arrests it, so it cannot be ratcheted around a loop.
			chase := 0.92 * math.Max(0, math.Abs(q)-0.015) * Dt
			if ext := m.State.Gear.Extension; ext > 0.02 && ext < 0.98 {
				chase = 0 // configuration change in transit: hold the datum FIRM — the trim is re-learning (decayed across the law switch), and chasing the un-trimmed sag walked the flyaway climb down to bare-airframe trim (the post-launch sudden pitch-down)
			}
			f.Reference += clamp(theta-f.Reference, -chase, chase)
			if in.Throttle > 0.85 && m.State.Position.Y < 150 {
				// Launch/waveoff condition in the CLEAN law too: hands-off at high
				// power near the water, the datum eases up to the flyaway attitude —
				// a prompt gear-up (the real technique) no longer abandons the flyaway
				// capture half-done.
				f.Reference = math.Min(math.Max(f.Reference, theta), math.Max(f.Reference, c.Flyaway-0.5*math.Pi/180)) // never yank it above where it is heading
				f.Reference += clamp(c.Flyaway-f.Reference, 0, 0.07*Dt)
			}
		}
		hold := clamp((f.Reference-theta)*2.0, -0.35, 0.35) - q*0.7 - clamp((a-0.30)*1.5, 0, 0.5)
		demand := level
		if stick >= 0 {
			demand = level + stick*(ceiling-level)
		} else {
			demand = level + stick*(level-floor)
		}
		// Onset shaping: the demand slews at ~15 g/s, so a stick slam builds
		// load the loops can track instead of a step they chase. (No zero-
		// means-fresh sentinel here: a full push slews THROUGH exactly zero,
		// and a sentinel reset turns it into a 1→0→1 loop that silently
		// refuses every negative-g command.)
		// Law blend across the gear transition: the PA law caps full stick
		// near approach alpha; the UA law gives it the full g ceiling. With
		// the stick held through gear retraction the command used to STEP —
		// the jet snapped 23°/s nose-up at gear-up. The ceiling now opens
		// with the gear (Extension 1→0 over ~2 s), as the real law fader does.
		if m.State.Gear.Extension > 0.02 && speed < 130 {
			demand = math.Min(demand, level+(ceiling-level)*(1-m.State.Gear.Extension))
		}
		f.Demand += clamp(demand-f.Demand, -25*Dt, 25*Dt)
		demand = f.Demand
		// Cascaded pitch: the g error commands a PITCH RATE, and the carefree
		// limits shape that rate demand — it fades to zero approaching the g
		// and alpha boundaries and goes negative beyond them, so the limiter
		// is a smooth property of the command path, not a switched override.
		// A fast inner rate loop owns the (very powerful) stabilator.
		// C* proper: blend pitch rate into the feedback at the classic
		// crossover (Vco 122 m/s). Below crossover the q term dominates, so
		// releasing the stick holds ATTITUDE; a pure-nz error re-acquires
		// the lagging flight path — the jet visibly snaps back to the pitch
		// it had before the input. The command is scaled by the same blend
		// so a sustained pull still reaches the commanded g exactly.
		vco := 122.0
		// The command-side blend must mirror the steady rate the feedback will
		// carry: (g/V)·(demand − upright). Anchored to `level` (the q = 0
		// straight-flight case) it under-compensated every steady TURN, and
		// the C* fixed point sat (Vco/V)/(1+Vco/V)·level ≈ 0.3-0.45 g below
		// the ceiling — a full-stick pull parked at 7.2 g, measured, with the
		// g-trim integral dead because star (its only drive) was already zero.
		star := (demand + vco/math.Max(speed, 60)*(demand-upright)) - (m.State.Fcs.Normal + vco/gravity*q)
		rateBound := math.Min(1.0, 150/math.Max(speed, 60)) // ~0.58 rad/s at 260 m/s, opening up low and slow
		// The g error commands the rate that closes it at a fixed loop
		// bandwidth: a rad/s of pitch rate yields V/g g's, so the gain must
		// carry g/V or the loop crossover climbs with speed past the alpha
		// lag and limit-cycles about 1g.
		gain := 30 / (math.Max(speed, 60) + vco) // biased hot at low speed: fully normalised the nz tracking went sloppy below ~150 kt and the phugoid ballooned                                       // normalised by the C* blend: star is scaled by (V+Vco)/V, so this keeps the nz-loop crossover speed-invariant (unnormalised, low-speed gain tripled = residual oscillation)
		delta := clamp(star, -0.25, 0.25)
		if star*f.Integral < 0 {
			delta = clamp(star, -1.5, 1.5) // unwinding: release trim fast — clamping both ways held wound-up nose-up trim through a deceleration (the low-power balloon)
		}
		fast := 0.0 // the boundary-hold rate: a full-stick pull PINS the demand at the g limit, and only there does the trim need to hurry — at a flat 0.3 sustained pulls parked ~1 g short for ~15 s; anything error-based misfires on fine tracking, whose small stick wiggles are LARGE demand swings
		if f.Demand > ceiling-0.5 || f.Demand < floor+0.5 {
			fast = 0.7
		}
		f.Integral = clamp(f.Integral+flying*delta*gain*(0.3+fast)*Dt, -0.5, 0.5) // trim learns only while the stick flies the jet; stick-free the attitude loop owns the state // conditional integration: trim tracks steady errors but big transients don't wind it (release-bounce). ERROR-ADAPTIVE rate (#131): the gentle 0.3 keeps fine tracking calm, but against a large persistent error it triples — at a flat 0.3 the boundary trim needed ~15 s to close the last g and sustained pulls parked at 6.5
		// Kinematic feedforward: sustaining n at this speed already needs
		// q = (g/V)·(n − upright) before any closure. Without it the star
		// gain path budgeted ~0.25 rad/s while a 7.5 g turn at 240 m/s owes
		// 0.31 sustained, so the g-trim integral spent five seconds winding
		// up to afford the rate the turn itself owed — the post-bite 0.1 g/s
		// crawl that made every full-stick pull arrive after the energy was
		// gone. The feedforward reads the DEMAND, never the sensed n (fed
		// from the sensed load an overshoot sustains itself — the boundary
		// became a ±0.5 g relaxation oscillator, measured), and it REPLACES
		// the old stick·rateBound surface-bite term: stacked on top of the
		// full kinematic rate that term parked pulls above the commanded g.
		steady := (m.State.Fcs.Normal - upright) * gravity / math.Max(speed, 60)
		excess := q - steady
		wanted := (demand - upright) * gravity / math.Max(speed, 60)
		rateDemand := clamp(flying*(wanted+star*gain+f.Integral)+(1-flying)*hold, -rateBound, rateBound) // the trim integral belongs INSIDE the flying mode: frozen outside the blend it biased the stick-free rate demand, parking the nose Integral/1.2 rad above the held reference (the push-release rebound)
		// Rate anticipation on the EXCESS pitch rate only: q above the steady
		// turn rate n·g/V is what is still building g. Penalising total q made
		// the limiter park a full g below the ceiling in a sustained pull.
		// The carefree caps are rate headroom ABOVE the steady-manoeuvre rate.
		// Referenced to zero they forced q below the steady turn rate as n
		// approached the ceiling — solve (ceiling−n)·0.9 = (g/V)(n−upright)
		// and the pull parks at 7.0-7.2 g — the second half of the
		// never-pegs-7.5 defect.
		capG := steady + (ceiling-m.State.Fcs.Normal)*0.9 - excess*(pressure/14000)
		capA := (m.Airframe.Limit.Alpha - a) * 2.2
		capFloor := steady + (m.Airframe.Limit.Negative-m.State.Fcs.Normal)*0.9 - excess*(pressure/14000) // mirrored anticipation: without it the negative boundary chatters
		capB := (-m.Airframe.Limit.Floor - a) * 2.2                                                       // negative-alpha protection: at low q̄ the -3g floor is unreachable and an unbounded push winds the wing into deep negative stall (mushy, ballistic pushover)
		shaped := clamp(rateDemand, math.Max(capFloor, capB), math.Min(capG, capA))
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
		saturated := math.Abs(f.Stabilator.Left) > 0.95*c.Throw.Down*clamp(c.Blowdown/math.Max(pressure, 1), 0, 1)
		if !saturated {
			// The trim integrator is the surface's slow walker along the
			// alpha-trim curve (~0.7°/α° of stabilator), and at 0.25 it was
			// the last-g bottleneck: the pull's tail closed at the trim's
			// ~0.7°/s, not the actuator's 40°/s. Stick-flown it triples;
			// hands-off keeps the calm rate. (The P path must NOT get the
			// same treatment — 3× P drove the actuator's rate limit into a
			// ±0.5 g limit cycle, measured.)
			f.Trim = clamp(f.Trim+inner*(0.25+0.50*flying)*authority*Dt, -0.35, 0.35)
		}
		command := -(inner*0.30*authority + f.Trim)
		// Overstress accounting for the damage model: exposure beyond the
		// positive and negative g limits, plus an overspeed term above the
		// airframe's placard (~740 KCAS equivalent) — battle converts the
		// accumulated exposure into structural weakness.
		if m.State.Fcs.Normal > m.Airframe.Limit.Positive {
			m.State.Damage.Stress += (m.State.Fcs.Normal - m.Airframe.Limit.Positive) * Dt
		}
		if m.State.Fcs.Normal < m.Airframe.Limit.Negative {
			m.State.Damage.Stress += (m.Airframe.Limit.Negative - m.State.Fcs.Normal) * Dt
		}
		if equivalent := speed * math.Sqrt(local.Density/1.225); equivalent > 380 {
			m.State.Damage.Stress += (equivalent - 380) * 0.02 * Dt
		}
		stabTarget = command
		// AUTO manoeuvring flaps: the trailing edge droops with alpha and
		// washes out with dynamic pressure — the FCS reshapes the wing
		// through a turn, exactly as the real jet's AUTO flap mode does.
		droopTarget = clamp(c.Flap.Slope*(a-c.Flap.Offset), 0, c.Flap.Limit) * clamp(1-pressure/c.Flap.Pressure, 0, 1)
		// Roll-rate command, tempered at low speed and high alpha.
		limit := 3.8 * clamp(speed/200, 0.35, 1) * clamp(1-0.9*a/m.Airframe.Limit.Alpha, 0.08, 1)
		limit *= clamp(1-math.Abs(b)/0.30, 0.05, 1) // sideslip strips roll authority: no spin fuel
		flapTarget = (lateral*limit - p) * 0.22
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
	}

	// Leading-edge flaps schedule with alpha (plus the PA floor set in the gear-down branch).
	slatTarget := math.Max(clamp(c.Slat.Slope*(a-c.Slat.Offset), 0, c.Slat.Limit), slatFloor)
	if m.Direct {
		slatTarget = 0
	}

	// Blowdown: available deflection falls with dynamic pressure.
	available := clamp(c.Blowdown/math.Max(pressure, 1), 0, 1)

	// Actuators: rate-limited slew toward the commanded positions.
	slew := func(current float64, target float64, rate float64, limit float64) float64 {
		bound := limit * math.Min(available, 1)
		target = clamp(target, -bound, bound)
		return current + clamp(target-current, -rate*Dt, rate*Dt)
	}
	symmetric := clamp(stabTarget, -c.Throw.Down, c.Throw.Up)
	differential := clamp(flapTarget, -0.35, 0.35)
	// Battle damage: a jammed actuator slews slower, and a fully jammed one
	// freezes AT ITS CURRENT DEFLECTION — the surface holds whatever it was
	// commanding when hit, and the FCS fights it with the others.
	d := &m.State.Damage
	f.Stabilator.Left = slew(f.Stabilator.Left, symmetric+0.25*differential, c.Rate.Stabilator*d.jam(ChannelStabilatorLeft), c.Throw.Down)
	f.Stabilator.Right = slew(f.Stabilator.Right, symmetric-0.25*differential, c.Rate.Stabilator*d.jam(ChannelStabilatorRight), c.Throw.Down)
	f.Flaperon.Left = slew(f.Flaperon.Left, clamp(droopTarget+differential, -c.Throw.Flaperon.Up, c.Throw.Flaperon.Down), c.Rate.Flaperon*d.jam(ChannelFlaperonLeft), c.Throw.Flaperon.Down)
	f.Flaperon.Right = slew(f.Flaperon.Right, clamp(droopTarget-differential, -c.Throw.Flaperon.Up, c.Throw.Flaperon.Down), c.Rate.Flaperon*d.jam(ChannelFlaperonRight), c.Throw.Flaperon.Down)
	f.Rudder = slew(f.Rudder, rudderTarget, c.Rate.Rudder*d.jam(ChannelRudder), c.Throw.Rudder)
	f.Slat += clamp(slatTarget-f.Slat, -c.Rate.Slat*d.jam(ChannelSlat)*Dt, c.Rate.Slat*d.jam(ChannelSlat)*Dt)
	f.Flap = f.Flaperon.Left*0 + droopTarget // droop is carried inside the flaperon targets; keep the readout
	f.Speedbrake += clamp(brakeTarget-f.Speedbrake, -c.Rate.Brake*d.jam(ChannelSpeedbrake)*Dt, c.Rate.Brake*d.jam(ChannelSpeedbrake)*Dt)
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
	// and coordination follows the roll (-interconnect). The PEDAL term is
	// negated: +pedal is "nose right" everywhere else (nosewheel steering,
	// Direct gearing, the interconnect's convention), and nose right is
	// negative rudder in this geometry.
	// The pedal commands rudder DIRECTLY, and the damper/beta terms fade as
	// it is applied: as a beta command the rudder kicked, washed back to
	// the small deflection holding ~3° of sideslip, then wobbled on the
	// dutch roll — held pedal now holds deflection.
	throw := m.Airframe.Control.Throw.Rudder
	weight := 1 - 0.75*math.Abs(pedal)
	return clamp(-pedal*throw*0.85+(damped*1.2-b*3.4-interconnect)*weight, -throw, throw)
}

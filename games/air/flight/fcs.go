// Mochi world: Flight control system
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// An augmenter and limiter on an airframe that flies honestly bare-handed: C*
// pitch with auto-trim, roll-rate command, yaw damping, scheduled slats, a
// powered-approach mode, and the carefree limiter. Model.Direct bypasses all.

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

	// The configuration gates below are CALIBRATED airspeed, not TAS: the real
	// gear/flap regime is CAS and a TAS gate would move with altitude. EAS stands
	// in - the compressibility gap is negligible below M0.5.
	calibrated := math.Sqrt(2 * pressure / 1.225)

	stick := clamp(in.Pitch, -1, 1)
	lateral := clamp(in.Roll, -1, 1)
	pedal := clamp(in.Yaw, -1, 1)

	var stabTarget, flapTarget, rudderTarget, droopTarget, slatFloor float64
	brakeTarget := clamp(in.Speedbrake, 0, 1)

	// Law selection with hysteresis: enter PA below 125 m/s, leave above 128.6.
	// Launder the integral across ANY law change - it means a pitch-RATE bias
	// up-and-away and a direct stabilator add in PA.
	pa := m.pa
	if !m.lawInit {
		pa = in.Gear && calibrated < 130
		m.pa = pa // initialisation is NOT a law change: leaving m.pa at its zero value made the first step of every fresh model read as a flip and launder the trim for its first two seconds (TestTrap's scripted pass missed the wires)
		m.lawInit = true
	} else if pa {
		// The virtual flap switch (NATOPS): the FLAPS own the configuration, not the
		// gear handle. AUTO is selected passing 180 KCAS clean, so a gear-up climb
		// keeps takeoff flap; 128.6 m/s is the overspeed cap.
		if (!in.Gear && calibrated > 92.6) || calibrated > 128.6 { // 128.6 m/s = the 250 KCAS flap placard (NATOPS figure 4-2); 135 sat 12 kt over it (#49)
			pa = false
		}
	} else if in.Gear && calibrated < 125 {
		pa = true
	}
	if !pa {
		m.halfleg = false // the clean-up ended the takeoff leg; the next PA entry is an approach
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

	// The trim hat's roll half: a standing differential-flaperon bias walked
	// at a fixed rate while held, for asymmetry the roll law would otherwise
	// fight forever. The reset is the whole hat: both datums to zero and the
	// attitude hold re-datumed to here (idempotent, so a held key is safe).
	if in.Lean != 0 {
		f.Bank = clamp(f.Bank+clamp(in.Lean, -1, 1)*0.02*Dt, -0.06, 0.06)
	}
	if in.Reset {
		f.Datum, f.Bank = 0, 0
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		f.Reference = math.Asin(clamp(forward.Y, -1, 1))
	}

	if m.Direct {
		// Geared surfaces, no augmentation — the bare-airframe validation path.
		stabTarget = -stick * c.Gearing.Pitch
		flapTarget = lateral * c.Gearing.Roll
		rudderTarget = -pedal * c.Gearing.Yaw
		f.Integral = 0
	} else if m.pa {
		// Powered approach: the stick commands alpha about on-speed, capped at the
		// alpha LEVEL FLIGHT needs in the CURRENT configuration (droop lift
		// included), converging on on-speed exactly at on-speed. The stick is
		// squared, sign-preserved: linear is unflyable on a keyboard.
		fine := stick * math.Abs(stick)
		// The pitch trim switch, PA sense: it biases the ALPHA datum (the real
		// law trims an on-speed AoA reference), so "trim to on-speed, fly the
		// ball with power" is a real technique here rather than an automatic.
		if in.Trim != 0 {
			f.Datum = clamp(f.Datum+clamp(in.Trim, -1, 1)*0.012*Dt, -4*math.Pi/180, 4*math.Pi/180)
		}
		droopTarget, slatFloor = m.Approaching(pressure)
		// The takeoff configuration is LATCHED, not read off the gear handle: HALF
		// flap set on deck and held through the clean-up climb, so gear-up cannot
		// halve the droop nor gear-down grant FULL off the bow.
		if m.State.Gear.Wow && m.State.Velocity.Length() < 40 {
			m.halfleg = true
		}
		if in.Flap >= 2 {
			// FULL selected: the heavy shot's and the short field's setting —
			// honoured even on deck.
		} else if m.halfleg || in.Flap >= 1 {
			// Takeoff flap: HALF from the deck through the clean-up climb, or when
			// selected. Scaled before the lift-cap maths so the cap prices the droop
			// actually flying.
			droopTarget *= c.Droop.Half
		}
		schedule := droopTarget / math.Max(c.Droop.Angle, 1e-9)
		need := m.mass * gravity / math.Max(pressure*m.Airframe.Reference.Area, 1)
		grade := clamp((need-c.Droop.Lift*schedule)/4.5, 0, c.Onspeed) // 4.5/rad: the TRIMMED lift slope (stabilator download included) fit through the on-speed anchor — see Droop.Lift
		// The cap serves the ARRIVE-DIRTY regime, not the ball: near on-speed `need`
		// moves as 1/v² and a hard min() walks the datum with every power correction.
		// Blended ~160-190 kt: AoA-referenced inside, level-flight auto-trim beyond.
		anchor := c.Droop.Lift + 4.5*c.Onspeed // the trimmed on-speed CL, from the same fit
		blend := clamp((0.80*anchor-need)/(0.18*anchor), 0, 1)
		level := c.Onspeed - blend*math.Max(c.Onspeed-grade, 0)
		demand := level + fine*(9*math.Pi/180) + f.Datum
		// Flyaway attitude capture: hands-off after a catapult shot the real
		// FCS settles at the trim-board flyaway datum (c.Flyaway, 16°) rather than riding approach alpha
		// into a full-burner zoom. Binds only when pitch exceeds the datum;
		// the approach (low attitude, low power) never feels it.
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		pitch := math.Asin(clamp(forward.Y, -1, 1))
		f.Reference = pitch // keep the attitude-hold datum CURRENT: crossing the 130 m/s law boundary otherwise handed the UA hold a stale deck pitch, and it flew the nose back down from the flyaway attitude ("suddenly pitches down" a few seconds after launch)
		capture := a + (c.Flyaway - pitch)
		if in.Throttle > 0.85 {
			// Launch/waveoff power: the flyaway datum is an ATTRACTION, not just a cap -
			// the hands-off climb tops out below it otherwise.
			demand = math.Max(demand, math.Min(capture, c.Onspeed+2*math.Pi/180))
		}
		demand = math.Min(demand, math.Max(capture, 0)+fine*(22*math.Pi/180)) // the capture yields to a DELIBERATE pull: at neutral stick it pins the flyaway attitude, but its stick opening (22°) outruns the main demand's (9°), so pulling past ~half stick clears the cap entirely — it no longer fought the climb-out (post-launch "unresponsive then suddenly alive")
		if m.State.Gear.Wow {
			// Ground mode: the alpha law would wind the stabilator full nose-up down the
			// stroke (deck alpha is far below approach alpha) and rotate the jet off the
			// shuttle. Follow current alpha instead.
			demand = a + fine*(12*math.Pi/180) // full aft stick rotates ~12° above deck alpha — field takeoffs need real rotation authority
			if in.Throttle < 0.3 && m.State.Gear.Wire < 0 {
				// Rollout derotation: pure alpha-follow is a RATCHET - every nose-up
				// disturbance becomes the setpoint and by ~11° the wing re-flies. At idle
				// the nose flies gently down instead.
				demand -= 2.5 * math.Pi / 180
			}
			f.Integral = 0
			f.Reference = pitch // leave the deck holding the deck attitude
		}
		errorTerm := (demand-a)*2.2 - q*1.8
		f.Integral = clamp(f.Integral+errorTerm*0.45*Dt, -0.45, 0.45) // clamp re-sized for the honest single-count droop moment (the old ±0.3 pinned alpha 2.5° shy of on-speed)
		stabTarget = -(errorTerm*0.34 + f.Integral) - fine*0.10       // direct stick path, like the UA feedforward: the surface bites immediately while the alpha loop trims behind it — without it PA full stick moved the stabilator ~2° and read as dead elevators
		brakeTarget = 0                                               // the landing configuration auto-retracts the speedbrake (NATOPS: flap extension retracts the board)
		// Wing leveler on deck: as lift builds down the stroke the wheels
		// unload and the crosswind's rolling moment grows — with no roll
		// channel the jet left the catapult at 17° bank, 1 rad/s (measured).
		// The real FCS wing-levels on the cat; stick can still command roll.
		up := m.State.Attitude.Rotate(Vec3{Y: 1})
		starboard := m.State.Attitude.Rotate(Vec3{Z: 1})
		bank := math.Atan2(starboard.Y, up.Y) // heading-independent roll: the old atan2(-up.Z, up.Y) is world-frame and reads pitch as PHANTOM BANK on any off-axis heading — on the carrier strip (~30 deg off world X) at the trap runout's -8 deg pitch the leveler chased ~4 deg of fiction at gain 2.5 and ground-looped the rollout (#72 scenario 9)
		leveler := 0.0
		if m.State.Gear.Wow || m.State.Gear.Catapult >= 0 {
			leveler = bank * 2.5 // the wing leveler belongs to the DECK alone (its own comment always said so): airborne it fought every bank the pilot held at gain 2.5, so entering PA in the turn to final snapped the jet toward wings-level at 1.5 rad/s — the uncommanded roll the pilot reported at ~250 KCAS, which is exactly the PA entry speed with the gear down
		}
		flapTarget = clamp(lateral+leveler-m.State.Omega.X*1.2, -1, 1)*0.30 + f.Bank // +bank: right roll gives bank<0 and needs a left (negative) command; the roll-trim datum rides outside the clamp so full stick retains full authority
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
	} else {
		// Up and away: C* command with the carefree limiter. The symmetric limits
		// schedule with gross weight (NATOPS) - the placard's g is written at
		// Limit.Reference; the paddle rides the same schedule.
		schedule := 1.0
		if m.Airframe.Limit.Reference > 0 {
			schedule = math.Min(1, m.Airframe.Limit.Reference/m.mass)
		}
		ceiling := m.Airframe.Limit.Positive * schedule
		// Rolling reduction (#46, NATOPS 11.1.7/2.8.2.3): commanded load falls to 80%
		// NzREF from a quarter of lateral stick to full - a rolling pull is limited
		// BELOW a straight one.
		ceiling *= 1 - 0.2*clamp((math.Abs(in.Roll)-0.25)/0.75, 0, 1)
		if in.Override {
			ceiling = m.Airframe.Limit.Override * schedule // the paddle defeats the limiter, rolling reduction included
		}
		// The negative command floor is FIXED at -3 g for all gross weights
		// (NATOPS 11.1.7) — only the positive side schedules with mass.
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
		// Stick-free = ATTITUDE HOLD. While the stick is displaced the held reference
		// follows the jet; on release it freezes and the error feeds the rate loop.
		// It follows the nose only while motion moves AWAY.
		flying := clamp(math.Abs(stick)*3.3, 0, 1)
		if flying > 0 {
			f.Reference = theta
		} else if in.Trim != 0 {
			// The pitch trim switch, UA sense: stick-free it walks the held
			// attitude datum — the chase pauses so the nudge is not undone.
			f.Reference = clamp(f.Reference+clamp(in.Trim, -1, 1)*0.0105*Dt, -0.6, 0.6)
		} else {
			// After release the reference CHASES the nose at a fraction of the pitch
			// rate (deadbanded), pinning where motion dies. A powered pitch-up outruns
			// the chase, so it cannot ratchet around a loop.
			chase := 0.92 * math.Max(0, math.Abs(q)-0.015) * Dt
			if ext := m.State.Gear.Extension; ext > 0.02 && ext < 0.98 {
				chase = 0 // configuration change in transit: hold the datum FIRM — the trim is re-learning (decayed across the law switch), and chasing the un-trimmed sag walked the flyaway climb down to bare-airframe trim (the post-launch sudden pitch-down)
			}
			f.Reference += clamp(theta-f.Reference, -chase, chase)
			if in.Throttle > 0.85 && m.State.Position.Y < 150 {
				// Launch/waveoff in the CLEAN law too: hands-off at high power near the
				// water, the datum eases up to the flyaway attitude.
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
		// Demand shaping, asymmetric: onset slews at 25 g/s, release at double,
		// judged against `level`. No zero-means-fresh sentinel - a full push slews
		// THROUGH zero. The ceiling opens with the gear (Extension 1→0), so a stick
		// held through retraction does not step to the full limit.
		if m.State.Gear.Extension > 0.02 && calibrated < 130 {
			demand = math.Min(demand, level+(ceiling-level)*(1-m.State.Gear.Extension))
		}
		if m.State.Gear.Extension > 0.02 {
			demand = math.Min(demand, 2.0) // gear in transit or down: +2.0 g structural cap (NATOPS 4.1.8), at ANY speed — the fast gear-down pull was uncapped
		}
		shaping := 25.0
		if math.Abs(demand-level) < math.Abs(f.Demand-level) {
			shaping = 50
		}
		asked := demand // the stick's own ask, pre-shaping: the release window below is scoped on sensed-minus-ASKED, which is zero in a steady tracked turn and large only when the stick has actually come off a pull
		f.Demand += clamp(demand-f.Demand, -shaping*Dt, shaping*Dt)
		demand = f.Demand
		// Cascaded pitch: the g error commands a PITCH RATE, and the carefree limits
		// shape that demand - the limiter is a smooth property of the command path,
		// not a switched override. C*: pitch rate blends in at the Vco 122 m/s
		// crossover, command scaled by the same blend.
		vco := 122.0
		// The command-side blend must mirror the steady rate the feedback will carry:
		// (g/V)·(demand − upright). Anchored to `level` instead it under-compensates
		// every steady turn and the C* fixed point droops.
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
		// Kinematic feedforward: sustaining n at this speed already needs q =
		// (g/V)·(n − upright) before any closure. It reads the DEMAND, never the
		// sensed n - fed the sensed load an overshoot sustains itself and the
		// boundary becomes a relaxation oscillator.
		steady := (m.State.Fcs.Normal - upright) * gravity / math.Max(speed, 60)
		excess := q - steady
		wanted := (demand - upright) * gravity / math.Max(speed, 60)
		// The RELEASE transient flies the g loop, not the hold: release means 1 g and
		// the g feedback kills the rotation at surface bandwidth. Blending on the
		// DEMAND's distance from level scopes it to that moment.
		release := clamp(math.Abs(m.State.Fcs.Normal-asked)/0.8, 0, 1) * (1 - flying) // sensed minus ASKED: |n−level| also fired during fine tracking in a turn (high g, light corrective stick) and detuned the tracking law at exactly the on-the-pipper moments — the veteran's gunnery went 0/12 on the conversion referendum before this scoping
		blend := math.Max(flying, release)
		// The trim integral rides at its own stick weight, OUTSIDE the blend product:
		// inside it the weight becomes flying² and partial-stick trim goes limp. At
		// full release it stays out entirely.
		rateDemand := clamp(blend*(wanted+star*gain)+flying*f.Integral+(1-blend)*hold, -rateBound, rateBound)
		// Rate anticipation on the EXCESS pitch rate only: q above the steady turn
		// rate is what is still building g. The carefree caps are rate headroom ABOVE
		// it - referenced to zero, the pull parks below ceiling.
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
		// Low-q tracking damper, WASHED OUT (acceptance: pio_test.go). Plain damping
		// clears the tracking band but breaks the idle-decel sink arrest - both live
		// in the same q band; the washout separates them.
		washed := excess - m.State.Fcs.Pitchwash
		m.State.Fcs.Pitchwash += (excess - m.State.Fcs.Pitchwash) * Dt / 0.8
		shaped -= washed * 0.75 * clamp((20000-pressure)/14000, 0, 1)
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
			// The trim integrator walks the alpha-trim curve; stick-flown its rate
			// triples so the pull's tail is not trim-limited. The P path must NOT get
			// the same treatment - 3× P limit-cycles the actuator.
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
		// NATOPS 2.8.4.8: airborne in AUTO FLAPS UP the speedbrake retracts itself
		// above 6.0 g or 28° alpha. The game's brake command is maintained, so the
		// board re-extends when the condition clears.
		if !m.State.Gear.Wow && (m.State.Fcs.Normal > 6.0 || a > 28*math.Pi/180) {
			brakeTarget = 0
		}
		// Roll-rate command, tempered at low speed and high alpha.
		limit := 3.8 * clamp(speed/200, 0.35, 1) * taper(a, m.Airframe.Limit.Alpha)
		limit *= clamp(1-math.Abs(b)/0.30, 0.05, 1) // sideslip strips roll authority: no spin fuel
		// R-LIM (NATOPS 2.8.2.8): wing-pylon tanks or air-to-ground stores
		// with their rack hooks closed cut the maximum roll rate by about a
		// third. The catalog flags the stores that engage it, so the
		// reduction rides the live mask and ends when the tanks depart.
		for i := range m.Airframe.Stores {
			if m.Airframe.Stores[i].Limit.Roll && m.stores&(1<<uint(i)) != 0 {
				limit *= 0.67
				break
			}
		}
		// Rudder-to-rolling-surface interconnect (NATOPS 11.1.8): above 25° AoA pedal
		// and lateral stick roll alike, so the pedal feeds the same roll-rate
		// command, blended in across 25-35° alpha.
		rolling := clamp(lateral+pedal*clamp((a-0.44)/0.17, 0, 1), -1, 1)
		flapTarget = (rolling*limit-p)*0.22 + f.Bank // the roll-trim datum rides outside the rate loop: a rate-command law re-trims itself, so the datum acts as a standing surface bias, exactly like the real jet's trim follow-up
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
		// Insufficient-airspeed departure (NATOPS 11.1.8.1): the one departure the
		// FCS does not prevent. A seeded wander on rudder and stabilator, fading in
		// below ~2.5 kPa. Seeded so prediction replays it exactly.
		if ballistic := clamp((2500-pressure)/1500, 0, 1); ballistic > 0 && !m.State.Gear.Wow {
			cycle := m.State.Time
			slew := func(index uint64, fast float64) float64 {
				return 0.6*math.Sin(fast*cycle+6.283*noise(m.Environment.Seed, index)) + 0.4*math.Sin(1.7*fast*cycle+6.283*noise(m.Environment.Seed, index+1))
			}
			rudderTarget += ballistic * slew(700, 0.5) * c.Throw.Rudder
			stabTarget += ballistic * slew(702, 0.4) * 0.7 * c.Throw.Down
			flapTarget += ballistic * slew(704, 0.6) * c.Throw.Flaperon.Down
		}
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

// taper is the roll command's alpha schedule: authority tapers as alpha rises
// toward the limiter, then holds at its 35° value - NATOPS 11.1.8 has roll
// performance "essentially constant" above 35° alpha.
func taper(a float64, limit float64) float64 {
	return clamp(1-0.9*a/limit, 1-0.9*(35*math.Pi/180)/limit, 1)
}

func (m *Model) yaw(pedal float64, lateral float64, a float64, b float64, r float64, f *FcsState) float64 {
	f.Washout += (r - f.Washout) * Dt / 1.0
	damped := r - f.Washout
	// RSRI (NATOPS 2.8.2.8): the interconnect schedules with increasing
	// alpha AND decreasing airspeed — slow and nose-high is where the rudder
	// does the rolling and the coordination.
	slow := 1 + 0.6*clamp((90-m.State.Velocity.Subtract(m.gust).Length())/60, 0, 1)
	interconnect := lateral * clamp(a/0.35, 0, 1) * 0.35 * slow
	// Pedal authority GROWS with alpha (NATOPS 2.8.2.8: half throw at low to
	// medium AoA, full throw available at high) — it faded to 10% at 40°
	// before, the exact opposite schedule, leaving the Hornet's nose-pointing
	// tool inert everywhere it matters (#45).
	pedal *= 0.55 + 0.45*clamp(a/0.55, 0, 1)
	// Signs under the -side rudder geometry (positive rudder yaws the nose LEFT):
	// +damped follows r, -b steers away from beta, -interconnect follows the roll,
	// and the PEDAL term is negated because +pedal is "nose right" everywhere
	// else. The pedal commands rudder DIRECTLY; damper and beta fade.
	throw := m.Airframe.Control.Throw.Rudder
	weight := 1 - 0.75*math.Abs(pedal)
	return clamp(-pedal*throw*0.85+(damped*1.2-b*3.4-interconnect)*weight, -throw, throw)
}

// Approaching reports the trailing-edge droop and slat floor the PA law
// commands at a dynamic pressure. Shared with the Approach trim helper so a
// spawn cannot disagree with the FCS. Holds through the band, then washes out.
func (m *Model) Approaching(pressure float64) (droop float64, slat float64) {
	c := &m.Airframe.Control
	schedule := clamp((c.Droop.Pressure-pressure)/(c.Droop.Pressure*0.55), 0, 1) // full below ~0.45·P, gone at P
	return c.Droop.Angle * schedule, 12 * math.Pi / 180 * schedule               // NATOPS droops the LEADING edge with the flaps (12°)
}

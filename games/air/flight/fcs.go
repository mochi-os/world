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

	// The configuration gates below are CALIBRATED airspeed, not TAS: the real
	// gear/flap regime is CAS, and a TAS gate would move with altitude (gear
	// down at height entered PA at the wrong dynamic pressure). EAS stands in
	// for CAS — the compressibility gap is negligible below M0.5 where these
	// gates live. At sea level it equals TAS, so the calibrated anchors hold.
	calibrated := math.Sqrt(2 * pressure / 1.225)

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
	// unpatched speed path. Enter PA below 125 m/s, leave above 128.6 (no
	// boundary chatter), and launder the integral across ANY law change — the
	// old gear-transit-only decay was a special case of this rule.
	pa := m.pa
	if !m.lawInit {
		pa = in.Gear && calibrated < 130
		m.pa = pa // initialisation is NOT a law change: leaving m.pa at its zero value made the first step of every fresh model read as a flip and launder the trim for its first two seconds (TestTrap's scripted pass missed the wires)
		m.lawInit = true
	} else if pa {
		// The virtual flap switch (NATOPS): the FLAPS own the configuration,
		// not the gear handle. HALF was selected with the gear; AUTO is
		// selected passing 180 KCAS clean (92.6 m/s CAS) — so raising the gear
		// on the climb-out keeps the takeoff flap and its lift, and the law
		// change arrives where the droop has already faded with q. Stripping
		// the whole configuration at the gear handle sagged the flight path
		// and pegged the HUD velocity vector on its 10° cage. 128.6 m/s stays
		// as the flap overspeed protection, gear position regardless.
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
		// Powered approach: the stick commands alpha about on-speed, and the
		// trailing edge droops for lift at approach speed. The neutral demand
		// is capped at the alpha LEVEL FLIGHT needs at this dynamic pressure IN
		// THE CURRENT CONFIGURATION — snapping straight to on-speed alpha at
		// gear-limit speed is a 2.5 g uncommanded zoom that full forward stick
		// cannot push out of; as the jet decelerates toward on-speed the cap
		// falls to meet it, converging on on-speed alpha exactly at on-speed
		// (both sides then say the same CL). The cap must count the droop's own
		// lift: the old bare-wing form (CLneed/5, no zero-alpha term) put level
		// alpha at 11°+ across the whole pattern band, so the cap never engaged
		// below ~190 kt and neutral stick commanded FULL on-speed alpha at any
		// gear-down speed — above on-speed that is a standing climb order, and
		// there is no trimming out what the law itself demands ("keeps wanting
		// to climb with gear down": unmasked once the integral clamp let the law
		// actually reach 8.1° and the droop stopped fading at on-speed).
		// Progressive stick gradient. The alpha command is what the pilot flies
		// the ball with, and a linear one gives the same 9°-per-unit sensitivity
		// at the centre as at the stops — fine on a long-throw stick, brutal on
		// a keyboard, where every tap ramps to full deflection: glideslope
		// corrections came out as ±9° alpha commands and the approach pitched in
		// oscillation. Squaring (sign-preserved) keeps the full authority at the
		// stops for the flare and the waveoff, while a quarter-deflection nudge
		// asks for a sixteenth of it. This is the real stick's force gradient
		// too — breakout is soft, the last inch is not.
		fine := stick * math.Abs(stick)
		// The pitch trim switch, PA sense: it biases the ALPHA datum (the real
		// law trims an on-speed AoA reference), so "trim to on-speed, fly the
		// ball with power" is a real technique here rather than an automatic.
		if in.Trim != 0 {
			f.Datum = clamp(f.Datum+clamp(in.Trim, -1, 1)*0.012*Dt, -4*math.Pi/180, 4*math.Pi/180)
		}
		droopTarget, slatFloor = m.Approaching(pressure)
		// The takeoff configuration is LATCHED, not read off the gear handle:
		// HALF flap is set on deck (standing start, not a bolter's touch) and
		// held through the whole clean-up climb — raising the gear must not
		// halve the droop mid-climb-out (it cost a step of CL at 150 kt heavy
		// and settled a hands-off launch into the sea), and leaving it down
		// must not grant the approach's FULL droop off the bow.
		if m.State.Gear.Wow && m.State.Velocity.Length() < 40 {
			m.halfleg = true
		}
		if in.Flap >= 2 {
			// FULL selected: the heavy shot's and the short field's setting —
			// honoured even on deck.
		} else if m.halfleg || in.Flap >= 1 {
			// Takeoff flap: HALF from the deck through the clean-up climb,
			// and whenever the pilot selects it (the field and
			// strong-crosswind technique). The FULL droop belongs to the
			// airborne approach entry. Scaled before the lift-cap maths so
			// the neutral-alpha cap prices the droop actually flying.
			droopTarget *= c.Droop.Half
		}
		schedule := droopTarget / math.Max(c.Droop.Angle, 1e-9)
		need := m.mass * gravity / math.Max(pressure*m.Airframe.Reference.Area, 1)
		grade := clamp((need-c.Droop.Lift*schedule)/4.5, 0, c.Onspeed) // 4.5/rad: the TRIMMED lift slope (stabilator download included) fit through the on-speed anchor — see Droop.Lift
		// The cap serves the ARRIVE-DIRTY regime, not the ball. Near on-speed,
		// need moves as 1/v², so a hard min() walked the neutral alpha datum
		// ~0.4° per m/s of speed — one fat power correction ran the datum from
		// 8.4° to 5.3° and back ("does not trim to maintain attitude", the
		// landing start without ATC). A real Hornet resolves the two regimes
		// with the pitch trim switch; without one, the law blends by speed:
		// within ~15 kt of on-speed it is AoA-referenced — the datum pinned at
		// on-speed while power flies the path, the Navy pattern regime — and
		// beyond that it auto-trims toward configuration level flight (the
		// climb the cap was built to remove). The blend spans ~160–190 kt.
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
			// Launch/waveoff power: the flyaway is an ATTRACTION, not just a cap —
			// with honest droop lift the hands-off climb tops out below the datum
			// and never used to reach the flyaway datum.
			demand = math.Max(demand, math.Min(capture, c.Onspeed+2*math.Pi/180))
		}
		demand = math.Min(demand, math.Max(capture, 0)+fine*(22*math.Pi/180)) // the capture yields to a DELIBERATE pull: at neutral stick it pins the flyaway attitude, but its stick opening (22°) outruns the main demand's (9°), so pulling past ~half stick clears the cap entirely — it no longer fought the climb-out (post-launch "unresponsive then suddenly alive")
		if m.State.Gear.Wow {
			// Ground mode: the alpha law would wind the stabilator full
			// nose-up during the catapult stroke (deck alpha is far below
			// approach alpha) and rotate the jet off the shuttle mid-stroke.
			// Follow the current alpha instead — no error, no windup; the
			// stick passes through for checks and early rotation stays manual.
			demand = a + fine*(12*math.Pi/180) // full aft stick rotates ~12° above deck alpha — field takeoffs need real rotation authority
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
		// Up and away: C* command with the carefree limiter. The symmetric
		// limits schedule with gross weight (NATOPS): the placard's g is
		// written at Limit.Reference, and a heavier jet buys fewer g for the
		// same wing-root bending moment. The paddle override rides the same
		// schedule — it is a fraction over the CURRENT placard, not a number.
		schedule := 1.0
		if m.Airframe.Limit.Reference > 0 {
			schedule = math.Min(1, m.Airframe.Limit.Reference/m.mass)
		}
		ceiling := m.Airframe.Limit.Positive * schedule
		// Rolling reduction (#46, NATOPS 11.1.7/2.8.2.3): the limiter takes
		// commanded load down to 80% NzREF, starting at a quarter of lateral
		// stick travel and full at full deflection — a rolling pull is
		// limited BELOW a straight one (asymmetric wing bending rides on the
		// g). It measured the OPPOSITE before: full-lateral pulls reached
		// 7.58 g against a 7.01 ceiling.
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
		} else if in.Trim != 0 {
			// The pitch trim switch, UA sense: stick-free it walks the held
			// attitude datum — the chase pauses so the nudge is not undone.
			f.Reference = clamp(f.Reference+clamp(in.Trim, -1, 1)*0.0105*Dt, -0.6, 0.6)
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
		// Demand shaping, asymmetric: onset slews at 25 g/s so a stick slam
		// builds load the loops can track instead of a step they chase, but
		// the RELEASE unloads at double that — letting g off has no
		// structural or PIO reason to wait, and the real law is quicker off
		// than on. Symmetric shaping made a released pull coast ~10° past the
		// stick (measured); the faster unload halves it to the real jet's
		// few-degree check. Loading vs unloading is judged against `level`
		// (the 1 g feedforward), so a pull-to-push reversal is fast down to
		// level and onset-limited beyond it. (No zero-means-fresh sentinel
		// here: a full push slews THROUGH exactly zero, and a sentinel reset
		// turns it into a 1→0→1 loop that silently refuses every negative-g
		// command.)
		// Law blend across the gear transition: the PA law caps full stick
		// near approach alpha; the UA law gives it the full g ceiling. With
		// the stick held through gear retraction the command used to STEP —
		// the jet snapped 23°/s nose-up at gear-up. The ceiling now opens
		// with the gear (Extension 1→0 over ~2 s), as the real law fader does.
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
		// The RELEASE transient flies the g loop, not the hold: the real law
		// has no attitude hold to hand a carried rotation to — release means
		// 1 g, and the g feedback kills the pitch rate at surface bandwidth.
		// Handing straight to the hold let the rotation coast ~10° on passive
		// damping (measured); the g path with the fast unload above checks it
		// in the real jet's few degrees. Blending on the DEMAND's distance
		// from level scopes this exactly to the moments after commanded g —
		// stick-free cruise, turbulence, and the idle-decel arrest all sit at
		// f.Demand ≈ level and never see the g path.
		release := clamp(math.Abs(m.State.Fcs.Normal-asked)/0.8, 0, 1) * (1 - flying) // sensed minus ASKED: |n−level| also fired during fine tracking in a turn (high g, light corrective stick) and detuned the tracking law at exactly the on-the-pipper moments — the veteran's gunnery went 0/12 on the conversion referendum before this scoping
		blend := math.Max(flying, release)
		// The trim integral rides at its own stick weight, outside the blend
		// product: inside it the weight became flying² and partial-stick trim
		// went limp (the slow-flight sink arrest lost its wound-up trim); at
		// full release it stays out entirely — frozen at pull trim it rode
		// the release window and doubled the coast it was meant to check.
		rateDemand := clamp(blend*(wanted+star*gain)+flying*f.Integral+(1-blend)*hold, -rateBound, rateBound)
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
		// Low-q tracking damper, WASHED OUT (the guns-tracking PIO; the battery in
		// pio_test.go is its acceptance test). Below the 20 kPa authority reference
		// the weak stabilator lags the rate demand and the loop rings through a
		// tracking pilot's ~0.3 s delay - onset gain 0.6-0.9 across 180-250 kt
		// against 1.3+ at 350, small steps ringing too (linear phase lag, not the
		// actuator limit; a plain gain raise feeds the limit and measures worse).
		// Damping the excess rate directly fixed the tracking but broke the
		// idle-decel sink arrest at every strength: an arrest is a quasi-steady
		// g-build in the same q band. The washout is the discriminator the yaw
		// damper already uses - the filter forgets steady content in ~0.8 s, so
		// arrests and sustained pulls pass while oscillation is damped whole.
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
		// NATOPS 2.8.4.8: airborne in the AUTO FLAPS UP mode the speedbrake
		// retracts itself above 6.0 g or 28° alpha (retraction is also part
		// of departure recovery, 11.2.1) — the board must not keep costing
		// energy through a hard turn the real jet would have saved. The
		// game's brake command is maintained, not momentary, so the board
		// re-extends when the condition clears with the command still held.
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
		// Rudder-to-rolling-surface interconnect (NATOPS 11.1.8): above 25°
		// AoA pedal and lateral stick produce similar roll responses, and
		// combined inputs outperform either alone from 35° up — the pedal
		// feeds the same roll-rate command the stick does, blended in across
		// 25-35° alpha. Below the blend it contributes nothing, as before.
		rolling := clamp(lateral+pedal*clamp((a-0.44)/0.17, 0, 1), -1, 1)
		flapTarget = (rolling*limit-p)*0.22 + f.Bank // the roll-trim datum rides outside the rate loop: a rate-command law re-trims itself, so the datum acts as a standing surface bias, exactly like the real jet's trim follow-up
		rudderTarget = m.yaw(pedal, lateral, a, b, r, f)
		// Insufficient-airspeed departure (NATOPS 11.1.8.1): the FCS prevents
		// every departure except this one — "nose high, ballistic conditions"
		// walk the jet in pitch and yaw with few cues. Modelled as a slow
		// seeded wander on the rudder and stabilator that the q̄-weak surfaces
		// cannot cancel: it fades in below ~2.5 kPa (124 KEAS), saturates by
		// 1 kPa, and dies on its own as speed returns. Up-and-away airborne
		// only — the catapult stroke, waveoff and approach all live above the
		// band, and the powered-approach law is untouched. Seeded phases keep
		// the core deterministic (multiplayer prediction replays it exactly).
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

// yaw is the directional law: a washed-out yaw damper, sideslip suppression
// with a touch of pedal-commanded beta, and an aileron-rudder interconnect
// that grows with alpha (pro-spin input ends up refused because the rudder
// is busy coordinating).
// taper is the roll command's alpha schedule: authority tapers as alpha
// rises toward the limiter, then holds at its 35° value — NATOPS 11.1.8 has
// roll performance "essentially constant" above 35° alpha. The old fixed
// 0.08 floor kept collapsing past the limiter to a 9°/s crawl in the
// transient excursions above 40° where rolling matters most.
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

// Approaching reports the trailing-edge droop and the leading-edge slat floor
// the powered-approach law commands at a dynamic pressure — the landing
// configuration, airborne (the deck's flaps-HALF factor is applied by the law
// itself). Shared with the Approach trim helper so a spawn can never disagree
// with the FCS about the configuration it is spawning into.
//
// Hold-then-washout: the real TEF schedule HOLDS the commanded setting through
// the approach band and retracts approaching the flap limit — a linear fade
// left only ~2/3 droop at on-speed ("flaps up" on a slightly fast approach)
// and nothing by 250 kt.
func (m *Model) Approaching(pressure float64) (droop float64, slat float64) {
	c := &m.Airframe.Control
	schedule := clamp((c.Droop.Pressure-pressure)/(c.Droop.Pressure*0.55), 0, 1) // full below ~0.45·P, gone at P
	return c.Droop.Angle * schedule, 12 * math.Pi / 180 * schedule               // NATOPS droops the LEADING edge with the flaps (12°)
}

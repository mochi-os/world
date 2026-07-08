// Mochi world: Catapult and arrestor cable
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The catapult holds the jet on the shuttle with a stiff holdback (easing
// onto the spot is emergent — the spring does it), then throws it with a
// constant stroke force sized to the cat's end speed. The arrestor cable
// runs anchor–hook–anchor: tension from the derived payout hauls the jet
// down and back to the centreline (the V geometry does the centring). A
// bolter is simply never engaging. Wire capture sweeps the hook path every
// substep, so a fast rollout cannot tunnel between frames.

package flight

import (
	"math"
)

const (
	capture  = 5.0    // m: catapult attach radius around the shuttle
	tension  = 3000.0 // N per metre of cable payout (at the reference weight setting)
	absorb   = 8000.0 // N·s/m of payout rate (at the reference weight setting)
	greatest = 4.2e5  // cable tension ceiling, N (at the reference weight setting): sized so a mid-weight engagement nets ~300 kN ≈ 2.4 g and ~100 m of runout, the Mk 7 class — at 6e5 the wire pulled 4.7 g and crushed the nose gear to a fuselage strike; at 3e5 the setting-scaled pull fell to 220 kN and the runout ran 156 m off the angle deck (#72 scenario 9)
	// The energy the constants above were tuned at: the setting formula below
	// evaluated at TestTrap's condition (fa18c, full internal fuel 15,600 kg,
	// sea-level deck: expected engagement 1.16×stall ≈ 76.5 m/s). Defined
	// through the SAME formula so that exact condition computes setting = 1.0
	// and behaves identically to the pre-setting gear.
	reference = 9.13e7 // kg·(m/s)² — 2× the tuned engagement energy
)

// hook is the tailhook tip position for a trial state (body frame offset:
// the deployed hook hangs down and aft).
func (m *Model) hook(s *State) Vec3 {
	tip := m.Airframe.Hook.Position.Add(Vec3{X: -0.5 * m.Airframe.Hook.Length, Y: -0.86 * m.Airframe.Hook.Length})
	return s.Position.Add(s.Attitude.Rotate(tip.Subtract(m.center)))
}

// holdback pins an attached, unfired jet to its shuttle.
func (m *Model) holdback(s *State, total *Forces) {
	c := m.World.Carrier
	if c == nil || s.Gear.Catapult < 0 || s.Gear.Stroke >= 0 {
		return
	}
	cat := &c.Catapults[s.Gear.Catapult]
	shuttle := c.world(cat.Position, s.Time)
	nose := m.Airframe.Gear.Nose.Attach
	point := s.Position.Add(s.Attitude.Rotate(nose.Subtract(m.center)))
	pull := Vec3{
		X: Shortest(point.X, shuttle.X, m.Environment.Wrap),
		Z: Shortest(point.Z, shuttle.Z, m.Environment.Wrap),
	}
	velocity := s.Velocity.Subtract(c.direction().Scale(c.Speed))
	heading := c.Heading + cat.Heading
	track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
	// The launch bar rides in the track SLOT: laterally the nose is captured
	// stiffly (the slot is mechanical), while the ALONG-track gather is
	// distance-scheduled — gentle at the capture radius (the full 240 kN
	// damper at the nose point once pitched a 3-4 m/s arrival onto its tail
	// probe), firming to the full holdback at the spot. Alignment emerges by
	// ROLLING into the slot line, as on the real deck: an in-place yaw torque
	// cannot turn a parked jet against main-tire grip (offset arrivals used
	// to settle ~11° cocked and launch crooked).
	along := track.Scale(pull.X*track.X + pull.Z*track.Z)
	cross := Vec3{X: pull.X - along.X, Z: pull.Z - along.Z}
	vAlong := velocity.X*track.X + velocity.Z*track.Z
	vCross := Vec3{X: velocity.X - track.X*vAlong, Z: velocity.Z - track.Z*vAlong}
	// Caps apply to the SPRINGS only — when spring and damping shared a cap,
	// saturation left no damping at all and the capture wobbled. Damping is
	// speed-scheduled instead: gentle at arrival speed (a hard decel at the
	// nose point is the tail-slam), stiffening near rest to kill oscillation.
	// The lateral slot engages softly at the capture radius and firms as the
	// jet closes: a full-stiffness step at attach is an impulse into the
	// struts (applied at the nose point BELOW the CG, every lateral force
	// also rolls and pitches the jet — the capture "bounce and roll").
	slotCap := 8e4 * clamp(1-along.Length()/5, 0.25, 1)
	spring := cross.Scale(4e5)
	if spring.Length() > slotCap {
		spring = spring.Normalize().Scale(slotCap)
	}
	slot := spring.Subtract(vCross.Scale(1.7e5))
	grip := clamp(1-along.Length()/4, 0.3, 1)
	gspring := along.Scale(2.4e5) // softer than the lateral slot: the gather couples into the pitch mode (the nose point moves fore-aft as the struts pitch, modulating the pull — the capture "bounce"); the cap, not the stiffness, provides the holding strength at rest
	if gspring.Length() > 2.4e5*grip {
		gspring = gspring.Normalize().Scale(2.4e5 * grip) // at rest: holds full reheat (196 kN static) with margin; it releases at the shot, never before
	}
	damp := vAlong * 1.7e5 * clamp(1-math.Abs(vAlong)/5, 0.15, 1)
	damp = clamp(damp, -1.0e5, 1.0e5) // bounded: mid-speed braking must never approach the tail-slam regime, and near rest it is far below the bound anyway; without a bound the far-field creep equilibrium (spring vs damping) crawled the last metres for tens of seconds
	gather := gspring.Subtract(track.Scale(damp))
	// Applied at CG HEIGHT over the nose point: the physical bar pulls at deck
	// level, but every horizontal force 2.6 m below the CG also pitches and
	// rolls the jet on its struts — the endless "bounce and roll on capture".
	// The XZ lever (nose ahead of the CG) still provides the yaw geometry.
	// The lateral slot applies at CG HEIGHT (a deck-level side force rolls the
	// jet on its struts — the capture "rock"). The along-track gather blends
	// by ENGINE POWER: docking happens at idle, where deck-level braking at
	// the nose pitched the jet over its nose gear (a -37°/s slam on arrival);
	// the run-up happens at full power, where the deck-level pitch coupling
	// is part of the tuned flyaway behaviour (raising it sank the shot).
	m.apply(s, slot, Vec3{X: point.X, Y: s.Position.Y, Z: point.Z}, total)
	power := clamp((s.Engine[0].Spool-0.2)/0.5, 0, 1)
	m.apply(s, gather, Vec3{X: point.X, Y: s.Position.Y + (point.Y-s.Position.Y)*power, Z: point.Z}, total)
	forward := s.Attitude.Rotate(Vec3{X: 1})
	swing := forward.X*track.Z - forward.Z*track.X // + when nose is left of track
	if s.Gear.Stroke <= -3 {
		// TENSION: a strong yaw trim squares the jet before the shot — the
		// castering nosewheel and the mains' short lever arm let a parked jet
		// yaw under torque. (A bar-tension force couple was tried first: its
		// moment is ~-7.8e5·sin(swing) against the trim's +8e5·swing — a
		// near-exact cancellation that stalled small crabs and SPUN large
		// ones.) Fires as soon as the jet is straight, so an aligned jet
		// launches the same step it asked to.
		// Soft-start over ~1.2 s using the tension clock (elapsed = -3 - Stroke):
		// stepping the full torque onto an 11° crab snapped the jet hard enough
		// to roll a wingtip probe into the deck. Heavily overdamped; the 4 s
		// timeout covers slow convergence.
		ramp := clamp((-3-s.Gear.Stroke)/1.2, 0.1, 1)
		total.Moment = total.Moment.Add(Vec3{Y: -swing * 1.6e6 * ramp}.Subtract(Vec3{Y: s.Omega.Y * 2.2e6})) // NEGATIVE: +Y yaw is nose LEFT and swing is + when the nose is left of track, so correction is -swing (the + form fed the crab — proven by telemetry: swing GREW under tension; the sign was masked for months by pre-aligned spawns). Strong enough to overwhelm tire grip by design
		// The FIRE decision lives in events (the once-per-step state pass) —
		// force functions run on trial integrator substates and a Stroke
		// mutation here is silently discarded.
		return
	}
	// Nose-down-the-track trim on top of the emergent rolling alignment.
	trim := clamp(1-velocity.Length()/2.0, 0, 1)                                                              // fades in through the final creep: while rolling fast the nose-point tow self-aligns the body like a trailer (caster) and a yaw torque only fights it; below ~2 m/s the wheels still roll enough to yaw, and the trim squares the last few degrees before tire grip locks the pose
	total.Moment = total.Moment.Add(Vec3{Y: -swing * 1.2e6 * trim}.Subtract(Vec3{Y: s.Omega.Y * 8e5 * trim})) // -swing: see the tension note; strength doubled — the regularised tire friction yields slowly and the weaker trim parked offset arrivals 12° crabbed
}

// stroke is the catapult throw: a constant force along the track while the
// shuttle runs, sized to reach the cat's end speed over its stroke.
func (m *Model) stroke(s *State, total *Forces) {
	c := m.World.Carrier
	if c == nil || s.Gear.Catapult < 0 || s.Gear.Stroke < 0 {
		return
	}
	cat := &c.Catapults[s.Gear.Catapult]
	if s.Gear.Stroke >= cat.Stroke {
		return
	}
	heading := c.Heading + cat.Heading
	track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
	// The bar stays captive in the slot for the whole run: a lateral nose
	// capture (force, not torque) pulls a crooked start onto the track line
	// and the body trails straight within the first metres — and it is
	// exactly zero for an aligned run, so a clean shot is untouched.
	nose := m.Airframe.Gear.Nose.Attach
	point := s.Position.Add(s.Attitude.Rotate(nose.Subtract(m.center)))
	shuttleLine := c.world(cat.Position, s.Time)
	off := Vec3{X: Shortest(point.X, shuttleLine.X, m.Environment.Wrap), Z: Shortest(point.Z, shuttleLine.Z, m.Environment.Wrap)}
	cross := off.Subtract(track.Scale(off.X*track.X + off.Z*track.Z))
	// Pure spring on the cross-track offset with the slot's physical clearance
	// as a deadband: an aligned run never touches the walls (zero force, the
	// golden trace is untouched), while a crabbed start has its nose held on
	// the line and the body trails straight within the first metres. No
	// velocity term — it reacted to the nose's pitch-sweep and put yaw into
	// clean shots; tire cornering damps the lateral mode on its own.
	span := cross.Length()
	if span > 0.15 {
		slot := cross.Scale((span - 0.15) / span * 1.2e5)
		if slot.Length() > 6e4 {
			slot = slot.Normalize().Scale(6e4)
		}
		m.apply(s, slot, Vec3{X: point.X, Y: s.Position.Y, Z: point.Z}, total) // CG height: a deck-level lateral shove at 90 m/s rolls the jet (see holdback)
	}
	// The shot is set for the aircraft's weight, as the real catapult crew
	// does: ~1.16× the powered-approach stall speed, capped by the cat's
	// mechanical limit. A light jet no longer rockets off mid-stroke at the
	// full 88 m/s; a heavy one settles toward the deck edge before flying
	// away — the sink real launches show.
	stall := math.Sqrt(2 * m.mass * gravity / (air(m.State.Position.Y, m.Environment).Density * 1.55 * m.Airframe.Reference.Area))
	speed := clamp(1.16*stall, 45, cat.Speed)
	force := m.mass * speed * speed / (2 * cat.Stroke)
	force *= clamp(s.Gear.Stroke/8, 0.3, 1) // the real cat builds force over the first metres — stepping full thrust on at fire bounced the jet on its struts (the average loss is made up within the stroke sizing margin)
	local := s.Attitude.Unrotate(track.Scale(force))
	total.Force = total.Force.Add(local)
}

// cable is the arrestor wire: tension along both legs from the derived
// payout — nothing about the cable is stored, so rewind is free.
func (m *Model) cable(s *State, in Inputs, total *Forces) {
	c := m.World.Carrier
	if c == nil || s.Gear.Wire < 0 || s.Gear.Wire >= len(c.Wires) {
		return
	}
	wire := &c.Wires[s.Gear.Wire]
	a := c.world(wire.A, s.Time)
	b := c.world(wire.B, s.Time)
	tip := m.hook(s)
	legA := a.Subtract(tip)
	legB := b.Subtract(tip)
	span := a.Subtract(b).Length()
	payout := legA.Length() + legB.Length() - span
	if payout <= 0 {
		return
	}
	// Payout rate from the hook-tip velocity resolved along both legs.
	velocity := s.Velocity.Subtract(c.direction().Scale(c.Speed))
	rate := -velocity.Dot(legA.Normalize()) - velocity.Dot(legB.Normalize())
	// Weight setting, dialled per engagement as the real gear crew does (and as
	// the catapult already sets its shot): the retard scales with the energy a
	// correctly-flown engagement carries at THIS gross mass — on-speed approach
	// (1.16× stall at this weight) less the deck's own way. Constant runout and
	// constant felt G across fuel states; an over-speed engagement still carries
	// excess energy and still pulls longer and harder, as it should.
	stall := math.Sqrt(2 * m.mass * gravity / (air(s.Position.Y, m.Environment).Density * 1.55 * m.Airframe.Reference.Area))
	engage := math.Max(1.16*stall-c.Speed, 30)
	setting := m.mass * engage * engage / reference
	pull := clamp(setting*(tension*payout+absorb*rate), 0, setting*greatest)
	if rate < 0 {
		pull = 0 // the arresting engine dissipates: no recoil — even a 12%-of-payout residual dragged the stopped jet 68 m backwards down the deck (#72 scenario 9)
	}
	direction := legA.Normalize().Add(legB.Normalize()).Normalize()
	m.apply(s, direction.Scale(pull), tip, total)
}

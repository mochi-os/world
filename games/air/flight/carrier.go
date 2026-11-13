// Mochi world: Catapult and arrestor cable
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The catapult holds the jet on the shuttle with a stiff holdback (easing
// onto the spot is emergent — the spring does it), then throws it as the
// Ford's electromagnetic catapults do: closed loop to the end speed set for
// the jet, at a constant acceleration. The arrestor cable runs
// anchor–hook–anchor and stops the jet as the Ford's arresting gear does:
// closed loop to a fixed run-out whatever its weight, speed or thrust, the V
// geometry doing the centring. Both laws read only the state of the moment,
// so nothing about the shot or the arrestment is stored and rewind is free.
// A bolter is simply never engaging. Wire capture sweeps the hook path every
// substep, so a fast rollout cannot tunnel between frames.

package flight

import (
	"math"
)

const (
	capture = 5.0   // m: catapult attach radius around the shuttle
	lateral = 2.0   // m: how far off the track line the nose gear may be for the slot to gather it onto the shuttle (TestTaxiUpSlow rolls in 1.5 m off)
	aligned = 0.978 // cos of the heading the crew will hook up at: within ~12° of the track
	// The catapult: the electromagnetic catapults' sustained launch force,
	// "~1.3 MN" (Meeker and Newman 2009), and the metres over which the shot's
	// force ramps up after the holdback lets go ("a gradual ramp-up of force",
	// its length not public): stepping full thrust on at fire bounced the jet
	// on its struts.
	shot = 1.3e6 // N
	rise = 1.5   // m
	// The end speed set over the minimum: 15 kt (NATOPS 8.2.5).
	margin = 15 * 0.5144 // m/s
	// The arresting gear: the hook's run-out from the wire to the stop. The
	// Ford's is not public; the Mk 7 Mod 3's 344 ft to the tail hook (NAVEDTRA
	// 14310) is the only cited figure, and the Ford's gear is held to the same
	// envelope. Its engagement transient, which the cable shock absorbers take
	// before the arrestment proper, is spread over the first metres of the run.
	runout     = 104.9 // m
	engagement = 6.0   // m
	// The most the gear pulls, in g along the run: the aircraft's hookload
	// limits are not public. A pass engaging faster than the gear can stop in
	// its run-out runs on past it, as an overspeed engagement would.
	hookload = 3.5
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
	shuttle := c.world(cat.Position, s.Time, m.place)
	nose := m.Airframe.Gear.Nose.Attach
	point := s.Position.Add(s.Attitude.Rotate(nose.Subtract(m.center)))
	pull := Vec3{
		X: shuttle.X - point.X,
		Z: shuttle.Z - point.Z,
	}
	velocity := s.Velocity.Subtract(c.velocity(point, s.Time, m.place))
	track := c.track(cat.Heading, s.Time, m.place)
	// The launch bar rides in the track SLOT: laterally stiff (the slot is
	// mechanical), the along-track gather distance-scheduled. Alignment emerges by
	// ROLLING into the line - yaw torque cannot turn a parked jet.
	along := track.Scale(pull.X*track.X + pull.Z*track.Z)
	cross := Vec3{X: pull.X - along.X, Z: pull.Z - along.Z}
	vAlong := velocity.X*track.X + velocity.Z*track.Z
	vCross := Vec3{X: velocity.X - track.X*vAlong, Z: velocity.Z - track.Z*vAlong}
	// Caps apply to the SPRINGS only - a shared cap saturates and leaves no
	// damping at all. Damping is speed-scheduled instead, and the lateral slot
	// engages softly at the capture radius to avoid an impulse at attach.
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
	// Applied at CG HEIGHT over the nose point: a horizontal force at deck level
	// also pitches and rolls the jet on its struts. The along-track gather blends
	// by engine power - deck-level braking at idle noses over.
	m.apply(s, slot, Vec3{X: point.X, Y: s.Position.Y, Z: point.Z}, total)
	power := clamp((s.Engine[0].Spool-0.2)/0.5, 0, 1)
	m.apply(s, gather, Vec3{X: point.X, Y: s.Position.Y + (point.Y-s.Position.Y)*power, Z: point.Z}, total)
	forward := s.Attitude.Rotate(Vec3{X: 1})
	swing := forward.X*track.Z - forward.Z*track.X // + when nose is left of track
	// The bar in the slot holds the nose gear straight down the track: a
	// squaring couple from the moment of attach, heavily overdamped, so any
	// crab the hookup leaves is gone at idle before the power comes up. A
	// crabbed jet under thrust is a jackknife - the restraint at the nose, the
	// push at the tail - and what holds it, tyre grip at deck level, rolls the
	// airframe: a 20° crab leaned 10° at mil, 25° went over. It used to run
	// only in TENSION, after the launch request, with a weak speed-faded trim
	// before that which left a rolling hookup 18° crabbed six seconds later.
	// NEGATIVE: +Y yaw is nose LEFT and swing is + when the nose is left of
	// track, so the correction is -swing (the + form fed the crab - proven by
	// telemetry: swing GREW under tension; the sign was masked for months by
	// pre-aligned spawns). Strong enough to overwhelm tire grip by design. The
	// FIRE decision lives in events (the once-per-step state pass) - force
	// functions run on trial integrator substates and a Stroke mutation here
	// is silently discarded. Its damping is on the jet's yaw rate against the
	// deck's: on the jet's own it fought a turning ship's rotation, and the jet
	// lagged its track by 1.7 degrees in a 30 kt turn, swinging its tail 0.2 m
	// across the deck.
	total.Moment = total.Moment.Add(Vec3{Y: -swing * 2.4e6}.Subtract(Vec3{Y: (s.Omega.Y - c.leg(s.Time).Turn) * 3.3e6}))
}

// stroke is the catapult throw, closed loop as the electromagnetic catapult's
// controller is: each step it pulls what takes the jet from its speed along the
// track now to the end speed at the stroke's end, less what the jet's own
// engines, drag and tyres already add along the track. The acceleration holds
// constant after the ramp-up, where steam's peaked at 1.25 times its mean
// (EMALS's requirement is 1.05, Doyle 1995), and the end speed is the one set,
// whatever the thrust or the weight.
func (m *Model) stroke(s *State, total *Forces) {
	c := m.World.Carrier
	if c == nil || s.Gear.Catapult < 0 || s.Gear.Stroke < 0 {
		return
	}
	cat := &c.Catapults[s.Gear.Catapult]
	if s.Gear.Stroke >= cat.Stroke {
		return
	}
	track := c.track(cat.Heading, s.Time, m.place)
	// The bar stays captive in the slot for the whole run: a lateral nose
	// capture (force, not torque) pulls a crooked start onto the track line
	// and the body trails straight within the first metres — and it is
	// exactly zero for an aligned run, so a clean shot is untouched.
	nose := m.Airframe.Gear.Nose.Attach
	point := s.Position.Add(s.Attitude.Rotate(nose.Subtract(m.center)))
	shuttleLine := c.world(cat.Position, s.Time, m.place)
	off := Vec3{X: shuttleLine.X - point.X, Z: shuttleLine.Z - point.Z}
	cross := off.Subtract(track.Scale(off.X*track.X + off.Z*track.Z))
	// Pure spring on the cross-track offset with the slot clearance as a deadband,
	// so an aligned run takes zero force. No velocity term: it reacted to the
	// nose's pitch-sweep and put yaw into clean shots.
	span := cross.Length()
	if span > 0.15 {
		slot := cross.Scale((span - 0.15) / span * 1.2e5)
		if slot.Length() > 6e4 {
			slot = slot.Normalize().Scale(6e4)
		}
		m.apply(s, slot, Vec3{X: point.X, Y: s.Position.Y, Z: point.Z}, total) // CG height: a deck-level lateral shove at 90 m/s rolls the jet (see holdback)
	}
	// The shot is set for the aircraft's weight, as the real crew does: its
	// minimum end speed, ~1.16× the powered-approach stall speed, and the 15 kt
	// over it NATOPS 8.2.5 recommends ("11 to 20 knots above minimum" is
	// normal), capped by the cat's limit. The open-loop shot this replaced got
	// 4-13 m/s over the speed it was set for from the engines' own thrust.
	stall := math.Sqrt(2 * m.mass * gravity / (air(m.State.Position.Y, m.Environment).Density * 1.55 * m.Airframe.Reference.Area))
	// The crew sets the shot for AIRSPEED off the bow, and wind over the deck
	// is part of the budget (#44): a 25 kt headwind means the stroke itself
	// needs 25 kt less. m.gust is the wind sampled at the jet this step;
	// -gust·track is the headwind component down the launch track.
	headwind := math.Max(0, -m.gust.Dot(track))
	speed := clamp(1.16*stall+margin-headwind, 45, cat.Speed)
	// stroke runs last in forces: total holds everything else on the jet
	moving := s.Velocity.Subtract(c.velocity(point, s.Time, m.place)).Dot(track)
	remaining := math.Max(cat.Stroke-s.Gear.Stroke, 0.5)
	want := (speed*speed - moving*moving) / (2 * remaining)
	others := s.Attitude.Rotate(total.Force).Dot(track) / m.mass
	force := clamp(m.mass*(want-others)*clamp(s.Gear.Stroke/rise, 0.3, 1), 0, shot)
	local := s.Attitude.Unrotate(track.Scale(force))
	total.Force = total.Force.Add(local)
}

// cable is the arrestor wire, closed loop as the arresting gear's controller
// is: each step it pulls what stops the hook at the run-out from where it is
// now and how fast it runs, so the deceleration holds constant whatever the
// weight, the engaging speed or the engines' thrust. It pulls along the V of
// its two legs from their anchors, which centres the jet. Nothing about the
// cable is stored, so rewind is free.
func (m *Model) cable(s *State, in Inputs, total *Forces) {
	c := m.World.Carrier
	if c == nil || s.Gear.Wire < 0 || s.Gear.Wire >= len(c.Wires) {
		return
	}
	wire := &c.Wires[s.Gear.Wire]
	a := c.world(wire.A, s.Time, m.place)
	b := c.world(wire.B, s.Time, m.place)
	tip := m.hook(s)
	legA := a.Subtract(tip)
	legB := b.Subtract(tip)
	span := a.Subtract(b).Length()
	payout := legA.Length() + legB.Length() - span
	if payout <= 0 {
		return
	}
	// The run: how far the hook has gone from the wire's line across the deck,
	// the way it goes and how fast.
	across := Vec3{X: b.X - a.X, Z: b.Z - a.Z}.Normalize()
	out := Vec3{X: tip.X - a.X, Z: tip.Z - a.Z}
	out = out.Subtract(across.Scale(out.Dot(across)))
	run := out.Length()
	relative := s.Velocity.Subtract(c.velocity(tip, s.Time, m.place))
	ahead := Vec3{X: relative.X, Z: relative.Z}
	ahead = ahead.Subtract(across.Scale(ahead.Dot(across)))
	if run > 0.01 {
		ahead = out
	}
	if ahead.Length() < 1e-6 {
		return
	}
	ahead = ahead.Normalize()
	speed := relative.Dot(ahead)
	if speed < 0 {
		return // the arresting engine dissipates: no recoil — even a small residual dragged the stopped jet backwards down the deck (#72 scenario 9)
	}
	// What the engines, drag and tyres already do along the run (cable runs
	// after aero, propulsion and the struts): the gear takes the push as well,
	// and holds the jet against it at the end of the run.
	others := s.Attitude.Rotate(total.Force).Dot(ahead) / m.mass
	want, left := 0.0, runout-run
	if left > 0.05 {
		want = speed * speed / (2 * left) * clamp(run/engagement, 0.2, 1)
	} else {
		want = speed / 0.2 // the gear's brake at the end of the run, where a law on the distance left loses its grip: held to half a metre, the jet crept on three metres past it
	}
	want = math.Min(want+math.Max(others, 0), hookload*gravity)
	direction := legA.Normalize().Add(legB.Normalize()).Normalize()
	// The V pulls partly across the run: enough along it to give want.
	along := -direction.Dot(ahead)
	pull := m.mass * want / math.Max(along, 0.5)
	// The shank is a pinned link and carries no moment, so the airframe feels
	// the pull at the hook PIVOT, along the tip's geometry. Applied at the tip,
	// 2.6 m below the CG, the V's lateral component rolls the jet with five
	// times the pivot's arm while the retard pitches it 8° nose down: a catch
	// a metre off the wire's centre lifts the inner main and rolls it over.
	pivot := s.Position.Add(s.Attitude.Rotate(m.Airframe.Hook.Position.Subtract(m.center)))
	m.apply(s, direction.Scale(pull), pivot, total)
}

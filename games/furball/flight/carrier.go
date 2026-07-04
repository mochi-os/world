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
	tension  = 3000.0 // N per metre of cable payout
	absorb   = 8000.0 // N·s/m of payout rate
	greatest = 6.0e5  // cable tension ceiling, N
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
	force := pull.Scale(4e5).Subtract(Vec3{X: velocity.X, Z: velocity.Z}.Scale(1.7e5)) // ~critically damped at combat weight: capture reels in without a slingshot
	if force.Length() > 2.4e5 {
		force = force.Normalize().Scale(2.4e5) // holds full reheat (196 kN static) with margin; it releases at the shot, never before
	}
	m.apply(s, force, point, total)
	// Align the nose down the catapult track.
	heading := c.Heading + cat.Heading
	track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
	forward := s.Attitude.Rotate(Vec3{X: 1})
	swing := forward.X*track.Z - forward.Z*track.X // + when nose is left of track
	total.Moment = total.Moment.Add(Vec3{Y: swing * 6e5}.Subtract(Vec3{Y: s.Omega.Y * 4e5}))
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
	// The shot is set for the aircraft's weight, as the real catapult crew
	// does: ~1.16× the powered-approach stall speed, capped by the cat's
	// mechanical limit. A light jet no longer rockets off mid-stroke at the
	// full 88 m/s; a heavy one settles toward the deck edge before flying
	// away — the sink real launches show.
	stall := math.Sqrt(2 * m.mass * gravity / (air(m.State.Position.Y, m.Environment).Density * 1.55 * m.Airframe.Reference.Area))
	speed := clamp(1.16*stall, 45, cat.Speed)
	force := m.mass * speed * speed / (2 * cat.Stroke)
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
	pull := clamp(tension*payout+absorb*rate, 0, greatest)
	if rate < 0 {
		pull = clamp(tension*payout*0.12, 0, greatest) // the arresting engine dissipates: almost no recoil
	}
	direction := legA.Normalize().Add(legB.Normalize()).Normalize()
	m.apply(s, direction.Scale(pull), tip, total)
}

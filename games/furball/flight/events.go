// Mochi world: Discrete contact events
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Everything discrete happens here, once per step, outside the RK4
// derivative: the gear actuator, weight-on-wheels and the touchdown record,
// the crash probes, catapult attach/fire/advance/detach, and arrestor-wire
// capture and release. The wire capture sweeps the hook path across the
// step (derived from velocity — nothing extra in State), so no rollout
// speed can tunnel through a cable.

package flight

import (
	"math"
)

func (m *Model) events(in Inputs) {
	s := &m.State
	// Gear travels in ~5 s.
	target := 0.0
	if in.Gear {
		target = 1
	}
	s.Gear.Extension += clamp(target-s.Gear.Extension, -Dt/5, Dt/5)

	m.touch(s)
	m.probes(s)
	m.catapult(s, in)
	m.wire(s, in)
}

// touch maintains weight-on-wheels and records the first contact of an
// arrival for the host's verdict gates.
func (m *Model) touch(s *State) {
	a := m.Airframe
	wow := false
	kind := 0
	sink := 0.0
	points := [3]Vec3{a.Gear.Nose.Attach, a.Gear.Left.Attach, a.Gear.Right.Attach}
	if s.Gear.Extension < 0.95 {
		if len(a.Belly) < 3 {
			s.Gear.Wow = false
			return
		}
		points = [3]Vec3{a.Belly[0], a.Belly[1], a.Belly[2]}
	}
	for _, at := range points {
		body := at.Subtract(m.center)
		point := s.Position.Add(s.Attitude.Rotate(body))
		height, k, carried, found := m.World.surface(point, s.Time, m.Environment.Wrap)
		if !found || point.Y > height {
			continue
		}
		wow = true
		kind = k
		velocity := s.Velocity.Add(s.Attitude.Rotate(s.Omega.Cross(body))).Subtract(carried)
		if -velocity.Y > sink {
			sink = -velocity.Y
		}
	}
	if wow && !s.Gear.Wow {
		right := s.Attitude.Rotate(Vec3{Z: 1})
		s.Gear.Touch = Touch{
			Occurred: true,
			Sink:     sink,
			Bank:     math.Asin(clamp(-right.Y, -1, 1)),
			Kind:     kind,
		}
	}
	s.Gear.Wow = wow
}

// probes reports any non-permitted airframe contact; the host judges (the
// project rule: anything but wheels, belly, and hook touching anything is a
// crash).
func (m *Model) probes(s *State) {
	s.Gear.Contact = -1
	for i, at := range m.Airframe.Probes {
		point := s.Position.Add(s.Attitude.Rotate(at.Subtract(m.center)))
		height, _, _, found := m.World.surface(point, s.Time, m.Environment.Wrap)
		if found && point.Y <= height {
			s.Gear.Contact = i
			return
		}
	}
}

// catapult handles attach (roll slowly over a shuttle, gear down), fire
// (Launch while held back), stroke advance, and end-of-stroke release.
func (m *Model) catapult(s *State, in Inputs) {
	c := m.World.Carrier
	if c == nil {
		return
	}
	if s.Gear.Catapult < 0 {
		if !s.Gear.Wow || s.Gear.Extension < 0.95 {
			return
		}
		nose := s.Position.Add(s.Attitude.Rotate(m.Airframe.Gear.Nose.Attach.Subtract(m.center)))
		if s.Gear.Stroke <= -2 {
			// Unhooked: stay released until the nose gear has taxied clear of
			// every shuttle, then re-arm — without the latch the crew would
			// hook you straight back up on the spot you just left.
			for i := range c.Catapults {
				shuttle := c.world(c.Catapults[i].Position, s.Time)
				dx := Shortest(nose.X, shuttle.X, m.Environment.Wrap)
				dz := Shortest(nose.Z, shuttle.Z, m.Environment.Wrap)
				if dx*dx+dz*dz < capture*capture {
					return
				}
			}
			s.Gear.Stroke = -1
			return
		}
		if in.Yaw > 0.5 || in.Yaw < -0.5 {
			return // steering away is not asking to hook up
		}
		relative := s.Velocity.Subtract(c.direction().Scale(c.Speed))
		if relative.Length() > 4 {
			return
		}
		forward := s.Attitude.Rotate(Vec3{X: 1})
		for i := range c.Catapults {
			shuttle := c.world(c.Catapults[i].Position, s.Time)
			dx := Shortest(nose.X, shuttle.X, m.Environment.Wrap)
			dz := Shortest(nose.Z, shuttle.Z, m.Environment.Wrap)
			if dx*dx+dz*dz >= capture*capture {
				continue
			}
			// The crew only hook up an ALIGNED aircraft: taxiing across the
			// shuttle perpendicular used to attach on proximity alone, and
			// the holdback yanked the jet sideways into the deck crash gates.
			heading := c.Heading + c.Catapults[i].Heading
			track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
			if forward.X*track.X+forward.Z*track.Z < 0.9 { // within ~25°
				continue
			}
			s.Gear.Catapult = i
			s.Gear.Stroke = -1
			return
		}
		return
	}
	cat := &c.Catapults[s.Gear.Catapult]
	if s.Gear.Stroke <= -3 {
		// Tension: fire when the jet is straight AND its nose is on the track
		// line — squaring a crab yaws about the CG and swings the nose off the
		// line (4.9 m arm), and firing then starts the run with a lateral
		// yank; the slot spring recentres the castering nose during the hold.
		// An aligned jet fires the same step it asked to.
		heading := c.Heading + cat.Heading
		track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
		forward := s.Attitude.Rotate(Vec3{X: 1})
		swing := forward.X*track.Z - forward.Z*track.X
		nose := s.Position.Add(s.Attitude.Rotate(m.Airframe.Gear.Nose.Attach.Subtract(m.center)))
		shuttle := c.world(cat.Position, s.Time)
		off := Vec3{X: Shortest(nose.X, shuttle.X, m.Environment.Wrap), Z: Shortest(nose.Z, shuttle.Z, m.Environment.Wrap)}
		cross := off.Subtract(track.Scale(off.X*track.X + off.Z*track.Z))
		straight := math.Abs(swing) < 0.026 || (math.Abs(swing) < 0.09 && math.Abs(s.Omega.Y) < 0.004)
		s.Gear.Stroke -= Dt // the tension clock: Stroke decays from -3 while holding
		// Fire on straightness alone — the nose-offset gate (cross) used to
		// block convergence-firing and push crabbed jets onto the TIMEOUT
		// path, and firing crabbed is the one real danger: at speed the tire
		// slip forces act at deck level and ROLL the jet (a wingtip probe hit
		// the deck at 30 m into the run). Even the timeout refuses to fire
		// beyond ~3.4°; the tension equilibrium sits inside that, so it
		// always fires eventually.
		_ = cross
		if straight || (s.Gear.Stroke < -7 && math.Abs(swing) < 0.06) {
			s.Gear.Stroke = 0
		}
		return
	}
	if s.Gear.Stroke < 0 {
		if in.Launch {
			if s.Gear.Stroke == -1 {
				s.Gear.Stroke = -3 // tension first: the couple squares the jet (holdback applies the forces), events fires the shot when straight
			}
		} else if (in.Yaw > 0.5 || in.Yaw < -0.5) && in.Throttle < 0.3 {
			s.Gear.Catapult = -1 // deliberate steer-away at low power: the crew unhooks and you taxi off
			s.Gear.Stroke = -2   // released latch: no re-attach until clear of the shuttle
		}
		return
	}
	heading := c.Heading + cat.Heading
	track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
	relative := s.Velocity.Subtract(c.direction().Scale(c.Speed))
	s.Gear.Stroke += math.Max(relative.Dot(track), 0) * Dt
	if s.Gear.Stroke >= cat.Stroke {
		s.Gear.Catapult = -1
		s.Gear.Stroke = -1
	}
}

// wire captures on a swept hook-path crossing and releases when the hook is
// raised.
func (m *Model) wire(s *State, in Inputs) {
	c := m.World.Carrier
	if c == nil {
		return
	}
	if s.Gear.Wire >= 0 {
		if !in.Hook {
			s.Gear.Wire = -1
		}
		return
	}
	if !in.Hook {
		return
	}
	relative := s.Velocity.Subtract(c.direction().Scale(c.Speed))
	if relative.Length() < 15 { // taxiing over a wire does not catch
		return
	}
	now := m.hook(s)
	before := now.Subtract(s.Velocity.Scale(Dt)) // the swept path, derived — nothing stored
	local := c.local(now, s.Time, m.Environment.Wrap)
	if local.Y < -3 || local.Y > 4 {
		return // hook nowhere near the deck plane
	}
	previous := c.local(before, s.Time, m.Environment.Wrap)
	for i := range c.Wires {
		w := &c.Wires[i]
		if crossing(previous.X, previous.Z, local.X, local.Z, w.A.X, w.A.Z, w.B.X, w.B.Z) {
			s.Gear.Wire = i
			return
		}
	}
}

// crossing is a 2D segment-intersection test.
func crossing(ax, az, bx, bz, cx, cz, dx, dz float64) bool {
	side := func(px, pz, qx, qz, rx, rz float64) float64 {
		return (qx-px)*(rz-pz) - (qz-pz)*(rx-px)
	}
	d1 := side(cx, cz, dx, dz, ax, az)
	d2 := side(cx, cz, dx, dz, bx, bz)
	d3 := side(ax, az, bx, bz, cx, cz)
	d4 := side(ax, az, bx, bz, dx, dz)
	return ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) && ((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))
}

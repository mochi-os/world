// Mochi world: Undercarriage and airframe contact
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Per-wheel contact: each strut independently queries the surface under its
// own contact point, so the moment balance — and toppling off a deck edge —
// emerges from the physics. Contact rule (project decision): wheels are
// simulated; the belly is a permitted low-friction skid for gear-up
// arrivals; the tailhook is the trap; ANY other airframe contact (the crash
// probes) is reported for the host to call a crash, with no force response.

package flight

import (
	"math"
)

const (
	regular   = 0.1  // m/s: regularised-Coulomb knee (no stiction chatter)
	rolling   = 0.02 // tyre rolling-resistance coefficient
	braking   = 0.45 // braking friction, both mains
	cornering = 0.8  // lateral tyre friction
	sliding   = 0.25 // belly-skid friction
	soft_drag = 0.18 // extra rolling coefficient on unpaved ground
)

// contact accumulates gear, belly, catapult, and cable forces for a trial
// state. Forces are continuous in the trial motion (spring-dampers and
// regularised friction), so they live inside the RK4 derivative; all
// discrete transitions happen in events().
func (m *Model) contact(s *State, in Inputs, total *Forces) {
	a := m.Airframe
	down := s.Gear.Extension
	if down > 0.05 {
		m.strut(s, &a.Gear.Nose, in, down, true, total)
		m.strut(s, &a.Gear.Left, in, down, false, total)
		m.strut(s, &a.Gear.Right, in, down, false, total)
	}
	if down < 0.95 { // belly skids carry a gear-up arrival
		for i := range a.Belly {
			m.skid(s, a.Belly[i], total)
		}
	}
	m.holdback(s, total)
	m.cable(s, in, total)
}

// strut is one landing-gear leg: a spring-damper normal force plus tyre
// friction, all applied at the wheel's own contact point.
func (m *Model) strut(s *State, leg *Strut, in Inputs, down float64, nose bool, total *Forces) {
	body := leg.Attach.Subtract(m.center)
	point := s.Position.Add(s.Attitude.Rotate(body))
	height, kind, carried, found := m.World.surface(point, s.Time, m.Environment.Wrap)
	if !found {
		return
	}
	depth := height - point.Y
	if depth <= 0 {
		return
	}
	if depth > leg.Travel*3 {
		depth = leg.Travel * 3 // bottomed out — the wheel never vanishes underground
	}
	velocity := s.Velocity.Add(s.Attitude.Rotate(s.Omega.Cross(body))).Subtract(carried)
	normal := (leg.Stiffness*depth - leg.Damping*velocity.Y) * down
	if normal <= 0 {
		return
	}
	force := Vec3{Y: normal}
	// Tyre friction in the surface plane, relative to the deck.
	slip := Vec3{X: velocity.X, Z: velocity.Z}
	roll := s.Attitude.Rotate(Vec3{X: 1})
	if nose && leg.Steer != 0 {
		// NWS authority blends LOW mode (22.5°, normal taxi) up to the full HI throw
		// only near standstill (tight spotting) — full HI at taxi speed spins the jet
		// on a dime and reads as twitchy under a bang-bang keyboard pedal.
		low := 22.5 * math.Pi / 180
		authority := leg.Steer
		if authority > low {
			authority = low + (leg.Steer-low)*clamp(1-slip.Length()/2.5, 0, 1)
		}
		steer := clamp(in.Yaw, -1, 1) * authority * clamp(1-slip.Length()/60, 0.1, 1)
		roll = s.Attitude.Rotate(Vec3{X: math.Cos(steer), Z: math.Sin(steer)})
	}
	roll = Vec3{X: roll.X, Z: roll.Z}.Normalize()
	along := slip.Dot(roll)
	side := slip.Subtract(roll.Scale(along))
	grip := rolling
	knee := regular
	if kind == Soft {
		grip += soft_drag
	}
	if in.Brake && !nose {
		grip += braking
		knee = regular / 10 // held brakes approximate stiction: idle thrust must not creep the parked jet
	}
	force = force.Add(roll.Scale(-grip * normal * along / math.Max(math.Abs(along), knee)))
	corner := cornering
	if nose && s.Gear.Catapult >= 0 && s.Gear.Stroke < 0 {
		corner = cornering * 0.2 // hookup: the nosewheel mostly casters while the bar rides the slot (full grip fights the lateral tow and parks the jet crabbed) — but not freely: some cornering keeps lateral damping in the nose, or the capture rolls and wobbles
	}
	force = force.Add(side.Scale(-corner * normal / math.Max(side.Length(), regular)))
	m.apply(s, force, point, total)
}

// skid is one belly contact point: a stiff structure spring with skid
// friction — survivable by design; the host judges the arrival.
func (m *Model) skid(s *State, at Vec3, total *Forces) {
	body := at.Subtract(m.center)
	point := s.Position.Add(s.Attitude.Rotate(body))
	height, _, carried, found := m.World.surface(point, s.Time, m.Environment.Wrap)
	if !found {
		return
	}
	depth := height - point.Y
	if depth <= 0 || depth > 1.5 {
		return
	}
	velocity := s.Velocity.Add(s.Attitude.Rotate(s.Omega.Cross(body))).Subtract(carried)
	normal := 2.5e6*depth - 2.5e5*velocity.Y
	if normal <= 0 {
		return
	}
	force := Vec3{Y: normal}
	slip := Vec3{X: velocity.X, Z: velocity.Z}
	force = force.Add(slip.Scale(-sliding * normal / math.Max(slip.Length(), regular)))
	m.apply(s, force, point, total)
}

// apply converts a world-frame contact force at a world point into the
// body-frame force/moment accumulator.
func (m *Model) apply(s *State, force Vec3, point Vec3, total *Forces) {
	local := s.Attitude.Unrotate(force)
	arm := s.Attitude.Unrotate(Vec3{
		X: Shortest(s.Position.X, point.X, m.Environment.Wrap),
		Y: point.Y - s.Position.Y,
		Z: Shortest(s.Position.Z, point.Z, m.Environment.Wrap),
	})
	total.Force = total.Force.Add(local)
	total.Moment = total.Moment.Add(arm.Cross(local))
}

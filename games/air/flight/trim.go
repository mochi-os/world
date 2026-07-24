// Mochi world: Trim solver
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Newton iteration for steady glide trim: unknowns (pitch attitude,
// symmetric stabilator, flight-path angle) zeroing the in-plane
// accelerations and the pitch moment. A converging bare-airframe trim is
// the first proof the aerodynamic signs are right (doc §15.1); the headless
// tool and the mode tests both start from it.

package flight

import (
	"math"
)

// Glide trims the model for a steady power-off descent at the given true
// airspeed and altitude. Returns the trimmed pitch attitude, stabilator
// deflection, and flight-path angle.
func Glide(m *Model, speed float64, altitude float64) (theta float64, stabilator float64, path float64, ok bool) {
	theta, stabilator, path = 0.05, -0.02, -0.05
	for iteration := 0; iteration < 60; iteration++ {
		r1, r2, r3 := m.residual(speed, altitude, theta, stabilator, path)
		if math.Abs(r1) < 1e-7 && math.Abs(r2) < 1e-7 && math.Abs(r3) < 1e-7 {
			return theta, stabilator, path, true
		}
		const h = 1e-6
		a1, a2, a3 := m.residual(speed, altitude, theta+h, stabilator, path)
		b1, b2, b3 := m.residual(speed, altitude, theta, stabilator+h, path)
		c1, c2, c3 := m.residual(speed, altitude, theta, stabilator, path+h)
		jacobian := Mat3{
			{(a1 - r1) / h, (b1 - r1) / h, (c1 - r1) / h},
			{(a2 - r2) / h, (b2 - r2) / h, (c2 - r2) / h},
			{(a3 - r3) / h, (b3 - r3) / h, (c3 - r3) / h},
		}
		step := jacobian.Inverse().Apply(Vec3{X: r1, Y: r2, Z: r3})
		limit := 0.1 // damped Newton: bounded steps keep the polar lookup sane
		theta -= clamp(step.X, -limit, limit)
		stabilator -= clamp(step.Y, -limit, limit)
		path -= clamp(step.Z, -limit, limit)
	}
	return theta, stabilator, path, false
}

// residual evaluates the steady-state errors for a trial trim: world-frame
// accelerations (horizontal, vertical) and the pitch moment, scaled for
// conditioning.
func (m *Model) residual(speed, altitude, theta, stabilator, path float64) (float64, float64, float64) {
	s := &m.State
	s.Position = Vec3{Y: altitude}
	s.Velocity = Vec3{X: speed * math.Cos(path), Y: speed * math.Sin(path)}
	s.Attitude = Axis(Vec3{Z: 1}, theta)
	s.Omega = Vec3{}
	s.Fcs.Stabilator = Pair{Left: stabilator, Right: stabilator}
	m.weigh()
	m.gust = Vec3{}
	local := air(altitude, m.Environment)
	total := m.forces(s, Inputs{}, local)
	accel := s.Attitude.Rotate(total.Force).Scale(1 / m.mass).Add(Vec3{Y: -m.Gravity})
	return accel.X / m.Gravity, accel.Y / m.Gravity, total.Moment.Z / (m.mass * m.Gravity * m.Airframe.Reference.Chord)
}

// Evaluate computes aircraft lift and drag coefficients for a body angle of
// attack in steady level flow at an altitude — the static analysis surface
// behind the trim solver, the EM sweeps, and the validation tooling.
func (m *Model) Evaluate(speed float64, angle float64, altitude float64) (float64, float64) {
	s := &m.State
	s.Position = Vec3{Y: altitude}
	s.Velocity = Vec3{X: speed}
	s.Attitude = Axis(Vec3{Z: 1}, angle)
	s.Omega = Vec3{}
	s.Fcs = FcsState{}
	m.weigh()
	m.gust = Vec3{}
	local := air(altitude, m.Environment)
	total := m.forces(s, Inputs{}, local)
	world := s.Attitude.Rotate(total.Force)
	q := 0.5 * local.Density * speed * speed * m.Airframe.Reference.Area
	return world.Y / q, -world.X / q
}

// Thrust reports installed dry and reheat thrust at a flight condition.
func (m *Model) Thrust(speed float64, altitude float64) (float64, float64) {
	local := air(altitude, m.Environment)
	mach := speed / local.Sound
	dry, wet := 0.0, 0.0
	for i := range m.Airframe.Engines {
		engine := &m.Airframe.Engines[i]
		d, _ := output(EngineState{Spool: 1}, engine, local.Density, mach)
		dry += d
		d, b := output(EngineState{Spool: 1, Reheat: 1}, engine, local.Density, mach)
		wet += d + b
	}
	return dry, wet
}

// Atmosphere exposes the local air state (density, pressure, temperature,
// speed of sound) for tooling.
func Atmosphere(altitude float64, env Environment) Air {
	return air(altitude, env)
}

// Level composes a steady level-flight state at a position, horizontal
// direction, and true airspeed — the shared spawn helper: server spawns,
// client resets, and tests all start from the same trim. The alpha and
// throttle come from the static solution, so the FCS takes over without a
// transient.
func Level(m *Model, position Vec3, direction Vec3, speed float64, fuel float64) State {
	m.State.Fuel = fuel
	m.weigh()
	q := 0.5 * air(position.Y, m.Environment).Density * speed * speed
	demand := m.mass * gravity / (q * m.Airframe.Reference.Area)
	angle := 0.0
	for sweep := -0.05; sweep <= 0.45; sweep += 0.002 {
		cl, _ := m.Evaluate(speed, sweep, position.Y)
		angle = sweep
		if cl >= demand {
			break
		}
	}
	_, drag := m.Evaluate(speed, angle, position.Y)
	dry, _ := m.Thrust(speed, position.Y)
	spool := clamp(drag*q*m.Airframe.Reference.Area/math.Max(dry, 1), 0.1, 1)
	forward := Vec3{X: direction.X, Z: direction.Z}.Normalize()
	// KNOWN DEFECT, deliberately left standing: this composes MINUS the alpha
	// solved for immediately above. Measured at 220 m/s / 4572 m the spawn holds
	// -2.865° when the solver returned +2.865°, and Evaluate confirms the
	// POSITIVE value is the one whose lift balances the weight — so every Level
	// spawn starts about 2·alpha off trim and the FCS flies out a transient this
	// helper exists to prevent. Correcting it is not a local change: with the
	// sign fixed, engagements turn markedly more lethal (14-seed section sweep:
	// total deaths 10 -> 27) and the section advantage TestBotSection pins
	// disappears (3 v 7 deaths becomes 15 v 12), because the bot doctrine was
	// calibrated against the mistrimmed spawn. Fix it together with a bot-doctrine
	// revalidation, not on its own. Approach below uses the correct sign.
	attitude := Axis(forward.Cross(Vec3{Y: 1}).Normalize(), -angle).Multiply(Look(forward)).Normalize()
	s := State{
		Position: position,
		Velocity: forward.Scale(speed),
		Attitude: attitude,
		Fuel:     fuel,
	}
	s.Engine[0] = EngineState{Spool: spool}
	s.Engine[1] = EngineState{Spool: spool}
	s.Fcs.Normal = 1
	s.Fcs.Reference = angle // stick-free attitude hold: spawn holding the trimmed attitude
	s.Fcs.Demand = 1
	s.Gear = GearState{Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	return s
}

// Approach composes a steady on-speed descent at a position, horizontal
// direction, and flight-path angle (radians, negative descending), returning
// the trimmed state and the throttle that holds it — the landing-configuration
// sibling of Level, and the shared source of approach trim for spawns, client
// resets, and tests.
//
// An AoA-referenced approach fixes ALPHA, not speed: on-speed is a wing
// condition, so the speed follows from the weight. That is why the answer must
// come from the core — a hand-carried spawn speed silently goes stale the
// moment the airframe, the droop schedule, or the PA law moves under it, and
// the aircraft then spawns off-trim (the carrier landing start balloon).
//
// The trim cannot be found by flying it: holding alpha leaves the speed mode
// neutrally stable, so a powered approach wanders on a long, barely-damped
// phugoid with no settled path to measure (which is exactly why the real jet
// carries an approach power compensator). It is solved statically instead.
func Approach(m *Model, position Vec3, direction Vec3, path float64, fuel float64) (State, float64) {
	m.State.Fuel = fuel
	m.weigh()
	alpha := m.Airframe.Control.Onspeed

	// Newton on (speed, stabilator, throttle) with alpha and the flight path
	// held: the three residuals are the in-plane accelerations and the pitch
	// moment, as in Glide. Solving for the stabilator matters — it carries the
	// download that balances the droop's nose-down moment, and a trim computed
	// without it is wrong in lift AND drag.
	speed, stabilator, throttle := 70.0, -0.02, 0.2
	for iteration := 0; iteration < 80; iteration++ {
		r1, r2, r3 := m.approaching(speed, position.Y, alpha, stabilator, throttle, path)
		if math.Abs(r1) < 1e-7 && math.Abs(r2) < 1e-7 && math.Abs(r3) < 1e-7 {
			break
		}
		const h = 1e-6
		a1, a2, a3 := m.approaching(speed+h, position.Y, alpha, stabilator, throttle, path)
		b1, b2, b3 := m.approaching(speed, position.Y, alpha, stabilator+h, throttle, path)
		c1, c2, c3 := m.approaching(speed, position.Y, alpha, stabilator, throttle+h, path)
		jacobian := Mat3{
			{(a1 - r1) / h, (b1 - r1) / h, (c1 - r1) / h},
			{(a2 - r2) / h, (b2 - r2) / h, (c2 - r2) / h},
			{(a3 - r3) / h, (b3 - r3) / h, (c3 - r3) / h},
		}
		step := jacobian.Inverse().Apply(Vec3{X: r1, Y: r2, Z: r3})
		speed = clamp(speed-clamp(step.X, -5, 5), 40, 160) // damped Newton, bounded to sane approach numbers
		stabilator -= clamp(step.Y, -0.05, 0.05)
		throttle = clamp(throttle-clamp(step.Z, -0.1, 0.1), 0, 1)
	}

	forward := Vec3{X: direction.X, Z: direction.Z}.Normalize()
	velocity := forward.Scale(speed * math.Cos(path))
	velocity.Y = speed * math.Sin(path)
	// Attitude = flight path + on-speed alpha: the PA law's neutral demand
	// exactly, so there is no capture transient to fly out of.
	side := forward.Cross(Vec3{Y: 1}).Normalize()
	attitude := Axis(side, path+alpha).Multiply(Look(forward)).Normalize() // +angle pitches the nose UP; see the note in Level, which still carries the opposite sign
	droop, slat := m.Approaching(0.5 * air(position.Y, m.Environment).Density * speed * speed)

	s := State{Position: position, Velocity: velocity, Attitude: attitude, Fuel: fuel}
	achieved := idle + clamp(throttle, 0, 1)*(1-idle) // the lever commands through the idle floor
	s.Engine[0] = EngineState{Spool: achieved}
	s.Engine[1] = EngineState{Spool: achieved}
	s.Fcs.Stabilator = Pair{Left: stabilator, Right: stabilator}
	s.Fcs.Flaperon = Pair{Left: droop, Right: droop}
	s.Fcs.Flap = droop
	s.Fcs.Slat = slat
	// Hand the PA law its own trim: on-speed with no rate leaves its error term
	// at zero, so the commanded stabilator IS minus the integrator. Spawning
	// with a cold integrator makes the law wind up from scratch and the jet
	// balloons through the first swing of the phugoid.
	s.Fcs.Integral = clamp(-stabilator, -0.45, 0.45)
	s.Fcs.Normal = 1
	s.Fcs.Demand = 1
	s.Fcs.Reference = path + alpha
	s.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	return s, throttle
}

// approaching evaluates the steady-state errors for a trial powered-approach
// trim at a FIXED angle of attack and flight path: world-frame accelerations
// (horizontal, vertical) and the pitch moment, scaled for conditioning — the
// landing-configuration counterpart of residual.
func (m *Model) approaching(speed, altitude, alpha, stabilator, throttle, path float64) (float64, float64, float64) {
	s := &m.State
	s.Position = Vec3{Y: altitude}
	s.Velocity = Vec3{X: speed * math.Cos(path), Y: speed * math.Sin(path)}
	s.Attitude = Axis(Vec3{Z: 1}, path+alpha)
	s.Omega = Vec3{}
	local := air(altitude, m.Environment)
	droop, slat := m.Approaching(0.5 * local.Density * speed * speed)
	// The droop reaches the wing through the FLAPERON actuators (Fcs.Flap is a
	// readout only) — the same path the FCS drives.
	s.Fcs = FcsState{
		Stabilator: Pair{Left: stabilator, Right: stabilator},
		Flaperon:   Pair{Left: droop, Right: droop},
		Flap:       droop,
		Slat:       slat,
	}
	s.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	achieved := idle + clamp(throttle, 0, 1)*(1-idle)
	s.Engine[0] = EngineState{Spool: achieved}
	s.Engine[1] = EngineState{Spool: achieved}
	m.weigh()
	m.gust = Vec3{}
	total := m.forces(s, Inputs{Gear: true, Throttle: throttle}, local)
	accel := s.Attitude.Rotate(total.Force).Scale(1 / m.mass).Add(Vec3{Y: -m.Gravity})
	return accel.X / m.Gravity, accel.Y / m.Gravity, total.Moment.Z / (m.mass * m.Gravity * m.Airframe.Reference.Chord)
}

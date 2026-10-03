// Mochi world: Trim solver
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Newton iteration for steady glide trim: unknowns (pitch attitude, symmetric
// stabilator, flight-path angle) zeroing the in-plane accelerations and the
// pitch moment (doc 15.1).

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
	s.Gear.Extension = 0 // bare airframe: clean, same rule as Evaluate
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
	s.Gear.Extension = 0 // the static analysis is the CLEAN aircraft (a fresh model carries the deck default, gear down, which silently added the undercarriage plate to every Evaluate consumer — Level's power solve included); the configured analysis composes its own state in approaching
	m.weigh()
	m.gust = Vec3{}
	local := air(altitude, m.Environment)
	total := m.forces(s, Inputs{}, local)
	world := s.Attitude.Rotate(total.Force)
	q := 0.5 * local.Density * speed * speed * m.Airframe.Reference.Area
	if q <= 0 {
		return 0, 0 // no flow, no coefficients: a division here poisons every caller with NaN
	}
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

// Cruise reports the fuel flow of trimmed, unaccelerated level flight at an
// altitude and Mach number on dry power, for the jet as it is now: its fuel,
// its external tanks and stores, and whatever damage it carries. The flow is
// in kg/s for all engines and the speed is the true airspeed, m/s. It is the
// cruise performance behind the FPAS best Mach and optimum figures (NATOPS
// 2.3.1.1): a host searches it over Mach and altitude.
//
// The solve is Newton on (alpha, stabilator, core fraction) zeroing the
// in-plane accelerations and the pitch moment, with the leading and trailing
// edges where the up-and-away law's schedules put them at that alpha and
// dynamic pressure - the configuration the stepping model settles in. It
// runs on a scratch model in free air, so the flying state is untouched.
//
// Not ok: an altitude out of range, no level trim there (too slow for the
// wing, or no Mach number at all), or one that needs more than military
// power. Dry power bounds the envelope at both ends before the stabilator's
// throw does.
func (m *Model) Cruise(altitude float64, mach float64) (flow float64, speed float64, ok bool) {
	if !(altitude >= 0 && altitude <= 20000) {
		return 0, 0, false
	}
	if m.cruise == nil {
		m.cruise = New(m.Airframe, m.Environment, World{Sea: -1e6})
	}
	c := m.cruise
	c.Environment = m.Environment
	c.stores = m.stores
	c.State.Fuel = m.State.Fuel
	c.State.External = m.State.External
	c.State.Damage = m.State.Damage
	local := air(altitude, c.Environment)
	speed = mach * local.Sound
	theta, stabilator, core := 0.05, -0.02, 0.5
	// The forces are not perfectly smooth in alpha: they carry steps of the
	// order of 1e-4 g, and a trim that lands on one has no exact root for
	// Newton to settle on. The nearest iterate is then the trim, if it is
	// within what such a step is worth - under half a percent of the thrust.
	const exact, near = 1e-7, 5e-4
	least, thrust := math.Inf(1), 0.0
	for iteration := 0; iteration < 30; iteration++ {
		r1, r2, r3 := c.cruising(speed, altitude, theta, stabilator, core)
		if worst := math.Max(math.Abs(r1), math.Max(math.Abs(r2), math.Abs(r3))); worst < least {
			least, thrust = worst, core
		}
		if least < exact {
			break
		}
		const h = 1e-6
		a1, a2, a3 := c.cruising(speed, altitude, theta+h, stabilator, core)
		b1, b2, b3 := c.cruising(speed, altitude, theta, stabilator+h, core)
		c1, c2, c3 := c.cruising(speed, altitude, theta, stabilator, core+h)
		jacobian := Mat3{
			{(a1 - r1) / h, (b1 - r1) / h, (c1 - r1) / h},
			{(a2 - r2) / h, (b2 - r2) / h, (c2 - r2) / h},
			{(a3 - r3) / h, (b3 - r3) / h, (c3 - r3) / h},
		}
		step := jacobian.Inverse().Apply(Vec3{X: r1, Y: r2, Z: r3})
		theta = clamp(theta-clamp(step.X, -0.05, 0.05), -0.2, c.Airframe.Limit.Alpha) // damped Newton, as Glide and Approach; no trim past the alpha limit
		stabilator -= clamp(step.Y, -0.05, 0.05)
		core = clamp(core-clamp(step.Z, -0.2, 0.2), -1, 3) // free past both ends: a trim outside the dry range must be found to be refused
	}
	if !(least < near) || !(thrust >= 0 && thrust <= 1) {
		return 0, speed, false
	}
	for i := range c.Airframe.Engines {
		dry, _ := output(EngineState{Spool: thrust}, &c.Airframe.Engines[i], local.Density, mach)
		flow += dry * c.Airframe.Engines[i].Flow.Dry * c.State.Damage.engine(i)
	}
	return flow, speed, true
}

// cruising evaluates the steady-state errors for a trial level-flight trim at
// a true airspeed: world-frame accelerations (horizontal, vertical) and the
// pitch moment, scaled as residual scales them. The wing is as the
// up-and-away law flies it in steady flight: the leading edge down with alpha
// and the AUTO manoeuvring flap with alpha washed out by dynamic pressure,
// both inside what blowdown leaves.
func (m *Model) cruising(speed, altitude, theta, stabilator, core float64) (float64, float64, float64) {
	s := &m.State
	c := &m.Airframe.Control
	local := air(altitude, m.Environment)
	pressure := 0.5 * local.Density * speed * speed
	slat := clamp(c.Slat.Slope*(theta-c.Slat.Offset), 0, c.Slat.Limit)
	droop := clamp(c.Flap.Slope*(theta-c.Flap.Offset), 0, c.Flap.Limit) * clamp(1-pressure/c.Flap.Pressure, 0, 1)
	droop = math.Min(droop, c.Throw.Flaperon.Down*clamp(c.Blowdown/math.Max(pressure, 1), 0, 1))
	s.Position = Vec3{Y: altitude}
	s.Velocity = Vec3{X: speed}
	s.Attitude = Axis(Vec3{Z: 1}, theta)
	s.Omega = Vec3{}
	s.Fcs = FcsState{
		Stabilator: Pair{Left: stabilator, Right: stabilator},
		Flaperon:   Pair{Left: droop, Right: droop},
		Slat:       slat,
	}
	s.Gear = GearState{Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	for i := range s.Engine {
		s.Engine[i] = EngineState{}
		if i < len(m.Airframe.Engines) {
			s.Engine[i] = EngineState{Spool: core}
		}
	}
	m.weigh()
	m.gust = Vec3{}
	total := m.forces(s, Inputs{}, local)
	accel := s.Attitude.Rotate(total.Force).Scale(1 / m.mass).Add(Vec3{Y: -m.Gravity})
	return accel.X / m.Gravity, accel.Y / m.Gravity, total.Moment.Z / (m.mass * m.Gravity * m.Airframe.Reference.Chord)
}

// Atmosphere exposes the local air state (density, pressure, temperature,
// speed of sound) for tooling.
func Atmosphere(altitude float64, env Environment) Air {
	return air(altitude, env)
}

// Level composes a steady level-flight state at a position, horizontal
// direction and true airspeed - the shared spawn helper for server spawns,
// client resets and tests. Alpha and throttle come from the static solution.
// The velocity is the airspeed plus the wind where the jet is: it was the
// airspeed alone, so in the single-player trade wind (62 kt at 15,000 ft) a
// jet spawned at 428 kt flew 366 kt through the air heading downwind and 490
// kt into it, and each joust began with the pilot 53 kt faster or slower than
// the bandit on the side the coin gave him.
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
	// Axis(side, +angle) pitches the nose UP by angle. Inverting this sign spawns
	// every jet at minus the trimmed alpha: a -1.4 g bunt and a phugoid.
	attitude := Axis(forward.Cross(Vec3{Y: 1}).Normalize(), angle).Multiply(Look(forward)).Normalize()
	s := State{
		Position: position,
		Velocity: forward.Scale(speed).Add(wind(position, 0, m.Environment, nil)), // the state's clock starts at zero
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

// Approach composes a steady on-speed descent (path in radians, negative
// descending), returning the trimmed state and the throttle that holds it.
// Alpha is fixed and the speed follows from weight, so it is solved statically.
func Approach(m *Model, position Vec3, direction Vec3, path float64, fuel float64) (State, float64) {
	m.State.Fuel = fuel
	m.weigh()
	alpha := m.Airframe.Control.Onspeed

	// Newton on (speed, stabilator, throttle) with alpha and the flight path held;
	// residuals as in Glide. The stabilator must be solved: it carries the
	// download balancing the droop's nose-down moment, in lift and drag.
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
	// The trim is through the air: the state rides the wind, as Level's does.
	// Left out, a start into the ship's wind over the deck began that much
	// fast, shed it in a climb, and ballooned off the altitude it was given.
	velocity = velocity.Add(wind(position, 0, m.Environment, nil)) // the state's clock starts at zero
	// Attitude = flight path + on-speed alpha: the PA law's neutral demand
	// exactly, so there is no capture transient to fly out of.
	side := forward.Cross(Vec3{Y: 1}).Normalize()
	attitude := Axis(side, path+alpha).Multiply(Look(forward)).Normalize() // +angle pitches the nose UP; see the note in Level, which still carries the opposite sign
	droop, slat := m.Approaching(0.5 * air(position.Y, m.Environment).Density * speed * speed)

	// The stores' tanks as the trim weighed them: the caller mounts the loadout
	// first, and a state without their fuel flies lighter than it was trimmed.
	s := State{Position: position, Velocity: velocity, Attitude: attitude, Fuel: fuel, External: m.State.External}
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

// Mochi world: Rigid body
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Newton–Euler six-degree-of-freedom dynamics with the full inertia tensor
// (including the roll–yaw cross-term — Mat3[0][1] in our y-up frame), RK4
// integration at the fixed timestep, quaternion attitude kinematics, and
// mass properties recomputed from fuel each step.

package flight

// Model binds the immutable airframe, environment, and world geometry to a
// mutable State and steps it. One Model per aircraft; prediction rewind is
// an assignment to State.
type Model struct {
	Airframe    *Airframe
	Environment Environment
	World       World
	State       State
	Direct      bool    // no augmentation: stick drives surfaces (validation)
	Gravity     float64 // m/s²; New sets standard, tests may zero it

	// Per-step caches:
	mass    float64
	center  Vec3 // combined CG, body, from datum
	inertia Mat3 // about the combined CG
	inverse Mat3
	gust    Vec3      // wind at the pre-step position (frozen per step)
	lift    []float64 // pass-one surface lift coefficients
	first   bool      // capture flag: record normal on the k1 stage only
}

// New builds a model at rest at the origin. Airframes declare at most four
// engines — the encoded state carries four fixed slots; gang a larger count
// into pods (a B-52's eight engines are four pods of two).
func New(airframe *Airframe, environment Environment, world World) *Model {
	if len(airframe.Engines) > len((&State{}).Engine) {
		panic("flight: airframe " + airframe.Name + " declares more than four engines — gang them into pods")
	}
	m := &Model{Airframe: airframe, Environment: environment, World: world, Gravity: gravity}
	m.State.Attitude = Quat{W: 1}
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Fuel = airframe.Mass.Fuel
	m.lift = make([]float64, len(airframe.Surfaces))
	return m
}

// SetWorld replaces the world geometry (host re-init).
func (m *Model) SetWorld(w World) { m.World = w }

// Step advances the simulation exactly Dt. Pure given (State, in,
// Environment, World): no I/O, clock, randomness, or allocation.
func (m *Model) Step(in Inputs) {
	m.weigh()
	local := air(m.State.Position.Y, m.Environment)
	m.gust = wind(m.State.Position, m.State.Time, m.Environment, m.World.Carrier)
	m.fcs(in, local)
	m.spool(in)
	m.events(in)
	m.integrate(in, local)
	m.wrap()
	m.burn()
	m.State.Time += Dt
}

// weigh caches mass, combined CG, and the inertia tensor (and inverse) for
// the step: empty airframe + fuel point mass at the tank + damage shift.
func (m *Model) weigh() {
	a := m.Airframe
	fuel := m.State.Fuel
	m.mass = a.Mass.Empty + fuel
	m.center = a.Center.Scale(a.Mass.Empty).Add(a.Tank.Scale(fuel)).Scale(1 / m.mass).Add(m.State.Damage.Shift)
	// Parallel-axis: empty tensor is about the empty CG; move both masses to
	// the combined CG.
	tensor := a.Inertia.Add(parallel(a.Center.Subtract(m.center), a.Mass.Empty))
	tensor = tensor.Add(parallel(a.Tank.Subtract(m.center), fuel))
	m.inertia = tensor
	m.inverse = tensor.Inverse()
}

// parallel is the parallel-axis theorem term for a point mass at offset r.
func parallel(r Vec3, mass float64) Mat3 {
	d := r.Dot(r)
	return Mat3{
		{mass * (d - r.X*r.X), -mass * r.X * r.Y, -mass * r.X * r.Z},
		{-mass * r.Y * r.X, mass * (d - r.Y*r.Y), -mass * r.Y * r.Z},
		{-mass * r.Z * r.X, -mass * r.Z * r.Y, mass * (d - r.Z*r.Z)},
	}
}

// motion is the integrated subset of State.
type motion struct {
	position Vec3
	velocity Vec3
	attitude Quat
	omega    Vec3
}

// derivative evaluates the equations of motion at a trial motion state.
func (m *Model) derivative(at motion, in Inputs, local Air) (rate motion) {
	trial := m.State
	trial.Position, trial.Velocity, trial.Attitude, trial.Omega = at.position, at.velocity, at.attitude, at.omega
	total := m.forces(&trial, in, local)
	if m.first {
		m.first = false
		m.State.Fcs.Normal = total.Force.Y / (m.mass * gravity) // the g meter and C* sensor
	}
	rate.position = at.velocity
	rate.velocity = at.attitude.Rotate(total.Force).Scale(1 / m.mass).Add(Vec3{Y: -m.Gravity})
	coriolis := at.omega.Cross(m.inertia.Apply(at.omega))
	rate.omega = m.inverse.Apply(total.Moment.Subtract(coriolis))
	q := at.attitude.Derivative(at.omega)
	rate.attitude = q
	return rate
}

// integrate advances the 13 dynamic variables one Dt by classic RK4, then
// renormalises the attitude once.
func (m *Model) integrate(in Inputs, local Air) {
	s := &m.State
	a := motion{s.Position, s.Velocity, s.Attitude, s.Omega}
	m.first = true
	k1 := m.derivative(a, in, local)
	k2 := m.derivative(advance(a, k1, Dt/2), in, local)
	k3 := m.derivative(advance(a, k2, Dt/2), in, local)
	k4 := m.derivative(advance(a, k3, Dt), in, local)
	s.Position = a.position.Add(combine(k1.position, k2.position, k3.position, k4.position))
	s.Velocity = a.velocity.Add(combine(k1.velocity, k2.velocity, k3.velocity, k4.velocity))
	s.Omega = a.omega.Add(combine(k1.omega, k2.omega, k3.omega, k4.omega))
	s.Attitude = Quat{
		W: a.attitude.W + blend(k1.attitude.W, k2.attitude.W, k3.attitude.W, k4.attitude.W),
		X: a.attitude.X + blend(k1.attitude.X, k2.attitude.X, k3.attitude.X, k4.attitude.X),
		Y: a.attitude.Y + blend(k1.attitude.Y, k2.attitude.Y, k3.attitude.Y, k4.attitude.Y),
		Z: a.attitude.Z + blend(k1.attitude.Z, k2.attitude.Z, k3.attitude.Z, k4.attitude.Z),
	}.Normalize()
}

func advance(a motion, k motion, dt float64) motion {
	return motion{
		position: a.position.Add(k.position.Scale(dt)),
		velocity: a.velocity.Add(k.velocity.Scale(dt)),
		attitude: Quat{a.attitude.W + k.attitude.W*dt, a.attitude.X + k.attitude.X*dt, a.attitude.Y + k.attitude.Y*dt, a.attitude.Z + k.attitude.Z*dt},
		omega:    a.omega.Add(k.omega.Scale(dt)),
	}
}

func combine(k1, k2, k3, k4 Vec3) Vec3 {
	return k1.Add(k2.Scale(2)).Add(k3.Scale(2)).Add(k4).Scale(Dt / 6)
}

func blend(k1, k2, k3, k4 float64) float64 {
	return (k1 + 2*k2 + 2*k3 + k4) * Dt / 6
}

// wrap applies the toroidal world after integration, centred on the origin.
func (m *Model) wrap() {
	size := m.Environment.Wrap
	if size <= 0 {
		return
	}
	m.State.Position.X = Shortest(0, m.State.Position.X, size)
	m.State.Position.Z = Shortest(0, m.State.Position.Z, size)
}

func (m *Model) forces(s *State, in Inputs, local Air) Forces {
	total := Forces{}
	m.aero(s, &total, local)
	m.propulsion(s, &total, local)
	m.contact(s, in, &total)
	m.stroke(s, &total)
	return total
}

// Forces is one accumulated evaluation, body frame, about the current CG.
type Forces struct {
	Force  Vec3
	Moment Vec3
}

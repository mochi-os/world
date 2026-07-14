// Mochi world: Propulsion
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Two F414-class turbofans: spool lag (asymmetric — engines accelerate
// slower than they decelerate), reheat staging gated on core speed,
// altitude lapse with ram recovery and an intake rolloff past M1.3, and
// fuel flow that feeds the mass/CG update. Thrust-to-weight stays below
// one at combat weight: no supercruise, energy is a budget.

package flight

import (
	"math"
)

const (
	spool_up   = 1.0  // s, core acceleration time constant
	spool_down = 0.6  // s, deceleration
	stage_lag  = 0.5  // s, reheat light/quench
	lapse      = 1.0  // density-ratio exponent: thrust falls with density — the transonic dash is hardest up high
	ram_dry    = 0.35 // Mach² ram-recovery gain, dry
	ram_wet    = 0.55 // reheat benefits more from ram
	rolloff    = 1.3  // Mach where intake losses begin
)

// idle is the flight-idle core fraction: a turbofan at idle still makes a
// few percent of military thrust, so a parked jet creeps and needs brakes.
const idle = 0.04

// spool advances the engine states one step.
func (m *Model) spool(in Inputs) {
	throttle := idle + clamp(in.Throttle, 0, 1)*(1-idle)
	if m.State.Fuel <= 0 {
		throttle = 0 // flameout: dry tanks wind the cores down and kill reheat
	}
	for i := range m.State.Engine {
		e := &m.State.Engine[i]
		if i >= len(m.Airframe.Engines) {
			*e = EngineState{} // no engine in this slot
			continue
		}
		constant := spool_up
		if throttle < e.Spool {
			constant = spool_down
		}
		e.Spool += (throttle - e.Spool) * Dt / constant
		lit := 0.0
		if in.Reheat > 0 && e.Spool > 0.85 && m.State.Fuel > 0 {
			// The F404 stages reheat in five discrete zones: the fuel control
			// lights whole segments, so the commanded fraction quantizes up.
			lit = math.Ceil(clamp(in.Reheat, 0, 1)*5) / 5
		}
		e.Reheat += (lit - e.Reheat) * Dt / stage_lag
	}
}

// output is one engine's dry and reheat thrust at the flight condition.
func output(state EngineState, engine *Engine, density float64, mach float64) (float64, float64) {
	sigma := math.Pow(density/1.225, lapse)
	intake := 1.0
	if mach > rolloff {
		intake = clamp(1-(mach-rolloff)*0.8, 0.3, 1)
	}
	dry := engine.Dry * state.Spool * sigma * (1 + ram_dry*mach*mach) * intake
	boost := (engine.Reheat - engine.Dry) * state.Reheat * sigma * (1 + ram_wet*mach*mach) * intake
	return dry, boost
}

// propulsion adds engine forces for a trial state.
func (m *Model) propulsion(s *State, total *Forces, local Air) {
	v := s.Attitude.Unrotate(s.Velocity.Subtract(m.gust))
	mach := v.Length() / local.Sound
	for i := range m.Airframe.Engines {
		engine := &m.Airframe.Engines[i]
		dry, boost := output(s.Engine[i], engine, local.Density, mach)
		force := Vec3{X: (dry + boost) * m.State.Damage.engine(i)}
		total.Force = total.Force.Add(force)
		total.Moment = total.Moment.Add(engine.Position.Subtract(m.center).Cross(force))
	}
}

// burn decrements fuel by the flow the current condition demands.
func (m *Model) burn() {
	if m.Environment.Cheat.Fuel {
		return // infinite-fuel cheat: the tank (and with it the leak drain) stays frozen at the spawn load
	}
	local := air(m.State.Position.Y, m.Environment)
	v := m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust))
	mach := v.Length() / local.Sound
	flow := m.State.Damage.Leak
	for i := range m.Airframe.Engines {
		engine := &m.Airframe.Engines[i]
		dry, boost := output(m.State.Engine[i], engine, local.Density, mach)
		flow += dry*engine.Flow.Dry + boost*engine.Flow.Reheat
	}
	m.State.Fuel = math.Max(0, m.State.Fuel-flow*Dt)
}

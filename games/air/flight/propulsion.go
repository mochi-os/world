// Mochi world: Propulsion
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Two F414-class turbofans: spool lag (asymmetric — engines accelerate
// slower than they decelerate), reheat staging gated on core speed,
// altitude lapse with ram recovery and an intake rolloff past M1.6, and
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
	rolloff    = 1.6  // Mach where intake losses begin. Calibrated against the brochure top end (TestTopSpeed): at 1.3 the fixed inlets gave up far too early and the jet terminated at M1.50 at 11 km against the documented M1.7-1.8; ablating the rolloff entirely runs to M2.16, so the intake — not wave drag — owns the ceiling. 1.6 lands M1.76; the deck (M1.02) never feels it
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
	// The zero/negative-g feed limit (NATOPS: ten-second business): the oil
	// and fuel-boost pickups uncover while unloaded, and past the limit the
	// cores roll back to idle until positive g re-covers them. The rollback
	// and the slow relight are the teeth — no permanent damage is invented.
	if m.State.Fcs.Normal < 0.15 {
		m.starved += Dt
	} else if m.State.Fcs.Normal > 0.5 {
		m.starved = math.Max(0, m.starved-2*Dt)
	}
	if m.starved > 10 {
		throttle = math.Min(throttle, idle+0.05)
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
// Output is output exported for the rehearsal surrogate (#256): a reduced-order
// rollout must use the SAME thrust lapse the real jet flies, or it ranks plays
// against an engine that does not exist.
func Output(state EngineState, engine *Engine, density float64, mach float64) (float64, float64) {
	return output(state, engine, density, mach)
}

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

// burn decrements fuel by the flow the current condition demands. External
// fuel goes first — the real jet's bleed-air transfer keeps the internal
// tanks topped while the externals drain — so State.Fuel only falls once
// State.External is dry. Battle-damage leaks drain the internal tanks
// regardless: punctures are in the airframe, not the drop tanks.
func (m *Model) burn() {
	if m.Environment.Cheat.Fuel {
		return // infinite-fuel cheat: the tank (and with it the leak drain) stays frozen at the spawn load
	}
	local := air(m.State.Position.Y, m.Environment)
	v := m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust))
	mach := v.Length() / local.Sound
	flow := 0.0
	for i := range m.Airframe.Engines {
		engine := &m.Airframe.Engines[i]
		dry, boost := output(m.State.Engine[i], engine, local.Density, mach)
		// A damaged core burns in proportion to what it still produces — the
		// same health factor propulsion() applies to its thrust. Without it a
		// dead engine drank at full rate, halving single-engine range in the
		// one scenario where fuel is the drama (#41).
		flow += (dry*engine.Flow.Dry + boost*engine.Flow.Reheat) * m.State.Damage.engine(i)
	}
	if m.State.External > 0 {
		m.State.External = math.Max(0, m.State.External-flow*Dt)
	} else {
		m.State.Fuel = math.Max(0, m.State.Fuel-flow*Dt)
	}
	m.State.Fuel = math.Max(0, m.State.Fuel-m.State.Damage.Leak*Dt)
}

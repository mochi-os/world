// Mochi world: Propulsion
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Two F414-class turbofans: asymmetric spool lag, reheat staging gated on core
// speed, altitude lapse with ram recovery and an intake rolloff past M1.6, and
// fuel flow feeding the mass/CG update. No supercruise.

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
		target := throttle
		if i < len(in.Secure) && in.Secure[i] {
			target = 0 // secured: fuel off, the core winds down and reheat dies (NATOPS 15.1 — the fire drill); clearing the switch spools it back up
		}
		constant := spool_up
		if target < e.Spool {
			constant = spool_down
		}
		e.Spool += (target - e.Spool) * Dt / constant
		lit := 0.0
		if in.Reheat > 0 && e.Spool > 0.85 && m.State.Fuel > 0 && target > idle {
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
	// Ram recovery grows in from M0.25 to M0.9 (#95): the plain Mach² term
	// handed the mid-band fight (M0.55-0.75) a quarter of static thrust as a
	// free bonus, which is what left a 7 g turn at 450 KCAS with thrust to
	// spare and the sustained-rate peak parked at 400-450 KCAS. Full ram from
	// M0.9 up leaves the calibrated top end (deck terminal, the M1.7x dash,
	// the intake rolloff) untouched.
	ram := ramp((mach - 0.25) / 0.65)
	dry := engine.Dry * state.Spool * sigma * (1 + ram_dry*mach*mach*ram) * intake
	boost := (engine.Reheat - engine.Dry) * state.Reheat * sigma * (1 + ram_wet*mach*mach*ram) * intake
	return dry, boost
}

// ramp clamps a rise fraction to the unit interval.
func ramp(fraction float64) float64 {
	return clamp(fraction, 0, 1)
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

// burn decrements fuel by the flow the condition demands. External fuel goes
// first, so State.Fuel falls only once State.External is dry; damage leaks
// drain the internal tanks regardless. dumping/dumpfloor are the DUMP switch
// rate and its bingo-caution floor (NATOPS 2.2.7), internal fuel only.
const (
	dumping   = 6.0  // kg/s ≈ 790 lb/min
	dumpfloor = 1361 // kg = the 3,000 lb bingo caution
)

func (m *Model) burn(in Inputs) {
	if m.Environment.Cheat.Fuel {
		return // infinite-fuel cheat: the tank (and with it the leak drain) stays frozen at the spawn load
	}
	if in.Dump && m.State.Fuel > dumpfloor {
		m.State.Fuel = math.Max(dumpfloor, m.State.Fuel-dumping*Dt)
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

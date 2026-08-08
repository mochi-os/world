// Mochi world: Fuel dump and per-engine cutoff
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestDump: the DUMP switch drains internal fuel at the NATOPS rate
// (2.2.7: 600-1,000 lb/min) and terminates on its own at the bingo floor.
func TestDump(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 200, 3000)
	throttle := m.State.Engine[0].Spool
	start := m.State.Fuel
	for i := 0; i < 240*60; i++ {
		m.Step(Inputs{Throttle: throttle, Dump: true})
	}
	dumped := start - m.State.Fuel
	if rate := dumped / 60 * 60 * 2.2046; rate < 500 || rate > 1200 { // lb/min including the engines' own burn
		t.Fatalf("dump rate off the NATOPS 600-1,000 lb/min band: %.0f lb/min", rate)
	}
	m.State.Fuel = 1400 // just above the floor: the drain must stop AT it
	for i := 0; i < 240*30; i++ {
		m.Step(Inputs{Throttle: throttle, Dump: true})
	}
	if m.State.Fuel < 1300 {
		t.Fatalf("dump must terminate at the bingo floor, not drain the tanks: %.0f kg left", m.State.Fuel)
	}
}

// TestSecure: the per-engine cutoff (NATOPS 15.1) winds one core down while
// the other keeps its power, the asymmetric thrust yaws the jet toward the
// dead engine, and clearing the switch relights it.
func TestSecure(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 200, Fighter.Mass.Fuel*0.5)
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{Throttle: 1, Secure: [2]bool{true, false}})
	}
	if left := m.State.Engine[0].Spool; left > 0.05 {
		t.Fatalf("the secured core must wind down: spool %.2f", left)
	}
	if right := m.State.Engine[1].Spool; right < 0.9 {
		t.Fatalf("the live engine must keep its power: spool %.2f", right)
	}
	// The FCS coordinates the asymmetry away (as the real one does), so the
	// signature is the rudder standing against the live engine — measured
	// ~1° at military — not a persistent yaw rate.
	if math.Abs(m.State.Fcs.Rudder) < 0.5*math.Pi/180 {
		t.Fatalf("asymmetric thrust must load the rudder: %.2f°", m.State.Fcs.Rudder*180/math.Pi)
	}
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{Throttle: 1})
	}
	if left := m.State.Engine[0].Spool; left < 0.9 {
		t.Fatalf("clearing the switch must relight: spool %.2f", left)
	}
}

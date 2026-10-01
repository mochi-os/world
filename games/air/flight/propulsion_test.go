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

// TestIdleStop: the inflight IDLE stop (NATOPS 2.1.1.7.2). With weight off the
// wheels the throttles rest on it - a higher idle than the ground's and a
// shorter run to MIL. Pulled to it at 5 g or more it retracts and the cores
// reach ground idle, until the throttles come off it or the jet lands.
func TestIdleStop(t *testing.T) {
	fresh := func(wow bool, g float64) *Model {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State.Fuel = 3000
		m.State.Gear.Wow = wow
		m.State.Fcs.Normal = g
		m.State.Engine[0] = EngineState{Spool: 0.5}
		m.State.Engine[1] = EngineState{Spool: 0.5}
		return m
	}
	settle := func(m *Model, in Inputs, seconds float64) float64 {
		for i := 0; i < int(240*seconds); i++ {
			m.spool(in)
		}
		return m.State.Engine[0].Spool
	}
	near := func(a float64, b float64) bool { return math.Abs(a-b) < 0.005 }
	deck, aloft := fresh(true, 1), fresh(false, 1)
	if ground, flight := settle(deck, Inputs{}, 30), settle(aloft, Inputs{}, 30); !near(ground, idle) || !near(flight, stop) {
		t.Fatalf("idle settled at %.3f on the wheels and %.3f airborne, want %.2f and %.2f", ground, flight, idle, stop)
	}
	// N2 as the cockpit reads it, 65% + 34% of the core: inside NATOPS 4.1.1.1's
	// 63 to 70% ground idle and 68 to 73% flight idle.
	if ground, flight := 65+34*idle, 65+34*stop; ground < 63 || ground > 70 || flight < 68 || flight > 73 {
		t.Fatalf("N2 %.1f%% on the ground and %.1f%% in flight, outside NATOPS 4.1.1.1", ground, flight)
	}
	run := func(m *Model) int {
		steps := 0
		for m.State.Engine[0].Spool < 0.95 && steps < 240*60 {
			m.spool(Inputs{Throttle: 1})
			steps++
		}
		return steps
	}
	if short, long := run(aloft), run(deck); short >= long {
		t.Errorf("flight idle to MIL took %d steps against ground idle's %d, want fewer", short, long)
	}
	// At the stop under 5 g it retracts, and stays retracted as the g comes off.
	m := fresh(false, 5.5)
	if got := settle(m, Inputs{}, 30); !near(got, idle) || !m.State.Retracted {
		t.Fatalf("at the stop under 5.5 g: spool %.3f retracted %v, want ground idle and the stop retracted", got, m.State.Retracted)
	}
	m.State.Fcs.Normal = 1
	if got := settle(m, Inputs{}, 5); !near(got, idle) || !m.State.Retracted {
		t.Fatalf("unloaded with the throttles still at idle: spool %.3f retracted %v, want it to stay retracted", got, m.State.Retracted)
	}
	// Coming off the stop puts it back.
	settle(m, Inputs{Throttle: 0.5}, 1)
	if m.State.Retracted {
		t.Fatal("the throttles came off the stop and it stayed retracted")
	}
	if got := settle(m, Inputs{}, 30); !near(got, stop) {
		t.Fatalf("back at idle the cores settled at %.3f, want the stop's %.2f again", got, stop)
	}
	// 4.9 g does not retract it, nor 6 g with the throttles above it.
	if m := fresh(false, 4.9); !near(settle(m, Inputs{}, 30), stop) || m.State.Retracted {
		t.Error("the stop retracted under 5 g")
	}
	if m := fresh(false, 6); settle(m, Inputs{Throttle: 0.5}, 2) < stop || m.State.Retracted {
		t.Error("the stop retracted with the throttles above it")
	}
	// Landing puts it back.
	m = fresh(false, 6)
	settle(m, Inputs{}, 1)
	m.State.Gear.Wow = true
	settle(m, Inputs{}, 0.1)
	if m.State.Retracted {
		t.Error("the stop stayed retracted on the wheels")
	}
}

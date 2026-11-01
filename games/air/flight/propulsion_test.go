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
	m := New(Fighter, Environment{Frame: scene, Chart: scene, Seed: 1}, World{Sea: 0})
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
// dead engine, and clearing the switch relights it: at 200 m/s the airstream
// windmills the rotor fast enough to light, and it is back at power within
// half a minute, the airstart's time rather than a spool's.
func TestSecure(t *testing.T) {
	m := New(Fighter, Environment{Frame: scene, Chart: scene, Seed: 1}, World{Sea: 0})
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
	for i := 0; i < 240*30; i++ {
		m.Step(Inputs{Throttle: 1})
	}
	if left := m.State.Engine[0].Spool; left < 0.9 {
		t.Fatalf("clearing the switch must relight: spool %.2f", left)
	}
}

// still is a jet at rest with both engines stopped, its rotors still: the
// cold and dark start (#133). The engines are stepped alone, so it stays at
// rest however long they turn.
func still() *Model {
	m := New(Fighter, Environment{Frame: scene, Chart: scene, Seed: 1}, World{Sea: 0})
	m.State.Fuel = 3000
	m.State.Gear.Wow = true
	m.State.Velocity = Vec3{}
	for i := range m.State.Engine {
		m.State.Engine[i] = EngineState{Rundown: 1}
	}
	return m
}

// motor steps the engines alone for seconds.
func motor(m *Model, in Inputs, seconds float64) {
	for t := 0.0; t < seconds; t += m.Stride() {
		m.spool(in)
	}
}

// TestColdStart: the NATOPS start (7.4.3). A stopped engine with fuel to it
// and no starter does not light; ENG CRANK turns its rotor on the APU's air to
// about a quarter of idle; the throttle moved from OFF lights it, and it
// reaches ground idle half a minute later, with no thrust until then. The other
// engine stays stopped. Shut down, the rotor runs down to a standstill.
func TestColdStart(t *testing.T) {
	m := still()
	stopped, crank := [2]bool{true, true}, [2]bool{false, true}
	motor(m, Inputs{Secure: [2]bool{false, false}}, 30)
	if e := m.State.Engine[1]; e.Rundown < 0.99 || e.Spool != 0 {
		t.Fatalf("fuel alone must not light a stopped engine: rundown %.2f spool %.3f", e.Rundown, e.Spool)
	}
	motor(m, Inputs{Secure: stopped, Crank: crank}, 20)
	if e := m.State.Engine[1]; math.Abs(e.Rundown-cranked) > 0.02 || e.Spool != 0 {
		t.Fatalf("the starter must turn the rotor to about a quarter of idle with no thrust: rundown %.2f spool %.3f", e.Rundown, e.Spool)
	}
	if e := m.State.Engine[0]; e.Rundown < 0.99 {
		t.Fatalf("the engine not cranked must stay stopped: rundown %.2f", e.Rundown)
	}
	right := [2]bool{true, false}
	lit := 0.0
	for ; lit < 60 && m.State.Engine[1].Rundown > 0; lit += m.Stride() {
		m.spool(Inputs{Secure: right, Crank: crank})
		if m.State.Engine[1].Spool != 0 {
			t.Fatalf("an engine below idle makes no thrust: spool %.3f at %.1f s", m.State.Engine[1].Spool, lit)
		}
	}
	if lit < 12 || lit > 25 {
		t.Fatalf("from the throttle at IDLE the engine must reach idle in about 18 s from the crank's speed: %.1f s", lit)
	}
	motor(m, Inputs{Secure: right}, 5)
	if !m.pumped() || m.State.Engine[0].Spool != 0 || m.State.Engine[1].Spool < 0.03 {
		t.Fatalf("the started engine turns, powering HYD 2, and the other does not: spools %.3f %.3f", m.State.Engine[0].Spool, m.State.Engine[1].Spool)
	}
	motor(m, Inputs{Secure: stopped}, 60)
	if e := m.State.Engine[1]; e.Rundown < 0.95 || e.Spool != 0 {
		t.Fatalf("shut down, the rotor runs down to a standstill: rundown %.2f spool %.3f", e.Rundown, e.Spool)
	}
}

// TestWindmill: in flight the airstream turns a shut-down rotor. Fast enough,
// it relights with the fuel on; slow, it needs the starter.
func TestWindmill(t *testing.T) {
	for _, c := range []struct {
		speed float64
		light bool
	}{{200, true}, {90, false}} {
		m := New(Fighter, Environment{Frame: scene, Chart: scene, Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, c.speed, 3000)
		motor(m, Inputs{Throttle: 0.5, Secure: [2]bool{true, true}}, 60)
		motor(m, Inputs{Throttle: 0.5}, 40)
		if lit := m.State.Engine[0].Rundown == 0; lit != c.light {
			t.Fatalf("at %.0f m/s the windmilling engine relit %v, want %v (rundown %.2f)", c.speed, lit, c.light, m.State.Engine[0].Rundown)
		}
	}
}

// TestRundownEncodes: the rundown rides the encoded state, appended last.
func TestRundownEncodes(t *testing.T) {
	m := still()
	m.State.Engine[1].Rundown = 0.37
	out := make([]float64, Size)
	m.State.Encode(out)
	back := Decode(out)
	if back.Engine[0].Rundown != 1 || back.Engine[1].Rundown != 0.37 {
		t.Fatalf("rundown lost in the encoding: %v %v", back.Engine[0].Rundown, back.Engine[1].Rundown)
	}
}

// TestIdleThrust: idle makes what the class's idle makes. No figure is
// published for the F404; the F100 idles at about 6% of military thrust on
// the ground and 8% in flight, and thrust rising about as rpm^3.4 puts the
// F404's flight idle a fifth above its ground idle. And at the flight idle
// stop a jet in the landing configuration sheds speed fast enough to be on
// speed by the Case I abeam: rolled out of the break at 214 kt, below 170 kt
// 20 s later. At a 15% stop it was still at 188 kt, and the demonstration
// pilot turned final 3 km astern.
func TestIdleThrust(t *testing.T) {
	m := New(Fighter, Environment{Frame: scene, Chart: scene}, World{})
	engine := &m.Airframe.Engines[0]
	military, _ := output(EngineState{Spool: 1}, engine, 1.225, 0)
	share := func(spool float64) float64 {
		dry, _ := output(EngineState{Spool: spool}, engine, 1.225, 0)
		return dry / military
	}
	if ground, flight := share(idle), share(stop); ground < 0.05 || ground > 0.07 || flight < 0.07 || flight > 0.10 || flight/ground < 1.1 || flight/ground > 1.6 {
		t.Fatalf("idle %.1f%% of military thrust on the ground and %.1f%% in flight, want about 6%% and 8%%", 100*ground, 100*flight)
	}
	m.State = Level(m, Vec3{Y: 245}, Vec3{X: 1}, 214*0.514444, 2041)
	for i := 0; i < 240*20; i++ {
		m.Step(Inputs{Gear: true, Hook: true, Flap: 2})
	}
	if speed := m.State.Velocity.Length() / 0.514444; speed > 170 {
		t.Fatalf("dirty at the flight idle stop, 214 kt fell only to %.0f kt in 20 s", speed)
	}
}

// TestIdleStop: the inflight IDLE stop (NATOPS 2.1.1.7.2). With weight off the
// wheels the throttles rest on it - a higher idle than the ground's and a
// shorter run to MIL. Pulled to it at 5 g or more it retracts and the cores
// reach ground idle, until the throttles come off it or the jet lands.
func TestIdleStop(t *testing.T) {
	fresh := func(wow bool, g float64) *Model {
		m := New(Fighter, Environment{Frame: scene, Chart: scene, Seed: 1}, World{Sea: 0})
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

// Mochi world: Catapult and arrestor cable
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestCatapultEndSpeed: the electromagnetic catapult is closed loop, so the
// jet leaves the stroke at the end speed set for its weight whatever its
// engines add, and once its force has risen the acceleration holds within 1.05
// of its mean, as EMALS's requirement has it (Doyle 1995). The open-loop shot
// left 4-13 m/s faster than it was set for, its engines' thrust on top.
func TestCatapultEndSpeed(t *testing.T) {
	cases := []struct {
		name             string
		fuel             float64
		throttle, reheat float64
	}{
		{"light at military", 952, 0.93, 0},
		{"light in reheat", 952, 1, 1},
		{"heavy at military", 4900, 0.93, 0},
		{"heavy in reheat", 4900, 1, 1},
	}
	for _, c := range cases {
		m := aboard()
		park(m, 42.7, -0.6)
		m.State.Fuel = c.fuel
		for i := 0; i < 240*2; i++ {
			m.Step(Inputs{Gear: true, Flap: 1})
		}
		if m.State.Gear.Catapult != 0 {
			t.Fatalf("%s: did not attach: %d", c.name, m.State.Gear.Catapult)
		}
		for i := 0; i < 240*4; i++ {
			m.Step(Inputs{Gear: true, Flap: 1, Throttle: c.throttle, Reheat: c.reheat})
		}
		stall := math.Sqrt(2 * m.mass * gravity / (air(m.State.Position.Y, m.Environment).Density * 1.55 * m.Airframe.Reference.Area))
		want := 1.16*stall + margin // the harbor's sea is calm and its ship stopped: no wind over the deck
		cat := m.World.Carrier.Catapults[0]
		track := Vec3{X: math.Cos(cat.Heading), Z: -math.Sin(cat.Heading)}
		var speeds []float64
		risen, started := -1, false
		end := math.NaN()
		for i := 0; i < 240*6 && math.IsNaN(end); i++ {
			m.Step(Inputs{Gear: true, Flap: 1, Throttle: c.throttle, Reheat: c.reheat, Launch: i < 240})
			switch {
			case m.State.Gear.Catapult >= 0 && m.State.Gear.Stroke >= 0:
				started = true
				speeds = append(speeds, m.State.Velocity.Dot(track))
				if risen < 0 && m.State.Gear.Stroke >= rise {
					risen = len(speeds) - 1
				}
			case started:
				end = m.State.Velocity.Dot(track)
			}
		}
		if math.IsNaN(end) || risen < 0 {
			t.Fatalf("%s: the catapult never released", c.name)
		}
		if math.Abs(end-want) > 0.5 {
			t.Errorf("%s: left the stroke at %.2f m/s, set for %.2f", c.name, end, want)
		}
		mean := (end - speeds[risen]) / (float64(len(speeds)-risen) / 240)
		peak := 0.0
		for i := risen + 1; i < len(speeds); i++ {
			peak = math.Max(peak, (speeds[i]-speeds[i-1])*240)
		}
		t.Logf("%s: end speed %.2f m/s (set %.2f); after the rise mean %.1f m/s², peak %.1f, %.3f of the mean", c.name, end, want, mean, peak, peak/mean)
		if peak/mean > 1.05 {
			t.Errorf("%s: peaked at %.3f of the mean acceleration, want 1.05 or less", c.name, peak/mean)
		}
	}
}

// TestTrapRunout: the arresting gear is closed loop, so the hook runs out the
// same distance past its wire whatever the jet weighs and whatever its engines
// do in the wire: idle, or the military power NATOPS has the pilot advance to
// at touchdown in case of a bolter. The gear then holds the jet against that
// thrust. Its pull rises over the first metres, where the cable's shock
// absorbers take the engagement, rather than stepping on as the hook meets the
// wire.
func TestTrapRunout(t *testing.T) {
	cases := []struct {
		name     string
		fuel     float64
		throttle float64
	}{
		{"light at idle", 2500, 0},
		{"heavy at idle", 4900, 0},
		{"light at military", 2500, 0.93},
		{"heavy at military", 4900, 0.93},
	}
	for _, c := range cases {
		m := aboard()
		approach(m, 0)
		m.State.Fuel = c.fuel
		caught, peak, previous, onset := -1, 0.0, 0.0, math.NaN()
		for i := 0; i < 240*12; i++ {
			throttle := 0.5
			if caught >= 0 {
				throttle = c.throttle
			}
			m.Step(Inputs{Gear: true, Hook: true, Flap: 2, Throttle: throttle})
			if m.State.Gear.Contact >= 0 {
				t.Fatalf("%s: probe %d fired at %.2f s", c.name, m.State.Gear.Contact, float64(i)/240)
			}
			if caught < 0 && m.State.Gear.Wire >= 0 {
				caught = m.State.Gear.Wire
				if c.throttle > 0 { // the pilot went to military power at touchdown: the engines are there as the wire takes the jet
					m.State.Engine[0] = EngineState{Spool: 1}
					m.State.Engine[1] = EngineState{Spool: 1}
				}
			}
			if caught >= 0 {
				pulled := (previous - m.State.Velocity.X) * 240 / gravity
				peak = math.Max(peak, pulled)
				if math.IsNaN(onset) {
					onset = pulled
				}
			}
			previous = m.State.Velocity.X
		}
		if caught < 0 {
			t.Fatalf("%s: hook never found a wire", c.name)
		}
		if speed := m.State.Velocity.Length(); speed > 0.5 {
			t.Fatalf("%s: still moving %.2f m/s in the wire", c.name, speed)
		}
		run := m.hook(&m.State).X - m.World.Carrier.Wires[caught].A.X
		t.Logf("%s: wire %d, run-out %.1f m, %.2f g as it caught, peak %.2f g", c.name, caught+1, run, onset, peak)
		if math.Abs(run-runout) > 0.5 {
			t.Errorf("%s: the hook ran out %.1f m past its wire, want %.1f", c.name, run, runout)
		}
		if onset > peak/2 {
			t.Errorf("%s: pulled %.2f g the moment it caught, against a peak of %.2f", c.name, onset, peak)
		}
		if peak > hookload {
			t.Errorf("%s: pulled %.2f g, past the gear's %.1f", c.name, peak, hookload)
		}
	}
}

// Mochi world: The realism package acceptances
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The 2026-08-03 flight/FCS realism audit, as behaviours under test: pitch
// trim, flap selection, the weight-scheduled g placard, the zero/negative-g
// feed limit, the hook-skip bolter, and the buffet channel.

package flight

import (
	"math"
	"testing"
)

// incidence reads the body angle of attack, degrees.
func incidence(m *Model) float64 {
	body := m.State.Attitude.Unrotate(m.State.Velocity)
	return math.Atan2(-body.Y, body.X) * 180 / math.Pi
}

// TestTrim: the pitch trim switch moves the datum in both laws. PA: nose-up
// trim raises the held alpha (trim to on-speed, fly the ball with power).
// UA: stick-free trim walks the held attitude.
func TestTrim(t *testing.T) {
	// Differential: the fixed-throttle scenario drifts (it climbs and slowly
	// accelerates), so the trimmed jet is judged against an untrimmed twin
	// flying the identical profile — the trim is the only difference.
	settled := func(trim bool) float64 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 800}, Vec3{X: 1}, 75, Fighter.Mass.Fuel*0.4)
		in := Inputs{Gear: true, Throttle: 0.30} // approach power: a hair downhill, so the runaway climb never pins the flyaway-attitude cap both twins would share
		total := 0.0
		for tick := 0; tick < 240*20; tick++ {
			in.Trim = 0
			if trim && tick >= 240*10 && tick < 240*12 {
				in.Trim = 1
			}
			m.Step(in)
			if tick >= 240*19 {
				total += incidence(m)
			}
		}
		return total / 240
	}
	if rise := settled(true) - settled(false); rise < 0.6 {
		t.Fatalf("PA nose-up trim raised alpha only %.2f° over the untrimmed twin", rise)
	}

	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 200, Fighter.Mass.Fuel*0.4)
	in := Inputs{Throttle: 0.7}
	for tick := 0; tick < 240*8; tick++ {
		m.Step(in)
	}
	pitch := func() float64 {
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		return math.Asin(clamp(forward.Y, -1, 1)) * 180 / math.Pi
	}
	held := pitch()
	in.Trim = 1
	for tick := 0; tick < 240*2; tick++ {
		m.Step(in)
	}
	in.Trim = 0
	for tick := 0; tick < 240*4; tick++ {
		m.Step(in)
	}
	if rise := pitch() - held; rise < 0.5 {
		t.Fatalf("UA nose-up trim raised the held attitude only %.2f°", rise)
	}
}

// TestFlapSelect: HALF commands roughly half the FULL droop in the same
// gear-down condition; FULL is honoured even where the automatic schedule
// would take HALF (the clean-up climb).
func TestFlapSelect(t *testing.T) {
	droop := func(flap float64, raise bool) float64 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 800}, Vec3{X: 1}, 75, Fighter.Mass.Fuel*0.4)
		in := Inputs{Gear: true, Throttle: 0.42, Flap: flap}
		for tick := 0; tick < 240*5; tick++ {
			m.Step(in)
		}
		if raise { // the clean-up climb: gear up, the law held below the AUTO gate
			in.Gear = false
			for tick := 0; tick < 240*3; tick++ {
				m.Step(in)
			}
		}
		return m.State.Fcs.Flap
	}
	full, half := droop(0, false), droop(1, false)
	if full < 0.05 {
		t.Fatalf("the automatic approach droop is missing: %.3f rad", full)
	}
	ratio := half / full
	if ratio < 0.35 || ratio > 0.75 {
		t.Fatalf("HALF droops %.2f of FULL — the flap switch is not scaling the configuration", ratio)
	}
	if forced := droop(2, true); forced < full*0.8 {
		t.Fatalf("FULL selected on the clean-up climb droops %.3f rad against the approach's %.3f — the switch is not honoured", forced, full)
	}
}

// TestWeightSchedule: the g placard follows gross weight. Light, a full-stick
// pull reaches the placard; at full tanks the same pull caps near
// Positive·(Reference/mass), not at the placard.
func TestWeightSchedule(t *testing.T) {
	pull := func(fuel float64) float64 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 2000}, Vec3{X: 1}, 250, fuel)
		in := Inputs{Throttle: 1, Reheat: 1}
		for tick := 0; tick < 240*2; tick++ {
			m.Step(in)
		}
		in.Pitch = 1
		peak := 0.0
		for tick := 0; tick < 240*6; tick++ {
			m.Step(in)
			if m.State.Fcs.Normal > peak {
				peak = m.State.Fcs.Normal
			}
		}
		return peak
	}
	light := pull(Fighter.Mass.Fuel * 0.2)
	heavy := pull(Fighter.Mass.Fuel)
	if light < Fighter.Limit.Positive-0.35 {
		t.Fatalf("light at full stick peaked %.2f g — the placard is out of reach", light)
	}
	mass := Fighter.Mass.Empty + Fighter.Mass.Fuel
	for i := range Fighter.Stores {
		mass += Fighter.Stores[i].Mass
	}
	scheduled := Fighter.Limit.Positive * Fighter.Limit.Reference / mass
	if heavy > scheduled+0.35 {
		t.Fatalf("full tanks peaked %.2f g against a %.2f scheduled placard — the limiter ignores weight", heavy, scheduled)
	}
	if light-heavy < 0.25 {
		t.Fatalf("light %.2f g vs heavy %.2f g — the schedule does not express", light, heavy)
	}
}

// TestStarve: the zero/negative-g feed limit. A sustained push past ten
// seconds rolls the cores back to idle; recovered positive g relights them.
func TestStarve(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 7000}, Vec3{X: 1}, 220, Fighter.Mass.Fuel*0.5)
	in := Inputs{Throttle: 1}
	for tick := 0; tick < 240*2; tick++ {
		m.Step(in)
	}
	in.Pitch = -1 // full forward stick: the outside arc holds negative g
	for tick := 0; tick < 240*14; tick++ {
		m.Step(in)
	}
	if m.State.Fcs.Normal > 0 {
		t.Fatalf("the push is not holding negative g (%.2f) — the harness is broken, not the limit", m.State.Fcs.Normal)
	}
	if spool := m.State.Engine[0].Spool; spool > 0.35 {
		t.Fatalf("fourteen seconds unloaded and the cores still spool %.2f — the feed limit never bit", spool)
	}
	in.Pitch = 0.3 // recover: positive g re-covers the pickups
	for tick := 0; tick < 240*12; tick++ {
		m.Step(in)
	}
	if spool := m.State.Engine[0].Spool; spool < 0.7 {
		t.Fatalf("recovered positive g but the cores only spooled back to %.2f", spool)
	}
}

// TestSkip: a flat, floating arrival bounces the hook over every wire — the
// hook-skip bolter — while TestTrap's flown-on pass (sink 3.4 m/s) still
// catches. The float carries the jet down the whole wire span with the point
// skittering, and no wire may engage.
func TestSkip(t *testing.T) {
	m := aboard()
	m.State.Position = Vec3{X: -98, Y: 22.1, Z: 0} // the hook tip already scraping, 8 m before the first wire
	m.State.Velocity = Vec3{X: 62, Y: -0.5}        // the flare: sink a real glideslope never has
	m.State.Attitude = Axis(Vec3{Z: 1}, 0.06)
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Engine[0] = EngineState{Spool: 0.7}
	m.State.Engine[1] = EngineState{Spool: 0.7}
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Gear: true, Hook: true, Throttle: 0.5})
		if m.State.Gear.Wire >= 0 {
			t.Fatalf("the flat arrival caught wire %d at x=%.0f — the hook never skipped", m.State.Gear.Wire, m.State.Position.X)
		}
	}
	if m.State.Position.X < -60 {
		t.Fatalf("the pass never carried through the wires: x=%.0f", m.State.Position.X)
	}
}

// TestBuffet: the shake channel is silent in cruise, present in slow trimmed
// flight riding the LEX, and strong in a hard pull.
func TestBuffet(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 220, Fighter.Mass.Fuel*0.5)
	in := Inputs{Throttle: 0.7}
	for tick := 0; tick < 240*4; tick++ {
		m.Step(in)
	}
	if m.State.Buffet > 0.05 {
		t.Fatalf("cruise buffet %.2f — the wing is shaking in level flight", m.State.Buffet)
	}
	in.Pitch = 1
	peak := 0.0
	for tick := 0; tick < 240*4; tick++ {
		m.Step(in)
		if m.State.Buffet > peak {
			peak = m.State.Buffet
		}
	}
	if peak < 0.25 {
		t.Fatalf("a full-stick pull peaked buffet at %.2f — the LEX shake never arrives", peak)
	}
}

// TestRelease: the stick released from a hard pull checks the rotation the
// way the real law does — fly the g back to level at surface bandwidth, hand
// to the hold once it arrives. The nose coasts a few degrees (you cannot
// stop a 20°/s rotation without negative g) and captures deadbeat; the old
// hold-only handover coasted ~11° at 350 kt on passive damping alone.
func TestRelease(t *testing.T) {
	coast := func(kt float64) (float64, int) {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 4000}, Vec3{X: 1}, kt*0.5144, Fighter.Mass.Fuel*0.5)
		in := Inputs{Throttle: 1}
		for i := 0; i < 240*3; i++ {
			m.Step(in)
		}
		pitch := func() float64 {
			f := m.State.Attitude.Rotate(Vec3{X: 1})
			return math.Asin(clamp(f.Y, -1, 1)) * 180 / math.Pi
		}
		target := pitch() + 60
		in.Pitch = 1
		peak, released, reversals, lastSign := -1e9, -1, 0, 0.0
		for i := 0; i < 240*10; i++ {
			if released < 0 && pitch() >= target-6 {
				in.Pitch = 0
				released = i
			}
			m.Step(in)
			if released >= 0 {
				if p := pitch(); p > peak {
					peak = p
				}
				q := m.State.Omega.Z
				sg := math.Copysign(1, q)
				if lastSign != 0 && sg != lastSign && math.Abs(q) > 1.5*math.Pi/180 {
					reversals++
				}
				if math.Abs(q) > 1.5*math.Pi/180 {
					lastSign = sg
				}
			}
		}
		return peak - (target - 6), reversals
	}
	for _, c := range []struct {
		kt, most float64
	}{{250, 7}, {350, 8}, {450, 5}} {
		past, reversals := coast(c.kt)
		if past > c.most {
			t.Errorf("%.0f kt: released pull coasts %.1f° (bound %.0f) — the release is back on passive damping", c.kt, past, c.most)
		}
		if reversals > 3 {
			t.Errorf("%.0f kt: %d rate reversals after release — the capture is ringing, not deadbeat", c.kt, reversals)
		}
	}
}

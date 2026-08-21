// Mochi world: Landing-configuration lateral-directional tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Landing-configuration lateral-directional coverage: roll authority with the
// flaperons drooped, whether a lineup correction costs the ball, and whether
// the jet stays flyable with a crosswind on the deck.

package flight

import (
	"math"
	"testing"
)

// approaching builds a jet trimmed on-speed on a 3.5° slope, in the given wind.
func approachJet(t *testing.T, wind Vec3) (*Model, float64) {
	t.Helper()
	m := New(Fighter, Environment{Seed: 1, Wind: wind}, World{Sea: 0})
	state, throttle := Approach(m, Vec3{Y: 300}, Vec3{X: 1}, -3.5*math.Pi/180, 2500)
	m.State = state
	return m, throttle
}

// TestApproachRoll: the flaperons carry the approach droop, so they have less
// left for roll — full lateral stick must still produce a usable, symmetric
// roll rate without disturbing on-speed alpha.
func TestApproachRoll(t *testing.T) {
	peak := func(side float64) (float64, float64) {
		m, throttle := approachJet(t, Vec3{})
		best, worst := 0.0, 0.0
		onspeed := m.Airframe.Control.Onspeed
		for i := 0; i < 240*3; i++ {
			m.Step(Inputs{Throttle: throttle, Roll: side, Gear: true})
			if p, _, _ := rates(m.State.Omega); math.Abs(p) > math.Abs(best) {
				best = p
			}
			held := alpha(m.State.Attitude.Unrotate(m.State.Velocity))
			if d := math.Abs(held - onspeed); d > worst {
				worst = d
			}
		}
		return best * 180 / math.Pi, worst * 180 / math.Pi
	}
	right, rightAlpha := peak(1)
	left, leftAlpha := peak(-1)
	if math.Abs(right) < 20 {
		t.Errorf("roll authority in the landing configuration is only %.1f°/s", right)
	}
	if math.Abs(right+left) > 1.0 {
		t.Errorf("roll must be mirror-symmetric: right %.1f°/s, left %.1f°/s", right, left)
	}
	// A roll should not cost the ball. The band is wide because full stick at
	// extreme bank moves alpha ~3° legitimately; the realistic correction case is
	// TestApproachLineup.
	if rightAlpha > 3.5 || leftAlpha > 3.5 {
		t.Errorf("rolling moved alpha off on-speed by %.2f°/%.2f°", rightAlpha, leftAlpha)
	}
}

// TestApproachLineup: the correction a pass is actually made of — bank off,
// translate, roll back. It has to move the jet sideways without costing the
// ball or leaving a wing down.
func TestApproachLineup(t *testing.T) {
	m, throttle := approachJet(t, Vec3{})
	onspeed := m.Airframe.Control.Onspeed
	start := m.State.Position.Z
	worst := 0.0
	for i := 0; i < 240*12; i++ {
		target := 15 * math.Pi / 180 // roll into the correction, then back to wings level
		if i >= 240*6 {
			target = 0
		}
		m.Step(Inputs{Throttle: throttle, Roll: clamp((target-bank(m))*4, -1, 1), Gear: true})
		held := alpha(m.State.Attitude.Unrotate(m.State.Velocity))
		if d := math.Abs(held - onspeed); d > worst {
			worst = d
		}
	}
	if moved := math.Abs(m.State.Position.Z - start); moved < 40 {
		t.Errorf("a 15° lineup correction moved the jet only %.1f m in 12 s", moved)
	}
	if worst*180/math.Pi > 0.8 {
		t.Errorf("the lineup correction cost the ball: alpha wandered %.2f° off on-speed", worst*180/math.Pi)
	}
	if math.Abs(bank(m))*180/math.Pi > 2 {
		t.Errorf("did not return to wings level: %.2f° of bank", bank(m)*180/math.Pi)
	}
}

// TestApproachCrosswind: hands off in a crosswind the jet must weathervane
// into a steady crab and stay there — sideslip bounded and decaying, not
// diverging, and on-speed alpha undisturbed by any of it.
func TestApproachCrosswind(t *testing.T) {
	for _, wind := range []float64{5, 10} {
		m, throttle := approachJet(t, Vec3{Z: wind})
		onspeed := m.Airframe.Control.Onspeed
		peak, worst := 0.0, 0.0
		for i := 0; i < 240*15; i++ {
			m.Step(Inputs{Throttle: throttle, Gear: true})
			v := m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust))
			peak = math.Max(peak, math.Abs(beta(v)))
			worst = math.Max(worst, math.Abs(alpha(v)-onspeed))
		}
		v := m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust))
		if peak*180/math.Pi > 15 {
			t.Errorf("%.0f m/s crosswind: sideslip peaked at %.1f°", wind, peak*180/math.Pi)
		}
		if math.Abs(beta(v))*180/math.Pi > 1.5 {
			t.Errorf("%.0f m/s crosswind: never settled into the crab, %.2f° of sideslip left", wind, beta(v)*180/math.Pi)
		}
		if worst*180/math.Pi > 1.2 {
			t.Errorf("%.0f m/s crosswind: alpha wandered %.2f° off on-speed", wind, worst*180/math.Pi)
		}
	}
}

// TestCrosswindTrap: the scripted pass of TestTrap, established in a crab with
// a crosswind on the deck. The wire has to find the hook with the jet's nose
// pointing off the centreline, and the arrest has to keep it on the angle.
func TestCrosswindTrap(t *testing.T) {
	const wind = 4 // m/s across the deck (~8 kt); hands-off, no lineup corrections
	m := New(Fighter, Environment{Wind: Vec3{Z: wind}}, harbor())
	ground := Vec3{X: 59, Y: -3.4}
	relative := ground.Subtract(Vec3{Z: wind})
	forward := Vec3{X: relative.X, Z: relative.Z}.Normalize()
	side := forward.Cross(Vec3{Y: 1}).Normalize()
	m.State.Position = Vec3{X: -300, Y: 25.5, Z: 0}
	m.State.Velocity = ground
	m.State.Attitude = Axis(side, 0.10).Multiply(Look(forward)).Normalize()
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Engine[0] = EngineState{Spool: 0.7}
	m.State.Engine[1] = EngineState{Spool: 0.7}
	caught, drift := false, 0.0
	for i := 0; i < 240*10; i++ {
		throttle := 0.42
		if caught {
			throttle = 0
		}
		m.Step(Inputs{Gear: true, Hook: true, Throttle: throttle})
		if m.State.Gear.Wire >= 0 {
			caught = true
		}
		if !caught {
			drift = math.Max(drift, math.Abs(m.State.Position.Z))
		}
	}
	if !caught {
		t.Fatalf("crosswind pass found no wire (drifted %.1f m off centreline)", drift)
	}
	if speed := m.State.Velocity.Length(); speed > 3 {
		t.Errorf("crosswind trap did not stop the jet: %.1f m/s", speed)
	}
	if m.State.Position.X > 60 {
		t.Errorf("rollout past the deck: x=%.0f", m.State.Position.X)
	}
	if drift > 14 { // the wires only span ±14 m
		t.Errorf("drifted %.1f m off centreline before the wire", drift)
	}
}

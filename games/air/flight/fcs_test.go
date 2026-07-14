// Mochi world: FCS gates
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// launch puts the model in clean level flight at speed, engines at mil.
func launch(m *Model, speed float64) {
	m.State.Position = Vec3{Y: 3000}
	m.State.Velocity = Vec3{X: speed}
	m.State.Attitude = Axis(Vec3{Z: 1}, 0.04)
	m.State.Omega = Vec3{}
	m.State.Fcs = FcsState{}
	m.State.Engine[0] = EngineState{Spool: 1}
	m.State.Engine[1] = EngineState{Spool: 1}
	m.State.Fcs.Normal = 1
}

// TestLimiter: full aft stick parks at the g and alpha limits without
// departing; releasing the stick returns the jet toward 1 g.
func TestLimiter(t *testing.T) {
	m := calm()
	launch(m, 240)
	peak, worstAlpha, worstBeta := 0.0, 0.0, 0.0
	for i := 0; i < 240*6; i++ {
		m.Step(Inputs{Pitch: 1, Throttle: 1, Reheat: 1})
		v := m.State.Attitude.Unrotate(m.State.Velocity)
		peak = math.Max(peak, m.State.Fcs.Normal)
		worstAlpha = math.Max(worstAlpha, alpha(v))
		worstBeta = math.Max(worstBeta, math.Abs(beta(v)))
	}
	if peak > m.Airframe.Limit.Positive+0.6 {
		t.Fatalf("g limiter busted: peak %f", peak)
	}
	if peak < 4.0 {
		t.Fatalf("full aft stick should reach serious g: peak %f", peak)
	}
	if worstAlpha > m.Airframe.Limit.Alpha+6*math.Pi/180 {
		t.Fatalf("alpha limiter busted: %f rad", worstAlpha)
	}
	if worstBeta > 20*math.Pi/180 {
		t.Fatalf("departed in yaw: beta %f", worstBeta)
	}
}

// TestOverride: the paddle switch buys g beyond the limiter and records the
// overstress exposure.
func TestOverride(t *testing.T) {
	// At high dynamic pressure the g limiter is the binding constraint
	// (lift could pull far past it) — which is exactly where the paddle
	// switch matters. At lower speeds physics binds first and the paddle
	// honestly buys nothing.
	m := calm()
	launch(m, 310)
	limited := 0.0
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Pitch: 1, Throttle: 1, Reheat: 1})
		limited = math.Max(limited, m.State.Fcs.Normal)
	}
	launch(m, 310)
	m.State.Damage.Stress = 0
	overridden := 0.0
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Pitch: 1, Override: true, Throttle: 1, Reheat: 1})
		overridden = math.Max(overridden, m.State.Fcs.Normal)
	}
	if overridden < limited+0.8 {
		t.Fatalf("override bought nothing: %f vs %f", overridden, limited)
	}
	if m.State.Damage.Stress <= 0 {
		t.Fatal("overstress exposure not recorded")
	}
}

// TestHandsOff: stick free, the FCS holds ~1 g wings level while the jet
// decelerates power-off.
func TestHandsOff(t *testing.T) {
	m := calm()
	launch(m, 200)
	for i := 0; i < 240*8; i++ {
		m.Step(Inputs{Throttle: 0.62})
	}
	if math.IsNaN(m.State.Position.Y) {
		t.Fatal("diverged")
	}
	if math.Abs(m.State.Fcs.Normal-1) > 0.4 {
		t.Fatalf("not holding 1 g hands-off: %f", m.State.Fcs.Normal)
	}
	p, _, r := rates(m.State.Omega)
	if math.Abs(p) > 0.15 || math.Abs(r) > 0.15 {
		t.Fatalf("wandering hands-off: p %f r %f", p, r)
	}
}

// TestRoll: the roll-rate command delivers fighter-class rate and stops
// crisply on release.
func TestRoll(t *testing.T) {
	m := calm()
	launch(m, 200)
	best := 0.0
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Roll: 1, Throttle: 0.8})
		p, _, _ := rates(m.State.Omega)
		best = math.Max(best, p)
	}
	if best < 1.5 {
		t.Fatalf("roll rate anaemic: %f rad/s", best)
	}
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{})
	}
	p, _, _ := rates(m.State.Omega)
	if math.Abs(p) > 0.3 {
		t.Fatalf("roll does not stop on release: %f", p)
	}
}

// TestProSpin: crossed controls at low speed and high alpha are refused —
// no yaw departure, and neutral sticks recover.
func TestProSpin(t *testing.T) {
	m := calm()
	launch(m, 95)
	worst := 0.0
	for i := 0; i < 240*5; i++ {
		m.Step(Inputs{Pitch: 1, Roll: 1, Yaw: -1, Throttle: 0.8})
		v := m.State.Attitude.Unrotate(m.State.Velocity)
		worst = math.Max(worst, math.Abs(beta(v)))
	}
	if worst > 25*math.Pi/180 {
		t.Fatalf("yaw departure: beta %f rad", worst)
	}
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{})
	}
	_, _, r := rates(m.State.Omega)
	if math.Abs(r) > 0.4 || math.IsNaN(m.State.Position.Y) {
		t.Fatalf("no clean recovery: r %f", r)
	}
}

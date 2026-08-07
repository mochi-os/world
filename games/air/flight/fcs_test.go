// Mochi world: FCS gates
// Copyright © 2026 Mochisoft OÜ
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
	t.Logf("peak roll rate %.0f°/s", best*180/math.Pi)
	if best < 2.3 || best > 4.4 {
		t.Fatalf("roll rate %.0f°/s outside the NATOPS-class 132-252°/s full-stick band", best*180/math.Pi)
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

// TestRudderAlphaSchedule: pedal authority GROWS with alpha (#45, NATOPS
// 2.8.2.8) — half throw low, full available high. The old schedule faded to
// 10% at 40 deg, the exact opposite.
func TestRudderAlphaSchedule(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	var f FcsState
	deflect := func(alpha float64) float64 {
		f = FcsState{}
		return math.Abs(m.yaw(1, 0, alpha, 0, 0, &f))
	}
	low, high := deflect(0.05), deflect(0.60)
	throw := Fighter.Control.Throw.Rudder
	if low > 0.65*throw {
		t.Fatalf("low alpha should give roughly half throw: %.1f of %.1f deg", low*180/math.Pi, throw*180/math.Pi)
	}
	if high <= low*1.4 {
		t.Fatalf("authority must GROW with alpha: %.2f rad at 34 deg vs %.2f at 3 deg", high, low)
	}
}

// TestPedalRollsAtHighAlpha: above 25 deg alpha the pedal feeds the roll
// command like the stick (NATOPS 11.1.8) — before #45 combined stick+pedal
// measured identical to stick alone at every alpha.
func TestPedalRollsAtHighAlpha(t *testing.T) {
	differential := func(alpha, pedal float64) float64 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 90, Fighter.Mass.Fuel*0.5)
		// Pitch the nose up so the flow arrives alpha below it.
		m.State.Attitude = Axis(Vec3{Z: 1}, alpha).Multiply(m.State.Attitude)
		for i := 0; i < 30; i++ {
			m.Step(Inputs{Throttle: 0.8, Yaw: pedal})
		}
		return math.Abs(m.State.Fcs.Flaperon.Left - m.State.Fcs.Flaperon.Right)
	}
	if high := differential(0.55, 1); high < 0.02 {
		t.Fatalf("full pedal at 31 deg alpha must move the rolling surfaces: %.3f rad differential", high)
	}
	if low := differential(0.05, 1); low > 0.01 {
		t.Fatalf("pedal at 3 deg alpha commands no roll: %.3f rad differential", low)
	}
}

// TestRollingReduction: a rolling pull is limited BELOW a straight one (#46,
// NATOPS 11.1.7 — up to 80% NzREF at full lateral stick). It measured the
// opposite before: full-lateral pulls exceeded the straight ceiling by half
// a g through the differential stabilator.
func TestRollingReduction(t *testing.T) {
	peak := func(roll float64) float64 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 300, Fighter.Mass.Fuel*0.7)
		top := 0.0
		for i := 0; i < 240*4; i++ { // 4 s of full aft stick
			m.Step(Inputs{Throttle: 1, Pitch: 1, Roll: roll})
			if m.State.Fcs.Normal > top {
				top = m.State.Fcs.Normal
			}
		}
		return top
	}
	straight, rolling := peak(0), peak(1)
	if rolling >= straight {
		t.Fatalf("full-lateral pull must peak BELOW the straight pull: %.2f vs %.2f g", rolling, straight)
	}
	if rolling < straight*0.72 || rolling > straight*0.92 {
		t.Fatalf("the rolling reduction is ~20%%: %.2f vs %.2f g (%.0f%%)", rolling, straight, 100*rolling/straight)
	}
}

// TestNegativeFloorFixed: the negative command floor does not schedule with
// gross weight (NATOPS 11.1.7: "fixed at negative 3 g's for all gross
// weights") — only the positive side does.
func TestNegativeFloorFixed(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 250, Fighter.Mass.Fuel)                        // full internal...
	m.Stores(Fighter.Default | mask(t, "pylon3", "tank3", "pylon5", "tank5", "pylon7", "tank7")) // ...plus three tanks: heavy enough that a weight-scaled floor would stop at -2.4
	least := 1.0
	for i := 0; i < 240*3; i++ {
		m.Step(Inputs{Throttle: 1, Pitch: -1})
		if m.State.Fcs.Normal < least {
			least = m.State.Fcs.Normal
		}
	}
	if least > -2.55 {
		t.Fatalf("heavy jet must still command toward -3 g, not a weight-scaled floor: reached %.2f g", least)
	}
}

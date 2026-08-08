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

// TestBrakeAutoRetract: airborne in the AUTO FLAPS UP mode the speedbrake
// stows itself above 6.0 g (NATOPS 2.8.4.8) and re-extends when the g clears
// with the command still held — the maintained-command reading of the real
// jet's momentary switch.
func TestBrakeAutoRetract(t *testing.T) {
	m := calm()
	launch(m, 240)
	// One continuous full-aft pull with the board commanded out. The g builds
	// slowly (the trim integrator walks the pull's tail), so the board is
	// fully out through the sub-6 g phase and the retract engages as the g
	// crosses the threshold several seconds in.
	extended, stowed, peak := 0.0, 1.0, 0.0
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: 1, Speedbrake: 1})
		if m.State.Fcs.Normal < 5 {
			extended = math.Max(extended, m.State.Fcs.Speedbrake)
		}
		if m.State.Fcs.Normal > 6.2 {
			stowed = math.Min(stowed, m.State.Fcs.Speedbrake)
		}
		peak = math.Max(peak, m.State.Fcs.Normal)
	}
	if extended < 0.9 {
		t.Fatalf("the board must be out below the threshold: %.2f", extended)
	}
	if peak < 6.2 {
		t.Fatalf("the pull must exceed the 6 g threshold to exercise the retract: peak %.1f g", peak)
	}
	if stowed > 0.1 {
		t.Fatalf("the board must auto-retract above 6 g: still %.2f out", stowed)
	}
	for i := 0; i < 240*3; i++ {
		m.Step(Inputs{Throttle: 0.6, Speedbrake: 1})
	}
	if m.State.Fcs.Speedbrake < 0.5 {
		t.Fatalf("the held command must re-extend the board once the g clears: %.2f", m.State.Fcs.Speedbrake)
	}
}

// TestRollLimitTanks: R-LIM (NATOPS 2.8.2.8) — wing-pylon tanks cut the
// maximum roll rate by about a third. The centreline tank is not a wing-pylon
// store and must not engage it.
func TestRollLimitTanks(t *testing.T) {
	peak := func(names ...string) float64 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 200, Fighter.Mass.Fuel)
		if len(names) > 0 {
			m.Stores(Fighter.Default | mask(t, names...))
		}
		best := 0.0
		for i := 0; i < 240*2; i++ {
			m.Step(Inputs{Roll: 1, Throttle: 0.8})
			p, _, _ := rates(m.State.Omega)
			best = math.Max(best, p)
		}
		return best
	}
	clean := peak()
	wing := peak("pylon3", "tank3", "pylon7", "tank7")
	centre := peak("pylon5", "tank5")
	t.Logf("peak roll: clean %.0f°/s, wing tanks %.0f°/s, centreline %.0f°/s", clean*180/math.Pi, wing*180/math.Pi, centre*180/math.Pi)
	if wing > clean*0.75 || wing < clean*0.50 {
		t.Fatalf("wing tanks must cut the peak roll rate about a third: %.0f vs %.0f°/s", wing*180/math.Pi, clean*180/math.Pi)
	}
	if centre < clean*0.85 {
		t.Fatalf("the centreline tank must not engage R-LIM: %.0f vs %.0f°/s clean", centre*180/math.Pi, clean*180/math.Pi)
	}
}

// TestRollTaperHighAlpha: roll performance is "essentially constant" above
// 35° alpha (NATOPS 11.1.8) — the fade tapers to its 35° value and holds
// there instead of collapsing toward zero (the old fixed 0.08 floor crawled
// at 9°/s by 40°). Pinned on the schedule directly: the jet cannot SUSTAIN
// the alphas where the floors differ, so a flown test only sees the pitch-up
// transient (flight-level high-alpha rolling is TestPedalRollsAtHighAlpha).
func TestRollTaperHighAlpha(t *testing.T) {
	at := func(deg float64) float64 { return taper(deg*math.Pi/180, Fighter.Limit.Alpha) }
	if math.Abs(at(36)-at(45)) > 0.02 {
		t.Fatalf("roll authority must hold essentially constant above 35°: %.3f at 36° vs %.3f at 45°", at(36), at(45))
	}
	if at(40) < 0.2 {
		t.Fatalf("the 40° roll crawl is back: fade %.3f", at(40))
	}
	if at(20) < at(35)+0.2 {
		t.Fatalf("the fade below 35° must still taper from real authority: %.3f at 20° vs %.3f at 35°", at(20), at(35))
	}
}

// TestBallisticDeparture: overcooking the vertical is the one departure the
// FCS does not prevent (NATOPS 11.1.8.1). A botched pull to near-vertical at
// idle must now wander in yaw and pitch while the airspeed is gone — the old
// model fell through the same profile perfectly symmetric (worst beta 0.0°,
// worst yaw 0.0°/s) and flew away as if nothing happened. And it must still
// fly away: the wander is bounded and dies with returning speed, not a spin.
func TestBallisticDeparture(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 250, Fighter.Mass.Fuel*0.5)
	for i := 0; i < 240*3; i++ { // pull hard toward the vertical
		m.Step(Inputs{Pitch: 1, Throttle: 1, Reheat: 1})
	}
	slowest, worstBeta, worstYaw := 1e9, 0.0, 0.0
	for i := 0; i < 240*30; i++ { // idle, light back stick: up, over, and down
		m.Step(Inputs{Pitch: 0.2})
		v := m.State.Velocity.Length()
		slowest = math.Min(slowest, v)
		if v < 90 {
			body := m.State.Attitude.Unrotate(m.State.Velocity)
			worstBeta = math.Max(worstBeta, math.Abs(beta(body)))
			_, _, yaw := rates(m.State.Omega)
			worstYaw = math.Max(worstYaw, math.Abs(yaw))
		}
	}
	t.Logf("slowest %.0f m/s, worst beta %.1f°, worst yaw %.0f°/s", slowest, worstBeta*180/math.Pi, worstYaw*180/math.Pi)
	if slowest > 80 {
		t.Fatalf("the botched vertical must actually starve the jet of airspeed: minimum %.0f m/s", slowest)
	}
	if worstBeta < 6*math.Pi/180 && worstYaw < 15*math.Pi/180 {
		t.Fatalf("no departure at %.0f m/s minimum: beta %.1f°, yaw %.0f°/s — the jet is still perfectly obedient out of airspeed", slowest, worstBeta*180/math.Pi, worstYaw*180/math.Pi)
	}
	for i := 0; i < 240*10; i++ { // military power, hands off: the wander must die with returning speed
		m.Step(Inputs{Throttle: 1})
	}
	if math.IsNaN(m.State.Position.Y) {
		t.Fatal("diverged after the departure")
	}
	if v := m.State.Velocity.Length(); v < 100 {
		t.Fatalf("must fly away once airspeed returns: %.0f m/s", v)
	}
	p, q, r := rates(m.State.Omega)
	if math.Abs(p) > 0.5 || math.Abs(q) > 0.5 || math.Abs(r) > 0.5 {
		t.Fatalf("still tumbling after recovery: p %.2f q %.2f r %.2f rad/s", p, q, r)
	}
}

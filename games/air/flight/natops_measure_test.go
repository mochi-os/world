// Mochi world: NATOPS handling measurement (#86)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestNatopsMeasure is a measurement instrument, not a gate: it flies the
// normal NATOPS inputs through the launch, approach, pattern and landing
// regimes and prints what the jet does — trim alpha, the authority a full-aft
// pull actually delivers per configuration and speed, control-law flips, and
// the scenario responses a pilot would expect. Run with:
//
//	go test ./games/air/flight -run TestNatopsMeasure -v
//
// Born from a Case I flight (recording 01a046218f05) where full aft stick at
// 245-260 kt gear-down gave 1.9 g / alpha 10 for half a minute, and the PA/UA
// hysteresis band (243-250 KCAS with gear down) sat exactly under the pilot's
// hand.
const knot = 0.514444

func lever(spool float64) float64 { return clamp((spool-0.04)/0.96, 0, 1) }

type pullResult struct {
	alpha, nz, rate, stab float64
	flips                 int
}

func fly(m *Model, in Inputs, seconds float64, watch func()) int {
	flips, pa := 0, m.pa
	for i := 0; i < int(seconds/Dt); i++ {
		m.Step(in)
		if m.pa != pa {
			flips++
			pa = m.pa
		}
		if watch != nil {
			watch()
		}
	}
	return flips
}

func pull(m *Model, in Inputs, stick float64, seconds float64) pullResult {
	in.Pitch = stick
	r := pullResult{}
	r.flips = fly(m, in, seconds, func() {
		r.alpha = math.Max(r.alpha, m.Alpha()*180/math.Pi)
		r.nz = math.Max(r.nz, m.Nz())
		r.rate = math.Max(r.rate, math.Abs(m.State.Omega.Z)*180/math.Pi)
		r.stab = math.Max(r.stab, math.Abs(m.State.Fcs.Stabilator.Left)*180/math.Pi)
	})
	return r
}

func cell(t *testing.T, label string, gear bool, flap float64, kt float64) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	fuel := Fighter.Mass.Fuel * 0.5
	m.State = Level(m, Vec3{Y: 250}, Vec3{X: 1}, kt*knot, fuel)
	if gear {
		m.State.Gear.Extension = 1 // pre-extended: the cell measures the steady configuration, not the transit (Level leaves the gear up)
	}
	in := Inputs{Gear: gear, Flap: flap, Throttle: lever(m.State.Engine[0].Spool)}
	settleFlips := fly(m, in, 3, nil)
	trimAlpha := m.Alpha() * 180 / math.Pi
	drift := m.State.Velocity.Y // m/s of climb the "level" state has wandered to at stick zero
	law := "UA"
	if m.pa {
		law = "PA"
	}
	full := pull(m, in, 1, 4)
	t.Logf("%-22s %3.0f kt  %s  trim a=%4.1f drift %+5.1f m/s flips %d | FULL AFT: a=%4.1f nz=%3.1f rate=%4.1f°/s stab=%4.1f° flips %d",
		label, kt, law, trimAlpha, drift, settleFlips, full.alpha, full.nz, full.rate, full.stab, full.flips)
}

func TestNatopsMeasure(t *testing.T) {
	t.Logf("=== A: configuration grid — trim at stick zero, then full-aft authority ===")
	for _, kt := range []float64{180, 250, 300, 350} {
		cell(t, "clean", false, 0, kt)
	}
	for _, kt := range []float64{135, 145, 160, 180, 220, 240, 246, 252, 260, 280} {
		cell(t, "gear down, flap AUTO", true, 0, kt)
	}
	for _, kt := range []float64{125, 135, 145, 160, 200, 240, 250} {
		cell(t, "gear down, flap FULL", true, 2, kt)
	}

	t.Logf("=== B1: on-speed approach, hands off (Approach solver = the NATOPS trim) ===")
	{
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		fuel := Fighter.Mass.Fuel * 0.4
		state, throttle := Approach(m, Vec3{Y: 200}, Vec3{X: 1}, -3.5*math.Pi/180, fuel)
		state.Gear.Extension = 1
		m.State = state
		in := Inputs{Gear: true, Flap: 2, Throttle: lever(throttle)}
		aMin, aMax, vyEnd := 90.0, -90.0, 0.0
		fly(m, in, 20, func() {
			a := m.Alpha() * 180 / math.Pi
			aMin, aMax = math.Min(aMin, a), math.Max(aMax, a)
			vyEnd = m.State.Velocity.Y
		})
		t.Logf("hands-off 20 s: alpha %4.1f..%4.1f (on-speed %.1f), sink %+6.0f fpm (target %+.0f), cas %3.0f kt",
			aMin, aMax, Fighter.Control.Onspeed*180/math.Pi, vyEnd*196.85, -m.State.Velocity.Length()*math.Sin(3.5*math.Pi/180)*196.85, m.Cas()/knot)

		t.Logf("=== B2: glideslope correction with power alone (+0.10 lever, 6 s) ===")
		in.Throttle = clamp(in.Throttle+0.10, 0, 1)
		aMax2, vy2 := -90.0, 0.0
		fly(m, in, 6, func() {
			aMax2 = math.Max(aMax2, m.Alpha()*180/math.Pi)
			vy2 = m.State.Velocity.Y
		})
		t.Logf("after +power: sink %+6.0f fpm (was %+6.0f), alpha held %4.1f — 'fly the ball with power'", vy2*196.85, vyEnd*196.85, aMax2)
	}

	t.Logf("=== B3: runway flare — approach trim at 20 m, +0.35 stick for 2.5 s ===")
	{
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		fuel := Fighter.Mass.Fuel * 0.4
		state, throttle := Approach(m, Vec3{Y: 20}, Vec3{X: 1}, -3.0*math.Pi/180, fuel)
		state.Gear.Extension = 1
		m.State = state
		in := Inputs{Gear: true, Flap: 2, Throttle: lever(throttle)}
		sink0 := -m.State.Velocity.Y * 196.85
		r := pull(m, in, 0.35, 2.5)
		t.Logf("flare: sink %4.0f -> %4.0f fpm, alpha peak %4.1f, nz peak %3.1f", sink0, -m.State.Velocity.Y*196.85, r.alpha, r.nz)
	}

	t.Logf("=== B4: catapult flyaway — end of stroke, hands off at MIL ===")
	{
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		fuel := Fighter.Mass.Fuel * 0.6
		m.State = Level(m, Vec3{Y: 20}, Vec3{X: 1}, 80, fuel)
		m.State.Gear.Extension = 1
		m.halfleg = true // HALF flap latched on deck, per the takeoff leg
		in := Inputs{Gear: true, Throttle: 1}
		minY, pitchEnd, aMax := 1e9, 0.0, -90.0
		fly(m, in, 12, func() {
			minY = math.Min(minY, m.State.Position.Y)
			forward := m.State.Attitude.Rotate(Vec3{X: 1})
			pitchEnd = math.Asin(clamp(forward.Y, -1, 1)) * 180 / math.Pi
			aMax = math.Max(aMax, m.Alpha()*180/math.Pi)
		})
		t.Logf("flyaway: min alt %4.1f m (start 20), pitch settles %4.1f° (flyaway datum %.0f°), alpha max %4.1f, cas %3.0f kt",
			minY, pitchEnd, Fighter.Control.Flyaway*180/math.Pi, aMax, m.Cas()/knot)
	}

	t.Logf("=== B5: rotation regime — 140 kt in ground effect, half flap, aft stick 0.5 ===")
	{
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		fuel := Fighter.Mass.Fuel * 0.6
		m.State = Level(m, Vec3{Y: 3}, Vec3{X: 1}, 140*knot, fuel)
		m.State.Gear.Extension = 1
		m.halfleg = true
		in := Inputs{Gear: true, Throttle: 1}
		r := pull(m, in, 0.5, 3)
		t.Logf("rotation: pitch rate peak %4.1f°/s, alpha peak %4.1f, nz %3.1f (a ground roll itself lives in the client)", r.rate, r.alpha, r.nz)
	}

	t.Logf("=== B6: the break — 350 kt clean, 80° bank, full aft ===")
	{
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		fuel := Fighter.Mass.Fuel * 0.5
		m.State = Level(m, Vec3{Y: 250}, Vec3{X: 1}, 350*knot, fuel)
		in := Inputs{Throttle: lever(m.State.Engine[0].Spool)}
		in.Roll = 1
		fly(m, in, 1.1, nil) // roll in
		in.Roll = 0.1
		cas0 := m.Cas() / knot
		r := pull(m, in, 1, 6)
		t.Logf("break: nz peak %3.1f, alpha peak %4.1f, cas %3.0f -> %3.0f kt", r.nz, r.alpha, cas0, m.Cas()/knot)
	}
}

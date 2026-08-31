// Mochi world: handling and procedure gates (#89)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestDepartureBoundary: the flight-1 crash ended in a departure, and the
// suite pinned single scenarios (TestProSpin at launch speed) but never the
// boundary. Coordinated full-aft must stay departure-free across the whole
// configuration grid — the FCS's protection is the contract; the limiter, not
// luck, is what keeps a clean pull flyable.
func TestDepartureBoundary(t *testing.T) {
	cell := func(name string, gear bool, flap, kcas float64) {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, kcas/1.94384, 3000)
		if gear {
			m.State.Gear.Extension = 1
		}
		in := Inputs{Gear: gear, Flap: flap, Throttle: 0.8}
		for i := 0; i < 240*2; i++ {
			m.Step(in)
		}
		in.Pitch = 1
		worstBeta, worstRoll := 0.0, 0.0
		for i := 0; i < 240*8; i++ {
			m.Step(in)
			v := m.State.Attitude.Unrotate(m.State.Velocity)
			worstBeta = math.Max(worstBeta, math.Abs(beta(v)))
			worstRoll = math.Max(worstRoll, math.Abs(m.State.Omega.X))
		}
		t.Logf("%-24s %3.0f kt: beta %5.1f° roll %5.1f°/s", name, kcas, worstBeta*180/math.Pi, worstRoll*180/math.Pi)
		if worstBeta > 20*math.Pi/180 {
			t.Errorf("%s at %.0f kt: coordinated full-aft departed — beta %.1f°", name, kcas, worstBeta*180/math.Pi)
		}
		if worstRoll > 90*math.Pi/180 {
			t.Errorf("%s at %.0f kt: coordinated full-aft rolled off — %.1f°/s", name, kcas, worstRoll*180/math.Pi)
		}
	}
	for _, kcas := range []float64{200, 250, 350} {
		cell("clean", false, 0, kcas)
	}
	for _, kcas := range []float64{135, 160} {
		cell("gear down, flap FULL", true, 2, kcas)
	}
	// The crossed-control extremes: full aft, full rudder, opposite stick,
	// held four seconds, then released. The dirty/slow cells are the
	// in-close-waveoff accident regime the flight-1 crash actually lived in
	// (#97): gear down, FULL flap, pattern speeds, adverse yaw.
	crossed := func(name string, gear bool, flap, kcas float64) (float64, float64) {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, kcas/1.94384, 3000)
		if gear {
			m.State.Gear.Extension = 1
		}
		in := Inputs{Gear: gear, Flap: flap, Throttle: 0.8}
		for i := 0; i < 240*2; i++ {
			m.Step(in)
		}
		worst := 0.0
		in.Pitch, in.Yaw, in.Roll = 1, -1, 1
		for i := 0; i < 240*4; i++ {
			m.Step(in)
			v := m.State.Attitude.Unrotate(m.State.Velocity)
			worst = math.Max(worst, math.Abs(beta(v)))
		}
		// Release: controls neutral. The last two seconds of the six-second
		// window say whether letting go brings the jet back.
		in.Pitch, in.Yaw, in.Roll = 0, 0, 0
		ending := 0.0
		for i := 0; i < 240*6; i++ {
			m.Step(in)
			v := m.State.Attitude.Unrotate(m.State.Velocity)
			if i >= 240*4 {
				ending = math.Max(ending, math.Abs(beta(v)))
			}
		}
		t.Logf("%-24s %3.0f kt crossed: beta peak %5.1f°, after release %5.1f°", name, kcas, worst*180/math.Pi, ending*180/math.Pi)
		return worst * 180 / math.Pi, ending * 180 / math.Pi
	}
	crossed("clean", false, 0, 160)
	for _, kcas := range []float64{130, 145} {
		peak, after := crossed("gear down, flap FULL", true, 2, kcas)
		// Realism cuts both ways in this cell (#97): the real jet departs
		// under crossed controls dirty and slow (the in-close-waveoff
		// accident class), so the model must neither shrug it off nor lose
		// the jet for good — a real yaw excursion, and a recovery once the
		// controls are released. Measured 2026-08-30: 19.8°/17.9° peaks
		// (clean 160 kt reaches only 13.8°), 0.6°/0.5° after release.
		if peak < 15 {
			t.Errorf("dirty crossed controls at %.0f kt peaked at only %.1f° beta — the accident regime's departure is protected away", kcas, peak)
		}
		if after > 12 {
			t.Errorf("dirty crossed controls at %.0f kt still at %.1f° beta two seconds after release — the departure does not recover", kcas, after)
		}
	}
}

// TestWaveoff: the pattern's safety number — from on-speed at 200 ft on
// glideslope, full burner and a climb command; how much altitude the go-around
// consumes before the climb is established.
func TestWaveoff(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	state, _ := Approach(m, Vec3{Y: 60}, Vec3{X: 1}, -3.5*math.Pi/180, Fighter.Mass.Fuel*0.4)
	state.Gear.Extension = 1
	m.State = state
	lowest, climbing := 60.0, -1.0
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{Gear: true, Flap: 2, Throttle: 1, Reheat: 1, Pitch: 0.25})
		lowest = math.Min(lowest, m.State.Position.Y)
		if climbing < 0 && m.State.Velocity.Y > 2 {
			climbing = float64(i) * Dt
		}
	}
	lost := (60 - lowest) * 3.28084
	t.Logf("waveoff: lost %.0f ft, climbing at t+%.1f s", lost, climbing)
	if climbing < 0 {
		t.Fatal("the waveoff never established a climb")
	}
	if lost > 100 {
		t.Errorf("waveoff from 200 ft lost %.0f ft — the pattern's safety margin is gone", lost)
	}
}

// TestBleed: knots lost per half-turn of maximum-performance level turning at
// military power — the fight's energy economy in one number. The bot ladders
// and every rate-fight instinct ride on it staying put.
func TestBleed(t *testing.T) {
	m := energy()
	m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, 350/1.94384, 2500)
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Throttle: 1})
	}
	start := m.Cas() * 1.94384
	heading := math.Atan2(m.State.Velocity.X, -m.State.Velocity.Z)
	turned, previous, stick := 0.0, heading, 0.0
	// A servoed 5 g hold, not full aft: full aft rides the limiter into
	// deep-alpha mush and measures the limiter, not the drag. Five g above
	// the ~4 g MIL-sustainable point at this speed is the honest bleed.
	for i := 0; i < 240*40 && turned < math.Pi; i++ {
		stick = clamp(stick+(5-m.Nz())*1.5*Dt, -0.3, 1)
		m.Step(Inputs{Throttle: 1, Pitch: stick, Roll: clamp((75*math.Pi/180-bank(m))*2, -1, 1)})
		h := math.Atan2(m.State.Velocity.X, -m.State.Velocity.Z)
		d := math.Abs(math.Atan2(math.Sin(h-previous), math.Cos(h-previous)))
		turned += d
		previous = h
	}
	lost := start - m.Cas()*1.94384
	t.Logf("bleed: %.0f -> %.0f KCAS across 180° holding 5 g at MIL (%.0f kt lost)", start, m.Cas()*1.94384, lost)
	if turned < math.Pi {
		t.Fatal("never completed the half-turn")
	}
	// Regression pin, not judgement: measured 170 kt on 2026-08-28 with the
	// post-#86 law, 178 after the #95 ram/drag-rise retune (the decay
	// compounds — as speed falls the 5 g hold needs rising alpha, and the
	// drag rises with it). No public exact-condition anchor exists (NATOPS
	// EM charts are restricted); the closest published figure, GAO's 62 kt/s
	// instantaneous bleed at 15,000 ft at max pull, brackets this ~19 kt/s
	// at 5 g MIL on the plausible side (#96). A move outside the band is a
	// thrust/drag change re-tuning every fight, not noise.
	if lost < 130 || lost > 210 {
		t.Errorf("half-turn bleed %.0f kt holding 5 g at MIL — outside the 130-210 regression band (measured 178)", lost)
	}
}

// TestRollTracking: the PIO battery only exercises pitch; this is the roll
// axis — a delayed pure-gain pilot capturing a 30° bank. Sustained ringing at
// working gain is the lateral PIO the pitch battery cannot see.
//
// The ≤0.7 working region is calibrated against the real pilot (#98): across
// twelve bank-capture events in the two 2026-08-28 fights the flown
// equivalent gain (peak stick per 25° of bank swing —
// claude/scripts/air/lateralgain.py) measured median 0.41, p75 0.58, max
// 0.61. The ringing at gain 1.0 is an over-aggressive synthetic pilot no
// human input reaches, not missing lateral damping.
func TestRollTracking(t *testing.T) {
	ring := func(kt, gain float64) (int, bool) {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, kt/1.94384, 2500)
		const tau = 0.30
		lag := make([]float64, int(tau/Dt))
		target := 0.0
		var history []float64
		crossings, last := 0, 0.0
		for i := 0; i < 240*16; i++ {
			if i == 240*2 {
				target = 30 * math.Pi / 180
			}
			b := bank(m)
			lag = append(lag, target-b)
			delayed := lag[0]
			lag = lag[1:]
			m.Step(Inputs{Throttle: 0.8, Roll: clamp(gain*delayed*57.3/25, -1, 1)})
			if i >= 240*2 {
				history = append(history, b*57.3)
			}
		}
		for _, v := range history {
			d := v - 30
			if last != 0 && math.Signbit(d) != math.Signbit(last) && math.Abs(d) > 1.5 {
				crossings++
			}
			if math.Abs(d) > 1.5 {
				last = d
			}
		}
		n := len(history)
		amp := func(a, b int) float64 {
			lo, hi := 1e9, -1e9
			for _, v := range history[a:b] {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
			return hi - lo
		}
		sustained := crossings >= 4 && amp(n-240*4, n) >= 0.5*amp(0, 240*4)
		return crossings, sustained
	}
	for _, kt := range []float64{250, 350} {
		for _, gain := range []float64{0.5, 0.7, 1.0} {
			crossings, sustained := ring(kt, gain)
			t.Logf("roll capture at %.0f kt, gain %.1f: %d crossings, sustained %v", kt, gain, crossings, sustained)
			// Gate at the moderate pilot: 1.0 rings at 350 kt on the day this
			// was written (measured, logged above) — that margin is the roll
			// law's known landscape, and the gate holds the working region.
			if gain <= 0.7 && sustained {
				t.Errorf("roll tracking rings at gain %.1f at %.0f kt — the lateral working region regressed", gain, kt)
			}
		}
	}
}

// TestPhugoid: the long-period mode, hands off for three minutes at cruise —
// the C* attitude hold should leave at most a small, decaying speed swap.
// TestHandsOff's eight seconds cannot see a 60-120 s period.
func TestPhugoid(t *testing.T) {
	m := energy()
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 200, 2500)
	var speeds []float64
	for i := 0; i < 240*180; i++ {
		m.Step(Inputs{Throttle: 0.7})
		if i%240 == 0 {
			speeds = append(speeds, m.State.Velocity.Length())
		}
	}
	swing := func(a, b int) float64 {
		lo, hi := 1e9, -1e9
		for _, v := range speeds[a:b] {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		return hi - lo
	}
	early, late := swing(10, 90), swing(100, 180)
	t.Logf("phugoid: speed swing %.1f m/s over t=10-90 s, %.1f over t=100-180 s", early, late)
	if late > 15 {
		t.Errorf("hands-off speed swings %.1f m/s in the third minute — the long-period mode is loose", late)
	}
	if late > early+2 {
		t.Errorf("the long-period mode GROWS hands-off: %.1f -> %.1f m/s", early, late)
	}
}

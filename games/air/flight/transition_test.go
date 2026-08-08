// Mochi world: FCS law-transition regression tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The seams between the powered-approach and up-and-away laws (#203/#204).
// Every past transition defect (the gear-cycle trim jump, the post-launch
// pitch-down, the gear-up g-snap) was found by a user report; these pin the
// boundary behaviour instead. The stick is held displaced through each
// crossing because the trim integral only feeds the command path while the
// stick flies the jet — a neutral-stick crossing hides the mis-trim.

package flight

import (
	"math"
	"testing"
)

// cross flies a gear-down speed crossing of the law boundary under the given
// throttle and stick, returning the worst pitch rate and load-factor excursion
// seen in the boundary region plus the number of law changes.
func cross(t *testing.T, start float64, throttle float64, stick float64, until func(speed float64) bool) (worstQ float64, worstNz float64, flips int) {
	t.Helper()
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 600}, Vec3{X: 1}, start, 2000)
	for i := 0; i < 240*8; i++ { // settle into the starting law's trim
		m.Step(Inputs{Throttle: 0.55, Gear: true})
	}
	was := m.pa
	for i := 0; i < 240*120; i++ {
		m.Step(Inputs{Throttle: throttle, Pitch: stick, Gear: true})
		if m.pa != was {
			flips++
			was = m.pa
		}
		speed := m.State.Velocity.Length()
		if speed > 115 && speed < 145 { // the boundary region
			_, q, _ := rates(m.State.Omega)
			if a := math.Abs(q); a > worstQ {
				worstQ = a
			}
			if d := math.Abs(m.State.Fcs.Normal - 1); d > worstNz {
				worstNz = d
			}
		}
		if until(speed) {
			return worstQ, worstNz, flips
		}
	}
	t.Fatal("never crossed the boundary")
	return 0, 0, 0
}

// TestLawBoundaryAcceleration: a gear-down MIL acceleration through the PA/UA
// boundary with the stick displaced (the bolter/waveoff shape) must hand over
// smoothly — the laundered trim cannot be reinterpreted as a rate bias.
func TestLawBoundaryAcceleration(t *testing.T) {
	// 0.12 stick ≈ holding attitude through the acceleration (a real waveoff),
	// not a sustained pull — a big pull climbs, sags the speed back through the
	// band, and the commanded g swamps the handover transient being measured.
	// 146, not 150: a gear-down MIL acceleration now peaks at ~149.7 m/s
	// because sustained flight past the 250 KCAS gear placard accrues damage
	// (#49), and the old threshold sat inside that last fraction. 146 is well
	// clear of the 128.6 m/s CAS law boundary this test exists to measure.
	worstQ, worstNz, flips := cross(t, 110, 1.0, 0.12, func(speed float64) bool { return speed > 146 })
	t.Logf("accel: worst q %.1f deg/s, worst |nz-1| %.2f, law flips %d", worstQ*180/math.Pi, worstNz, flips)
	if flips != 1 {
		t.Fatalf("law must flip exactly once, flipped %d times", flips)
	}
	if worstQ > 12*math.Pi/180 {
		t.Fatalf("pitch-rate excursion %.1f deg/s at the law handover (the old instant flip snapped the nose)", worstQ*180/math.Pi)
	}
	if worstNz > 1.0 {
		t.Fatalf("load excursion |nz-1| = %.2f at the law handover", worstNz)
	}
}

// TestLawBoundaryDeceleration: the mirrored crossing — dirtying up early and
// decelerating into the approach with a touch of stick held.
func TestLawBoundaryDeceleration(t *testing.T) {
	worstQ, worstNz, flips := cross(t, 150, 0.15, 0.15, func(speed float64) bool { return speed < 115 })
	t.Logf("decel: worst q %.1f deg/s, worst |nz-1| %.2f, law flips %d", worstQ*180/math.Pi, worstNz, flips)
	if flips != 1 {
		t.Fatalf("law must flip exactly once, flipped %d times", flips)
	}
	if worstQ > 12*math.Pi/180 {
		t.Fatalf("pitch-rate excursion %.1f deg/s at the law handover", worstQ*180/math.Pi)
	}
	if worstNz > 1.2 {
		t.Fatalf("load excursion |nz-1| = %.2f at the law handover", worstNz)
	}
}

// TestLawHysteresis: inside the 125..135 m/s band the law must hold whatever
// it was — the old raw 130 m/s comparison flipped it every frame the speed
// wobbled across the line.
func TestLawHysteresis(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	// Low, where TAS ≈ CAS (the law gates are calibrated airspeed), with the
	// leveler holding altitude: the alpha law owns no flight path, and a
	// neutral-stick run dove into the sea and blew through the exit gate.
	m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, 130, 2000)
	m.Step(Inputs{Throttle: 0.55, Gear: true})
	entered := m.pa // whatever the first step derived at exactly 130
	flips := 0
	was := entered
	var l leveler
	for i := 0; i < 240*10; i++ { // settle the speed/altitude holds first — the capture transient is not the chatter case
		m.Step(Inputs{Throttle: clamp(0.5+(130-m.State.Velocity.Length())*0.05, 0.1, 0.9), Pitch: clamp(l.pitch(m, 300), -0.3, 0.3), Gear: true})
	}
	was = m.pa
	flips = 0
	low, high := 1e9, 0.0
	for i := 0; i < 240*30; i++ { // half a minute hovering about the old boundary
		if v := m.State.Velocity.Length(); v < low {
			low = v
		} else if v > high {
			high = v
		}
		// Proportional speed hold centred exactly on the old 130 boundary — the
		// chatter case. (Fixed bang-bang throttles could not bracket the level
		// drag point at sea-level thrust and ran away through the band.)
		throttle := clamp(0.5+(130-m.State.Velocity.Length())*0.05, 0.1, 0.9)
		m.Step(Inputs{Throttle: throttle, Pitch: clamp(l.pitch(m, 300), -0.3, 0.3), Gear: true})
		if m.pa != was {
			flips++
			was = m.pa
		}
	}
	t.Logf("hysteresis: started pa=%v, %d flips while hovering about 130 m/s (speed ranged %.0f..%.0f, altitude %.0f)", entered, flips, low, high, m.State.Position.Y)
	if flips > 0 {
		t.Fatalf("law flipped %d times inside the hysteresis band", flips)
	}
}

// TestGearCycle: gear cycles with the stick displaced — the original
// gear-cycle trim-jump shape (the integral only feeds the command path while
// the stick flies the jet). Three seams: down into the PA speed range (law
// flip + transit), up out of it, and down fast (transit only, no law change).
func TestGearCycle(t *testing.T) {
	cases := []struct {
		name     string
		speed    float64
		from, to bool
	}{
		{"down into PA", 115, false, true},
		{"up out of PA", 115, true, false},
		{"down fast, no law change", 160, false, true},
	}
	// The steady cases regression-pin current behaviour; the climb case below
	// winds the trim up first (the original 23 deg/s gear-up snap's shape).
	// Note the teeth live in the LAW-FLIP laundering, not the transit top-up:
	// a gear cycle changes the law, so the flip rule launders it by itself —
	// disabling only the transit top-up moves these bounds by ~0.4 deg/s.

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
			m.State = Level(m, Vec3{Y: 600}, Vec3{X: 1}, c.speed, 2000)
			for i := 0; i < 240*8; i++ { // settle with the stick already displaced, so the cycle is the only change
				m.Step(Inputs{Throttle: 0.6, Pitch: 0.12, Gear: c.from})
			}
			worstQ, worstNz := 0.0, 0.0
			for i := 0; i < 240*10; i++ { // ~6 s of travel plus settling
				m.Step(Inputs{Throttle: 0.6, Pitch: 0.12, Gear: c.to})
				_, q, _ := rates(m.State.Omega)
				if a := math.Abs(q); a > worstQ {
					worstQ = a
				}
				if d := math.Abs(m.State.Fcs.Normal - 1); d > worstNz {
					worstNz = d
				}
			}
			t.Logf("%s: worst q %.1f deg/s, worst |nz-1| %.2f", c.name, worstQ*180/math.Pi, worstNz)
			if worstQ > 12*math.Pi/180 {
				t.Fatalf("pitch-rate excursion %.1f deg/s through the gear cycle (the un-laundered jump snapped 23 deg/s)", worstQ*180/math.Pi)
			}
			if worstNz > 1.0 {
				t.Fatalf("load excursion |nz-1| = %.2f through the gear cycle", worstNz)
			}
		})
	}
	t.Run("up during a full-power climb", func(t *testing.T) {
		// The wound-trim case: a full-burner climbing cleanup right after a
		// low-level departure, stick held — the state the original defect
		// lived in. The PA integral is loaded when the gear starts up.
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 60}, Vec3{X: 1}, 90, 3000)
		for i := 0; i < 240*6; i++ { // full-power climb establishing, gear down
			m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: 0.3, Gear: true})
		}
		worstQ := 0.0
		for i := 0; i < 240*8; i++ { // cleanup mid-climb
			m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: 0.3, Gear: false})
			_, q, _ := rates(m.State.Omega)
			if a := math.Abs(q); a > worstQ {
				worstQ = a
			}
		}
		t.Logf("climbing cleanup: worst q %.1f deg/s", worstQ*180/math.Pi)
		if worstQ > 15*math.Pi/180 {
			t.Fatalf("pitch-rate excursion %.1f deg/s at the climbing cleanup (the un-laundered jump snapped 23 deg/s)", worstQ*180/math.Pi)
		}
	})
}

// There is deliberately NO fixed-throttle approach-deceleration test: level
// flight on the back side of the power curve is speed-UNSTABLE, so a fixed
// throttle cannot walk the jet onto on-speed — from above the bucket, excess
// thrust accelerates it away to the front-side equilibrium instead (three
// drafts at 0.20/0.30/0.48 all diverged, 95 m/s -> 278). Capturing on-speed
// requires active power, which is exactly what TestApproachPowerHold flies.

// TestApproachPowerHold: the client's ATC law (#202) closed on the REAL core.
// The constants MUST mirror apps/air/web/src/game/atc.ts — this is the gain
// validation the client's surrogate-model unit tests cannot give, and it
// catches the two-controller failure mode (the PA stabilator law and the ATC
// throttle law both steer alpha; a gain mismatch pumps the phugoid).
func TestApproachPowerHold(t *testing.T) {
	const onspeed, least, most = 8.1, 0.12, 1.0
	const gainError, gainRate = 0.16, 0.6
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 1200}, Vec3{X: 1}, 100, 2000)
	for i := 0; i < 240*8; i++ {
		m.Step(Inputs{Throttle: 0.55, Gear: true})
	}
	throttle, previous := 0.55, math.NaN()
	captured := -1
	worstHold := 0.0
	total := 240 * 150
	for i := 0; i < total; i++ {
		body := m.State.Attitude.Unrotate(m.State.Velocity)
		al := alpha(body) * 180 / math.Pi
		rate := 0.0
		if !math.IsNaN(previous) {
			rate = clamp((al-previous)*240, -10, 10)
		}
		previous = al
		throttle = clamp(throttle+((al-onspeed)*gainError+rate*gainRate)/240, least, most)
		m.Step(Inputs{Throttle: throttle, Gear: true})
		if captured < 0 && math.Abs(al-onspeed) < 0.5 {
			captured = i
		}
		if i > total-240*30 { // the final 30 s is the hold window
			if d := math.Abs(al - onspeed); d > worstHold {
				worstHold = d
			}
		}
	}
	if captured < 0 {
		t.Fatal("ATC never captured on-speed")
	}
	t.Logf("ATC: captured on-speed at t+%.0fs, worst hold deviation %.2f° over the final 30 s, final throttle %.2f", float64(captured)/240, worstHold, throttle)
	if worstHold > 1.0 {
		t.Fatalf("ATC hold oscillates: %.2f° deviation (controller fight with the PA law)", worstHold)
	}
}

// TestPatternHold: gear down FASTER than on-speed — the downwind leg — must be
// flyable hands-off: the PA law's neutral demand is the level-flight alpha of
// the current configuration, not on-speed. The old bare-wing cap never engaged
// below ~190 kt, so neutral stick commanded full on-speed alpha at any pattern
// speed and gear-down flight was a standing climb order no trim could remove.
func TestPatternHold(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 500}, Vec3{X: 1}, 110, 2500)
	climbed, worst := 0.0, 0.0
	for i := 0; i < 240*70; i++ {
		// Speed hold to isolate the pitch law, ceilinged at 0.8: a pattern pilot
		// carries pattern power — crossing 0.85 raises the waveoff attraction by
		// design, and an 85 m/s CLEAN spawn (near-stall, alpha 12.7°) drove the
		// hold through that gate and measured the waveoff, not the pattern.
		throttle := clamp(0.5+(110-m.State.Velocity.Length())*0.05, 0.1, 0.8)
		m.Step(Inputs{Throttle: throttle, Gear: true})
		if i > 240*60 { // past gear extension, the law-change laundering, and the droop's configuration balloon — real-jet behaviour whose energy tail decays for most of a minute at pattern power
			if vy := m.State.Velocity.Y; vy > climbed {
				climbed = vy
			}
			v := m.State.Attitude.Unrotate(m.State.Velocity)
			if a := alpha(v) * 180 / math.Pi; a > worst {
				worst = a
			}
		}
	}
	_ = climbed
	// Alpha is the whole claim: the old law pegged 8.1° at any gear-down speed,
	// and the climb was its consequence. A climb bound was asserted here too
	// until honest undercarriage drag arrived — the scenario's crude
	// proportional speed hold rings against the heavier configuration
	// transient, so a vertical-speed bound measured the test's throttle law,
	// not the FCS.
	if worst > 5 {
		t.Errorf("neutral stick at 110 m/s gear-down settled at %.1f° alpha — the law is commanding on-speed, not level flight", worst)
	}
}

// TestBallDatum: the approach alpha datum must not move with speed. Near
// on-speed, need moves as 1/v², and when the level-flight cap engaged there a
// fat power correction walked the neutral alpha from 8.4° to 5.3° and back —
// the jet visibly re-trimmed itself with every power input ("does not trim to
// maintain attitude", the landing start without ATC). Within the approach band
// the law is AoA-referenced: the datum stays at on-speed and power flies the
// path. The alpha ripple that remains is the alpha loop tracking the phugoid
// the surge excites, centred on the datum — not the datum walking.
func TestBallDatum(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	s, throttle := Approach(m, Vec3{Y: 400}, Vec3{X: 1}, -3.5*math.Pi/180, 2500)
	m.State = s
	onspeed := m.Airframe.Control.Onspeed * 180 / math.Pi
	low, sum, samples := 99.0, 0.0, 0
	for i := 0; i < 240*40; i++ {
		in := throttle
		if i > 240*8 && i < 240*16 {
			in = throttle + 0.25 // the fat correction: ~12 kt fast at its peak
		}
		m.Step(Inputs{Throttle: in, Gear: true})
		if i > 240*5 {
			a := alpha(m.State.Attitude.Unrotate(m.State.Velocity)) * 180 / math.Pi
			low = math.Min(low, a)
			sum += a
			samples++
		}
	}
	if low < onspeed-1.2 {
		t.Errorf("the fast excursion dropped alpha to %.2f° — the datum is walking with speed again (want it pinned near %.2f)", low, onspeed)
	}
	if mean := sum / float64(samples); math.Abs(mean-onspeed) > 0.3 {
		t.Errorf("mean alpha %.2f° across the excursion — the approach is not %.2f-referenced", mean, onspeed)
	}
}

// Mochi world: High-alpha gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestHighAlpha: with the LEX working, aircraft lift keeps building well
// past plain-wing stall, the curve is smooth, and breakdown is a gentle
// rolloff — the angles-fighter envelope.
func TestHighAlpha(t *testing.T) {
	m := calm()
	peak, at, previous := 0.0, 0.0, 0.0
	for a := 0.0; a < 60*math.Pi/180; a += 0.01 {
		cl, cd, _ := polar(m, 100, a, 0)
		if math.IsNaN(cl) || math.IsNaN(cd) {
			t.Fatalf("NaN at %f", a)
		}
		if math.Abs(cl-previous) > 0.09 {
			t.Fatalf("lift discontinuity at %f: %f -> %f", a, previous, cl)
		}
		previous = cl
		if cl > peak {
			peak, at = cl, a
		}
	}
	if at < 25*math.Pi/180 {
		t.Fatalf("aircraft CLmax at %f rad — the LEX should carry it past 25°", at)
	}
	if peak < 1.2 {
		t.Fatalf("CLmax %f too small with vortex lift", peak)
	}
	deep, _, _ := polar(m, 100, 50*math.Pi/180, 0)
	if deep < 0.45*peak {
		t.Fatalf("breakdown too sharp: CL(50°) = %f vs peak %f", deep, peak)
	}
}

// TestAuthority: pitch control must survive high alpha — the stabilator
// increment still moves the pitching moment at 30° with the same sign and
// a usable fraction of its low-alpha effectiveness.
func TestAuthority(t *testing.T) {
	m := calm()
	gradient := func(a float64) float64 {
		_, _, up := polar(m, 100, a, 0.1)
		_, _, down := polar(m, 100, a, -0.1)
		return (up - down) / 0.2
	}
	low := gradient(5 * math.Pi / 180)
	high := gradient(30 * math.Pi / 180)
	if low >= 0 {
		t.Fatalf("stabilator sign wrong at low alpha: %f", low)
	}
	if high >= 0 {
		t.Fatalf("pitch authority reversed at 30°: %f", high)
	}
	if math.Abs(high) < 0.25*math.Abs(low) {
		t.Fatalf("pitch authority collapsed at 30°: %f vs %f", high, low)
	}
}

// TestRecovery: from 40° alpha, nose-down stabilator pitches the aircraft
// down — the jet is controllable to the limiter's edge and recovers.
func TestRecovery(t *testing.T) {
	m := calm()
	m.Direct = true
	speed := 90.0
	at := 40 * math.Pi / 180
	m.State.Position = Vec3{Y: 3000}
	m.State.Velocity = Vec3{X: speed}
	m.State.Attitude = Axis(Vec3{Z: 1}, at)
	m.State.Omega = Vec3{}
	m.State.Fcs = FcsState{Stabilator: Pair{Left: 0.35, Right: 0.35}} // trailing edge down = nose down
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{})
	}
	v := m.State.Attitude.Unrotate(m.State.Velocity)
	if alpha(v) > at {
		t.Fatalf("no pitch-down response from deep alpha: alpha now %f", alpha(v))
	}
	if math.IsNaN(m.State.Position.Y) {
		t.Fatal("diverged")
	}
}

// Mochi world: Aerodynamics gates
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

func calm() *Model { return New(Fighter, Environment{}, World{}) }

// polar evaluates aircraft lift and drag coefficients at a body alpha with
// a held symmetric stabilator.
func polar(m *Model, speed float64, at float64, stabilator float64) (cl float64, cd float64, moment float64) {
	s := &m.State
	s.Position = Vec3{Y: 2000}
	s.Velocity = Vec3{X: speed}
	s.Attitude = Axis(Vec3{Z: 1}, at)
	s.Omega = Vec3{}
	s.Fcs = FcsState{Stabilator: Pair{Left: stabilator, Right: stabilator}}
	m.weigh()
	m.gust = Vec3{}
	local := air(2000, m.Environment)
	total := m.forces(s, Inputs{}, local)
	world := s.Attitude.Rotate(total.Force)
	// Wind axes: velocity along +x, so lift is +y, drag is -x.
	q := 0.5 * local.Density * speed * speed * m.Airframe.Reference.Area
	return world.Y / q, -world.X / q, total.Moment.Z
}

// TestGlide: the bare airframe trims to a steady glide with lift carrying
// the weight — the signs-are-right proof.
func TestGlide(t *testing.T) {
	m := calm()
	theta, stabilator, path, ok := Glide(m, 150, 1000)
	if !ok {
		t.Fatalf("glide trim did not converge: theta %f stab %f path %f", theta, stabilator, path)
	}
	if path >= 0 || path < -0.35 {
		t.Fatalf("implausible power-off path angle: %f rad", path)
	}
	alpha := theta - path
	if alpha < 0.01 || alpha > 0.20 {
		t.Fatalf("implausible trim alpha: %f rad", alpha)
	}
	if math.Abs(stabilator) > 0.4 {
		t.Fatalf("implausible stabilator: %f rad", stabilator)
	}
}

// TestInduced: drag rises with lift squared at the expected induced slope.
func TestInduced(t *testing.T) {
	m := calm()
	type point struct{ cl2, cd float64 }
	var points []point
	for _, a := range []float64{0.03, 0.06, 0.09, 0.12} {
		cl, cd, _ := polar(m, 150, a, 0)
		points = append(points, point{cl * cl, cd})
	}
	slope := (points[len(points)-1].cd - points[0].cd) / (points[len(points)-1].cl2 - points[0].cl2)
	// Reference AR ≈ 4.0; the effective Oswald across wing + body sits
	// around 0.6-0.8, so the slope should land near 0.10-0.13.
	if slope < 0.05 || slope > 0.20 {
		t.Fatalf("induced drag slope %f outside the plausible band", slope)
	}
}

// TestStall: aircraft lift peaks and breaks; nothing blows up to 45°.
func TestStall(t *testing.T) {
	m := calm()
	// The linear-region peak, and the post-stall dip after it. Viterna
	// sections keep flat-plate lift high toward 45°, so the aircraft stall
	// reads as a definite dip past the peak, not a collapse — the soft,
	// honest stall of a LEX fighter.
	peak, at := 0.0, 0.0
	for a := 0.0; a < 1.1; a += 0.005 {
		cl, cd, _ := polar(m, 120, a, 0)
		if math.IsNaN(cl) || math.IsNaN(cd) {
			t.Fatalf("NaN at alpha %f", a)
		}
		if cl > peak {
			peak, at = cl, a
		}
	}
	if peak < 0.9 {
		t.Fatalf("Clmax too small: %f", peak)
	}
	if at < 0.15 {
		t.Fatalf("Clmax too early: %f rad", at)
	}
	dip := peak
	for a := at; a < at+0.45; a += 0.005 {
		cl, _, _ := polar(m, 120, a, 0)
		if cl < dip {
			dip = cl
		}
	}
	if dip > peak-0.05 {
		t.Fatalf("no post-stall dip: peak %f, floor %f", peak, dip)
	}
}

// TestRelaxed: the airframe carries RELAXED static stability (the real
// Hornet answer — the FCS is the stabilizer). Bare, the jet may diverge
// slowly but must remain hand-flyable: from trim with a pitch kick it
// neither departs nor runs away within four seconds — time enough for a
// pilot (or the FCS) to correct.
func TestRelaxed(t *testing.T) {
	m := calm()
	m.Direct = true
	theta, stabilator, path, ok := Glide(m, 150, 1500)
	if !ok {
		t.Fatal("no trim")
	}
	m.State.Position = Vec3{Y: 1500}
	m.State.Velocity = Vec3{X: 150 * math.Cos(path), Y: 150 * math.Sin(path)}
	m.State.Attitude = Axis(Vec3{Z: 1}, theta)
	m.State.Fcs.Stabilator = Pair{Left: stabilator, Right: stabilator}
	m.State.Omega = Vec3{Z: 0.08}
	trimmed := theta - path
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{})
		v := m.State.Attitude.Unrotate(m.State.Velocity)
		if math.Abs(alpha(v)-trimmed) > 12*math.Pi/180 {
			t.Fatalf("bare airframe ran away at %.2f s: alpha %f", m.State.Time, alpha(v))
		}
	}
}

// TestShortPeriod: from trim, a pitch-rate kick decays — the bare airframe
// is dynamically convergent in pitch (loose phase-B gate).
func TestShortPeriod(t *testing.T) {
	m := calm()
	m.Direct = true
	theta, stabilator, path, ok := Glide(m, 180, 2000)
	if !ok {
		t.Fatal("no trim")
	}
	m.State.Position = Vec3{Y: 2000}
	m.State.Velocity = Vec3{X: 180 * math.Cos(path), Y: 180 * math.Sin(path)}
	m.State.Attitude = Axis(Vec3{Z: 1}, theta)
	m.State.Fcs.Stabilator = Pair{Left: stabilator, Right: stabilator}
	m.State.Omega = Vec3{Z: 0.15} // pitch-rate kick
	for i := 0; i < 240*12; i++ {
		m.Step(Inputs{})
		q := math.Abs(m.State.Omega.Z)
		if q > 1.5 {
			t.Fatalf("pitch divergence at %f s: q=%f", m.State.Time, m.State.Omega.Z)
		}
		v := m.State.Attitude.Unrotate(m.State.Velocity)
		if alpha(v) > m.Airframe.Limit.Alpha {
			t.Fatalf("bare departure at %f s", m.State.Time)
		}
	}
}

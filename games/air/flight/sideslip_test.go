// Mochi world: Sideslip gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// slipped is the trimmed level state with the flow brought in from the right
// by beta, at the same speed and alpha.
func slipped(base State, deg float64) State {
	vb := base.Attitude.Unrotate(base.Velocity)
	v := vb.Length()
	b := deg * math.Pi / 180
	s := base
	s.Velocity = s.Attitude.Rotate(Vec3{X: vb.X * math.Cos(b), Y: vb.Y * math.Cos(b), Z: v * math.Sin(b)})
	return s
}

// TestSideslipDerivatives: the flight-measured basic F-18 (NASA/TP-1999-206573,
// figure 15, alpha 5-10 deg) has Cy_beta about -0.0135, Cn_beta about
// +0.0016 to +0.0020 and Cl_beta -0.0013 to -0.0025 per degree on the 400 ft²
// reference. The model, trimmed at 130 m/s (alpha ~6 deg), is held inside
// that scatter - the fins at their published area and the forebody's
// sideslip cross-section are what put it there.
func TestSideslipDerivatives(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	local := Atmosphere(1000, m.Environment)
	base := Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 130, 2500)
	S, b := m.Airframe.Reference.Area, m.Airframe.Reference.Span
	v := base.Velocity.Length()
	q := 0.5 * local.Density * v * v
	s := slipped(base, 2)
	var total Forces
	m.aero(&s, &total, local)
	cy := total.Force.Z / (q * S) / 2
	cn := -total.Moment.Y / (q * S * b) / 2
	cl := total.Moment.X / (q * S * b) / 2
	if cy > -0.012 || cy < -0.016 {
		t.Errorf("Cy_beta %+.5f/deg, want -0.012..-0.016 (flight: about -0.0135)", cy)
	}
	if cn < 0.0014 || cn > 0.0022 {
		t.Errorf("Cn_beta %+.5f/deg, want +0.0014..+0.0022 (flight: +0.0016..+0.0020)", cn)
	}
	if cl > -0.0013 || cl < -0.0025 {
		t.Errorf("Cl_beta %+.5f/deg, want -0.0013..-0.0025 (flight)", cl)
	}
}

// TestSideslipDrag: what a slip costs, all of it emergent - the fins' lift
// tilting into induced drag, the fuselage crossflow, the nose - so the rise is
// gated rather than set. At 130 m/s trimmed, 10 deg of sideslip is a quarter
// to two-fifths more drag, 5 deg a twentieth, and the rise is monotonic.
func TestSideslipDrag(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	local := Atmosphere(1000, m.Environment)
	base := Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 130, 2500)
	drag := func(deg float64) float64 {
		s := slipped(base, deg)
		var total Forces
		m.aero(&s, &total, local)
		return -total.Force.Dot(s.Attitude.Unrotate(s.Velocity).Normalize())
	}
	zero := drag(0)
	five, ten, fifteen := drag(5)/zero-1, drag(10)/zero-1, drag(15)/zero-1
	if five < 0.04 || five > 0.10 {
		t.Errorf("5 deg of sideslip: %+.1f%% drag, want +4..+10%%", 100*five)
	}
	if ten < 0.20 || ten > 0.40 {
		t.Errorf("10 deg of sideslip: %+.1f%% drag, want +20..+40%%", 100*ten)
	}
	if !(fifteen > ten && ten > five) {
		t.Errorf("the rise is not monotonic: %+.1f%% %+.1f%% %+.1f%%", 100*five, 100*ten, 100*fifteen)
	}
}

// TestFinPolar: a fin's drag-due-to-lift is priced by the lift vector's tilt
// alone - no supplementary K - and comes out at the lifting-line total
// 1/(pi·AR·e) for its effective aspect ratio, so nobody adds one blindly.
func TestFinPolar(t *testing.T) {
	a := *Fighter
	a.Surfaces = nil
	var fin *Surface
	for _, s := range Fighter.Surfaces {
		if s.Kind == Fin {
			a.Surfaces = append(a.Surfaces, s)
			fin = &a.Surfaces[len(a.Surfaces)-1]
		}
	}
	a.Body = nil
	a.Stores = nil
	m := New(&a, Environment{}, World{})
	local := Atmosphere(1000, m.Environment)
	area := 0.0
	for _, s := range a.Surfaces {
		area += s.Area
	}
	v := 150.0
	q := 0.5 * local.Density * v * v
	polar := func(deg float64) (float64, float64) {
		b := deg * math.Pi / 180
		s := State{Attitude: Quat{W: 1}, Position: Vec3{Y: 1000}, Velocity: Vec3{X: v * math.Cos(b), Z: v * math.Sin(b)}, Gear: GearState{Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}}
		var total Forces
		m.aero(&s, &total, local)
		unit := s.Velocity.Normalize()
		drag := -total.Force.Dot(unit)
		return total.Force.Add(unit.Scale(drag)).Length() / (q * area), drag / (q * area)
	}
	_, cd0 := polar(0)
	cl, cd := polar(6)
	k := (cd - cd0) / (cl * cl)
	want := 1 / (math.Pi * fin.Ratio * fin.Oswald)
	if fin.Induced != 0 {
		t.Errorf("the fin carries a supplementary K of %.2f: the tilt already prices its lift", fin.Induced)
	}
	if math.Abs(k-want) > 0.25*want {
		t.Errorf("fin drag-due-to-lift K %.3f, want the lifting-line %.3f within a quarter", k, want)
	}
}

// TestSideslipPedal: full pedal in level flight. The FCS - half throw at low
// alpha, lateral-acceleration feedback for coordination (NATOPS 2.8.2) - lets a
// modest sideslip stand: half of the 30 deg rudder against a twin-rudder
// fighter's rudder power and the flight-measured weathercock comes to ten
// degrees or so. Holding it costs energy: the jet with its feet on the floor
// ends the twenty seconds with more specific energy.
func TestSideslipPedal(t *testing.T) {
	energy := func(pedal float64) (float64, float64) {
		m := New(Fighter, Environment{}, World{})
		m.State = Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 130, 2500)
		throttle := m.State.Engine[0].Spool
		sum, n := 0.0, 0
		for i := 0; i < 240*20; i++ {
			m.Step(Inputs{Throttle: throttle, Yaw: pedal})
			if i > 240*5 {
				sum += math.Abs(beta(m.State.Attitude.Unrotate(m.State.Velocity))) * 180 / math.Pi
				n++
			}
		}
		v := m.State.Velocity.Length()
		return v*v/2 + 9.8*m.State.Position.Y, sum / float64(n)
	}
	feet, _ := energy(0)
	slip, held := energy(1)
	t.Logf("full pedal at 130 m/s: %.1f deg of sideslip held, %.0f J/kg of specific energy spent over 20 s", held, feet-slip)
	if held < 4 || held > 13 {
		t.Errorf("full pedal held %.1f deg of sideslip, want 4..13", held)
	}
	if loss := feet - slip; loss < 60 {
		t.Errorf("the slip cost %.0f J/kg of specific energy over 20 s, want at least 60 (about 0.5 m/s at 130 m/s)", loss)
	}
}

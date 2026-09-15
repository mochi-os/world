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

// derivative measures Cy_beta, Cn_beta and Cl_beta per degree for a model at
// a state, from a 2 degree slip.
func derivative(m *Model, base State, local Air) (cy, cn, cl float64) {
	S, b := m.Airframe.Reference.Area, m.Airframe.Reference.Span
	v := base.Velocity.Length()
	q := 0.5 * local.Density * v * v
	s := slipped(base, 2)
	var total Forces
	m.aero(&s, &total, local)
	return total.Force.Z / (q * S) / 2, -total.Moment.Y / (q * S * b) / 2, total.Moment.X / (q * S * b) / 2
}

// scheduled applies the leading-edge flap schedule the FCS would fly at the
// state's alpha, which the static Level trim leaves at zero.
func scheduled(m *Model, s State) State {
	c := &m.Airframe.Control
	a := alpha(s.Attitude.Unrotate(s.Velocity))
	s.Fcs.Slat = clamp(c.Slat.Slope*(a-c.Slat.Offset), 0, c.Slat.Limit)
	return s
}

// TestSweptWing: the wing's elements lie along the swept aerodynamic-centre
// line, so at sideslip the leading panel unsweeps and lifts more and the
// trailing one less - the swept wing's own dihedral effect, rolling the jet
// away from the slip and growing with its lift coefficient as simple sweep
// theory has it (about -0.2·CL·tan Λ per radian, a fifth to a third of the
// whole jet's). Before, every axis ran straight out the side and the wings
// gave exactly nothing. The whole jet, flaps scheduled, stays inside the
// flight scatter from 4 to 7 degrees alpha (NASA/TP-1999-206573 figure 15);
// past 10 degrees the model's wing section is into its stall blend and the
// differential fades, which the envelope calibration owns.
func TestSweptWing(t *testing.T) {
	a := *Fighter
	a.Surfaces = nil
	for _, s := range Fighter.Surfaces {
		if s.Kind == Wing {
			a.Surfaces = append(a.Surfaces, s)
		}
	}
	a.Body = nil
	a.Stores = nil
	wings := New(&a, Environment{}, World{})
	whole := New(Fighter, Environment{}, World{})
	local := Atmosphere(1000, whole.Environment)
	fast := scheduled(whole, Level(whole, Vec3{Y: 1000}, Vec3{X: 1}, 175, 2500))
	slow := scheduled(whole, Level(whole, Vec3{Y: 1000}, Vec3{X: 1}, 130, 2500))
	_, cn, low := derivative(wings, fast, local)
	_, _, high := derivative(wings, slow, local)
	degrees := func(s State) float64 { return alpha(s.Attitude.Unrotate(s.Velocity)) * 180 / math.Pi }
	t.Logf("wings alone: Cl_beta %+.5f/deg at %.1f deg alpha, %+.5f at %.1f; Cn_beta %+.5f", low, degrees(fast), high, degrees(slow), cn)
	if low > -0.0001 || high > -0.0004 {
		t.Errorf("the wings alone give Cl_beta %+.5f/deg at %.1f deg alpha and %+.5f at %.1f, want the swept wing's dihedral effect: below -0.0001 and -0.0004", low, degrees(fast), high, degrees(slow))
	}
	if high > low {
		t.Errorf("the wings' dihedral effect shrinks from %+.5f to %+.5f/deg as alpha rises from %.1f to %.1f, want it growing with lift", low, high, degrees(fast), degrees(slow))
	}
	if cn < 0 {
		t.Errorf("the wings alone give Cn_beta %+.5f/deg, want the leading panel's extra drag to weathercock, not the reverse", cn)
	}
	for _, s := range []State{fast, slow} {
		cy, cn, cl := derivative(whole, s, local)
		t.Logf("whole jet at %.1f deg alpha: Cy_beta %+.5f Cn_beta %+.5f Cl_beta %+.5f per deg", degrees(s), cy, cn, cl)
		if cy > -0.012 || cy < -0.016 || cn < 0.0014 || cn > 0.0022 || cl > -0.0013 || cl < -0.0025 {
			t.Errorf("at %.1f deg alpha: Cy_beta %+.5f Cn_beta %+.5f Cl_beta %+.5f per deg, want -0.012..-0.016, +0.0014..+0.0022, -0.0013..-0.0025 (flight)", degrees(s), cy, cn, cl)
		}
	}
}

// TestSweptFrames: the swept strips change nothing at zero sideslip. The
// calibrated terms in the aero pass - drag-due-to-lift, the polar break,
// camber, vortex lift, the compressibility - are written on the body frame,
// and the swept section's coefficients, alpha and pressure convert to it on
// the way in; get one wrong and the whole jet's lift curve moves by several
// percent. The pins are the unswept model's values, clean with the slats out
// at 134 m/s and 6,000 m.
func TestSweptFrames(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	local := Atmosphere(6000, m.Environment)
	v := 134.0
	q := 0.5 * local.Density * v * v
	S := m.Airframe.Reference.Area
	for _, want := range []struct{ degrees, lift, moment float64 }{{30, 1.820, -0.2467}, {40, 2.146, -0.2944}} {
		incidence := want.degrees * math.Pi / 180
		s := State{Attitude: Quat{W: 1}, Position: Vec3{Y: 6000}, Velocity: Vec3{X: v * math.Cos(incidence), Y: -v * math.Sin(incidence)}, Fuel: 3000, Gear: GearState{Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}}
		s.Fcs.Slat = 25 * math.Pi / 180
		var total Forces
		m.aero(&s, &total, local)
		lift := (total.Force.Y*math.Cos(incidence) + total.Force.X*math.Sin(incidence)) / (q * S)
		moment := total.Moment.Z / (q * S * 4)
		if math.Abs(lift-want.lift) > 0.01*want.lift || math.Abs(moment-want.moment) > 0.03*math.Abs(want.moment) {
			t.Errorf("clean at %.0f deg alpha: CL %.4f Cm %+.4f, want %.3f and %+.4f within 1%% and 3%% (the unswept model)", want.degrees, lift, moment, want.lift, want.moment)
		}
	}
}

// TestApproachWeathercock: in the landing configuration at the alpha of a
// slow approach the jet still weathercocks into a slip - the fins are not in
// any wake there. Swept fin strips lost it: simple sweep theory has the
// upward flow at alpha running along an aft-swept span axis, a quarter of
// the fins' side force gone by 16 degrees, and the weathercock went negative
// with the jet spiralling off hands-off in the on-speed settle.
func TestApproachWeathercock(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	local := Atmosphere(400, m.Environment)
	base := Level(m, Vec3{Y: 400}, Vec3{X: 1}, 72, 2500)
	q := 0.5 * local.Density * 72 * 72
	droop, slat := m.Approaching(q)
	base.Fcs.Flaperon = Pair{Left: droop, Right: droop}
	base.Fcs.Flap = droop
	base.Fcs.Slat = slat
	base.Gear.Extension = 1
	cy, cn, cl := derivative(m, base, local)
	a := alpha(base.Attitude.Unrotate(base.Velocity)) * 180 / math.Pi
	t.Logf("landing configuration at %.1f deg alpha: Cy_beta %+.5f Cn_beta %+.5f Cl_beta %+.5f per deg", a, cy, cn, cl)
	if cn < 0.0010 {
		t.Errorf("landing configuration at %.1f deg alpha: Cn_beta %+.5f/deg, want the weathercock to hold (above +0.0010)", a, cn)
	}
	if cl > -0.002 || cl < -0.008 {
		t.Errorf("landing configuration at %.1f deg alpha: Cl_beta %+.5f/deg, want the dihedral effect between -0.002 and -0.008", a, cl)
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

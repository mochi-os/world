// Mochi world: Gun recoil gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// kicked flies the airframe trimmed and level for the given seconds with the
// trigger held or not, and reports the speed lost and the pitch attitude it
// ends at, degrees.
func kicked(air *Airframe, fire bool, seconds float64) (lost float64, pitch float64) {
	m := New(air, Environment{}, World{})
	m.State = Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 180, 2500)
	throttle := m.State.Engine[0].Spool
	start := m.State.Velocity.Length()
	for i := 0; i < int(240*seconds); i++ {
		m.Step(Inputs{Throttle: throttle, Fire: fire})
	}
	nose := m.State.Attitude.Rotate(Vec3{X: 1})
	return start - m.State.Velocity.Length(), math.Asin(nose.Y) * 180 / math.Pi
}

// TestRecoilForce: the trigger held puts the cannon's average recoil on the
// airframe, aft along the body axis at the port, and the port sitting above
// the CG turns it into a nose-up moment. Trigger up, or a gun whose kick is
// not modelled, and nothing is added.
func TestRecoilForce(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State = Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 180, 2500)
	gun := Fighter.Gun
	if gun.Recoil < 16000 || gun.Recoil > 18000 || gun.Position.X < 6 || gun.Position.Y <= 0 {
		t.Fatalf("the fighter's gun: %+v, want the M61A1's 3,818 lbf (17 kN) at a port on the nose top", gun)
	}
	var total Forces
	m.recoil(Inputs{Fire: true}, &total)
	if total.Force.X != -gun.Recoil || total.Force.Y != 0 || total.Force.Z != 0 {
		t.Errorf("recoil force %+v, want %.0f N aft along the body axis", total.Force, gun.Recoil)
	}
	arm := gun.Position.Y - m.center.Y
	if want := arm * gun.Recoil; total.Moment.Z <= 0 || math.Abs(total.Moment.Z-want) > 1 || total.Moment.X != 0 || total.Moment.Y != 0 {
		t.Errorf("recoil moment %+v, want %.0f N·m nose-up (the port %.2f m above the CG)", total.Moment, want, arm)
	}
	var quiet Forces
	m.recoil(Inputs{}, &quiet)
	if quiet != (Forces{}) {
		t.Errorf("trigger up adds %+v", quiet)
	}
	unarmed := *Fighter
	unarmed.Gun.Recoil = 0
	u := New(&unarmed, Environment{}, World{})
	u.State = m.State
	var none Forces
	u.recoil(Inputs{Fire: true}, &none)
	if none != (Forces{}) {
		t.Errorf("a gun with no modelled kick adds %+v", none)
	}
}

// TestRecoilKick: two seconds on the trigger cost the jet the recoil impulse
// over its mass - about 2.5 m/s at 13.4 t - against the same flight with the
// trigger up, and the nose sits a fraction of a degree higher: the moment is
// real, and the FCS holds it to a bobble rather than a pitch-up.
func TestRecoilKick(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State = Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 180, 2500)
	m.Step(Inputs{Throttle: m.State.Engine[0].Spool})
	want := Fighter.Gun.Recoil / m.mass * 2
	quiet, level := kicked(Fighter, false, 2)
	fired, nose := kicked(Fighter, true, 2)
	if lost := fired - quiet; math.Abs(lost-want) > 0.1*want {
		t.Errorf("two seconds of fire cost %.2f m/s over the quiet flight, want %.2f (%.0f N over %.0f kg)", lost, want, Fighter.Gun.Recoil, m.mass)
	}
	if up := nose - level; up < 0.02 || up > 0.5 {
		t.Errorf("the nose sits %.3f° higher after two seconds of fire, want a held bobble of 0.02..0.5°", up)
	}
	unarmed := *Fighter
	unarmed.Gun.Recoil = 0
	if lost, pitch := kicked(&unarmed, true, 2); lost != quiet || pitch != level {
		t.Errorf("a gun with no modelled kick: lost %.3f m/s at %.3f°, want the quiet flight's %.3f at %.3f", lost, pitch, quiet, level)
	}
}

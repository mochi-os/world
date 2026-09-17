// Mochi world: Speed brake gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// boarding is the drag area the speed brake adds at an extension, m², from
// the aero pass alone at trimmed level flight.
func boarding(extension float64) float64 {
	m := New(Fighter, Environment{}, World{})
	local := Atmosphere(1000, m.Environment)
	base := Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 180, 2500)
	v := base.Velocity.Length()
	q := 0.5 * local.Density * v * v
	drag := func(open float64) float64 {
		s := base
		s.Fcs.Speedbrake = open
		var total Forces
		m.aero(&s, &total, local)
		return -total.Force.Dot(s.Attitude.Unrotate(s.Velocity).Normalize())
	}
	return (drag(extension) - drag(0)) / q
}

// TestBrakeDrag: the panel the airframe's own model carries - 0.84 by 2.13 m,
// hinged out to 60 degrees - is a flat plate at sin² of its deflection: about
// 1.5 m² of drag area fully out, and a third of that at half travel.
func TestBrakeDrag(t *testing.T) {
	full := boarding(1)
	if full < 1.3 || full > 1.8 {
		t.Errorf("speed brake fully out adds %.2f m² of drag area, want 1.3..1.8 (a 1.7 m² panel at 60 deg)", full)
	}
	if half := boarding(0.5) / full; math.Abs(half-1.0/3) > 0.06 {
		t.Errorf("half travel adds %.2f of the full drag, want a third (sin² 30 / sin² 60)", half)
	}
}

// TestBrakeTravel: the actuator takes 2.5 s from stowed to full (NASA
// TM-110216: 60/2.5 deg/s no-load; NATOPS: 3 s maximum), not the second it
// used to.
func TestBrakeTravel(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State = Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 180, 2500)
	throttle := m.State.Engine[0].Spool
	reached := -1.0
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Throttle: throttle, Speedbrake: 1})
		if m.State.Fcs.Speedbrake >= 0.99 && reached < 0 {
			reached = float64(i+1) / 240
		}
	}
	if reached < 2.2 || reached > 2.8 {
		t.Errorf("speed brake fully out after %.2f s, want 2.2..2.8", reached)
	}
}

// TestBrakeOnDeck: NATOPS 2.8.4.8 - the speedbrake operates normally on the
// ground, and the landing configuration retracts it only in flight. The
// ground law used to drive the board closed whatever the switch said, so a
// landing rollout had no speedbrake.
func TestBrakeOnDeck(t *testing.T) {
	m := aboard()
	park(m, -30, 5)
	for i := 0; i < 240*3; i++ {
		m.Step(Inputs{Gear: true, Brake: true})
	}
	if !m.State.Gear.Wow {
		t.Fatal("no weight on wheels")
	}
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Gear: true, Brake: true, Flap: 1, Speedbrake: 1})
	}
	if m.State.Fcs.Speedbrake < 0.99 {
		t.Fatalf("the board stayed in on deck with the switch held: %.2f", m.State.Fcs.Speedbrake)
	}
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Gear: true, Brake: true, Flap: 1})
	}
	if m.State.Fcs.Speedbrake > 0.01 {
		t.Fatalf("the board did not stow on deck with the switch released: %.2f", m.State.Fcs.Speedbrake)
	}
	// Airborne in the landing configuration below the law's handover, the
	// board retracts whatever the switch says.
	a := New(Fighter, Environment{}, World{})
	a.State = Level(a, Vec3{Y: 300}, Vec3{X: 1}, 60, 2500)
	throttle := a.State.Engine[0].Spool
	for i := 0; i < 240*4; i++ {
		a.Step(Inputs{Throttle: throttle, Gear: true, Flap: 1, Speedbrake: 1})
	}
	if a.State.Fcs.Speedbrake > 0.01 {
		t.Fatalf("the landing configuration must retract the board in flight: %.2f", a.State.Fcs.Speedbrake)
	}
}

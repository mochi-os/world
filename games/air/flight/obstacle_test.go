// Mochi world: Obstacles are solid
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import "testing"

// built is the harbor with a hangar and a mast on the island's plain.
func built() World {
	w := harbor()
	w.Prisms = []Prism{{Outline: []Vec3{{X: 900, Z: 900}, {X: 1100, Z: 900}, {X: 1100, Z: 1100}, {X: 900, Z: 1100}}, Top: 30}}
	w.Posts = []Post{{Position: Vec3{X: 2000, Z: 2000}, Radius: 2, Top: 40}}
	return w
}

// flown poses the model level, gear up, heading +X at a speed, steps once and
// reports the crash probe that met something, -1 for none.
func flown(m *Model, position Vec3, speed float64) int {
	m.State.Position = position
	m.State.Velocity = Vec3{X: speed}
	m.State.Attitude = Quat{W: 1}
	m.State.Omega = Vec3{}
	m.State.Fcs = FcsState{}
	m.State.Gear = GearState{Extension: 0, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.Step(Inputs{})
	return m.State.Gear.Contact
}

// TestObstacles: a building is a solid from the ground to its roof and a mast
// from the ground to its top; the crash probes meet them at any speed, and
// clear them by flying over or beside.
func TestObstacles(t *testing.T) {
	m := New(Fighter, Environment{}, built())
	if flown(m, Vec3{X: 1000, Y: 20, Z: 1000}, 150) < 0 {
		t.Errorf("through the hangar at 150 m/s: no contact")
	}
	if flown(m, Vec3{X: 1000, Y: 20, Z: 1000}, 5) < 0 {
		t.Errorf("into the hangar at a taxi's 5 m/s: no contact - a wall is solid at any speed")
	}
	if c := flown(m, Vec3{X: 1000, Y: 45, Z: 1000}, 150); c >= 0 {
		t.Errorf("over the roof: probe %d met it", c)
	}
	if c := flown(m, Vec3{X: 1250, Y: 20, Z: 1000}, 150); c >= 0 {
		t.Errorf("beside the hangar: probe %d met it", c)
	}
	if flown(m, Vec3{X: 1992, Y: 20, Z: 2000}, 150) < 0 {
		t.Errorf("nose on the mast: no contact")
	}
	if c := flown(m, Vec3{X: 1992, Y: 50, Z: 2000}, 150); c >= 0 {
		t.Errorf("over the mast: probe %d met it", c)
	}
}

// TestObstaclesSeam: an obstacle at a wrap seam is met from the other side of
// it - the world is a torus, and the hangar at x 900-1100 is one image of the
// hangar at x -4100.
func TestObstaclesSeam(t *testing.T) {
	m := New(Fighter, Environment{Wrap: 5000}, built())
	if flown(m, Vec3{X: -4000, Y: 20, Z: 1000}, 150) < 0 {
		t.Errorf("through the hangar's image across the seam: no contact")
	}
	if c := flown(m, Vec3{X: -3750, Y: 20, Z: 1000}, 150); c >= 0 {
		t.Errorf("beside the image: probe %d met it", c)
	}
}

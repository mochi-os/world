// Mochi world: Trim and spawn helper tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestLevel: the spawn helper produces flight the FCS holds without a
// transient — a second later the aircraft is still level, near 1g, on speed.
func TestLevel(t *testing.T) {
	m := New(Fighter, Environment{Wrap: 250000}, World{})
	s := Level(m, Vec3{Y: 4572}, Vec3{X: 1}, 220, 3000)
	m.State = s
	in := Inputs{Throttle: s.Engine[0].Spool}
	for i := 0; i < 240*3; i++ {
		m.Step(in)
	}
	if math.Abs(m.State.Position.Y-4572) > 60 {
		t.Fatalf("altitude drifted to %.1f", m.State.Position.Y)
	}
	if speed := m.State.Velocity.Length(); math.Abs(speed-220) > 25 {
		t.Fatalf("speed drifted to %.1f", speed)
	}
	if nz := m.State.Fcs.Normal; math.Abs(nz-1) > 0.3 {
		t.Fatalf("load factor %.2f three seconds after spawn", nz)
	}
}

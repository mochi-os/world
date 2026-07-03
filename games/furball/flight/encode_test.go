// Mochi world: Serialisation tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"testing"
)

// TestEncode: a flown state round-trips bit-exact, and stepping the decoded
// copy matches stepping the original — the property prediction relies on.
func TestEncode(t *testing.T) {
	m := New(Fighter, Environment{Seed: 7, Turbulence: 1, Wrap: 250000}, World{})
	m.State.Position = Vec3{Y: 2000}
	m.State.Velocity = Vec3{X: 180}
	in := Inputs{Throttle: 0.9, Pitch: 0.4, Roll: 0.2, Yaw: 0.1}
	for i := 0; i < 240*5; i++ {
		m.Step(in)
	}
	m.State.Gear.Touch = Touch{Occurred: true, Sink: -2.5, Bank: 0.1, Kind: 3}
	m.State.Damage.Stress = 0.25

	buffer := make([]float64, Size)
	if n := m.State.Encode(buffer); n != Size {
		t.Fatalf("encoded %d words, want %d", n, Size)
	}
	decoded := Decode(buffer)
	again := make([]float64, Size)
	decoded.Encode(again)
	for i := range buffer {
		if buffer[i] != again[i] {
			t.Fatalf("round trip differs at word %d: %v vs %v", i, buffer[i], again[i])
		}
	}

	twin := New(Fighter, m.Environment, World{})
	twin.State = decoded
	one, two := make([]float64, Size), make([]float64, Size)
	for i := 0; i < 240; i++ {
		m.Step(in)
		twin.Step(in)
	}
	m.State.Encode(one)
	twin.State.Encode(two)
	for i := range one {
		if one[i] != two[i] {
			t.Fatalf("decoded twin diverged within a second at word %d: %v vs %v", i, one[i], two[i])
		}
	}
}

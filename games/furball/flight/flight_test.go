// Mochi world: Flight model tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

func spawn() State {
	return State{
		Position:  Vec3{X: -2778, Y: 4572, Z: 0},
		Direction: Vec3{X: 1, Y: 0, Z: 0},
		Speed:     220,
		Attitude:  Look(Vec3{X: 1, Y: 0, Z: 0}),
	}
}

// script is a deterministic input sequence exercising rolls, pulls, throttle
// changes, and the speedbrake.
func script(tick int) Inputs {
	in := Inputs{Throttle: 0.85}
	switch {
	case tick < 120:
	case tick < 300:
		in.Roll = 1
	case tick < 600:
		in.Pitch = 0.8
	case tick < 900:
		in.Throttle = 1
		in.Pitch = 0.3
	case tick < 1200:
		in.Throttle = 0.2
		in.Speedbrake = 1
	default:
		in.Roll = -0.5
		in.Yaw = 0.3
	}
	return in
}

// TestDeterminism runs the same script twice and expects bit-identical state.
func TestDeterminism(t *testing.T) {
	env := Environment{Wrap: 250000}
	a, b := spawn(), spawn()
	for tick := 0; tick < 3600; tick++ {
		a = Step(a, script(tick), 1.0/60, env)
		b = Step(b, script(tick), 1.0/60, env)
	}
	if a != b {
		t.Fatalf("divergence: %+v vs %+v", a, b)
	}
}

// TestStable runs the script and expects the state to stay physical.
func TestStable(t *testing.T) {
	env := Environment{Wrap: 250000}
	s := spawn()
	for tick := 0; tick < 3600; tick++ {
		s = Step(s, script(tick), 1.0/60, env)
		if math.IsNaN(s.Position.X + s.Position.Y + s.Position.Z + s.Speed) {
			t.Fatalf("NaN at tick %d: %+v", tick, s)
		}
		if s.Speed < 0 || s.Speed > 360 {
			t.Fatalf("speed out of range at tick %d: %f", tick, s.Speed)
		}
		if math.Abs(s.Position.X) > 125000 || math.Abs(s.Position.Z) > 125000 {
			t.Fatalf("outside the wrap at tick %d: %+v", tick, s.Position)
		}
		length := s.Direction.length()
		if length < 0.99 || length > 1.01 {
			t.Fatalf("direction not unit at tick %d: %f", tick, length)
		}
	}
}

// TestApproach expects speed to settle near the throttle target in level
// flight (the placeholder's defining behaviour).
func TestApproach(t *testing.T) {
	env := Environment{Wrap: 250000}
	s := spawn()
	in := Inputs{Throttle: 1}
	for tick := 0; tick < 3600; tick++ {
		s = Step(s, in, 1.0/60, env)
	}
	if math.Abs(s.Speed-360) > 5 {
		t.Fatalf("full throttle level speed %f, expected ~360", s.Speed)
	}
}

// TestWrap flies east across the seam and expects a clean wrap with the
// minimum-image difference staying small.
func TestWrap(t *testing.T) {
	env := Environment{Wrap: 250000}
	s := spawn()
	s.Position.X = 124900 // 100 m short of the seam
	in := Inputs{Throttle: 0.85}
	previous := s.Position.X
	for tick := 0; tick < 600; tick++ {
		s = Step(s, in, 1.0/60, env)
		step := Shortest(previous, s.Position.X, env.Wrap)
		if math.Abs(step) > 10 {
			t.Fatalf("seam jump at tick %d: %f", tick, step)
		}
		previous = s.Position.X
	}
	if s.Position.X > 0 {
		t.Fatalf("expected a wrapped (negative) x, got %f", s.Position.X)
	}
}

// TestShortest checks the minimum-image convention at the seam.
func TestShortest(t *testing.T) {
	if d := Shortest(124950, -124950, 250000); math.Abs(d-100) > 1e-9 {
		t.Fatalf("expected +100 across the seam, got %f", d)
	}
	if d := Shortest(-124950, 124950, 250000); math.Abs(d+100) > 1e-9 {
		t.Fatalf("expected -100 across the seam, got %f", d)
	}
	if d := Shortest(0, 300, 0); d != 300 {
		t.Fatalf("no-wrap difference wrong: %f", d)
	}
}

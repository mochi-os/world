// Mochi world: Atmosphere tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestAtmosphere checks the ISA tables at the standard reference altitudes.
func TestAtmosphere(t *testing.T) {
	cases := []struct {
		altitude, density, pressure, temperature, sound float64
	}{
		{0, 1.2250, 101325, 288.15, 340.29},
		{11000, 0.36391, 22632, 216.65, 295.07},
		{20000, 0.088035, 5474.9, 216.65, 295.07},
	}
	for _, c := range cases {
		a := air(c.altitude, Environment{})
		check := func(name string, got, want float64) {
			if math.Abs(got-want)/want > 0.001 {
				t.Fatalf("%s at %.0f m: got %f, want %f", name, c.altitude, got, want)
			}
		}
		check("density", a.Density, c.density)
		check("pressure", a.Pressure, c.pressure)
		check("temperature", a.Temperature, c.temperature)
		check("sound", a.Sound, c.sound)
	}
}

// TestAtmosphereOffsets: a hot day thins the air; low pressure thins it too.
func TestAtmosphereOffsets(t *testing.T) {
	standard := air(0, Environment{})
	hot := air(0, Environment{Temperature: 15})
	if hot.Density >= standard.Density {
		t.Fatalf("hot day should cut density: %f vs %f", hot.Density, standard.Density)
	}
	low := air(0, Environment{Pressure: 98000})
	if low.Density >= standard.Density {
		t.Fatalf("low pressure should cut density: %f vs %f", low.Density, standard.Density)
	}
	if math.Abs(hot.Sound-standard.Sound) < 1 {
		t.Fatal("hot day should raise the speed of sound")
	}
}

// TestShortest: the loop-free minimum-image must match the iterative
// definition across the normal range, and the hostile tiny wrap that once
// hung the session goroutine must return instantly.
func TestShortest(t *testing.T) {
	iterative := func(a, b, size float64) float64 {
		d := b - a
		half := size / 2
		for d > half {
			d -= size
		}
		for d < -half {
			d += size
		}
		return d
	}
	for _, size := range []float64{10000, 250000} {
		for d := -3.4 * size; d <= 3.4*size; d += size / 7.3 {
			got := Shortest(0, d, size)
			want := iterative(0, d, size)
			if math.Abs(got-want) > 1e-6*size {
				t.Fatalf("Shortest(0, %g, %g) = %g, iterative %g", d, size, got, want)
			}
		}
	}
	if d := Shortest(0, 2778, 1e-9); math.IsNaN(d) || math.IsInf(d, 0) {
		t.Fatalf("hostile wrap: %v", d) // and it returned at all — the old loop never did
	}
}

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

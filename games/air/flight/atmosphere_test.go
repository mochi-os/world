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

// TestShortest: the loop-free minimum-image matches the iterative definition
// across the normal range, and a tiny wrap returns instantly, not hanging.
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

// TestPitotReadsTheNose: the airspeed box is a pitot, not a speedometer —
// parked in wind it reads the ram component down the probe axis, swinging
// with the nose through a taxi circle (#44): into wind it shows the wind,
// downwind near zero, crosswind little.
func TestPitotReadsTheNose(t *testing.T) {
	m := New(Fighter, Environment{Wind: Vec3{X: -12}, Seed: 1}, World{Sea: 0})
	m.State.Position.Y = 20
	m.gust = wind(m.State.Position, 0, m.Environment, nil)
	face := func(x, z float64) float64 {
		m.State.Attitude = Look(Vec3{X: x, Z: z})
		return m.Cas()
	}
	into := face(1, 0)  // the air moves toward -x, so the apparent wind is FROM +x: nose +x faces into it
	away := face(-1, 0) // tail to it
	cross := face(0, 1) // beam-on
	if into < 10 {
		t.Fatalf("into wind the box must show the wind: %.1f m/s", into)
	}
	if away > 1 {
		t.Fatalf("downwind a pitot has no ram: %.1f m/s", away)
	}
	if cross > into/3 {
		t.Fatalf("beam-on reads little: %.1f vs %.1f into wind", cross, into)
	}
}

// TestPitotRecoveryCone: inside the probe's ~25 deg working cone the box
// reads the full airspeed — fight-regime AoA must not sag the indication —
// while far off-axis flow (the deck tailwind) still reads nothing.
func TestPitotRecoveryCone(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State.Position.Y = 2000
	at := func(alpha float64) float64 {
		m.State.Attitude = Look(Vec3{X: 1})
		a := alpha * math.Pi / 180
		m.State.Velocity = Vec3{X: 150 * math.Cos(a), Y: -150 * math.Sin(a)} // flow arrives alpha above the nose
		return m.Cas()
	}
	level := at(0)
	if fight := at(20); math.Abs(fight-level) > 0.5 {
		t.Fatalf("20 deg AoA sagged the box: %.1f vs %.1f m/s", fight, level)
	}
	if deep := at(45); deep >= at(20) {
		t.Fatalf("beyond the cone recovery must fall: %.1f at 45 deg", deep)
	}
}

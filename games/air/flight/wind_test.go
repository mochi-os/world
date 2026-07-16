// Mochi world: Wind tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

func breeze() Environment {
	return Environment{Seed: 7, Wind: Vec3{X: 6, Z: 3}, Turbulence: 1.5, Wrap: 250000}
}

// TestWindDeterminism: identical (seed, position, time) gives identical wind.
func TestWindDeterminism(t *testing.T) {
	env := breeze()
	p := Vec3{X: 1234, Y: 500, Z: -8000}
	a := wind(p, 42.5, env, nil)
	b := wind(p, 42.5, env, nil)
	if a != b {
		t.Fatalf("nondeterministic wind: %+v vs %+v", a, b)
	}
	other := env
	other.Seed = 8
	if wind(p, 42.5, other, nil) == a {
		t.Fatal("seed does not change the field")
	}
}

// TestWindShear: the mean strengthens with altitude and veers.
func TestWindShear(t *testing.T) {
	env := breeze()
	env.Turbulence = 0
	low := wind(Vec3{Y: 10}, 0, env, nil)
	high := wind(Vec3{Y: 3000}, 0, env, nil)
	if high.Length() <= low.Length() {
		t.Fatalf("no shear: %f at 10 m vs %f at 3 km", low.Length(), high.Length())
	}
	surface := math.Atan2(low.Z, low.X)
	aloft := math.Atan2(high.Z, high.X)
	if math.Abs(aloft-surface) < 0.05 {
		t.Fatal("no veer with altitude")
	}
}

// TestWindVariation: the mean differs across the map (mesoscale), and
// turbulence has roughly the commanded intensity.
func TestWindVariation(t *testing.T) {
	env := breeze()
	env.Turbulence = 0
	here := wind(Vec3{Y: 100}, 0, env, nil)
	there := wind(Vec3{X: 40000, Y: 100, Z: -30000}, 0, env, nil)
	if here.Subtract(there).Length() < 0.1 {
		t.Fatal("no mesoscale variation")
	}
	env.Turbulence = 2
	sum, samples := 0.0, 0
	calm := breeze()
	calm.Turbulence = 0
	for i := 0; i < 2000; i++ {
		p := Vec3{X: float64(i) * 37, Y: 1000, Z: float64(i) * 11}
		gust := wind(p, 0, env, nil).Subtract(wind(p, 0, calm, nil))
		sum += gust.Dot(gust)
		samples++
	}
	rms := math.Sqrt(sum / float64(samples))
	if rms < 0.5 || rms > 6 {
		t.Fatalf("turbulence intensity unreasonable: rms %f for sigma 2", rms)
	}
}

// TestBurble: astern of the carrier at deck height there is a sink; far
// away there is none.
func TestBurble(t *testing.T) {
	env := breeze()
	env.Turbulence = 0
	boat := &Carrier{Position: Vec3{Y: 19}, Heading: 0, Speed: 0}
	env.Wind = Vec3{X: -10} // wind down the deck axis
	groove := Vec3{X: -300, Y: 25}
	clean := Vec3{X: -3000, Y: 25}
	with := wind(groove, 0, env, boat)
	without := wind(clean, 0, env, boat)
	if with.Y >= without.Y {
		t.Fatalf("no burble sink: %f vs %f", with.Y, without.Y)
	}
}

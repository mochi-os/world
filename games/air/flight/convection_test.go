// Mochi world: Cloud convection tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

var trades = Cloud{Base: 600, Top: 2400, High: 5000, Convective: 1, Gate: Gate{Minimum: 0.22, Maximum: 0.50}}

// strongest scans the cell field for the most and least vigorous columns —
// the tests must not hardcode cell positions, only the field's guarantees.
func strongest(t *testing.T) (Vec3, Vec3, float64, float64) {
	t.Helper()
	var core, gap Vec3
	high, low := -1.0, 2.0
	for x := -24000.0; x <= 24000; x += 400 {
		for z := -24000.0; z <= 24000; z += 400 {
			v := vigour(x, z)
			if v > high {
				high, core = v, Vec3{X: x, Z: z}
			}
			if v < low {
				low, gap = v, Vec3{X: x, Z: z}
			}
		}
	}
	return core, gap, high, low
}

func TestVigourField(t *testing.T) {
	_, _, high, low := strongest(t)
	if high < 0.7 || low > 0.2 {
		t.Fatalf("cell field lacks contrast: strongest %.2f weakest %.2f", high, low)
	}
	if vigour(3120, -7810) != vigour(3120, -7810) {
		t.Fatal("vigour is not deterministic")
	}
}

func TestConvectionShape(t *testing.T) {
	core, gap, _, _ := strongest(t)

	// Inside a vigorous cell: bumpy, rising.
	core.Y = 1200
	chop, draft := convection(core, trades)
	if chop < 1 || draft < 1 {
		t.Fatalf("inside a strong cell: chop %.2f draft %.2f, want both well above calm", chop, draft)
	}

	// Beneath the cell: a thermal.
	core.Y = 450
	_, thermal := convection(core, trades)
	if thermal < 0.5 {
		t.Fatalf("under a strong cell: draft %.2f, want a rising thermal", thermal)
	}

	// The gap between cells: gentle sink, no chop.
	gap.Y = 450
	chop, sink := convection(gap, trades)
	if chop != 0 || sink >= 0 {
		t.Fatalf("between cells: chop %.2f draft %.2f, want calm sink", chop, sink)
	}

	// Above the tops: smooth.
	core.Y = 5200
	chop, draft = convection(core, trades)
	if chop != 0 || draft != 0 {
		t.Fatalf("above the tops: chop %.2f draft %.2f, want smooth air", chop, draft)
	}
}

func TestStratiformSmooth(t *testing.T) {
	core, _, _, _ := strongest(t)
	core.Y = 300
	deck := Cloud{Base: 152, Top: 460, High: 460, Convective: 0, Gate: Gate{}}
	if chop, draft := convection(core, deck); chop != 0 || draft != 0 {
		t.Fatalf("stratiform deck: chop %.2f draft %.2f, want smooth stable air", chop, draft)
	}
	if chop, draft := convection(core, Cloud{}); chop != 0 || draft != 0 {
		t.Fatalf("clear sky: chop %.2f draft %.2f, want nothing", chop, draft)
	}
}

func TestWindCloudIntegration(t *testing.T) {
	core, _, _, _ := strongest(t)
	env := Environment{Seed: 7, Wrap: 250000}
	calm := wind(Vec3{X: core.X, Y: 1200, Z: core.Z}, 30, env, nil)
	env.Cloud = trades
	stormy := wind(Vec3{X: core.X, Y: 1200, Z: core.Z}, 30, env, nil)
	if stormy.Y <= calm.Y {
		t.Fatalf("wind inside a cell: vertical %.2f vs calm %.2f, want an updraft", stormy.Y, calm.Y)
	}
	if math.Abs(stormy.X-calm.X) < 1e-9 && math.Abs(stormy.Z-calm.Z) < 1e-9 {
		t.Fatal("wind inside a cell shows no gust component")
	}
	// The zero-value cloud changes nothing: the golden traces depend on it.
	env.Cloud = Cloud{}
	again := wind(Vec3{X: core.X, Y: 1200, Z: core.Z}, 30, env, nil)
	if again != calm {
		t.Fatal("zero-value cloud must leave the wind field bit-identical")
	}
}

// Mochi world: Aerofoil tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

func section() Section {
	return Section{Slope: 5.9, Stall: 0.19, Drag: 0.007, Ratio: 4}
}

// TestPolar: linear region slope, a stall break, post-stall lift recovery
// falling to zero at 90°, and drag rising toward the flat-plate ceiling.
func TestPolar(t *testing.T) {
	table := Synthesize(section())
	cl5, _, _ := table.Sample(5 * math.Pi / 180)
	if math.Abs(cl5-5.9*5*math.Pi/180) > 0.02 {
		t.Fatalf("linear region off: Cl(5°) = %f", cl5)
	}
	peak, at := 0.0, 0.0
	for a := 0.0; a < math.Pi/2; a += 0.005 {
		cl, _, _ := table.Sample(a)
		if cl > peak {
			peak, at = cl, a
		}
	}
	if at < 0.15 || at > 0.35 {
		t.Fatalf("Clmax at unreasonable alpha: %f rad", at)
	}
	after, _, _ := table.Sample(at + 0.2)
	if after >= peak {
		t.Fatal("no stall break")
	}
	cl90, cd90, _ := table.Sample(math.Pi / 2)
	if math.Abs(cl90) > 0.15 {
		t.Fatalf("Cl at 90° should be ~0: %f", cl90)
	}
	if cd90 < 1.0 {
		t.Fatalf("Cd at 90° should be near flat plate: %f", cd90)
	}
	// Symmetric section: odd lift, even drag.
	clp, cdp, _ := table.Sample(0.1)
	cln, cdn, _ := table.Sample(-0.1)
	if math.Abs(clp+cln) > 1e-9 || math.Abs(cdp-cdn) > 1e-9 {
		t.Fatal("symmetric section is not symmetric")
	}
}

// TestMoment: pre-stall pitching moment about quarter chord ~0; post-stall
// nose-down (negative) as the centre of pressure drifts aft.
func TestMoment(t *testing.T) {
	table := Synthesize(section())
	_, _, before := table.Sample(0.1)
	if math.Abs(before) > 0.01 {
		t.Fatalf("pre-stall Cm should be ~0: %f", before)
	}
	_, _, deep := table.Sample(0.8)
	if deep >= 0 {
		t.Fatalf("deep-stall Cm should be nose-down: %f", deep)
	}
}

// TestEffectiveness: a bigger flap moves more of the section's lift.
func TestEffectiveness(t *testing.T) {
	quarter := Effectiveness(0.25)
	half := Effectiveness(0.5)
	if !(0 < quarter && quarter < half && half < 1) {
		t.Fatalf("flap effectiveness ordering wrong: %f %f", quarter, half)
	}
}

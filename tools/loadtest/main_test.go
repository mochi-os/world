// Mochi world: load-test statistics helpers
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import "testing"

// TestMostSeedsFromTheFirstElement — `most` seeded from 0.0 while its sibling
// `least` seeded from a[0], so it answered 0 for a slice whose every element is
// negative. Latent while the three call sites measure rates and jitter, all
// non-negative; live the moment anything signed is measured.
func TestMostSeedsFromTheFirstElement(t *testing.T) {
	for _, c := range []struct {
		name   string
		values []float64
		least  float64
		most   float64
	}{
		{"all negative", []float64{-3, -1, -7}, -7, -1},
		{"mixed", []float64{-3, 4, -7}, -7, 4},
		{"all positive", []float64{3, 1, 7}, 1, 7},
		{"single", []float64{-2}, -2, -2},
	} {
		if got := least(c.values); got != c.least {
			t.Errorf("%s: least(%v) = %v, want %v", c.name, c.values, got, c.least)
		}
		if got := most(c.values); got != c.most {
			t.Errorf("%s: most(%v) = %v, want %v", c.name, c.values, got, c.most)
		}
	}
}

// Mochi world: Tick budget
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"world/game"
)

// TestBudget is the gate on #256: the arbiter must not be able to stall the
// session goroutine, because that goroutine also drains input, sends snapshots
// and runs the liveness sweep — a multi-second tick means the sweep evicts
// everyone in the session as 15-seconds-silent. It is reachable from one
// unauthenticated POST /sessions with a bot roster and no players at all.
//
// Measured 2026-08-08, furball with 16 aces: mean 76.9 ms and a 6976 ms worst
// tick before the surrogate rollout and the per-tick allowance; the numbers
// below after. Teams (1.4 ms) was always fine, which is what proved it was the
// arbiter rather than the population.
func TestBudget(t *testing.T) {
	heavy(t)
	for _, roster := range []float64{16, 99} {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("budget%.0f", roster), Game: "air",
			Mode: "furball", Capacity: 128, Seed: 5,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": roster}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		var spent []float64
		for tick := uint64(0); tick < 900; tick++ {
			start := time.Now()
			i.Step(tick, nil)
			spent = append(spent, float64(time.Since(start).Microseconds())/1000)
		}
		sorted := append([]float64(nil), spent...)
		sort.Float64s(sorted)
		total, over := 0.0, 0
		for _, v := range spent {
			total += v
			if v > 16.6 {
				over++
			}
		}
		mean, worst := total/float64(len(spent)), sorted[len(sorted)-1]
		fmt.Printf("furball %2.0f aces | mean %6.2f ms | p99 %6.2f | worst %6.2f ms | over budget %4.1f%%\n",
			roster, mean, sorted[len(sorted)*99/100], worst, 100*float64(over)/float64(len(spent)))
		// The worst tick is the gate: a mean inside budget with a multi-second
		// tail is exactly the shape that evicted everyone.
		if worst > 100 {
			t.Errorf("%2.0f aces: worst tick %.0f ms — the session goroutine stalls and the liveness sweep evicts the match", roster, worst)
		}
		if mean > 16.6 {
			t.Errorf("%2.0f aces: mean tick %.1f ms exceeds the 16.6 ms budget — the session cannot hold 60 Hz", roster, mean)
		}
	}
}

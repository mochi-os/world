// Mochi world: Simulation determinism
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestDeterminism (#225): two instances built from the same session must play
// out the same fight, tick for tick. The leak class is Go's randomised map
// order in the b.known loops, where a strict-< min-pick breaks ties by visit.
func TestDeterminism(t *testing.T) {
	heavy(t)
	sessions := []game.Session{}
	for seed := 1; seed <= 3; seed++ {
		sessions = append(sessions,
			// The free-for-all: many contacts, so the b.known loops carry ties.
			game.Session{Identifier: fmt.Sprintf("det%d", seed), Game: "air", Mode: "furball",
				Capacity: 16, Seed: uint64(seed),
				Parameters: map[string]any{"bots": map[string]any{"superhuman": 2.0, "ace": 2.0, "pilot": 2.0}}},
			// The section geometry, exactly as the sweep that caught #225 runs
			// it: teams, missiles up (seekers, flares), a paired side against a
			// doubled one. This is the arm that actually diverged.
			game.Session{Identifier: fmt.Sprintf("sec%d", seed), Game: "air", Mode: "teams",
				Capacity: 16, Seed: uint64(seed),
				Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{
					"red": map[string]any{"ace": 2.0}, "blue": map[string]any{"ace": 4.0}}}})
	}
	for _, session := range sessions {
		g1, g2 := New(), New()
		one, _ := g1.Create(session)
		two, _ := g2.Create(session)
		a, b := one.(*instance), two.(*instance)
		words1 := make([]float64, flight.Size)
		words2 := make([]float64, flight.Size)
		for tick := uint64(0); tick < 60*300; tick++ {
			a.Step(tick, nil)
			b.Step(tick, nil)
			for _, slot := range a.slots() {
				c1, c2 := a.aircraft[slot], b.aircraft[slot]
				if (c1 == nil) != (c2 == nil) || (c1 != nil && c1.alive != c2.alive) {
					t.Fatalf("%s: roster diverged at t=%.1fs slot %d", session.Identifier, float64(tick)/60, slot)
				}
				if c1 == nil || c1.model == nil || c2.model == nil {
					continue
				}
				c1.model.State.Encode(words1)
				c2.model.State.Encode(words2)
				for w := range words1 {
					if words1[w] != words2[w] {
						t.Fatalf("%s: state diverged at t=%.1fs slot %d word %d: %v vs %v",
							session.Identifier, float64(tick)/60, slot, w, words1[w], words2[w])
					}
				}
			}
		}
	}
}

// TestSpacedRespawnIsDeterministic (#133) — the spaced respawn point is the
// mean position of the living aircraft, and it was accumulated by ranging the
// aircraft MAP. Go randomises map order per range and floating-point addition
// is not associative, so the spawn moved bit-for-bit between identical runs.
//
// The branch is narrow and worth naming: bvr() computes teams and joust starts
// closed-form and never touches the map, so only a SPACED, unteamed furball
// reaches the sum. That is why TestBvrWide, a joust, was byte-identical across
// four 48-seed runs while this was broken.
//
// Sizing is measured, not guessed. The defect is intermittent in a thin fight
// (7 of 8 respawns agreed at 6 bots and 120 ticks, which would make a
// run-it-twice test flaky-green and worse than no gate); at 20 bots and 400
// ticks the positions have messy enough mantissas that ZERO of 8 agreed. So
// this asks for eight respawns and requires all eight to match.
func TestSpacedRespawnIsDeterministic(t *testing.T) {
	respawn := func() flight.Vec3 {
		bots_live.Store(0)
		made, err := (&Air{}).Create(game.Session{Identifier: "spacedrespawn", Game: "air",
			Mode: "furball", Capacity: 8, Seed: 3,
			Parameters: map[string]any{"spaced": true, "bots": map[string]any{"pilot": 20.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		defer i.Close()
		for tick := uint64(0); tick < 400; tick++ {
			i.Step(tick, nil)
		}
		slot := i.slots()[0]
		a := i.aircraft[slot]
		if a == nil || a.model == nil {
			t.Fatal("no craft to respawn")
		}
		i.spawn(slot, a.model, a.team, a.tank)
		return a.model.State.Position
	}

	first := respawn()
	for n := 1; n < 8; n++ {
		if got := respawn(); got != first {
			t.Fatalf("respawn %d landed at %v, run 1 landed at %v: the spaced spawn point is not reproducible, "+
				"so neither is any fight that follows it", n+1, got, first)
		}
	}
}

// Mochi world: Damage-soak calibration
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"testing"

	"world/game"
)

// TestSoak measures what a kill COSTS: a scripted attacker with the crude
// pursuit tracks a gently-weaving drone from its six with the trigger held,
// through the real rounds, the real pierce chains and the real damage cascade.
// It exists because the pilot spent 511 of 578 rounds killing one ace and
// reported the bandit surviving "a huge number of hits" — dead astern is the
// damage model's most absorbent aspect (the chains spend themselves in the
// engines), and this is the instrument that says by how much. Diagnostic.
func TestSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("simulated minutes")
	}
	for seed := uint64(1); seed <= 6; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "soak", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"drone": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
			t.Fatal(err)
		}
		drone := -1
		for slot, a := range i.aircraft {
			if a != nil && slot != 0 {
				drone = slot
			}
		}
		place(i, 0, drone, 400) // parked 400 m ahead: the tracking shot
		me, foe := &i.aircraft[0].model.State, &i.aircraft[drone].model.State
		hits, killedAt := 0, -1.0
		for tick := uint64(0); tick < 60*120; tick++ {
			data := pursue(me, foe)
			data["fire"] = true // pursue sets "guns", but the wire key is "fire"
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			for _, e := range i.events {
				if e["kind"] == "hit" && e["by"] == 0 {
					hits += e["count"].(int)
				}
			}
			i.events = i.events[:0]
			if !i.aircraft[drone].alive {
				killedAt = float64(tick) / 60
				break
			}
		}
		spent := rounds - i.aircraft[0].ammunition
		engines := i.aircraft[drone].model.State.Damage.Engine
		fmt.Printf("seed %d: rounds spent %3d   hits landed %3d   killed at %6.1fs   engines dead L%.2f R%.2f\n",
			seed, spent, hits, killedAt, engines[0], engines[1])
	}
}

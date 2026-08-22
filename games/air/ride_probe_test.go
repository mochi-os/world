// Mochi world: the limiter ride under human-style pressure (#63)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
)

// TestRideEmployment (#63): the scripted chaser holds the ace's rear quarter —
// the angle stalemate a human wins with alpha the g-commanded catalogue never
// uses. Gates: the ride is actually CHOSEN when licensed, and it never strands
// the jet below fighting speed beyond the bailout's own tolerance. The
// counter-offensive share (bot in the ATTACKER's rear quarter) is recorded as
// the improvement instrument, not gated — it moves with seed luck.
func TestRideEmployment(t *testing.T) {
	heavy(t)
	modes := map[string]int{}
	total, converted, slow, downed, lost := 0, 0, 0, 0, 0
	for seed := uint64(1); seed <= 24; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "ride", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
			t.Fatal(err)
		}
		bot := -1
		for slot, a := range i.aircraft {
			if a != nil && a.brain != nil {
				bot = slot
			}
		}
		if bot < 0 {
			t.Fatal("no bot in the session")
		}
		place(i, bot, 0, -1200)
		me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
		hunter := &chaser{}
		pace := corner(i.aircraft[bot].model)
		for tick := uint64(0); tick < 120*60; tick++ {
			data := hunter.fly(me, foe, tick)
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			total++
			modes[i.aircraft[bot].brain.mode]++
			if foe.Velocity.Length() < 0.55*pace {
				slow++
			}
			toward := foe.Position.Subtract(me.Position)
			if r := toward.Length(); r > 1 && r < 900 {
				tail := me.Velocity.Normalize().Scale(-1)
				if math.Acos(clamp(toward.Scale(1/r).Dot(tail), -1, 1))*57.3 < 45 {
					converted++
				}
			}
		}
		if i.aircraft[bot].kills > 0 {
			downed++
		}
		if !i.aircraft[bot].alive || i.aircraft[bot].model == nil {
			lost++
		}
		i.Close()
	}
	share := func(n int) float64 {
		if total == 0 {
			return 0
		}
		return 100 * float64(n) / float64(total)
	}
	t.Logf("ride %.1f%% of %d ticks | counter-offensive %.1f%% | slow %.1f%% | attacker downed %d, bandit lost %d of 24",
		share(modes["ride"]), total, share(converted), share(slow), downed, lost)
	if modes["ride"] == 0 {
		t.Errorf("the ride was never chosen across 24 pressured seeds: the play is inert")
	}
	if share(slow) > 20 {
		t.Errorf("slow %.1f%% of the fight: the ride strands the jet below fighting speed", share(slow))
	}
}

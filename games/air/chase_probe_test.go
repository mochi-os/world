// Mochi world: the far-field stern chase (#69)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/game"
	"world/games/air/flight"
)

// flee flies the runner: wings level, hold the spawn altitude, high military
// power - the ATC-held duck of the kill harness, server-side. No defence, no
// weapons: the probe asks only whether the hunter closes.
func flee(me *flight.State, altitude float64) map[string]any {
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	roll := clamp(-right.Y*3, -1, 1)
	climb := (altitude-me.Position.Y)*0.004 - me.Velocity.Y*0.05
	pitch := clamp(climb+0.1*(1-up.Y), -0.5, 0.5)
	return map[string]any{"pitch": pitch, "roll": roll, "throttle": 0.85}
}

// TestChaseArbitration (#69): both execution paths of the stern chase are
// measured healthy (press commands and achieves full reheat), and the fights
// were lost in ARBITRATION - beyond the rehearsal's discrimination the parking
// laws (saddle's speed-match, lag's corner-pace reheat) won coin flips and
// MIL-parked a matched-speed chase. From 5 km dead astern of a straight
// runner, the far field must never fly a parking law, and the chase must
// actually arrive.
func TestChaseArbitration(t *testing.T) {
	heavy(t)
	far, park, arrived := 0, 0, 0
	closest := make([]float64, 0, 12)
	for seed := uint64(1); seed <= 12; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "chase", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
		place(i, bot, 0, 5000) // the runner dead ahead of the hunter, co-speed
		me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
		altitude := me.Position.Y
		least := 5000.0
		for tick := uint64(0); tick < 150*60; tick++ {
			data := flee(me, altitude)
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				least = 0 // the chase ended in a kill: it arrived
				break
			}
			distance := foe.Position.Subtract(me.Position).Length()
			if distance < least {
				least = distance
			}
			if distance > 2100 { // past the gate, with margin for the commitment tail
				far++
				if mode := i.aircraft[bot].brain.mode; mode == "saddle" || mode == "lag" {
					park++
				}
			}
		}
		if least < 2500 {
			arrived++
		}
		closest = append(closest, least)
		i.Close()
	}
	t.Logf("far-field ticks %d, parked %d | arrived (inside 2,500 m) %d of 12 | closest per seed %.0f", far, park, arrived, closest)
	if far > 0 && float64(park)/float64(far) > 0.05 { // phases are the defect (32% of the far field pre-fix); brief referendum-commitment tails while the geometry crosses the gate's own thresholds are not (~1%)
		t.Errorf("the far field flew a parking law for %d of %d ticks: the chase is being handed back to saddle/lag", park, far)
	}
	if arrived < 9 {
		t.Errorf("the chase arrived in only %d of 12 seeds from 5 km astern: the pursuit is still bleeding", arrived)
	}
}

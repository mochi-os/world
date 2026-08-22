// Mochi world: the conceded-height probe (#64)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// hover flies the scripted perch: pursue() chasing a ghost that orbits a
// FIXED centre at a FIXED ceiling — the perch owns its sky but the sky is
// reachable, so a bot that climbs to co-altitude or leaves the circle has
// honestly refused to linger. (The first version anchored the ghost 1,500 m
// above the BOT, which re-based the perch every tick and made the metric
// unwinnable by construction.) The high fighter that converts at a moment of
// its choosing is stood in for by one that never converts at all — every
// second beneath it is the bot's own choice.
func hover(me *flight.State, centre flight.Vec3, ceiling float64, tick uint64) map[string]any {
	angle := float64(tick) / 60 * 0.15
	ghost := flight.State{}
	ghost.Position = flight.Vec3{X: centre.X + 900*math.Cos(angle), Y: ceiling, Z: centre.Z + 900*math.Sin(angle)}
	ghost.Velocity = flight.Vec3{X: -135 * math.Sin(angle), Z: 135 * math.Cos(angle)}
	data := pursue(me, &ghost)
	data["fire"] = false
	return data
}

// TestPerchDiscipline (#64): a scripted opponent holds a fast circle 1,500 m
// above. Bot-v-bot ladders cannot see conceded height (symmetric lowness
// cancels), so this probe is the gate: the bot must not LINGER beneath the
// perch — below it, inside conversion range, and too slow to climb out.
func TestPerchDiscipline(t *testing.T) {
	heavy(t)
	beneath, total := 0, 0
	closest := 1e9
	modes := map[string]int{}
	speedSum, gapSum := 0.0, 0.0
	for seed := uint64(1); seed <= 24; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "perch", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
		place(i, bot, 0, -1800)
		me := &i.aircraft[0].model.State
		me.Position.Y = i.aircraft[bot].model.State.Position.Y + 1500
		foe := &i.aircraft[bot].model.State
		centre := foe.Position
		ceiling := me.Position.Y
		for tick := uint64(0); tick < 120*60; tick++ {
			data := hover(me, centre, ceiling, tick)
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			total++
			modes[i.aircraft[bot].brain.mode]++
			speedSum += foe.Velocity.Length()
			gapSum += me.Position.Y - foe.Position.Y
			apart := me.Position.Subtract(foe.Position).Length()
			if apart < closest {
				closest = apart
			}
			if me.Position.Y-foe.Position.Y > 300 && apart < 2500 {
				beneath++
			}
		}
		i.Close()
	}
	share := 100 * float64(beneath) / math.Max(float64(total), 1)
	t.Logf("lingered beneath the perch %.1f%% of %d ticks | closest %0.f m | mean speed %.0f m/s | mean gap %.0f m | modes %v",
		share, total, closest, speedSum/math.Max(float64(total), 1), gapSum/math.Max(float64(total), 1), modes)
	// RATCHET, not target: against a FIXED perch the ordinary doctrine reaches
	// a mean gap of ~300 m and lingers 42% (#64) — the deep entrapment of the
	// player fights needs a perch-and-POUNCE opponent this probe does not yet
	// fly, and both fix attempts (a scorer term, a regain bailout) measured
	// inert here. The bound holds the ground; the real gate arrives with the
	// pouncing opponent.
	if share > 45 {
		t.Errorf("the bot lingered beneath the perch %.1f%% of the fight: conceded height got cheaper again", share)
	}
}

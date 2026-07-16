// Mochi world: Air gun-lethality probe (#144)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The hits-to-kill instrument: a pinned perfect track at fixed aspects,
// reporting kill rate, time, hit counts, and the damage-event mix. This is
// what calibrated the 20 mm penetration/detonation constants — re-run it
// whenever those numbers move. Env-gated out of the normal suite:
//
//	AIR_LETHALITY=1 go test ./games/air -run TestLethalityAspects -v
package air

import (
	"os"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestLethalityAspects: hits-to-kill and the damage-event mix from a pinned
// perfect track at three aspects, across seeds.
func TestLethalityAspects(t *testing.T) {
	if os.Getenv("AIR_LETHALITY") == "" {
		t.Skip("set AIR_LETHALITY=1 — a calibration probe, not a CI test")
	}
	aspects := []struct {
		name string
		post func(prey *flight.State) (flight.Vec3, flight.Vec3) // shooter position, velocity
	}{
		{"six", func(p *flight.State) (flight.Vec3, flight.Vec3) {
			return p.Position.Add(p.Attitude.Rotate(flight.Vec3{X: -400})), p.Velocity
		}},
		{"high", func(p *flight.State) (flight.Vec3, flight.Vec3) {
			return p.Position.Add(p.Attitude.Rotate(flight.Vec3{X: -350, Y: 200})), p.Velocity
		}},
		{"beam", func(p *flight.State) (flight.Vec3, flight.Vec3) {
			return p.Position.Add(p.Attitude.Rotate(flight.Vec3{Z: -400})), p.Velocity
		}},
	}
	for _, aspect := range aspects {
		kills, ticks, hits, rounds_spent := 0, 0.0, 0, 0
		mix := map[string]int{}
		for seed := uint64(1); seed <= 6; seed++ {
			g := New()
			made, _ := g.Create(game.Session{Identifier: "lethal", Game: "air", Mode: "furball", Capacity: 100, Seed: seed,
				Parameters: map[string]any{"bots": map[string]any{"drone": 1.0, "ace": 1.0}}})
			i := made.(*instance)
			hunter, prey := i.aircraft[98], i.aircraft[99]
			for tick := uint64(0); tick < 60*300; tick++ {
				position, velocity := aspect.post(&prey.model.State)
				hunter.model.State.Position = position
				hunter.model.State.Velocity = velocity
				// Aim at the ballistic lead, not the man: rounds fly ~0.4 s.
				gap, span := i.bearing(position, prey.model.State.Position)
				_ = gap
				time := span / 1050
				lead := prey.model.State.Position.Add(prey.model.State.Velocity.Subtract(velocity).Scale(time))
				look, _ := i.bearing(position, lead)
				hunter.model.State.Attitude = flight.Look(look)
				i.Step(tick, nil)
				for _, e := range i.events {
					kind, _ := e["kind"].(string)
					switch kind {
					case "hit":
						count, _ := e["count"].(int)
						hits += count
					case "fire", "jam", "pilot", "explode", "shed", "gear":
						mix[kind]++
					}
				}
				i.events = nil
				if prey.deaths > 0 {
					kills++
					ticks += float64(tick) / 60
					break
				}
			}
			rounds_spent += rounds - hunter.ammunition
		}
		mean := 300.0
		if kills > 0 {
			mean = ticks / float64(kills)
		}
		t.Logf("%s: %d/6 kills, mean %.0fs, %d hits %d rounds, events %v", aspect.name, kills, mean, hits, rounds_spent, mix)
	}
}

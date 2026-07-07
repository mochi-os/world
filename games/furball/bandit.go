// Mochi world: Single-opponent bandit harness
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The SP joust opponent: the SAME brain the server flies for multiplayer
// bots, wrapped around a private two-craft arena — the player's mirrored
// state at slot 0, the bandit at slot 1. The client owns the real player
// model, weapons, and damage; this harness owns only the bandit's flying and
// its trigger/flare decisions. Compiled into the browser wasm boundary, so
// the joust ace IS the multiplayer ace, bit for bit.

package furball

import (
	"world/game"
	"world/games/furball/aircraft"
	"world/games/furball/flight"
)

type Bandit struct {
	arena *instance
	craft *craft
	tick  uint64
}

// NewBandit builds the harness. Unknown levels fly as veteran. The bandit
// carries no missiles (the client's joust is a guns fight today); the
// visibility model still applies — sky and night must match the mission.
func NewBandit(level string, seed uint64, wrap float64, sky string, night bool) *Bandit {
	environment := flight.Environment{Seed: seed, Wrap: wrap}
	mirror := &craft{player: game.Player{Name: "player", Slot: 0}, kind: "fa18c",
		model: flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea}), alive: true, flared: 1e9}
	mirror.arm()
	thought := mind(level)
	if thought == nil {
		thought = mind("veteran")
	}
	fighter := &craft{player: game.Player{Name: "bandit", Slot: 1}, kind: "fa18c",
		model: flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea}), alive: true, flared: 1e9,
		bot: true, brain: thought}
	fighter.arm()
	return &Bandit{
		arena: &instance{mode: "furball", environment: environment, sky: sky, night: night,
			aircraft: map[int]*craft{0: mirror, 1: fighter}},
		craft: fighter,
	}
}

// Place resets the bandit to a fresh state (spawn or respawn) and clears the
// brain's per-life memory.
func (b *Bandit) Place(words []float64) {
	b.craft.model.State = flight.Decode(words)
	b.craft.brain.reborn()
	b.craft.alive = true
}

// Spawn places the bandit fresh: nose on the velocity, engines spooled,
// clean airframe — the client's joust merge entry and every respawn.
func (b *Bandit) Spawn(position, velocity flight.Vec3) {
	s := &b.craft.model.State
	*s = flight.State{Position: position, Velocity: velocity, Attitude: flight.Look(velocity.Normalize()), Fuel: fuel}
	s.Engine[0] = flight.EngineState{Spool: 0.9}
	s.Engine[1] = flight.EngineState{Spool: 0.9}
	b.craft.arm()
	b.craft.flared = 1e9
	b.craft.brain.reborn()
	b.craft.alive = true
}

// Mirror updates the player's reflection: encoded state words, whether the
// player is firing (tracer perception), and whether the player still flies.
func (b *Bandit) Mirror(words []float64, firing bool, alive bool) {
	reflection := b.arena.aircraft[0]
	reflection.model.State = flight.Decode(words)
	reflection.latest.Fire = firing
	reflection.alive = alive
}

// Menace declares the player's missiles currently chasing the bandit, six
// words each (position, velocity) — the brain's evade logic reads them.
func (b *Bandit) Menace(words []float64) {
	b.arena.flying = b.arena.flying[:0]
	for at := 0; at+6 <= len(words); at += 6 {
		b.arena.flying = append(b.arena.flying, &missile{shooter: 0, target: 1, life: missile_life,
			position: flight.Vec3{X: words[at], Y: words[at+1], Z: words[at+2]},
			velocity: flight.Vec3{X: words[at+3], Y: words[at+4], Z: words[at+5]}})
	}
}

// Step advances the bandit one 60 Hz frame: think, fly four substeps, and
// report the trigger and any flare drop.
func (b *Bandit) Step() (fire bool, flare bool) {
	b.tick++
	b.arena.think(1, b.craft, b.tick)
	for _, event := range b.arena.events {
		if event["kind"] == "flare" {
			flare = true
		}
	}
	b.arena.events = b.arena.events[:0]
	for substep := 0; substep < 4; substep++ {
		b.craft.model.Step(b.craft.latest)
	}
	b.craft.flared += 1.0 / 60
	return b.craft.latest.Fire, flare
}

// State exposes the bandit's flight state for the client to render.
func (b *Bandit) State() *flight.State { return &b.craft.model.State }

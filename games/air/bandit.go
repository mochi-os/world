// Mochi world: Single-opponent bandit harness
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The SP joust opponent: the SAME brain the server flies for multiplayer
// bots, wrapped around a private two-craft arena — the player's mirrored
// state at slot 0, the bandit at slot 1. The client owns the real player
// model, weapons, and damage; this harness owns only the bandit's flying and
// its trigger/flare decisions. Compiled into the browser wasm boundary, so
// the joust ace IS the multiplayer ace, bit for bit.

package air

import (
	"world/game"
	"world/games/air/aircraft"
	"world/games/air/battle"
	"world/games/air/flight"
	"world/games/air/round"
)

type Bandit struct {
	arena *instance
	craft *craft
	tick  uint64
}

// NewBandit builds the harness. Unknown levels fly as ace. The bandit
// fires no missiles of its own (the client's joust is a guns fight today), but
// `missiles` says whether the PLAYER can, which is what the bandit's defensive
// doctrine reacts to: pre-emptive flaring is insurance against a shot it cannot
// see coming, and buys nothing in a guns fight (#211). The visibility model
// still applies — sky and night must match the mission.
// The bots' world is SEA LEVEL ONLY, deliberately (2026-07-29). flight.World
// carries Fields (island height, coast outline, paved strips) and a Carrier,
// and none of it is populated here: every bot flies against a flat sea.
//
// That is accurate on the maps we ship. Midway's island tops out at 3.5 m, the
// runway at ~5 m and the tallest mast at ~12 m, while the brain's own recovery
// floor works in 400-900 m margins - so modelling the relief would move nothing
// it can measure. What the flat model costs is avoidance of VERTICAL clutter
// (masts, the carrier superstructure), and that is covered on the consequence
// side instead: the client tests the bandit against the same terrain, buildings,
// masts and deck the player is tested against, so a bot that hits one dies.
//
// Revisit when a map with real relief lands. The tell is a bot holding a level
// break straight into rising ground - the floor will read its height above the
// SEA and be perfectly happy.
// weapons is the match's weapons class, exactly as a server session carries
// it (settled 2026-08-13: bots arm to the class, same equipment as humans).
// "open" arms the bandit with the BVR fit and wakes hunt.go — radar,
// AMRAAM employment, crank, and the radar-round defence — in single player,
// the same brain path the server flies. "" preserves the older callers:
// fox2 when the player can fire missiles, guns otherwise.
func NewBandit(level string, seed uint64, wrap float64, sky string, night bool, missiles bool, weapons string) *Bandit {
	if weapons == "" {
		if missiles {
			weapons = "fox2"
		} else {
			weapons = "guns"
		}
	}
	environment := flight.Environment{Seed: seed, Wrap: wrap}
	mirror := &craft{player: game.Player{Name: "player", Slot: 0}, kind: "fa18c",
		model: flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea}), alive: true, flared: 1e9}
	mirror.arm()
	thought := mind(level)
	if thought == nil {
		thought = mind("ace")
	}
	fighter := &craft{player: game.Player{Name: "bandit", Slot: 1}, kind: "fa18c",
		model: flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea}), alive: true, flared: 1e9,
		bot: true, brain: thought, lock: -1, loadout: bots_loadout(weapons)}
	fighter.arm()
	return &Bandit{
		arena: &instance{mode: "furball", environment: environment, sky: sky, night: night,
			missiles: missiles || weapons == "open", weapons: weapons,
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

// Spawn places the bandit fresh: trimmed on the velocity, engines spooled,
// clean airframe — the client's joust merge entry and every respawn.
func (b *Bandit) Spawn(position, velocity flight.Vec3) {
	// Level, not a hand-rolled literal. The literal flew nose-on-velocity —
	// zero alpha — so every bandit spawned a few degrees off trim and rode the
	// barely-damped phugoid: the same porpoise the Level sign fix removed from
	// server spawns, and the same accidental gun-target armour, aimed at the
	// single player. The literal's zero-value gear field was also live-looking
	// (catapult 0, stroke 0, wire 0), inert only because this arena carries no
	// carrier. The merge-entry power stays deliberately high: excess thrust on
	// a trimmed attitude just accelerates, it does not porpoise.
	s := &b.craft.model.State
	*s = flight.Level(b.craft.model, position, velocity, velocity.Length(), fuel)
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

// Menace declares every missile in the air, eight words each: position,
// velocity, shooter (0 the player, 1 the bandit), and phase (-1 a live
// heater, -2 a heater already BEATEN, otherwise the radar round's guidance
// phase). The client is the source of truth for rounds in single player — it
// flies them — and the stubs built here are exactly what the brain reads
// everywhere else: the evade logic wants inbound positions, the defence wants
// phase >= Active radar rounds targeting the bandit, and the shoot-look-shoot
// discipline wants the bandit's own rounds' phases. Rebuilt every frame, so
// nothing accumulates.
//
// The -2 sentinel exists because the stubs were built with `loose` and `blind`
// left at their zero values, and `beaten` (bot.go) reads exactly those two —
// so the instructor tiers' refusal to abandon a fight for a round that has
// already lost was dead in single player, the one place a human sees it. The
// client tracks both flags itself, identically to the server. A client that
// never sends -2 degrades to the old behaviour rather than misbehaving.
func (b *Bandit) Menace(words []float64) {
	b.arena.flying = b.arena.flying[:0]
	for at := 0; at+8 <= len(words); at += 8 {
		shooter := 0
		if words[at+6] > 0.5 {
			shooter = 1
		}
		m := &missile{shooter: shooter, target: 1 - shooter, life: missile_life,
			position: flight.Vec3{X: words[at], Y: words[at+1], Z: words[at+2]},
			velocity: flight.Vec3{X: words[at+3], Y: words[at+4], Z: words[at+5]}}
		if phase := int(words[at+7]); phase >= 0 {
			m.radar = &round.Model{Phase: phase, Position: m.position, Velocity: m.velocity}
		} else if phase <= -2 {
			m.loose = true // seduced onto a flare, or gimballed off and gone ballistic
		}
		b.arena.flying = append(b.arena.flying, m)
	}
}

// Step advances the bandit one 60 Hz frame: think, fly four substeps, and
// report the trigger and any flare drop.
// Step advances the brain one frame and reports what left the aircraft:
// the trigger, a countermeasure dispense, an AMRAAM, and a HEATER. The
// heater was missing until 2026-08-15 — the brain fired 9Ms into its own
// arena, where the only target was a mirror of the player that the client
// never sees, so a single-player bandit could not threaten the pilot with a
// heat-seeker at all. Every launch the brain makes now crosses the boundary
// and the client flies the round.
func (b *Bandit) Step() (fire bool, flare bool, launch bool, heater bool) {
	b.tick++
	// The single-player bandit drives think() directly rather than through
	// instance.Step, so it owns the per-tick arbiter allowance reset (#256)
	// too. Without it the counter only ever climbed: the bandit re-planned
	// twice, deferred for the rest of the mission, and flew straight —
	// TestBandit's pursuit invariant caught it before it shipped.
	b.arena.rehearsals = 0
	b.arena.think(1, b.craft, b.tick)
	for _, event := range b.arena.events {
		if event["kind"] == "flare" {
			flare = true
		}
		if event["kind"] == "fox3" {
			launch = true
		}
		if event["kind"] == "missile" {
			heater = true
		}
	}
	b.arena.events = b.arena.events[:0]
	for substep := 0; substep < 4; substep++ {
		b.craft.model.Step(b.craft.latest)
	}
	b.craft.flared += 1.0 / 60
	b.craft.clouded += 1.0 / 60
	b.craft.release += 1.0 / 60
	return b.craft.latest.Fire, flare, launch, heater
}

// State exposes the bandit's flight state for the client to render.
func (b *Bandit) State() *flight.State { return &b.craft.model.State }

// Instruments is the bandit's own instrument tail — the same five words the
// player's flight frame carries after its state (alpha, beta, load factor,
// Mach, calibrated airspeed) — so the client can record the bandit's
// telemetry from the indices it already uses for the ownship (#33 debrief).
func (b *Bandit) Instruments() (alpha, beta, nz, mach, cas float64) {
	m := b.craft.model
	return m.Alpha(), m.Beta(), m.Nz(), m.Mach(), m.Cas()
}

// Mode is the doctrine state the brain last chose (#212 flight recorder, and
// the #206 human-fight diagnostic). Empty for a drone with no brain.
func (b *Bandit) Mode() string {
	if b.craft == nil || b.craft.brain == nil {
		return ""
	}
	return b.craft.brain.mode
}

// Wound mirrors the client's damage authority into the harness: the wasm
// battle hulk owns the bandit's damage and fires, and copying them here is
// what lets the brain fly wounded (#130) — and the flight model fly the
// honest degraded dynamics — in single-player, exactly as they do on the
// server where damage lives in the real model.
func (b *Bandit) Wound(damage flight.DamageState, condition battle.Condition) {
	b.craft.model.State.Damage = damage
	b.craft.condition = condition
}

// Throttle reports the brain's current commanded throttle: the client's fire
// cascade feeds engine fires on it, so the fire drill can actually starve
// them (a hardcoded cascade throttle would burn a drilling bandit forever).
func (b *Bandit) Throttle() float64 { return b.craft.latest.Throttle }

// Emitter reports the bandit's radar state for the player's RWR: the same
// craft field a server pose relays, driven by the same hunt.go.
func (b *Bandit) Emitter() int { return b.craft.emitter }

// Locked reports whether the bandit's STT holds the player — the datalink
// truth for a bandit-shot round the client is flying.
func (b *Bandit) Locked() bool { return b.craft.emitter == 2 && b.craft.lock == 0 }

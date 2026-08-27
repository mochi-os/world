// Mochi world: Single-opponent bandit harness
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The SP joust opponent: the SAME brain the server flies, wrapped around a
// private two-craft arena (mirrored player at slot 0, bandit at slot 1). The
// client owns the player model, weapons, and damage; this owns the bandit.

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
	tank  float64 // spawn fuel, kg: the player's own load, so the single-player joust is fought on equal tanks
}

// NewBandit builds the harness. Unknown levels fly as ace. `missiles` says
// whether the PLAYER can shoot, which the bandit's defensive doctrine reacts to
// (#211); `weapons` is the match's class, and "open" wakes the BVR brain.
func NewBandit(level string, seed uint64, wrap float64, sky string, night bool, missiles bool, weapons string, tank float64) *Bandit {
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
	if tank <= 0 {
		tank = fuel // the server's default load; the client passes the player's own so the joust is fought on equal tanks
	}
	return &Bandit{
		arena: &instance{mode: "furball", environment: environment, sky: sky, night: night,
			missiles: missiles || weapons == "open", weapons: weapons,
			aircraft: map[int]*craft{0: mirror, 1: fighter}},
		craft: fighter,
		tank:  tank,
	}
}

// Place resets the bandit to a fresh state (spawn or respawn) and clears the
// brain's per-life memory.
func (b *Bandit) place(words []float64) {
	b.craft.model.State = flight.Decode(words)
	b.craft.brain.reborn()
	b.craft.alive = true
}

// Spawn places the bandit fresh: trimmed on the velocity, engines spooled,
// clean airframe — the client's joust merge entry and every respawn.
func (b *Bandit) Spawn(position, velocity flight.Vec3) {
	// Level, not a hand-rolled literal: a nose-on-velocity spawn carries zero
	// alpha, sits off trim, and rides the barely-damped phugoid - accidental
	// gun-target armour.
	s := &b.craft.model.State
	*s = flight.Level(b.craft.model, position, velocity, velocity.Length(), b.tank)
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
// velocity, shooter (0 player, 1 bandit), phase (-1 live heater, -2 already
// beaten, else the radar round's guidance phase). Rebuilt every frame.
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

// Coast flies a dead bandit for one frame: no thinking, stick free so the FCS
// holds attitude, throttle as the pilot left it, and a small standing roll so
// the jet spirals instead of gliding flat. The server's wrecks fly the same.
func (b *Bandit) Coast(lean float64) {
	b.tick++
	held := flight.Inputs{Throttle: b.craft.latest.Throttle, Reheat: b.craft.latest.Reheat, Lean: lean}
	b.craft.latest = held
	for substep := 0; substep < 4; substep++ {
		b.craft.model.Step(held)
	}
}

// Step advances the bandit one 60 Hz frame and reports what left the aircraft:
// the trigger, a flare, an AMRAAM, a heater, and a chaff bloom. The client
// flies every round the brain launches.
func (b *Bandit) Step() (fire bool, flare bool, launch bool, heater bool, chaff bool) {
	b.tick++
	// The single-player bandit drives think() directly rather than through
	// instance.Step, so it owns the per-tick arbiter allowance reset (#256).
	b.arena.rehearsals = 0
	b.arena.think(1, b.craft, b.tick)
	for _, event := range b.arena.events {
		if event["kind"] == "flare" {
			flare = true
		}
		if event["kind"] == "chaff" {
			chaff = true // its own magazine and its own bloom (#43): the client stamps the chaff window without a flare
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
	return b.craft.latest.Fire, flare, launch, heater, chaff
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

// Wound mirrors the client's damage authority into the harness: the wasm battle
// hulk owns the bandit's damage, and copying it here is what lets the brain fly
// wounded (#130) on the honest degraded dynamics.
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

// Mochi world: Browser bandit boundary
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build js && wasm

// The SP joust opponent across the wasm boundary: the same air.Bandit
// brain-and-airframe harness the server flies for multiplayer bots. The
// client mirrors the player's state in, steps the bandit, and reads its
// encoded state back out — one crossing per rendered frame, same buffer
// conventions as the flight exports.

package main

import (
	"encoding/json"
	"syscall/js"

	"world/games/air"
	"world/games/air/flight"
)

var (
	bandit *air.Bandit
	mirror [flight.Size + 1]float64 // player state + a flags word (1 firing, 2 alive)
	back   [flight.Size + 5]float64 // bandit state out, plus the instrument tail (alpha, beta, nz, mach, cas) at the ownship's own indices
	shots  [64]float64              // up to eight rounds in the air, eight words each: position, velocity, shooter, phase
)

func bandits() map[string]any {
	return map[string]any{
		"bandit_init":   js.FuncOf(banditInitialize),
		"bandit_place":  js.FuncOf(banditPlace),
		"bandit_mirror": js.FuncOf(banditMirror),
		"bandit_menace": js.FuncOf(banditMenace),
		"bandit_step":   js.FuncOf(banditStep),
		"bandit_mode":   js.FuncOf(banditMode),
	}
}

// banditInitialize builds the harness from a JSON payload: level, seed, wrap,
// sky (cloud preset), night, missiles. Returns an error string, or "" on success.
func banditInitialize(this js.Value, arguments []js.Value) any {
	payload := struct {
		Level    string
		Seed     uint64
		Wrap     float64
		Sky      string
		Night    bool
		Missiles bool
		Weapons  string
	}{}
	if err := json.Unmarshal([]byte(arguments[0].String()), &payload); err != nil {
		return err.Error()
	}
	bandit = air.NewBandit(payload.Level, payload.Seed, payload.Wrap, payload.Sky, payload.Night, payload.Missiles, payload.Weapons)
	return ""
}

// banditPlace spawns the bandit from a JSON payload: position and velocity
// triples — attitude, engines, and the brain's fresh life derive in Go.
func banditPlace(this js.Value, arguments []js.Value) any {
	if bandit == nil {
		return "uninitialised"
	}
	payload := struct{ Position, Velocity [3]float64 }{}
	if err := json.Unmarshal([]byte(arguments[0].String()), &payload); err != nil {
		return err.Error()
	}
	bandit.Spawn(
		flight.Vec3{X: payload.Position[0], Y: payload.Position[1], Z: payload.Position[2]},
		flight.Vec3{X: payload.Velocity[0], Y: payload.Velocity[1], Z: payload.Velocity[2]})
	return ""
}

// banditMirror updates the player's reflection: flight.Size encoded state
// words plus one flags word (bit 1 firing, bit 2 alive).
func banditMirror(this js.Value, arguments []js.Value) any {
	if bandit == nil {
		return "uninitialised"
	}
	receive(arguments[0], mirror[:])
	flags := int(mirror[flight.Size])
	bandit.Mirror(mirror[:flight.Size], flags&1 != 0, flags&2 != 0)
	return ""
}

// banditMenace declares the player's missiles chasing the bandit: count
// missiles of six words each (position, velocity).
func banditMenace(this js.Value, arguments []js.Value) any {
	if bandit == nil {
		return "uninitialised"
	}
	count := arguments[1].Int()
	if count > 8 {
		count = 8
	}
	receive(arguments[0], shots[:count*8])
	bandit.Menace(shots[:count*8])
	return ""
}

// banditStep advances one 60 Hz frame and writes the bandit's encoded state
// into the given buffer. Returns flags: bit 1 firing, bit 2 flare dropped.
func banditStep(this js.Value, arguments []js.Value) any {
	if bandit == nil {
		return -1
	}
	if fleet[0].used {
		bandit.Wound(fleet[0].damage, fleet[0].condition) // hulk 0 IS the bandit: its damage authority feeds the brain and the flight model
	}
	fire, flare, launch := bandit.Step()
	bandit.State().Encode(back[:flight.Size])
	back[flight.Size], back[flight.Size+1], back[flight.Size+2], back[flight.Size+3], back[flight.Size+4] = bandit.Instruments()
	send(back[:], arguments[0])
	flags := 0
	if fire {
		flags |= 1
	}
	if flare {
		flags |= 2
	}
	if launch {
		flags |= 4 // an AMRAAM left the rail this frame: the client owns the round from here
	}
	flags |= bandit.Emitter() << 3 // bits 3-4: the radar state the player's RWR reads
	if bandit.Locked() {
		flags |= 32 // the STT holds the player: datalink support for the client-flown round
	}
	return flags
}

// banditMode reports the brain's current doctrine state — the flight
// recorder's developer-only channel (#212). Empty when no brain is flying.
func banditMode(this js.Value, arguments []js.Value) any {
	if bandit == nil {
		return ""
	}
	return bandit.Mode()
}

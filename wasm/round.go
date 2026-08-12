// Mochi world: Browser round boundary (WebAssembly)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build js && wasm

// Single-player AIM-120s fly the SAME round package the multiplayer server
// runs natively (#27 phase 2): the client owns the world against the AI and
// steps each launched round here; the launch-zone ladder runs the identical
// integrator, so the cockpit's ranges can never disagree with the flight.
//
// round_launch input words: 0 slot, 1-3 position, 4-6 velocity, 7 visual
// (1 = no estimate, seeker hot off the rail), 8-10 estimate position,
// 11-13 estimate velocity, 14 wrap, 15 loft (0 disables). No output.
//
// round_step input words: 0 slot, 1 dt, 2 support (1 = fresh datalink),
// 3-5 support position, 6-8 support velocity, 9 truth (1 = a target
// exists), 10-12 truth position, 13-15 truth velocity. Output words:
// 0 alive, 1 phase, 2-4 position, 5-7 velocity, 8 estimate range,
// 9 stale seconds, 10 time, 11 life, 12 fused (against the truth),
// 13 Mach, 14 closest approach so far.
//
// round_ladder input words: 0-2 shooter position, 3-5 shooter velocity,
// 6-8 target position, 9-11 target velocity, 12 wrap. Output words:
// 0 aero, 1 max, 2 escape, 3 minimum, 4 seconds-to-active preview.
//
// round_distract input words: 0 slot, 1-3 bloom position, 4-6 truth
// position, 7-9 truth velocity. Returns whether the seduction took — the
// core's doppler gate decides (#29).
//
// round_drop input words: 0 slot. No output.
package main

import (
	"math"
	"syscall/js"

	"world/games/air/flight"
	"world/games/air/round"
)

var flying [32]*round.Model
var quiver []float64 // round buffer scratch

func rounds() map[string]any {
	quiver = make([]float64, 16)
	return map[string]any{
		"round_launch": js.FuncOf(round_launch),
		"round_step":   js.FuncOf(round_step),
		"round_ladder":   js.FuncOf(round_ladder),
		"round_distract": js.FuncOf(round_distract),
		"round_drop":     js.FuncOf(round_drop),
	}
}

func round_distract(this js.Value, arguments []js.Value) any {
	receive(arguments[0], quiver[:10])
	slot := int(quiver[0])
	if slot < 0 || slot >= len(flying) || flying[slot] == nil {
		return false
	}
	return flying[slot].Distract(vec(quiver, 1), round.Target{Position: vec(quiver, 4), Velocity: vec(quiver, 7)})
}

func vec(words []float64, at int) flight.Vec3 {
	return flight.Vec3{X: words[at], Y: words[at+1], Z: words[at+2]}
}

func round_launch(this js.Value, arguments []js.Value) any {
	receive(arguments[0], quiver[:16])
	slot := int(quiver[0])
	if slot < 0 || slot >= len(flying) {
		return "slot"
	}
	var estimate *round.Target
	if quiver[7] < 0.5 {
		estimate = &round.Target{Position: vec(quiver, 8), Velocity: vec(quiver, 11)}
	}
	m := round.New(vec(quiver, 1), vec(quiver, 4), estimate, quiver[14])
	if quiver[15] < 0.5 {
		m.Loft = false
	}
	flying[slot] = m
	return nil
}

func round_step(this js.Value, arguments []js.Value) any {
	receive(arguments[0], quiver[:16])
	slot := int(quiver[0])
	if slot < 0 || slot >= len(flying) || flying[slot] == nil {
		return "slot"
	}
	m := flying[slot]
	var support, truth *round.Target
	if quiver[2] > 0.5 {
		support = &round.Target{Position: vec(quiver, 3), Velocity: vec(quiver, 6)}
	}
	if quiver[9] > 0.5 {
		truth = &round.Target{Position: vec(quiver, 10), Velocity: vec(quiver, 13)}
	}
	alive, fired, burst := m.Advance(quiver[1], support, truth)
	out := quiver[:15]
	out[0] = 0
	if alive {
		out[0] = 1
	}
	out[1] = float64(m.Phase)
	out[2], out[3], out[4] = m.Position.X, m.Position.Y, m.Position.Z
	out[5], out[6], out[7] = m.Velocity.X, m.Velocity.Y, m.Velocity.Z
	out[8] = m.Range()
	out[9] = m.Stale
	out[10] = m.Time
	out[11] = m.Life
	out[12] = 0
	if fired {
		out[12] = 1
		// The burst point: where the round actually passed the target within
		// the slice, which is the blast's origin — not the frame's sampled
		// position, which at Mach 2.5 is tens of metres away.
		out[2], out[3], out[4] = burst.X, burst.Y, burst.Z
	}
	out[13] = 0
	if speed := m.Velocity.Length(); speed > 0 {
		out[13] = speed / sound(m.Position.Y)
	}
	out[14] = m.Least
	send(out, arguments[1])
	return nil
}

func round_ladder(this js.Value, arguments []js.Value) any {
	receive(arguments[0], quiver[:13])
	zone := round.Ladder(
		round.Target{Position: vec(quiver, 0), Velocity: vec(quiver, 3)},
		round.Target{Position: vec(quiver, 6), Velocity: vec(quiver, 9)},
		quiver[12],
	)
	out := quiver[:5]
	out[0], out[1], out[2], out[3], out[4] = zone.Aero, zone.Max, zone.Escape, zone.Minimum, zone.Active
	send(out, arguments[1])
	return nil
}

func round_drop(this js.Value, arguments []js.Value) any {
	receive(arguments[0], quiver[:1])
	if slot := int(quiver[0]); slot >= 0 && slot < len(flying) {
		flying[slot] = nil
	}
	return nil
}

// sound is the ISA speed of sound, mirroring the round package's atmosphere
// for the Mach readout.
func sound(altitude float64) float64 {
	h := math.Max(0, altitude)
	temperature := 216.65
	if h <= 11000 {
		temperature = 288.15 - 0.0065*h
	}
	return math.Sqrt(1.4 * 287.05 * temperature)
}

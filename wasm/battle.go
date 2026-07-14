// Mochi world: Browser battle boundary (WebAssembly)
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build js && wasm

// Single-player damage runs the SAME battle package the multiplayer server
// runs natively: the client is the local authority against the AI, and the
// ownship's wounds are judged by byte-identical Go either way. Hulks are
// model-less target bodies for the AI aircraft (the bandit, neutral
// traffic); the ownship's body binds straight into the flight model's
// damage state, so hits change the aero on the next step.
//
// burst input words: 0 target (-1 ownship, else hulk), 1-3 shooter
// position, 4-6 forward, 7-9 up, 10-12 target position, 13-16 target
// attitude (hulk targets; the ownship poses from its model), 17 rounds,
// 18 shooter identity, 19 tick, 20-22 shooter velocity, 23-25 target
// velocity (hulk targets; the ownship's comes from its model).
// Output: 0 hits, 1 event mask.
//
// blast input words: 0 target, 1-3 burst point, 4-6 target position,
// 7-10 attitude, 11 shooter identity, 12 tick. Output: 0 kill, 1 mask.
//
// progress input words: 0 ownship throttle, 1 tick, 2 reset (1 clears all
// battle state on mission start). Output: 0-5 ownship (fire left, fire
// right, burning, killed, event mask, leak), then 8 words per hulk:
// fire left, fire right, burning, killed, event mask, thrust loss,
// wing loss, element total.
package main

import (
	"syscall/js"

	"world/games/air/aircraft"
	"world/games/air/battle"
	"world/games/air/flight"
)

// The event mask bits shared with the client.
const (
	maskFire = 1 << iota
	maskPilot
	maskExplode
	maskJam
	maskShed
	maskLeak
)

const hulks = 9 // bandit + up to eight neutral traffic aircraft

type hulk struct {
	body      battle.Body
	condition battle.Condition
	damage    flight.DamageState
	used      bool
}

var (
	fleet     [hulks]hulk
	condition battle.Condition // the ownship's fires and pilot
	arsenal   []float64        // battle buffer scratch
)

func battles() map[string]any {
	arsenal = make([]float64, 64)
	return map[string]any{
		"hulk":     js.FuncOf(rig),
		"burst":    js.FuncOf(burst),
		"blast":    js.FuncOf(blast),
		"progress": js.FuncOf(progress),
	}
}

// rig builds or resets the hulk at an index: hulk(index, aircraft) -> bool.
func rig(this js.Value, arguments []js.Value) any {
	index := arguments[0].Int()
	if index < 0 || index >= hulks {
		return false
	}
	airframe := aircraft.Get(arguments[1].String())
	if airframe == nil {
		return false
	}
	h := &fleet[index]
	h.damage = flight.DamageState{}
	h.condition = battle.Condition{Damager: -1}
	h.body = battle.Body{Airframe: airframe, Parts: battle.Parts(airframe), Damage: &h.damage, Condition: &h.condition}
	h.used = true
	return true
}

// events folds battle events into the client's bitmask.
func events(list []battle.Event) float64 {
	mask := 0
	for _, e := range list {
		switch e.Kind {
		case "fire":
			mask |= maskFire
		case "pilot":
			mask |= maskPilot
		case "explode":
			mask |= maskExplode
		case "jam":
			mask |= maskJam
		case "shed":
			mask |= maskShed
		}
	}
	return float64(mask)
}

// aim resolves a target selector into its body and pose.
func aim(words []float64) (*battle.Body, flight.Vec3, flight.Quat) {
	target := int(words[0])
	if target < 0 {
		if model == nil {
			return nil, flight.Vec3{}, flight.Quat{}
		}
		return &battle.Body{Airframe: model.Airframe, Parts: parts(), Damage: &model.State.Damage, Condition: &condition},
			model.State.Position, model.State.Attitude
	}
	if target >= hulks || !fleet[target].used {
		return nil, flight.Vec3{}, flight.Quat{}
	}
	return &fleet[target].body,
		flight.Vec3{X: words[10], Y: words[11], Z: words[12]},
		flight.Quat{W: words[13], X: words[14], Y: words[15], Z: words[16]}
}

// cache of the ownship's part geometry (rebuilt when the model changes).
var (
	geometry []battle.Part
	geared   *flight.Airframe
)

func parts() []battle.Part {
	if geared != model.Airframe {
		geometry = battle.Parts(model.Airframe)
		geared = model.Airframe
	}
	return geometry
}

func burst(this js.Value, arguments []js.Value) any {
	receive(arguments[0], arsenal[:26])
	body, position, attitude := aim(arsenal)
	if body == nil {
		return 0
	}
	shooter := battle.Pose{
		Position: flight.Vec3{X: arsenal[1], Y: arsenal[2], Z: arsenal[3]},
		Forward:  flight.Vec3{X: arsenal[4], Y: arsenal[5], Z: arsenal[6]},
		Up:       flight.Vec3{X: arsenal[7], Y: arsenal[8], Z: arsenal[9]},
		Velocity: flight.Vec3{X: arsenal[20], Y: arsenal[21], Z: arsenal[22]},
	}
	shooter.Right = shooter.Forward.Cross(shooter.Up)
	velocity := flight.Vec3{X: arsenal[23], Y: arsenal[24], Z: arsenal[25]}
	if int(arsenal[0]) < 0 && model != nil {
		velocity = model.State.Velocity // the ownship as the target: its motion comes from its own model
	}
	wrap := 0.0
	if model != nil {
		wrap = model.Environment.Wrap
	}
	hits, raised := battle.Burst(shooter, position, attitude, velocity, body, int(arsenal[17]), wrap,
		model.Environment.Seed, uint64(arsenal[18]), uint64(arsenal[19]))
	if hits > 0 {
		body.Condition.Damager = int(arsenal[18])
		body.Condition.Damaged = 0
	}
	out := [2]float64{float64(hits), events(raised)}
	send(out[:], arguments[1])
	return hits
}

func blast(this js.Value, arguments []js.Value) any {
	receive(arguments[0], arsenal[:13])
	// blast reuses the aim layout with the pose at different offsets.
	selector := [17]float64{arsenal[0]}
	copy(selector[10:13], arsenal[4:7])
	copy(selector[13:17], arsenal[7:11])
	body, position, attitude := aim(selector[:])
	if body == nil {
		return 0
	}
	point := flight.Vec3{X: arsenal[1], Y: arsenal[2], Z: arsenal[3]}
	wrap := 0.0
	if model != nil {
		wrap = model.Environment.Wrap
	}
	kill, raised := battle.Blast(point, position, attitude, body, wrap,
		model.Environment.Seed, uint64(arsenal[11]), uint64(arsenal[12]))
	if kill || len(raised) > 0 {
		body.Condition.Damager = int(arsenal[11])
		body.Condition.Damaged = 0
	}
	out := [2]float64{0, events(raised)}
	if kill {
		out[0] = 1
	}
	send(out[:], arguments[1])
	return kill
}

func progress(this js.Value, arguments []js.Value) any {
	receive(arguments[0], arsenal[:3])
	if arsenal[2] != 0 { // mission reset: everything pristine
		condition = battle.Condition{Damager: -1}
		for i := range fleet {
			if fleet[i].used {
				fleet[i].damage = flight.DamageState{}
				fleet[i].condition = battle.Condition{Damager: -1}
			}
		}
	}
	tick := uint64(arsenal[1])
	out := arsenal[:64]
	for i := range out {
		out[i] = 0
	}
	if model != nil {
		body := battle.Body{Airframe: model.Airframe, Parts: parts(), Damage: &model.State.Damage, Condition: &condition}
		raised := battle.Advance(&body, model, arsenal[0], 60, model.Environment.Seed, 0, tick)
		out[0], out[1] = condition.Fire[0], condition.Fire[1]
		out[2], out[3] = bit(condition.Burning), bit(condition.Killed)
		out[4] = events(raised)
		out[5] = model.State.Damage.Leak
	}
	for i := range fleet {
		h := &fleet[i]
		if !h.used {
			continue
		}
		throttle := 0.8
		if i == 0 && bandit != nil {
			throttle = bandit.Throttle() // the bandit's fires feed on ITS lever, so the brain's fire drill (#130) can starve them
		}
		raised := battle.Advance(&h.body, nil, throttle, 60, model.Environment.Seed, uint64(i+1), tick)
		base := 6 + i*8
		out[base], out[base+1] = h.condition.Fire[0], h.condition.Fire[1]
		out[base+2], out[base+3] = bit(h.condition.Burning), bit(h.condition.Killed)
		out[base+4] = events(raised)
		out[base+5] = (h.damage.Engine[0] + h.damage.Engine[1]) / 2
		out[base+6] = wings(h)
		out[base+7] = sum(h.damage.Element)
	}
	send(out, arguments[1])
	return nil
}

// wings reports the worst per-wing outboard loss for the AI's spiral logic.
func wings(h *hulk) float64 {
	worst, base := 0.0, 0
	for si := range h.body.Airframe.Surfaces {
		s := &h.body.Airframe.Surfaces[si]
		if s.Kind == flight.Wing && h.damage.Element != nil {
			loss, count := 0.0, 0
			for ei := len(s.Elements) / 2; ei < len(s.Elements); ei++ {
				if base+ei < len(h.damage.Element) {
					loss += h.damage.Element[base+ei]
					count++
				}
			}
			if count > 0 && loss/float64(count) > worst {
				worst = loss / float64(count)
			}
		}
		base += len(s.Elements)
	}
	return worst
}

func sum(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total
}

func bit(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

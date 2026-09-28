// Mochi world: Browser wake boundary
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build js && wasm

// Every jet the browser knows lays its wake here: the pilot's (id 0), the
// single-player bandit's (id 1) and, in a match, each remote's from its pose
// (1000 + its slot). Before the pilot's model and the bandit's step, each is
// handed the pieces near it, so the pilot flies through the bandit's wake and
// the bandit through the pilot's, and either through its own once it is old
// enough. The server lays the same wakes for a match, so prediction agrees.

package main

import (
	"encoding/json"
	"syscall/js"

	"world/games/air/flight"
)

const (
	pilot  = 0    // the pilot's own trail
	enemy  = 1    // the single-player bandit's
	remote = 1000 // a match's remotes, by slot above this
	pose   = 12   // words per remote in wake_shed: slot, position ×3, attitude w x y z, velocity ×3, load factor
)

var (
	wakes  flight.Wakes
	clock  float64   // s, the wakes' clock: advanced by the pilot's frames, shared by every trail
	shed   []float64 // wake_shed's poses
	spoken struct {  // what the pilot's model was last handed, and how many jets have a wake laid, for wake_state
		Pieces int
		Swirl  [3]float64
		Trails int
	}
)

func wake_exports() map[string]any {
	return map[string]any{
		"wake_shed":  guard(wake_shed),
		"wake_state": guard(wake_state),
	}
}

// wake_reset starts the wakes afresh with a new mission's air.
func wake_reset(environment flight.Environment) {
	wakes = flight.Wakes{Turbulence: environment.Turbulence}
	clock = 0
}

// meet hands a model the wake pieces near it on the shared clock.
func meet(id int, m *flight.Model) {
	m.Wake = wakes.Near(id, m.State.Position, clock, m.World.Sea, m.Environment.Wrap)
}

// lay records a model's mark on the shared clock.
func lay(id int, m *flight.Model) {
	wakes.Record(id, clock, m)
}

// wake_shed lays the marks of a match's remotes from their poses: the count,
// then that many runs of pose words (slot, position, attitude as w x y z,
// velocity, load factor). Their mass is the pilot's airframe at half fuel,
// the lift the load factor it reports.
func wake_shed(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	count := arguments[0].Int()
	if count <= 0 {
		return ""
	}
	if cap(shed) < count*pose {
		shed = make([]float64, count*pose)
	}
	shed = shed[:count*pose]
	receive(arguments[1], shed)
	frame := model.Airframe
	mass := frame.Mass.Empty + frame.Mass.Fuel/2
	for k := 0; k < count; k++ {
		w := shed[k*pose : (k+1)*pose]
		position := flight.Vec3{X: w[1], Y: w[2], Z: w[3]}
		attitude := flight.Quat{W: w[4], X: w[5], Y: w[6], Z: w[7]}.Normalize()
		velocity := flight.Vec3{X: w[8], Y: w[9], Z: w[10]}
		density := flight.Atmosphere(position.Y, model.Environment).Density
		wakes.Shed(remote+int(w[0]), clock, position, attitude, velocity, w[11]*mass*model.Gravity, frame.Reference.Span, density, flight.Vec3{})
	}
	return ""
}

// wake_state reports, as JSON, what the pilot's model was handed on its last
// frame - how many pieces, and the air they induce at its CG, world m/s - and
// how many jets have a wake laid.
func wake_state(this js.Value, arguments []js.Value) any {
	text, _ := json.Marshal(spoken)
	return string(text)
}

// heard notes what the pilot's model was handed, for wake_state.
func heard(m *flight.Model) {
	swirl := m.Swirl()
	spoken.Pieces = len(m.Wake)
	spoken.Swirl = [3]float64{swirl.X, swirl.Y, swirl.Z}
	spoken.Trails = wakes.Trails()
}

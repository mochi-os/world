// Mochi world: Browser flight core (WebAssembly boundary)
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build js && wasm

// The furball flight core compiled for the browser. One JS object,
// globalThis.furball_flight, with one boundary crossing per rendered frame:
// the client fills a small input buffer, frame() steps the model and fills
// the output buffer (encoded state plus derived instruments). Prediction
// rings live on this side; a reconciliation replay never crosses the
// boundary. All buffers cross as Uint8Array views over Float64Array data
// (wasm and every supported browser are little-endian).
//
// Input buffer layout (float64 words):
//
//	0 pitch, 1 roll, 2 yaw, 3 throttle, 4 speedbrake,
//	5 flags (1 reheat, 2 brake, 4 gear, 8 hook, 16 launch, 32 override),
//	6 sequence, 7 steps
//
// Output buffer layout: flight.Size encoded state words, then
// alpha, beta, nz, mach, cas.
package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"syscall/js"

	"world/games/furball/flight"
)

// Extra is the instrument tail appended to the encoded state.
const extra = 5

// ring is the prediction history: one slot per input sequence.
const ring = 512

type slot struct {
	inputs flight.Inputs
	steps  int
	state  [flight.Size]float64
	used   bool
}

var (
	model  *flight.Model
	rings  [ring]slot
	input  [8]float64
	output [flight.Size + extra]float64
	bytes  []byte // scratch for boundary copies
)

func main() {
	js.Global().Set("furball_flight", js.ValueOf(map[string]any{
		"version": js.FuncOf(version),
		"init":    js.FuncOf(initialize),
		"set":     js.FuncOf(set),
		"get":     js.FuncOf(get),
		"frame":   js.FuncOf(frame),
		"mark":    js.FuncOf(mark),
		"ack":     js.FuncOf(ack),
		"level":   js.FuncOf(level),
		"clear":   js.FuncOf(clear),
	}))
	select {} // the exports keep serving; the program never exits
}

func version(js.Value, []js.Value) any { return flight.Version }

// initialize builds the model from a JSON payload of environment and world
// geometry. Returns an error string, or "" on success.
func initialize(this js.Value, arguments []js.Value) any {
	payload := struct {
		Environment flight.Environment
		World       flight.World
	}{}
	if err := json.Unmarshal([]byte(arguments[0].String()), &payload); err != nil {
		return err.Error()
	}
	model = flight.New(flight.Fighter, payload.Environment, payload.World)
	rings = [ring]slot{}
	if len(bytes) == 0 {
		bytes = make([]byte, (flight.Size+extra)*8)
	}
	return ""
}

// receive copies a JS Uint8Array into a float64 slice.
func receive(view js.Value, floats []float64) {
	js.CopyBytesToGo(bytes[:len(floats)*8], view)
	for i := range floats {
		floats[i] = math.Float64frombits(binary.LittleEndian.Uint64(bytes[i*8:]))
	}
}

// send copies a float64 slice into a JS Uint8Array.
func send(floats []float64, view js.Value) {
	for i, f := range floats {
		binary.LittleEndian.PutUint64(bytes[i*8:], math.Float64bits(f))
	}
	js.CopyBytesToJS(view, bytes[:len(floats)*8])
}

func set(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	receive(arguments[0], output[:flight.Size])
	model.State = flight.Decode(output[:flight.Size])
	return ""
}

func get(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	emit(arguments[0])
	return ""
}

// emit writes the encoded state and instrument tail to a JS view.
func emit(view js.Value) {
	model.State.Encode(output[:flight.Size])
	output[flight.Size] = model.Alpha()
	output[flight.Size+1] = model.Beta()
	output[flight.Size+2] = model.Nz()
	output[flight.Size+3] = model.Mach()
	output[flight.Size+4] = model.Cas()
	send(output[:], view)
}

// controls decodes the input buffer into a control sample and step count.
func controls() (flight.Inputs, int) {
	flags := int(input[5])
	in := flight.Inputs{
		Pitch:      input[0],
		Roll:       input[1],
		Yaw:        input[2],
		Throttle:   input[3],
		Speedbrake: input[4],
		Reheat:     flags&1 != 0,
		Brake:      flags&2 != 0,
		Gear:       flags&4 != 0,
		Hook:       flags&8 != 0,
		Launch:     flags&16 != 0,
		Override:   flags&32 != 0,
		Sequence:   uint32(input[6]),
	}
	steps := int(input[7])
	if steps < 0 {
		steps = 0
	}
	if steps > 30 {
		steps = 30 // tab-throttle spiral cap; the host blends or snaps beyond
	}
	return in, steps
}

// frame steps the model with one input sample and fills the output buffer.
func frame(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	receive(arguments[0], input[:])
	in, steps := controls()
	for i := 0; i < steps; i++ {
		model.Step(in)
	}
	emit(arguments[1])
	return ""
}

// mark records the post-frame state and the sample that produced it under
// its sequence, for later reconciliation replay.
func mark(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	receive(arguments[0], input[:])
	in, steps := controls()
	entry := &rings[in.Sequence%ring]
	entry.inputs = in
	entry.steps = steps
	entry.used = true
	model.State.Encode(entry.state[:])
	return ""
}

// ack reconciles against the authoritative state for an acknowledged
// sequence: measure divergence at that point, adopt the server state, and
// replay every later recorded sample. Returns the divergence in metres, or
// -1 when the ring no longer holds the sequence (caller hard-snaps).
func ack(this js.Value, arguments []js.Value) any {
	if model == nil {
		return -1.0
	}
	sequence := uint32(arguments[0].Int())
	receive(arguments[1], output[:flight.Size])
	authority := flight.Decode(output[:flight.Size])
	entry := &rings[sequence%ring]
	if !entry.used || entry.inputs.Sequence != sequence {
		model.State = authority
		return -1.0
	}
	predicted := flight.Decode(entry.state[:])
	divergence := predicted.Position.Subtract(authority.Position).Length()
	model.State = authority
	latest := latest()
	for s := sequence + 1; s <= latest; s++ {
		replay := &rings[s%ring]
		if !replay.used || replay.inputs.Sequence != s {
			continue
		}
		for i := 0; i < replay.steps; i++ {
			model.Step(replay.inputs)
		}
		model.State.Encode(replay.state[:])
	}
	return divergence
}

// level places the model in trimmed level flight — the transient-free air
// spawn (position x y z, horizontal direction x z, speed, fuel).
func level(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	position := flight.Vec3{X: arguments[0].Float(), Y: arguments[1].Float(), Z: arguments[2].Float()}
	direction := flight.Vec3{X: arguments[3].Float(), Z: arguments[4].Float()}
	model.State = flight.Level(model, position, direction, arguments[5].Float(), arguments[6].Float())
	return ""
}

// clear acknowledges the contact events the host has read: the touchdown
// record and any crash-probe contact.
func clear(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	model.State.Gear.Touch = flight.Touch{}
	model.State.Gear.Contact = -1
	return ""
}

// latest is the highest sequence currently recorded.
func latest() uint32 {
	best := uint32(0)
	for i := range rings {
		if rings[i].used && rings[i].inputs.Sequence > best {
			best = rings[i].inputs.Sequence
		}
	}
	return best
}

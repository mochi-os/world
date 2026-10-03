// Mochi world: Browser flight core (WebAssembly boundary)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build js && wasm

// The air flight core compiled for the browser. One JS object,
// globalThis.air_flight, with one boundary crossing per rendered frame:
// the client fills a small input buffer, frame() steps the model and fills
// the output buffer (encoded state plus derived instruments). Prediction
// rings live on this side; a reconciliation replay never crosses the
// boundary. All buffers cross as Uint8Array views over Float64Array data
// (wasm and every supported browser are little-endian).
//
// Input buffer layout (float64 words):
//
//	0 pitch, 1 roll, 2 yaw, 3 throttle, 4 speedbrake,
//	5 flags (1 dump, 2 brake, 4 gear, 8 hook, 16 launch, 32 override, 64 probe, 128 reset, 256/512 fuel off, 1024 fire, 2048 anti-skid off, 4096 emergency gear, 8192 MECH ON),
//	6 sequence, 7 steps, 8 reheat, 9 pitch trim, 10 flap switch, 11 roll trim,
//	12/13 the EXT TANKS switches, WING and CTR (-1 STOP, 0 NORM, +1 ORIDE),
//	14 nosewheel steering (-1 off, 0 LOW, +1 HI)
//
// Output buffer layout: flight.Size encoded state words, then
// alpha, beta, nz, mach, cas, power, stage, and the spin recovery display's
// stick direction (-1 left, +1 right, 0 none).
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"runtime/debug"
	"syscall/js"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// Extra is the instrument tail appended to the encoded state.
const extra = 8

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
	input  [16]float64
	output [flight.Size + extra]float64
	bytes  []byte // scratch for boundary copies
)

func main() {
	exports := map[string]any{
		"version":  guard(version),
		"init":     guard(initialize),
		"set":      guard(set),
		"get":      guard(get),
		"frame":    guard(frame),
		"mark":     guard(mark),
		"ack":      guard(ack),
		"level":    guard(level),
		"stores":   guard(stores),
		"catalog":  guard(catalog),
		"gust":     guard(gust),
		"approach": guard(approach),
		"cruise":   guard(cruise),
		"clear":    guard(clear),
	}
	for name, export := range battles() {
		exports[name] = export
	}
	for name, export := range bandits() {
		exports[name] = export
	}
	for name, export := range rounds() {
		exports[name] = export
	}
	for name, export := range wake_exports() {
		exports[name] = export
	}
	for name, export := range debris_exports() {
		exports[name] = export
	}
	js.Global().Set("air_flight", js.ValueOf(exports))
	select {} // the exports keep serving; the program never exits
}

// guard wraps an export so a panic fails that one call instead of killing
// the whole program: the stack reaches the console, the call returns the
// panic message, and the core keeps serving.
func guard(export func(js.Value, []js.Value) any) js.Func {
	return js.FuncOf(func(this js.Value, arguments []js.Value) (result any) {
		defer func() {
			if fault := recover(); fault != nil {
				println("air_flight panic:", fmt.Sprint(fault))
				debug.PrintStack()
				result = fmt.Sprint("panic: ", fault)
			}
		}()
		return export(this, arguments)
	})
}

func version(js.Value, []js.Value) any { return flight.Version }

// gust reports the wind vector sampled at the jet this step — the headless
// verification that Environment.Wind actually reached the core (#44).
func gust(js.Value, []js.Value) any {
	if model == nil {
		return "no model"
	}
	g := model.Gust()
	return fmt.Sprintf("%.2f,%.2f,%.2f", g.X, g.Y, g.Z)
}

// initialize builds the model from a JSON payload of environment and world
// geometry. Returns an error string, or "" on success.
func initialize(this js.Value, arguments []js.Value) any {
	payload := struct {
		Aircraft    string
		Environment flight.Environment
		World       flight.World
	}{}
	if err := json.Unmarshal([]byte(arguments[0].String()), &payload); err != nil {
		return err.Error()
	}
	airframe := aircraft.Get(payload.Aircraft)
	if airframe == nil {
		return "unknown aircraft"
	}
	model = flight.New(airframe, payload.Environment, payload.World)
	rings = [ring]slot{}
	wake_reset(payload.Environment)
	debris_reset()
	return ""
}

// scratch returns the shared boundary buffer grown to hold count bytes.
// Sizing on demand means no export depends on another having run first.
func scratch(count int) []byte {
	if len(bytes) < count {
		bytes = make([]byte, count)
	}
	return bytes[:count]
}

// receive copies a JS Uint8Array into a float64 slice. A view shorter than the
// export's declared word count is refused rather than tolerated: scratch hands
// the same growing buffer to every export, so a short copy left the tail of
// whatever the previous call wrote, and the export read those stale words as
// its own arguments. guard turns the panic into a returned string.
func receive(view js.Value, floats []float64) {
	buffer := scratch(len(floats) * 8)
	if n := js.CopyBytesToGo(buffer, view); n != len(buffer) {
		panic(fmt.Sprintf("receive: %d of %d bytes", n, len(buffer)))
	}
	for i := range floats {
		floats[i] = math.Float64frombits(binary.LittleEndian.Uint64(buffer[i*8:]))
	}
}

// send copies a float64 slice into a JS Uint8Array.
func send(floats []float64, view js.Value) {
	buffer := scratch(len(floats) * 8)
	for i, f := range floats {
		binary.LittleEndian.PutUint64(buffer[i*8:], math.Float64bits(f))
	}
	js.CopyBytesToJS(view, buffer)
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
	// Achieved power across the AIRFRAME'S engines (0..4): spool fraction of
	// military and reheat stage, so hosts never count engine slots.
	power, stage := 0.0, 0.0
	if count := len(model.Airframe.Engines); count > 0 {
		for i := 0; i < count; i++ {
			power += model.State.Engine[i].Spool
			stage += model.State.Engine[i].Reheat
		}
		power /= float64(count)
		stage /= float64(count)
	}
	output[flight.Size+5] = power
	output[flight.Size+6] = stage
	output[flight.Size+7] = model.Spin()
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
		Reheat:     input[8], // analog reheat: the commanded afterburner-zone fraction (flag bit 1 retired)
		Brake:      flags&2 != 0,
		Bypass:     flags&2048 != 0, // the ANTI SKID switch OFF
		Gear:       flags&4 != 0,
		Emergency:  flags&4096 != 0, // the gear handle turned and pulled
		Mechanical: flags&8192 != 0, // MECH ON
		Hook:       flags&8 != 0,
		Launch:     flags&16 != 0,
		Override:   flags&32 != 0,
		Probe:      flags&64 != 0,
		Trim:       input[9],
		Flap:       input[10],
		Lean:       input[11],
		Reset:      flags&128 != 0,
		Onspeed:    flags&32768 != 0, // the pitch trim alone back to on-speed
		Reverted:   flags&16384 != 0, // mission computer 1 lost
		Held:       input[15],        // wing fuel held at INHIBIT, kg
		Dump:       flags&1 != 0,     // bit 1 reclaimed from the retired boolean reheat (the SP wasm ships with its client, so no cross-version wire exists)
		Secure:     [2]bool{flags&256 != 0, flags&512 != 0},
		Transfer:   [2]int{int(input[12]), int(input[13])},
		Steering:   position(input[14]),
		Fire:       flags&1024 != 0, // the trigger while rounds leave: the client gates it on its own magazine, the core kicks back
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

// position reads a three-position switch's slot as -1, 0 or +1; a slot the
// client never filled (NaN) reads as the centre.
func position(word float64) int {
	switch {
	case word >= 0.5:
		return 1
	case word <= -0.5:
		return -1
	}
	return 0
}

// frame steps the model with one input sample and fills the output buffer.
func frame(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	receive(arguments[0], input[:])
	in, steps := controls()
	meet(pilot, model) // the wake as it stands at the frame's start, for every step of it
	for i := 0; i < steps; i++ {
		model.Step(in)
	}
	heard(model) // after the steps: the pieces and the air they made, from the same frame
	clock += float64(steps) * flight.Dt
	lay(pilot, model)
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

// approach places the model on a trimmed on-speed descent - the landing spawn
// (position x y z, horizontal direction x z, glideslope in DEGREES below the
// horizon, fuel). Returns the throttle that holds the trim.
func approach(this js.Value, arguments []js.Value) any {
	if model == nil {
		return 0.0
	}
	position := flight.Vec3{X: arguments[0].Float(), Y: arguments[1].Float(), Z: arguments[2].Float()}
	direction := flight.Vec3{X: arguments[3].Float(), Z: arguments[4].Float()}
	state, throttle := flight.Approach(model, position, direction, -arguments[5].Float()*math.Pi/180, arguments[6].Float())
	model.State = state
	return throttle
}

// cruise reports trimmed level flight on dry power at an altitude (m) and Mach
// number for the jet as it is now - its weight, stores and damage - as
// [fuel flow kg/s for all engines, true airspeed m/s]: the figure the client
// searches for the FPAS best Mach and optimum cruise (NATOPS 2.3.1.1). The
// flow is negative where the jet cannot fly level on dry power. The flying
// state is untouched.
func cruise(this js.Value, arguments []js.Value) any {
	if model == nil {
		return []any{-1.0, 0.0}
	}
	flow, speed, ok := model.Cruise(arguments[0].Float(), arguments[1].Float())
	if !ok {
		flow = -1
	}
	return []any{flow, speed}
}

// stores sets the attached external-store bitmask: the client asserts the
// flown loadout's bits and clears each store's bit as it departs, dropping
// its mass and carriage drag (tanks also fill or clamp the external fuel on
// the transition).
func stores(this js.Value, arguments []js.Value) any {
	if model == nil {
		return "uninitialised"
	}
	model.Stores(uint64(arguments[0].Int())) // the mask crosses as a JS number — f64-exact well past the catalog's bit count
	return ""
}

// catalog returns the named aircraft's fitment catalog as JSON — the
// world-owned property table (mask bit order, station, mass, drag area, fuel
// capacity, default mask). The client derives its loadout table from this at
// boot instead of mirroring the numbers, so the two sides cannot drift.
func catalog(this js.Value, arguments []js.Value) any {
	airframe := aircraft.Get(arguments[0].String())
	if airframe == nil {
		return ""
	}
	entries := make([]map[string]any, len(airframe.Stores))
	for i := range airframe.Stores {
		store := &airframe.Stores[i]
		entries[i] = map[string]any{
			"name":    store.Name,
			"station": store.Station,
			"mass":    store.Mass,
			"area":    store.Area,
			"fuel":    store.Fuel,
			"lateral": store.Position.Z, // m signed, port negative — the client's asymmetry arithmetic (NATOPS 4.1.5)
		}
	}
	payload, err := json.Marshal(map[string]any{"stores": entries, "default": airframe.Default, "internal": airframe.Mass.Fuel, "empty": airframe.Mass.Empty})
	if err != nil {
		return ""
	}
	return string(payload)
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

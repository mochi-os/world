// Mochi world: Air carrier under way, for the browser
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/json"
	"syscall/js"

	"world/games/air/flight"
	"world/games/air/ship"
)

// The carrier under way: single player plans and steers the ship here with the
// same code the world server steers a match's with; a match takes the
// server's track instead. Either way the core's carrier flies the track, so
// the deck the jet meets and the deck the engine draws are one.
var (
	vessel  ship.Ship // the steering: planned here, or the server's heading into the wind
	steered bool      // this core steers the ship: single player
	held    float64   // the speed single player's temporary select holds her to, m/s; 0 leaves her to her plan
	sudden  bool      // the select has just been changed: she takes its speed at once
)

func ship_exports() map[string]any {
	return map[string]any{
		"plan":    guard(plan),
		"steer":   guard(steer),
		"voyage":  guard(voyage),
		"speed":   guard(speed),
		"carrier": guard(carrier),
		"deck":    guard(deck),
	}
}

// plan sets single player's ship under way from the jet's clock: into the
// mission's wind, its area clear of the map's coasts.
func plan(js.Value, []js.Value) any {
	if model == nil || model.World.Carrier == nil {
		return "uninitialised"
	}
	var coasts [][]flight.Vec3
	for _, f := range model.World.Fields {
		if len(f.Coast) >= 3 {
			coasts = append(coasts, f.Coast)
		}
	}
	vessel = ship.Plan(model.World.Carrier, model.Environment.Wind, coasts, model.State.Time)
	steered, held, sudden = true, 0, false
	return ""
}

// steer moves single player's ship on at the jet's clock, the jet needing
// the deck when it is aboard or coming in to land. A match's server steers.
func steer(js.Value, []js.Value) any {
	if model == nil || model.World.Carrier == nil || !steered {
		return false
	}
	c, t := model.World.Carrier, model.State.Time
	jet := model.State
	jet.Position = model.Environment.Placement().Point(jet.Position) // the ship sails the chart
	changed := vessel.Step(c, t, ship.Needed(c, t, []flight.State{jet}))
	if held > 0 {
		place := model.Environment.Placement()
		before := model.World.Footing(model.State.Position, t, place)
		if vessel.Hold(c, t, held, sudden) {
			changed = true
			// A jet on her deck changes speed with her, or the deck jumps out from under it.
			model.State.Velocity = model.State.Velocity.Add(model.World.Footing(model.State.Position, t, place).Subtract(before))
		}
		if last := c.Track[len(c.Track)-1]; last.Start <= t && last.Turn == 0 {
			sudden = false // taken, or nothing to take; a manoeuvre under way runs its course first, and she takes it as that ends
		}
	}
	return changed
}

// speed is single player's temporary speed select: given a speed in m/s, the
// ship takes it at once on the next steer and holds it on her straight runs,
// gathering way to it after her turns as she does; either way it
// returns her speed into the wind, or -1 when this core does not steer her.
func speed(this js.Value, arguments []js.Value) any {
	if model == nil || model.World.Carrier == nil || !steered {
		return -1.0
	}
	if len(arguments) > 0 && arguments[0].Type() == js.TypeNumber && arguments[0].Float() > 0 {
		held, sudden = arguments[0].Float(), true
		vessel.Speed = held
	}
	return vessel.Speed
}

// voyage takes a match's track from the server, as its welcome and its ship
// events carry it: the heading into the wind, and each leg as [start, x, z,
// heading, speed, turn, change]. Returns an error string, or "".
func voyage(this js.Value, arguments []js.Value) any {
	if model == nil || model.World.Carrier == nil {
		return "uninitialised"
	}
	payload := struct {
		Into  float64
		Track [][7]float64
	}{}
	if err := json.Unmarshal([]byte(arguments[0].String()), &payload); err != nil {
		return err.Error()
	}
	c := model.World.Carrier
	track := make([]flight.Leg, len(payload.Track))
	for n, l := range payload.Track {
		track[n] = flight.Leg{Start: l[0], Position: flight.Vec3{X: l[1], Y: c.Position.Y, Z: l[2]}, Heading: l[3], Speed: l[4], Turn: l[5], Change: l[6]}
	}
	c.Track = track
	vessel = ship.Ship{Into: payload.Into}
	steered = false
	return ""
}

// carrier is the ship's pose at time t, as [x, z, heading, closed, way,
// known]: closed is 1 while it turns or steams away from the wind, when the
// deck takes no one; way is her speed through the water, which her bow wave
// and wake are drawn from; known is the earliest time her track covers
// (-Infinity without one).
func carrier(this js.Value, arguments []js.Value) any {
	if model == nil || model.World.Carrier == nil {
		return nil
	}
	c, t := model.World.Carrier, arguments[0].Float()
	at, heading := c.At(t)
	closed := 0.0
	if len(c.Track) > 0 && vessel.Closed(c, t) {
		closed = 1
	}
	return []any{at.X, at.Z, heading, closed, c.Way(t), c.Known()}
}

// deck is where the ship floats at time t in the client's frame, as [x, y,
// z, w, qx, qy, qz]: her deck reference point and the attitude of her deck
// frame (x to the bow, y up, z to starboard), level on the curved sea.
func deck(this js.Value, arguments []js.Value) any {
	if model == nil || model.World.Carrier == nil {
		return nil
	}
	at, attitude := model.World.Carrier.Where(arguments[0].Float(), model.Environment.Placement())
	return []any{at.X, at.Y, at.Z, attitude.W, attitude.X, attitude.Y, attitude.Z}
}

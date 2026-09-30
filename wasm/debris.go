// Mochi world: Air debris for single player
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"syscall/js"

	"world/games/air/battle"
	"world/games/air/flight"
)

// Single-player debris (battle/debris.go), as the world server meets it: the
// client lays the bandit's wreckage where it fell, and asks after each frame
// whether the pilot's jet met any of it over the physics time the frame
// stepped, on the wakes' clock.

var (
	wreckage []battle.Debris
	swept    float64    // the clock the last debris_meet reached
	meetings uint64     // debris_meet calls, the rolls' tick
	laid     [6]float64 // debris_lay's words
)

func debris_exports() map[string]any {
	return map[string]any{
		"debris_lay":  guard(debris_lay),
		"debris_meet": guard(debris_meet),
	}
}

func debris_reset() {
	wreckage = nil
	swept = clock
	meetings = 0
}

// debris_lay lays a kill's wreckage: words 0-2 where the jet fell, 3-5 its
// velocity then.
func debris_lay(this js.Value, arguments []js.Value) any {
	receive(arguments[0], laid[:])
	wreckage = append(wreckage, battle.Debris{
		Origin:   flight.Vec3{X: laid[0], Y: laid[1], Z: laid[2]},
		Velocity: flight.Vec3{X: laid[3], Y: laid[4], Z: laid[5]},
		Time:     clock,
	})
	return ""
}

// debris_meet rolls the pilot's jet against every cloud still able to strike
// over the time since the last call, a tenth of a second at most, and writes
// the strikes, the event mask and the impact points in the body frame:
// [strikes, mask, count, x y z ...]. The output is written whatever happens,
// so a stale buffer is never read as this frame's verdict.
func debris_meet(this js.Value, arguments []js.Value) any {
	var out [3 + 3*8]float64
	span := min(clock-swept, 0.1) // frames it was not asked over (a crash, the cheat) are not rolled in one
	swept = clock
	live := wreckage[:0]
	for _, d := range wreckage {
		if _, _, _, alive := d.Cloud(clock); alive {
			live = append(live, d)
		}
	}
	wreckage = live
	if model == nil || span <= 0 || len(wreckage) == 0 {
		send(out[:3], arguments[0])
		return 0
	}
	meetings++
	body, position, attitude := aim([]float64{-1})
	var raised []battle.Event
	var struck []flight.Vec3
	strikes := 0
	for k, d := range wreckage {
		seed := model.Environment.Seed + uint64(k+1)*0x9e3779b97f4a7c15
		hit, found, points := d.Meet(clock, span, position, model.State.Velocity, attitude, body, model.Environment.Wrap, seed, pilot, meetings)
		if hit {
			strikes++
			raised = append(raised, found...)
			struck = append(struck, points...)
		}
	}
	if len(struck) > 8 {
		struck = struck[:8]
	}
	out[0] = float64(strikes)
	out[1] = events(raised)
	out[2] = float64(len(struck))
	for h, hit := range struck {
		out[3+h*3], out[4+h*3], out[5+h*3] = hit.X, hit.Y, hit.Z
	}
	send(out[:3+3*len(struck)], arguments[0])
	return strikes
}

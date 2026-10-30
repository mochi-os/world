// Mochi world: Battle tests - rounds on the curved earth
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// TestRoundsFallLevel: a burst fired level 2,000 km from Midway, in the world
// frame, drops below the bore and slows exactly as one fired level at
// Midway: it falls down the level up where it was fired, and its drag reads
// its own height above the sea; a round fired at the waves stops there.
func TestRoundsFallLevel(t *testing.T) {
	flown := func(at flight.Vec3, turn flight.Quat, level flight.Vec3, altitude float64) (drop, speed float64) {
		pose := Pose{Position: at, Forward: turn.Rotate(flight.Vec3{X: 1}), Up: turn.Rotate(flight.Vec3{Y: 1}), Right: turn.Rotate(flight.Vec3{Z: 1}),
			Velocity: turn.Rotate(flight.Vec3{X: 200}), Level: level, Altitude: altitude}
		r := Volley(pose, 1, 1, 1, 1)[0]
		r.Velocity = pose.Forward.Scale(Muzzle).Add(pose.Velocity) // the bore itself, no scatter
		for k := 0; k < 120; k++ {
			Fly(&r, 1.0/60)
		}
		return altitude - r.Height(), r.Velocity.Length()
	}
	far, level := flight.Home.Sea(2000000, -500000)
	up := level.Rotate(flight.Vec3{Y: 1})
	homeDrop, homeSpeed := flown(flight.Vec3{Y: 3000}, flight.Quat{W: 1}, flight.Vec3{Y: 1}, 3000)
	farDrop, farSpeed := flown(far.Add(up.Scale(3000)), level, up, 3000)
	if math.Abs(farDrop-homeDrop) > 0.01 || math.Abs(farSpeed-homeSpeed) > 0.01 {
		t.Errorf("2,000 km out the burst dropped %.3f m at %.2f m/s, at Midway %.3f m at %.2f m/s", farDrop, farSpeed, homeDrop, homeSpeed)
	}
	if homeDrop < 15 || homeDrop > 25 {
		t.Errorf("two seconds of flight dropped %.1f m, want about 20", homeDrop)
	}
	low := Pose{Position: far.Add(up.Scale(20)), Forward: level.Rotate(flight.Vec3{X: 1, Y: -0.5}.Normalize()), Up: up, Right: level.Rotate(flight.Vec3{Z: 1}), Level: up, Altitude: 20}
	r := Volley(low, 1, 1, 1, 1)[0]
	spent := false
	for k := 0; k < 60 && !spent; k++ {
		spent = Fly(&r, 1.0/60)
	}
	if !spent || r.Age > 0.2 {
		t.Errorf("a round fired down at the waves from 20 m flew %.2f s, spent %v", r.Age, spent)
	}
}

// Mochi world: non-finite input rejection (#174)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestPoisonInput: a client feeding NaN/±Inf control inputs must not corrupt
// the authoritative flight state. Without the number() guard, NaN passes
// clamp() (math.Min/Max propagate it) into Fcs and from there attitude,
// velocity and position — one datagram poisons the session.
func TestPoisonInput(t *testing.T) {
	finite := func(v flight.Vec3) bool {
		return !math.IsNaN(v.X) && !math.IsInf(v.X, 0) &&
			!math.IsNaN(v.Y) && !math.IsInf(v.Y, 0) &&
			!math.IsNaN(v.Z) && !math.IsInf(v.Z, 0)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		i := build(t, "furball", nil, 1)
		poison := map[string]any{
			"pitch": bad, "roll": bad, "yaw": bad,
			"throttle": bad, "reheat": bad, "speedbrake": bad, "sequence": bad,
		}
		for tick := uint64(0); tick < 60*3; tick++ {
			i.Step(tick, map[int][]game.Input{0: {{Sequence: 1, Data: poison}}})
		}
		s := &i.aircraft[0].model.State
		if !finite(s.Position) || !finite(s.Velocity) {
			t.Fatalf("input %v poisoned the state: pos %v vel %v", bad, s.Position, s.Velocity)
		}
		q := s.Attitude
		if math.IsNaN(q.W) || math.IsNaN(q.X) || math.IsNaN(q.Y) || math.IsNaN(q.Z) {
			t.Fatalf("input %v poisoned the attitude quaternion: %+v", bad, q)
		}
		if math.IsNaN(s.Fcs.Normal) || math.IsInf(s.Fcs.Normal, 0) {
			t.Fatalf("input %v poisoned the g meter: %v", bad, s.Fcs.Normal)
		}
	}
}

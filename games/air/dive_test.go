// Mochi world: Dive recovery
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestDiveRecovery: a bot pointed at the sea pulls out of it. No test covered
// that at all until a recorded joust found the gap the hard way — the ace rode
// a 77° nose-down dive from 4100 m into the water over fifteen seconds,
// rolling continuously and never establishing the pull.
//
// This guards the plain invariant, not that incident: with no one to fight,
// the energy floor engages at once and every tier recovers, so this passes
// against the code that produced the incident too. Reproducing it needs the
// threat geometry that had the brain calling for a zoom while the nose was
// 77° down, which is not yet isolated — see the dive note on #215.
func TestDiveRecovery(t *testing.T) {
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		for _, dive := range []float64{45, 60, 77, 85} {
			b := NewBandit(level, 5, 250000, "", false, false, "")
			const speed = 260.0
			down := math.Sin(dive * math.Pi / 180)
			along := math.Cos(dive * math.Pi / 180)
			b.Spawn(flight.Vec3{Y: 4000}, flight.Vec3{X: along * speed, Y: -down * speed})

			// A player far away and level: nothing to fight, so the only thing
			// under test is whether the jet gets its nose up before the water.
			env := flight.Environment{Seed: 5, Wrap: 250000}
			pm := flight.New(aircraft.Get("fa18c"), env, flight.World{Sea: sea})
			pm.State = flight.Level(pm, flight.Vec3{X: 9000, Y: 4000}, flight.Vec3{X: -1}, 220, fuel)
			words := make([]float64, flight.Size)

			lowest := 4000.0
			for tick := 0; tick < 60*40; tick++ {
				pm.State.Encode(words)
				b.Mirror(words, false, true)
				b.Step()
				y := b.craft.model.State.Position.Y
				if y < lowest {
					lowest = y
				}
				if y <= sea {
					t.Fatalf("%s from a %.0f° dive: flew into the sea at %.1f s",
						level, dive, float64(tick)/60)
				}
			}
			if lowest < 150 {
				t.Errorf("%s from a %.0f° dive: recovered but only %.0f m clear of the water",
					level, dive, lowest)
			}
		}
	}
}

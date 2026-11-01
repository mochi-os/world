// Mochi world: AIM-120 tests - the round on the curved earth
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package round

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// TestShotAnywhere: the same shot - 30 km head-on, then a beam target at 25
// km - flies the same time to the fuse at the same Mach at Midway and 2,000
// km away in the world frame, where Midway's y leans 18 degrees off the
// round's up: the round flies level under itself, wherever it is.
func TestShotAnywhere(t *testing.T) {
	far, level := scene.Sea(2000000, -500000)
	place := func(p flight.Vec3) flight.Vec3 { return far.Add(level.Rotate(p)) }
	for _, s := range []struct {
		name      string
		line, way flight.Vec3
		apart     float64
	}{{"head-on", east, west, 30000}, {"beam", east, south, 25000}} {
		calm := flight.Environment{Frame: scene, Chart: scene, Seed: 1}
		home, mach := shoot(calm, s.line, s.way, s.apart)
		shooter, target := duel(calm, s.line, s.way, s.apart)
		shooter.Position, shooter.Velocity = place(shooter.Position), level.Rotate(shooter.Velocity)
		target.Position, target.Velocity = place(target.Position), level.Rotate(target.Velocity)
		m := New(shooter.Position, shooter.Velocity, &Target{Position: target.Position, Velocity: target.Velocity}, calm, 0)
		away, there := -1.0, -1.0
		for m.Life > 0 {
			alive, fired, _ := m.Advance(dt, &target, &target)
			if fired {
				away, there = m.Time, m.Mach()
				break
			}
			if !alive {
				break
			}
			target.Position = target.Position.Add(target.Velocity.Scale(dt))
		}
		if home < 0 || math.Abs(away-home) > 0.05 || math.Abs(there-mach) > 0.01 {
			t.Errorf("%s: %.2f s to the fuse at Mach %.3f 2,000 km out, %.2f s at Mach %.3f at Midway", s.name, away, there, home, mach)
		}
	}
}

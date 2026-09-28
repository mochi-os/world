// Mochi world: Wake vortex session tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// trailing flies two jets at 150 m/s along +x through a real session: the
// leader ahead, the follower 10 s behind on the same line and at the same
// height, and aside by offset. Both settle a little down from their spawn
// trim, together, so where the follower flies the leader's wake has sunk
// under it by about as much as the leader was higher then: the follower
// rides a few metres over the pair's middle, in its downwash. It reports the
// most air the follower met at its CG and the largest roll rate it saw.
func trailing(t *testing.T, offset float64) (swirl float64, roll float64) {
	t.Helper()
	i := build(t, "furball", nil, 2)
	leader, follower := i.aircraft[0], i.aircraft[1]
	leader.model.State = flight.Level(leader.model, flight.Vec3{X: 1500, Y: 3000, Z: offset}, flight.Vec3{X: 1}, 150, 3000)
	follower.model.State = flight.Level(follower.model, flight.Vec3{Y: 3000}, flight.Vec3{X: 1}, 150, 3000)
	leader.latest = flight.Inputs{Throttle: leader.model.State.Engine[0].Spool}
	follower.latest = flight.Inputs{Throttle: follower.model.State.Engine[0].Spool}
	for tick := uint64(1); tick <= 60*14; tick++ {
		i.Step(tick, nil)
		swirl = math.Max(swirl, follower.model.Swirl().Length())
		roll = math.Max(roll, math.Abs(follower.model.State.Omega.X))
	}
	return swirl, roll
}

// TestSessionWake: in a session, a jet that flies into another's wake meets
// it, and one that stays clear of it meets nothing.
func TestSessionWake(t *testing.T) {
	inside, rolled := trailing(t, 0)
	clear, steady := trailing(t, 2000)
	t.Logf("behind the leader: up to %.2f m/s of wake at the CG, rolling up to %.1f°/s; 2 km aside: %.2f m/s, %.1f°/s",
		inside, rolled*180/math.Pi, clear, steady*180/math.Pi)
	if inside < 2 {
		t.Errorf("flying the leader's line 10 s behind, the follower met only %.2f m/s of wake", inside)
	}
	if clear != 0 {
		t.Errorf("2 km aside the follower met %.2f m/s of wake", clear)
	}
}

// TestSessionForget: a player who leaves takes their wake with them.
func TestSessionForget(t *testing.T) {
	i := build(t, "furball", nil, 2)
	stay, leaving := i.aircraft[0], i.aircraft[1]
	stay.model.State = flight.Level(stay.model, flight.Vec3{Y: 3000, Z: 20000}, flight.Vec3{X: 1}, 150, 3000)
	leaving.model.State = flight.Level(leaving.model, flight.Vec3{Y: 3000}, flight.Vec3{X: 1}, 150, 3000)
	leaving.latest = flight.Inputs{Throttle: leaving.model.State.Engine[0].Spool}
	for tick := uint64(1); tick <= 60*5; tick++ {
		i.Step(tick, nil)
	}
	behind := leaving.model.State.Position.Subtract(flight.Vec3{X: 300, Y: 3})
	if len(i.wakes.Near(9, behind, 5, 0, i.environment.Wrap)) == 0 {
		t.Fatal("no wake behind the jet before it left")
	}
	if n := i.wakes.Trails(); n != 2 {
		t.Fatalf("%d trails laid before the player left, want both jets'", n)
	}
	i.Leave(leaving.player)
	if pieces := i.wakes.Near(9, behind, 5, 0, i.environment.Wrap); len(pieces) != 0 {
		t.Errorf("the player left and %d pieces of their wake are still laid", len(pieces))
	}
	if n := i.wakes.Trails(); n != 1 {
		t.Errorf("%d trails laid after the player left, want the one who stayed", n)
	}
}

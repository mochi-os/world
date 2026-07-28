// Mochi world: Client bandit tests
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

// TestBanditSpawnTrimmed: the client bandit spawns ON trim — alpha carried by
// the attitude, honest gear sentinels — not the old nose-on-velocity literal
// (zero alpha), which handed the single player's gun target the same
// porpoise armour the Level sign fix removed everywhere else.
func TestBanditSpawnTrimmed(t *testing.T) {
	b := NewBandit("veteran", 1, 250000, "", false, false)
	b.Spawn(flight.Vec3{Y: 2000}, flight.Vec3{X: 200})
	s := &b.craft.model.State
	v := s.Attitude.Unrotate(s.Velocity)
	if alpha := math.Atan2(-v.Y, v.X); alpha < 0.01 {
		t.Fatalf("bandit spawned at %.2f° alpha — off trim", alpha*180/math.Pi)
	}
	if s.Gear.Catapult != -1 || s.Gear.Stroke != -1 || s.Gear.Wire != -1 || s.Gear.Contact != -1 {
		t.Fatalf("live-looking gear sentinels at spawn: %+v", s.Gear)
	}
	if s.Engine[0].Spool < 0.85 {
		t.Fatalf("merge-entry power lost: spool %.2f", s.Engine[0].Spool)
	}
}

// Mochi world: Client bandit tests
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

// TestBanditSpawnTrimmed: the client bandit spawns ON trim — alpha carried by
// the attitude, honest gear sentinels — not the old nose-on-velocity literal
// (zero alpha), which handed the single player's gun target the same
// porpoise armour the Level sign fix removed everywhere else.
func TestBanditSpawnTrimmed(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, false, "", 0)
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

// TestBanditBvr: the SP bandit flies the same BVR brain the server does —
// the open weapons class arms it, its radar acquires the mirrored player
// far beyond visual range, a DLZ shot leaves the rail, and the client's
// round reports (Menace stubs) gate the shoot-look-shoot discipline exactly
// as the server's own flying list does. The guns class stays byte-inert.
func TestBanditBvr(t *testing.T) {
	b := NewBandit("ace", 3, 250000, "", false, true, "open", 0)
	if b.craft.amraams == 0 {
		t.Fatal("open-class bandit spawned without AMRAAMs")
	}
	b.Spawn(flight.Vec3{X: 25000, Y: 6096}, flight.Vec3{X: -272})

	environment := flight.Environment{Seed: 3, Wrap: 250000}
	player := flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea})
	player.State = flight.Level(player, flight.Vec3{X: 0, Y: 6096}, flight.Vec3{X: 1}, 272, fuel)
	words := make([]float64, flight.Size)

	launched, stt := uint64(0), uint64(0)
	for tick := uint64(1); tick <= 60*120 && launched == 0; tick++ {
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(nil)
		_, _, launch, _, _ := b.Step()
		if b.craft.emitter == 2 && stt == 0 {
			stt = tick
		}
		if launch {
			launched = tick
		}
	}
	if stt == 0 {
		t.Fatal("the bandit never built an STT on a hot player 25 km out")
	}
	if launched == 0 {
		t.Fatal("the bandit never launched in two minutes with the player inside its DLZ")
	}
	if !b.Locked() {
		t.Fatal("Locked() false at the moment of a datalinked launch")
	}
	if b.Emitter() != 2 {
		t.Fatalf("Emitter() reads %d at launch; the RWR would miss the hard lock", b.Emitter())
	}

	// The look: while the client reports the bandit's own round in
	// midcourse, no second round may leave; once it reports pitbull, the
	// next shot frees. Eight words: position, velocity, shooter, phase.
	stub := func(phase float64) []float64 {
		return []float64{12000, 6000, 0, -800, 0, 0, 1, phase}
	}
	for tick := uint64(0); tick < 60*10; tick++ {
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(stub(0)) // Midcourse
		if _, _, launch, _, _ := b.Step(); launch {
			t.Fatal("a second round left the rail with the first still in midcourse: the look discipline is not reading the client's report")
		}
	}
	second := false
	for tick := uint64(0); tick < 60*20 && !second; tick++ {
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(stub(2)) // Pitbull: the supporter is free
		_, _, second, _, _ = b.Step()
	}
	if !second {
		t.Fatal("no follow-up shot after the client reported pitbull")
	}

	// The guns class: today's exact joust, nothing radiates.
	quiet := NewBandit("ace", 3, 250000, "", false, false, "guns", 0)
	quiet.Spawn(flight.Vec3{X: 8000, Y: 3000}, flight.Vec3{X: -272})
	for tick := 0; tick < 60*5; tick++ {
		player.State.Encode(words)
		quiet.Mirror(words, false, true)
		quiet.Menace(nil)
		quiet.Step()
		if quiet.craft.emitter != 0 {
			t.Fatalf("guns-class bandit radiating (emitter %d)", quiet.craft.emitter)
		}
	}
}

// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestRadarPoseWire: byte 34 of the pose record carries the emitter state.
// With the high bit set it is STT locked on the slot in the low seven bits;
// clear, the mode alone. A craft that never reported must encode silent, not a
// phantom lock on slot 0.
func TestRadarPoseWire(t *testing.T) {
	i := build(t, "furball", nil, 2)

	fresh := pose(0, i.aircraft[0])
	if len(fresh) != pose_record {
		t.Fatalf("pose record %d bytes, want %d", len(fresh), pose_record)
	}
	for _, want := range []struct {
		mode, target int
		byte         byte
		what         string
	}{{-9, 0, 0, "unreported"}, {2, 1, 0x80 | 1, "STT on 1"}, {1, -1, 1, "searching"}, {2, -1, 2, "STT holding nothing"}} {
		if want.mode >= 0 {
			i.Radar(0, want.mode, want.target)
		}
		if got := pose(0, i.aircraft[0])[34]; got != want.byte {
			t.Errorf("%s: emitter byte %#x, want %#x", want.what, got, want.byte)
		}
	}
}

// TestRadarValidation: the wire promises nothing — a lock may only name a
// present aircraft, only STT carries one, nonsense modes die, and bots (which
// never send the message) cannot be puppeted through it.
func TestRadarValidation(t *testing.T) {
	i := build(t, "furball", nil, 2)

	i.Radar(0, 2, 9) // slot 9 holds nothing
	if a := i.aircraft[0]; a.emitter != 2 || a.lock != -1 {
		t.Errorf("lock on an absent slot: emitter %d lock %d, want 2/-1", a.emitter, a.lock)
	}

	i.Radar(0, 1, 1) // search never carries a target
	if a := i.aircraft[0]; a.emitter != 1 || a.lock != -1 {
		t.Errorf("search with a target: emitter %d lock %d, want 1/-1", a.emitter, a.lock)
	}

	i.Radar(0, 7, 1) // nonsense mode: state unchanged
	if a := i.aircraft[0]; a.emitter != 1 || a.lock != -1 {
		t.Errorf("nonsense mode moved state: emitter %d lock %d", a.emitter, a.lock)
	}

	i.Radar(0, 2, slot_most+1) // beyond the pose record's seven bits, where no aircraft sits
	if a := i.aircraft[0]; a.lock != -1 {
		t.Errorf("lock %d accepted beyond the wire's slot space", a.lock)
	}
}

// TestRadarLocksAServerBot: a pilot's STT on a server bot is a lock. The bots
// sit from slot 99 down, above the six bits byte 34 once gave the locked slot,
// and there Radar refused the lock: the radar trigger launched nothing, the bot
// never saw itself spiked, and no other client could see the lock.
func TestRadarLocksAServerBot(t *testing.T) {
	joust := func() (*instance, *craft, int) {
		made, err := (&Air{}).Create(game.Session{Identifier: "spike", Game: "air", Mode: "joust", Seed: 1,
			Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{"expert": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		t.Cleanup(i.Close)
		if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0, Stores: quiver()}); err != nil {
			t.Fatal(err)
		}
		bot := -1
		for slot, a := range i.aircraft {
			if a != nil && a.brain != nil {
				bot = slot
			}
		}
		if bot < 63 {
			t.Fatalf("the bot sits at slot %d, want 63 or above", bot)
		}
		return i, i.aircraft[0], bot
	}

	i, me, bot := joust()
	i.Radar(0, 2, bot)
	if me.emitter != 2 || me.lock != bot {
		t.Fatalf("STT on the bot at slot %d: emitter %d lock %d", bot, me.emitter, me.lock)
	}
	if got := pose(0, me)[34]; got != 0x80|byte(bot) {
		t.Errorf("STT on slot %d: emitter byte %#x, want %#x", bot, got, 0x80|byte(bot))
	}
	if !i.fox3(0, me, false) {
		t.Fatalf("the radar trigger launched nothing with STT on the bot at slot %d", bot)
	}
	if m := i.flying[len(i.flying)-1]; m.radar == nil || m.target != bot {
		t.Errorf("the round left after slot %d, want an AIM-120 after the bot at %d", m.target, bot)
	}

	// Spiked 15 km off, beyond the merge seam, with no round of its own to crank
	// for, the expert flies the offset.
	i, me, bot = joust()
	b, brain := i.aircraft[bot], i.aircraft[bot].brain
	b.amraams = 0
	b.model.State.Position, b.model.State.Velocity, b.model.State.Attitude = flight.Vec3{Y: 3000}, flight.Vec3{X: 250}, flight.Quat{W: 1}
	park(me, flight.Vec3{X: 15000, Y: 3000}, flight.Vec3{X: -250})
	brain.target = 0
	brain.known[0] = &track{when: 120, position: me.model.State.Position, velocity: me.model.State.Velocity}
	i.Radar(0, 2, bot)
	i.hunt(bot, b, 120)
	if brain.cranked != 120 {
		t.Errorf("spiked by the pilot's STT at 15 km, the bot at slot %d did not crank", bot)
	}
}

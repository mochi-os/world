// Mochi world: Roster and slot occupancy
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"testing"

	"world/game"
)

// TestRosterSurvivesJoins (#257): a creator's practice bots must still be there
// after the humans arrive. Bots fill the slot space downward from 99 and the
// server assigns players upward from 0 out of a map that cannot see them.
func TestRosterSurvivesJoins(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "roster", Game: "air", Mode: "furball", Capacity: 8, Seed: 1,
		Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 6.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	bots := func() int {
		n := 0
		for _, a := range i.aircraft {
			if a != nil && a.bot {
				n++
			}
		}
		return n
	}
	if bots() != 6 {
		t.Fatalf("roster of 6 created %d bots", bots())
	}
	// Join the way the server does: the lowest slot free of a player AND of
	// anything the game placed itself.
	players := map[int]bool{}
	for n := 0; n < 8; n++ {
		slot := 0
		for players[slot] || i.Occupied(slot) {
			slot++
		}
		if _, err := i.Join(game.Player{Name: fmt.Sprintf("p%d", n), Slot: slot}); err != nil {
			t.Fatalf("join %d refused: %v", n, err)
		}
		players[slot] = true
		if bots() != 6 {
			t.Fatalf("join %d (slot %d) destroyed a bot: %d left of 6", n, slot, bots())
		}
	}
	// And the departures give their slots back without taking a bot along.
	for slot := range players {
		i.Leave(game.Player{Slot: slot})
	}
	if bots() != 6 {
		t.Fatalf("the players leaving took %d bots with them", 6-bots())
	}
}

// TestRosterSitsAboveThePlayers: a bot must never occupy a slot a joining
// player can be given, whatever the capacity or roster size, and the
// 99-downward numbering must survive ordinary sessions - the wire reads it that
// way. The roster is bounded above as well as below: every slot has to stay
// inside the seven bits the missile record gives the shooter (#477), and since
// the bots sit ABOVE the players that ceiling comes off the bot count.
func TestRosterSitsAboveThePlayers(t *testing.T) {
	for _, c := range []struct {
		capacity int
		asked    float64
		bots     int
		highest  int // the classic top slot, where it still fits
	}{
		{8, 6, 6, 99},      // the ordinary session: unchanged, bots 99..94
		{100, 2, 2, 101},   // a full-house capacity pushes the bots above it
		{120, 16, 8, 127},  // a large one takes only what is left below slot 128
		{128, 16, 0, -1},   // a capacity filling the wire ceiling leaves room for none
		{8, 99, 99, 106},   // the maximum roster in a small session
		{16, 200, 99, 114}, // an over-large request is still capped at 99 bots
	} {
		// Each case is its own server as far as the bot budget is concerned:
		// bots are reserved at Create and released by Close, which only the
		// server calls, so a test discarding instances would otherwise see the
		// budget drain case by case and the later rosters come up short.
		bots_live.Store(0)
		g := New()
		made, err := g.Create(game.Session{Identifier: "above", Game: "air", Mode: "furball",
			Capacity: c.capacity, Seed: 1,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": c.asked}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		n, lowest, highest := 0, 1<<30, -1
		for slot, a := range i.aircraft {
			if a == nil || !a.bot {
				continue
			}
			n++
			if slot < lowest {
				lowest = slot
			}
			if slot > highest {
				highest = slot
			}
		}
		if n != c.bots {
			t.Errorf("capacity %d asked %.0f: created %d bots, want %d", c.capacity, c.asked, n, c.bots)
			continue
		}
		if lowest < c.capacity {
			t.Errorf("capacity %d: a bot sits at slot %d, where a player can be seated", c.capacity, lowest)
		}
		if highest != c.highest {
			t.Errorf("capacity %d asked %.0f: top bot at slot %d, want %d", c.capacity, c.asked, highest, c.highest)
		}
	}
}

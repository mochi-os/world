// Mochi world: slot reuse and stale orders (#186, #187)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"testing"

	"world/game"
)

// slotSession is a session ready to drain orders, with `held` occupying slot 0.
// session_orders runs on the caller's goroutine and returns once the inbox is
// empty, so every test here is deterministic - no ticking, no sleeping.
func slotSession(inst game.Instance, held link) *session {
	s := bareSession(inst, 4)
	s.inbox = make(chan order, 8)
	s.done = make(chan struct{})
	s.players[0] = &player{Player: game.Player{Slot: 0}, link: held}
	return s
}

// TestStaleLeaveSparesNewOccupant: a reader stamps its slot at connect, so the
// leave queued from its defer can drain after the freed slot was refilled. It
// must not evict whoever holds the slot now.
func TestStaleLeaveSparesNewOccupant(t *testing.T) {
	inst := &fakeInstance{}
	gone, current := silent_link(), silent_link()
	s := slotSession(inst, current)

	s.inbox <- order{kind: "leave", slot: 0, link: gone}
	session_orders(s)

	if s.players[0] == nil {
		t.Fatal("a stale leave evicted the slot's new occupant")
	}
	if s.players[0].link != current {
		t.Fatalf("slot 0 holds the wrong link after a stale leave")
	}
	if inst.left != 0 {
		t.Fatalf("Instance.Leave ran for a player who never left: %d", inst.left)
	}
}

// TestLeaveRemovesOwnPlayer is the control for the test above: the check must
// not have made leave inert. A leave from the CURRENT occupant still removes.
func TestLeaveRemovesOwnPlayer(t *testing.T) {
	inst := &fakeInstance{}
	current := silent_link()
	s := slotSession(inst, current)

	s.inbox <- order{kind: "leave", slot: 0, link: current}
	session_orders(s)

	if s.players[0] != nil {
		t.Fatal("a player's own leave did not remove them")
	}
	if inst.left != 1 {
		t.Fatalf("Instance.Leave did not run for a genuine leave: %d", inst.left)
	}
}

// TestStaleInputSparesNewOccupant (#187): the same stale order carrying inputs
// would fly the new occupant's aircraft.
func TestStaleInputSparesNewOccupant(t *testing.T) {
	gone, current := silent_link(), silent_link()
	s := slotSession(&fakeInstance{}, current)

	s.inbox <- order{kind: "input", slot: 0, link: gone, inputs: []game.Input{{Sequence: 1}}}
	session_orders(s)

	if queued := len(s.players[0].queue); queued != 0 {
		t.Fatalf("a stale input was applied to the new occupant: %d queued", queued)
	}
}

// TestInputAppliesFromOwnPlayer is the control for the test above.
func TestInputAppliesFromOwnPlayer(t *testing.T) {
	current := silent_link()
	s := slotSession(&fakeInstance{}, current)

	s.inbox <- order{kind: "input", slot: 0, link: current, inputs: []game.Input{{Sequence: 1}}}
	session_orders(s)

	if queued := len(s.players[0].queue); queued != 1 {
		t.Fatalf("the occupant's own input was dropped: %d queued", queued)
	}
}

// TestLinklessOrderStillApplies: nothing in connection.go sends an order with
// no link, but the join tests build orders that way and a future producer
// might. Such an order is honoured on the slot alone rather than silently
// dropped, which is the pre-existing behaviour.
func TestLinklessOrderStillApplies(t *testing.T) {
	inst := &fakeInstance{}
	s := slotSession(inst, silent_link())

	s.inbox <- order{kind: "leave", slot: 0}
	session_orders(s)

	if s.players[0] != nil {
		t.Fatal("a linkless leave was dropped; it should fall back to the slot")
	}
}

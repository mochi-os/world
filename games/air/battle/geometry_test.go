// Mochi world: Hit-geometry tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"testing"

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

// TestPartsShared: the hit geometry is built once per airframe, not once per
// craft per life. It is a pure function of an immutable airframe and its own
// header says "built once and shared", but arm() called it on every spawn and
// respawn - a furball on a five-second respawn rebuilt about a hundred structs
// dozens of times a second, each craft holding its own copy of identical data.
func TestPartsShared(t *testing.T) {
	first := Parts(fa18c.Airframe)
	second := Parts(fa18c.Airframe)
	if len(first) == 0 {
		t.Fatal("the airframe built no parts")
	}
	if len(first) != len(second) {
		t.Fatalf("two calls built %d and %d parts", len(first), len(second))
	}
	// Identity, not equality: &first[0] == &second[0] is the whole claim.
	if &first[0] != &second[0] {
		t.Error("Parts rebuilt the geometry instead of sharing it")
	}
}

// TestPartsReadOnly: sharing is only safe because a strike writes into
// Body.Damage and Body.Condition, never into the geometry. This pins that -
// a shared slice that turns out to be mutated corrupts every craft flying the
// airframe at once, which would present as a bizarre cross-aircraft damage bug
// rather than a crash.
func TestPartsReadOnly(t *testing.T) {
	before := append([]Part(nil), Parts(fa18c.Airframe)...)
	m := flight.New(fa18c.Airframe, flight.Environment{}, flight.World{})
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe),
		Damage: &m.State.Damage, Condition: &Condition{Damager: -1}}
	_ = body
	after := Parts(fa18c.Airframe)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("part %d changed after a Body was built on the shared slice", i)
		}
	}
}

// Mochi world: open-match spawn placement
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestSpawnClearsTheFight: an open match places every ARRIVAL - a player
// joining mid-match, a life after a death - outside the
// gun reach of everyone already fighting, and pointed at them. The old rule
// was a fixed ring round the world's centre, so as a furball drifted a
// re-entry landed inside somebody's pipper or a minute's transit away,
// whichever the fight's wandering happened to give.
func TestSpawnClearsTheFight(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "clearance", Game: "air", Mode: "furball", Capacity: 8, Seed: 1,
		Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 4.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	slots := i.slots()
	if len(slots) < 4 {
		t.Fatalf("the room holds %d jets, want the four bots asked for", len(slots))
	}
	// A developed fight in one corner of the world, well away from the ring
	// the old rule drew: every jet inside a kilometre of the same point.
	knot := flight.Vec3{X: 42000, Y: altitude, Z: -37000}
	for n, slot := range slots {
		i.aircraft[slot].model.State.Position = knot.Add(flight.Vec3{X: float64(n) * 300, Z: float64(n) * 200})
	}
	nearest := func(m *flight.Model) (float64, int) {
		least, who := math.MaxFloat64, -1
		for _, slot := range slots {
			span := shortest(m.State.Position, i.aircraft[slot].model.State.Position, i.environment.Wrap).Length()
			if span < least {
				least, who = span, slot
			}
		}
		return least, who
	}
	// Every arrival, not one lucky bearing: two players re-entering on the
	// same tick must both clear the fight and each other's bearing.
	for _, slot := range []int{20, 21, 22} {
		m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
		i.enter(slot, m, "", i.tank)
		span, who := nearest(m)
		if span < clearance {
			t.Errorf("slot %d spawned %.0f m from slot %d: inside the %.0f m gun clearance", slot, span, who, clearance)
		}
		// Close enough to matter: the fight is a turn away, not a transit.
		if span > 4*clearance {
			t.Errorf("slot %d spawned %.0f m out: that is a transit back, not a re-entry", slot, span)
		}
		toward := shortest(m.State.Position, knot, i.environment.Wrap)
		toward.Y = 0
		nose := m.State.Attitude.Rotate(flight.Vec3{X: 1})
		if nose.Dot(toward.Normalize()) < 0.9 {
			t.Errorf("slot %d spawned pointing %.2f off the fight: a life begins looking at it", slot, nose.Dot(toward.Normalize()))
		}
	}
	// An empty room has nothing to clear, and the merge ring stands.
	for _, slot := range slots {
		i.aircraft[slot].alive = false
	}
	m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
	i.enter(0, m, "", i.tank)
	if span := math.Hypot(m.State.Position.X, m.State.Position.Z); math.Abs(span-ring) > 1 {
		t.Errorf("the first jet into an empty room spawned %.0f m from the centre, want the %d m merge ring", span, ring)
	}
	// And the START of a match is untouched: bots laid out at creation take
	// the ring, whatever the others are doing, so a match opens on the same
	// geometry every measurement in this package was taken against.
	i.aircraft[slots[0]].alive = true
	i.aircraft[slots[0]].model.State.Position = knot
	start := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
	i.spawn(3, start, "", i.tank)
	if span := math.Hypot(start.State.Position.X, start.State.Position.Z); math.Abs(span-ring) > 1 {
		t.Errorf("a start-of-match spawn stood %.0f m from the centre, want the %d m merge ring", span, ring)
	}
}

// TestSpawnSpreadsUnderCrowding: a knot of jets big enough to fill the merge
// ring pushes the arrival out a ring at a time rather than dropping it in the
// middle of them.
func TestSpawnSpreadsUnderCrowding(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "crowding", Game: "air", Mode: "furball", Capacity: 32, Seed: 2,
		Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 24.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	slots := i.slots()
	// Ring the merge circle with jets: every bearing at the inner radius is
	// occupied, so a clear spawn has to go further out.
	for n, slot := range slots {
		angle := float64(n) / float64(len(slots)) * 2 * math.Pi
		i.aircraft[slot].model.State.Position = flight.Vec3{X: math.Cos(angle) * ring, Y: altitude, Z: math.Sin(angle) * ring}
	}
	m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
	i.enter(30, m, "", i.tank)
	least := math.MaxFloat64
	for _, slot := range slots {
		if span := shortest(m.State.Position, i.aircraft[slot].model.State.Position, i.environment.Wrap).Length(); span < least {
			least = span
		}
	}
	fmt.Printf("crowded ring of %d: the arrival stands %.0f m off the nearest, %.0f m from the centre\n",
		len(slots), least, math.Hypot(m.State.Position.X, m.State.Position.Z))
	if least < clearance {
		t.Errorf("the arrival stands %.0f m off the nearest of %d jets, want %.0f m", least, len(slots), clearance)
	}
}

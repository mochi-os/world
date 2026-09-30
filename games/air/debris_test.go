// Mochi world: Air debris session tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"
)

// cross holds b in the wreckage of the jet that fell `at`, crossing it at 200 m/s
// over the pieces' own speed, for half a second of ticks, and returns the hits
// raised on it and the damage it took.
func cross(i *instance, b *craft, at uint64) (int, float64) {
	hits := 0
	for tick := at + 1; tick <= at+30; tick++ {
		i.stepped = tick
		centre, _, moving, alive := i.debris[0].Cloud(float64(tick) / 60)
		if !alive {
			break
		}
		b.model.State.Position = centre
		b.model.State.Velocity = moving
		b.model.State.Velocity.X += 200
		i.events = nil
		i.scatter(1.0/60, tick)
		hits += len(events(i, "hit", 1))
	}
	damage := b.body.Damage.Drag
	for _, e := range b.body.Damage.Element {
		damage += e
	}
	return hits, damage
}

func TestSessionDebris(t *testing.T) {
	i, a, b := pair(t, "furball")
	fell := a.model.State.Position
	i.stepped = 600
	i.kill(0, -1)
	if len(i.debris) != 1 || i.debris[0].Origin != fell || i.debris[0].Time != 10 {
		t.Fatalf("the kill lays its wreckage where the jet fell, on the session's clock: %+v", i.debris)
	}
	hits, damage := cross(i, b, 600)
	if hits == 0 || damage == 0 {
		t.Fatalf("a jet held in fresh wreckage is struck: %d hits, %.3f damage", hits, damage)
	}
}

func TestSessionDebrisCheat(t *testing.T) {
	i, _, b := pair(t, "furball")
	i.cheat.invulnerable = true
	i.stepped = 600
	i.kill(0, -1)
	if hits, damage := cross(i, b, 600); hits != 0 || damage != 0 {
		t.Fatalf("pieces pass through a human under the invulnerable cheat: %d hits, %.3f damage", hits, damage)
	}
}

func TestSessionDebrisExpires(t *testing.T) {
	i, _, _ := pair(t, "furball")
	i.stepped = 600
	i.kill(0, -1)
	i.Step(600+6*60, nil) // past the cloud's life: the tick's own scatter lets it go
	if len(i.debris) != 0 {
		t.Fatalf("the wreckage is forgotten once it can no longer strike: %d clouds", len(i.debris))
	}
}

// TestSessionDebrisTrail flies it whole, through the session's own Step: a
// jet in trail 40 m behind one that falls overtakes its slowing wreckage and
// crosses it, about a second old. A single crossing is struck about half the
// time, so ten crossings at different ticks must see strikes no one fired.
func TestSessionDebrisTrail(t *testing.T) {
	struck := 0
	for trial := uint64(0); trial < 10; trial++ {
		i, a, b := pair(t, "furball")
		posed(a, a.model.State.Position, 0, 200)
		posed(b, a.model.State.Position.Subtract(a.model.State.Velocity.Normalize().Scale(40)), 0, 200)
		at := 600 + 1000*trial
		i.stepped = at
		i.kill(0, -1)
		for tick := at + 1; tick <= at+120; tick++ {
			i.events = nil
			i.Step(tick, nil)
			for _, e := range events(i, "hit", 1) {
				if e["by"] == -1 {
					struck++
				}
			}
		}
	}
	if struck < 2 {
		t.Fatalf("ten jets in trail crossed a fallen jet's wreckage and took %d strikes", struck)
	}
}

// Mochi world: aircraft meeting in the air
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/battle"
	"world/games/air/flight"
)

// posed puts a craft level at a heading (about the vertical) and a speed.
func posed(c *craft, position flight.Vec3, heading float64, speed float64) {
	s := &c.model.State
	s.Attitude = flight.Quat{W: math.Cos(heading / 2), Y: math.Sin(heading / 2)}
	s.Position = position
	s.Velocity = s.Attitude.Rotate(flight.Vec3{X: speed})
}

// span is how far the wing elements reach from the centreline.
func span(c *craft) float64 {
	reach := 0.0
	for _, p := range c.body.Parts {
		if p.Kind == battle.Structure && c.body.Airframe.Surfaces[p.Surface].Kind == flight.Wing {
			reach = math.Max(reach, math.Max(math.Abs(p.A.Z), math.Abs(p.B.Z)))
		}
	}
	return reach
}

// length is how far the body reaches ahead of the origin and behind it.
func length(c *craft) (ahead, behind float64) {
	for _, p := range c.body.Parts {
		if p.Kind == battle.Structure || p.Kind == battle.Ordnance || p.Kind == battle.Gear {
			continue
		}
		ahead = math.Max(ahead, math.Max(p.A.X, p.B.X)+p.Radius)
		behind = math.Max(behind, -math.Min(p.A.X, p.B.X)+p.Radius)
	}
	return
}

// wrecks counts the wings a craft has shed: surfaces whose outboard elements
// are all gone.
func wrecks(c *craft) int {
	torn, base := 0, 0
	for _, s := range c.body.Airframe.Surfaces {
		if s.Kind == flight.Wing && c.body.Damage.Element != nil {
			gone := true
			for ei := len(s.Elements) / 2; ei < len(s.Elements); ei++ {
				if c.body.Damage.Element[base+ei] < 1 {
					gone = false
				}
			}
			if gone {
				torn++
			}
		}
		base += len(s.Elements)
	}
	return torn
}

// events counts the wire events of a kind for a slot (-1: any slot).
func events(i *instance, kind string, slot int) (found []map[string]any) {
	for _, e := range i.events {
		if e["kind"] == kind && (slot < 0 || e["slot"] == slot) {
			found = append(found, e)
		}
	}
	return
}

// pair is a two-player match with both jets level at 3,000 m, the events
// cleared so a test reads only what its own tick raised.
func pair(t *testing.T, mode string) (*instance, *craft, *craft) {
	t.Helper()
	i := build(t, mode, nil, 2)
	a, b := i.aircraft[0], i.aircraft[1]
	if a == nil || b == nil || a.model == nil || b.model == nil {
		t.Fatalf("two jets expected in the air")
	}
	i.events = nil
	return i, a, b
}

// TestMidairHeadOn: two fuselages through each other is a break-up for both,
// credited to nobody - each jet's death counts, no kill does - and the kill
// events name the cause and the other jet.
func TestMidairHeadOn(t *testing.T) {
	i, a, b := pair(t, "furball")
	posed(a, flight.Vec3{Y: 3000}, 0, 200)
	posed(b, flight.Vec3{X: 10, Y: 3000}, math.Pi, 200)
	i.Step(1, nil)
	if a.alive || b.alive {
		t.Fatalf("head-on: alive %v %v", a.alive, b.alive)
	}
	if a.deaths != 1 || b.deaths != 1 || a.kills != 0 || b.kills != 0 {
		t.Errorf("a midair is nobody's kill: kills %d %d, deaths %d %d", a.kills, b.kills, a.deaths, b.deaths)
	}
	killed := events(i, "kill", -1)
	if len(killed) != 2 {
		t.Fatalf("kill events: %d, want 2", len(killed))
	}
	for _, e := range killed {
		other := 1 - e["slot"].(int)
		if e["cause"] != "midair" || e["by"] != -1 || e["other"] != other {
			t.Errorf("kill event %v: want cause midair, by -1, other %d", e, other)
		}
	}
}

// TestMidairClip: wingtips overlapping by half a metre tear the outboard wing
// off each jet, and both fly on. The next tick, the stumps overlapping still,
// nothing new tears: a stump is a stump.
func TestMidairClip(t *testing.T) {
	i, a, b := pair(t, "furball")
	posed(a, flight.Vec3{Y: 3000}, 0, 200)
	posed(b, flight.Vec3{Y: 3000, Z: 2*span(a) - 0.5}, 0, 200)
	i.Step(1, nil)
	if !a.alive || !b.alive {
		t.Fatalf("a clip is flown home from: alive %v %v", a.alive, b.alive)
	}
	for slot, c := range []*craft{a, b} {
		if wrecks(c) != 1 || c.body.Damage.Loss < 300 {
			t.Errorf("jet %d: %d wings shed, %.0f kg lost; want one wing", slot, wrecks(c), c.body.Damage.Loss)
		}
		if len(events(i, "shed", slot)) != 1 {
			t.Errorf("jet %d: %d shed events, want 1", slot, len(events(i, "shed", slot)))
		}
	}
	if len(events(i, "kill", -1)) != 0 {
		t.Errorf("a clip kills nobody: %v", events(i, "kill", -1))
	}
	i.events = nil
	i.Step(2, nil)
	if !a.alive || !b.alive || len(events(i, "shed", -1)) != 0 || a.body.Damage.Loss != 300 || b.body.Damage.Loss != 300 {
		t.Errorf("the stumps met again: alive %v %v, shed %d, lost %.0f %.0f", a.alive, b.alive, len(events(i, "shed", -1)), a.body.Damage.Loss, b.body.Damage.Loss)
	}
}

// TestMidairInboard: a wingtip driven into the other jet's wing root - the
// spar and the wing tank - is a break-up for the wing's owner, while the tip
// that did it is a clip: its owner sheds it and flies on. Two straight wings
// abreast overlap each other's roots alike, so the asymmetric case is a
// knife-edge jet dropping its low wingtip through the wing of the one beneath
// it, its own body a wingspan above.
func TestMidairInboard(t *testing.T) {
	i, a, b := pair(t, "furball")
	posed(b, flight.Vec3{Y: 3000}, 0, 200)
	posed(a, flight.Vec3{Y: 3000 + span(a) - 0.2, Z: -0.375 * span(a)}, 0, 200)
	a.model.State.Attitude = flight.Quat{W: math.Cos(math.Pi / 4), X: math.Sin(math.Pi / 4)} // right wing down
	i.Step(1, nil)
	if b.alive {
		t.Errorf("a tip through the wing root: the wing's owner flies on")
	}
	if !a.alive || wrecks(a) != 1 {
		t.Errorf("the tip's owner: alive %v, wings shed %d; want flying on one wing", a.alive, wrecks(a))
	}
}

// TestMidairTrail: a jet run into from behind loses its tail or worse; the
// jet that ran into it loses its nose.
func TestMidairTrail(t *testing.T) {
	i, a, b := pair(t, "furball")
	ahead, behind := length(a)
	posed(a, flight.Vec3{Y: 3000}, 0, 200)
	posed(b, flight.Vec3{X: -(ahead + behind - 2), Y: 3000}, 0, 200)
	i.Step(1, nil)
	if b.alive {
		t.Errorf("the nose that hit is a break-up: alive")
	}
	if a.alive && a.body.Damage.Loss == 0 {
		t.Errorf("the tail that was hit is whole")
	}
}

// TestMidairNearMiss: eight metres between the wingtips is a miss - no
// damage, no events, nothing on the wire.
func TestMidairNearMiss(t *testing.T) {
	i, a, b := pair(t, "furball")
	posed(a, flight.Vec3{Y: 3000}, 0, 200)
	posed(b, flight.Vec3{Y: 3000, Z: 2*span(a) + 8}, math.Pi, 200)
	i.Step(1, nil)
	if !a.alive || !b.alive || a.body.Damage.Loss != 0 || b.body.Damage.Loss != 0 || len(i.events) != 0 {
		t.Errorf("near miss: alive %v %v, lost %.0f %.0f, events %v", a.alive, b.alive, a.body.Damage.Loss, b.body.Damage.Loss, i.events)
	}
}

// TestMidairSwept: wingtips crossing head-on at 600 kt of closure have passed
// each other by the end of the tick - only the sweep back along the motion
// sees the clip.
func TestMidairSwept(t *testing.T) {
	i, a, b := pair(t, "furball")
	posed(a, flight.Vec3{Y: 3000}, 0, 300)
	posed(b, flight.Vec3{X: 5, Y: 3000, Z: 2*span(a) - 0.5}, math.Pi, 300)
	i.Step(1, nil)
	if a.model.State.Position.X <= b.model.State.Position.X+3 {
		t.Fatalf("the jets have not passed: x %.1f %.1f", a.model.State.Position.X, b.model.State.Position.X)
	}
	if !a.alive || !b.alive || wrecks(a) != 1 || wrecks(b) != 1 {
		t.Errorf("crossing clip: alive %v %v, wings shed %d %d; want both flying on one wing each", a.alive, b.alive, wrecks(a), wrecks(b))
	}
}

// TestMidairJoust: a joust that ends in a midair has no winner.
func TestMidairJoust(t *testing.T) {
	i, a, b := pair(t, "joust")
	posed(a, flight.Vec3{Y: 3000}, 0, 200)
	posed(b, flight.Vec3{X: 10, Y: 3000}, math.Pi, 200)
	i.Step(1, nil)
	done, results := i.Finished()
	if !done || results["winner"] != -1 || results["loser"] != -1 {
		t.Errorf("joust midair: done %v, results %v; want no winner", done, results)
	}
}

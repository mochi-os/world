// Mochi world: the match carries its map's scenery
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestSceneryCarried: a match loads Midway's collision geometry - islands,
// strips, buildings, masts, the carrier - names it with its hash in the
// welcome, and serves the same bytes to the lobby under a guarded name.
func TestSceneryCarried(t *testing.T) {
	i := build(t, "furball", nil, 1)
	c := i.chart
	if c == nil || c.name != "midway" || len(c.hash) != 64 {
		t.Fatalf("chart %+v", c)
	}
	w := &c.world
	if len(w.Prisms) < 50 || len(w.Posts) < 5 || len(w.Fields) < 2 || w.Carrier == nil || w.Sea != sea { // the file carries the sea the server flies: nothing is overridden, so the served copy is the flown one
		t.Errorf("midway: %d prisms, %d posts, %d fields, carrier %v, sea %v", len(w.Prisms), len(w.Posts), len(w.Fields), w.Carrier != nil, w.Sea)
	}
	spawn, err := i.Join(game.Player{Name: "q", Slot: 3})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	named, _ := spawn["map"].(map[string]any)
	if named["name"] != "midway" || named["hash"] != c.hash {
		t.Errorf("welcome map %v, want midway %s", named, c.hash)
	}
	body, found := New().Map("midway")
	sum := sha256.Sum256(body)
	if !found || hex.EncodeToString(sum[:]) != c.hash {
		t.Errorf("the lobby's bytes hash %x, the welcome says %s", sum[:6], c.hash[:12])
	}
	for _, bad := range []string{"", "../midway", "Midway", "midway.json", "nowhere"} {
		if _, found := New().Map(bad); found {
			t.Errorf("map %q served", bad)
		}
	}
}

// biggest is the prism with the largest footprint and its centroid.
func biggest(w *flight.World) (flight.Prism, flight.Vec3) {
	var best flight.Prism
	area, centre := -1.0, flight.Vec3{}
	for _, p := range w.Prisms {
		a, cx, cz := 0.0, 0.0, 0.0
		for k := range p.Outline {
			q, r := p.Outline[k], p.Outline[(k+1)%len(p.Outline)]
			a += q.X*r.Z - r.X*q.Z
			cx += q.X
			cz += q.Z
		}
		if a = math.Abs(a) / 2; a > area {
			area, best = a, p
			centre = flight.Vec3{X: cx / float64(len(p.Outline)), Z: cz / float64(len(p.Outline))}
		}
	}
	return best, centre
}

// TestSceneryStrike: a jet flown through the biggest building on the map dies
// by the surface rule; ten metres over its roof it flies on; the tallest mast
// takes its nose off; and the sea still ends a flight at three metres.
func TestSceneryStrike(t *testing.T) {
	i := build(t, "furball", nil, 1)
	a := i.aircraft[0]
	hangar, centre := biggest(&i.chart.world)
	posed(a, flight.Vec3{X: centre.X, Y: hangar.Top - 1, Z: centre.Z}, 0, 100)
	i.events = nil
	i.Step(1, nil)
	if a.alive || len(events(i, "kill", 0)) != 1 {
		t.Errorf("through the hangar: alive %v, kills %d", a.alive, len(events(i, "kill", 0)))
	}

	i = build(t, "furball", nil, 1)
	a = i.aircraft[0]
	posed(a, flight.Vec3{X: centre.X, Y: hangar.Top + 12, Z: centre.Z}, 0, 100)
	i.Step(1, nil)
	if !a.alive {
		t.Errorf("over the hangar's roof: dead")
	}

	i = build(t, "furball", nil, 1)
	a = i.aircraft[0]
	var mast flight.Post
	for _, p := range i.chart.world.Posts {
		if p.Top > mast.Top {
			mast = p
		}
	}
	ahead, _ := length(a)
	posed(a, flight.Vec3{X: mast.Position.X - ahead + 0.5, Y: mast.Top - 1, Z: mast.Position.Z}, 0, 100)
	i.Step(1, nil)
	if a.alive {
		t.Errorf("nose on the mast at %.0f,%.0f: alive", mast.Position.X, mast.Position.Z)
	}

	i = build(t, "furball", nil, 1)
	a = i.aircraft[0]
	posed(a, flight.Vec3{X: -20000, Y: 2, Z: -20000}, 0, 100)
	i.Step(1, nil)
	if a.alive {
		t.Errorf("at two metres over open sea: alive")
	}
}

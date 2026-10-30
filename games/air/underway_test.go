// Mochi world: Air game module tests - the carrier under way in a match
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
	"world/games/air/ship"
)

// underway_match opens a furball with two practice bots and one player.
func underway_match(t *testing.T) (*instance, map[string]any) {
	t.Helper()
	made, err := New().Create(game.Session{Identifier: "underway", Game: "air", Mode: "furball", Capacity: 8, Seed: 1,
		Parameters: map[string]any{"missiles": false, "bots": map[string]any{"novice": 2.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	t.Cleanup(i.Close)
	for tick := uint64(1); tick <= 30; tick++ {
		i.Step(tick, nil)
	}
	welcome, err := i.Join(game.Player{Name: "pilot", Slot: 0})
	if err != nil {
		t.Fatal(err)
	}
	return i, welcome
}

// TestMatchUnderWay: a match's carrier is under way from its start, every jet
// in it flies against that one carrier, the welcome carries the track, and
// every jet keeps the session's clock - so all of them, and the clients that
// predict them, meet the deck in the same place.
func TestMatchUnderWay(t *testing.T) {
	i, welcome := underway_match(t)
	c := i.chart.world.Carrier
	if c == nil || len(c.Track) == 0 || c.Track[0].Speed <= 0 {
		t.Fatalf("the match's carrier is not under way: %+v", c)
	}
	voyage, _ := welcome["ship"].(map[string]any)
	legs, _ := voyage["track"].([][]float64)
	if len(legs) != len(c.Track) || voyage["into"] != i.ship.Into {
		t.Fatalf("the welcome's ship %v does not carry the track %+v", welcome["ship"], c.Track)
	}
	for n, l := range c.Track {
		want := []float64{l.Start, l.Position.X, l.Position.Z, l.Heading, l.Speed, l.Turn, l.Change}
		if len(legs[n]) != len(want) {
			t.Fatalf("leg %d goes out as %v, want %v", n, legs[n], want)
		}
		for k := range want {
			if legs[n][k] != want[k] {
				t.Fatalf("leg %d goes out as %v, want %v", n, legs[n], want)
			}
		}
	}
	joined := i.aircraft[0].model.State.Time
	if joined != 31.0/60 {
		t.Fatalf("a jet joining after tick 30 starts on %.4f s, want the next tick's %.4f", joined, 31.0/60)
	}
	for tick := uint64(31); tick <= 200; tick++ {
		if tick == 100 {
			i.aircraft[0].model.State.Time = 7 // a jet laid down on some other clock: the tick puts it back on the session's
		}
		i.Step(tick, nil)
		for _, slot := range i.slots() {
			a := i.aircraft[slot]
			if a.model.World.Carrier != c {
				t.Fatalf("slot %d flies against a carrier of its own", slot)
			}
			if a.alive && math.Abs(a.model.State.Time-float64(tick+1)/60) > 1e-9 {
				t.Fatalf("after tick %d slot %d stands at %.6f s, want %.6f", tick, slot, a.model.State.Time, float64(tick+1)/60)
			}
		}
	}
	// A respawn is laid down on the session's clock too.
	m := i.aircraft[0].model
	m.State.Time = 0
	i.enter(0, m, "", i.tank)
	if m.State.Time != i.clock() {
		t.Fatalf("a re-entry starts on %.4f s, want %.4f", m.State.Time, i.clock())
	}
}

// TestMatchSteers: the ship turns at the end of its area, on a tick that
// steers, and the turn goes to every client as a ship event carrying the new
// track - ordered a second ahead, so no client has flown past it.
func TestMatchSteers(t *testing.T) {
	i, _ := underway_match(t)
	i.Events()
	c := i.chart.world.Carrier
	// Put the ship at its area's upwind end, as the hours of steaming would.
	now := 3600.0
	along := flight.Vec3{X: math.Cos(i.ship.Into), Z: -math.Sin(i.ship.Into)}
	end := i.ship.Station.Add(along.Scale(ship.Reach + 10))
	c.Track = []flight.Leg{{Start: now - 10, Position: end.Subtract(along.Scale(10 * i.ship.Speed)), Heading: i.ship.Into, Speed: i.ship.Speed}}
	for tick := uint64(now*60 - 59); tick <= uint64(now*60+59); tick++ {
		for _, a := range i.aircraft {
			a.model.State.Gear.Extension = 0
		}
		i.Step(tick, nil)
		var turned map[string]any
		for _, e := range i.Events() {
			if e["kind"] == "ship" {
				turned = e
			}
		}
		if (turned != nil) != (tick == uint64(now*60)) {
			t.Fatalf("tick %d: ship event %v (the steering runs once a second)", tick, turned)
		}
		if turned == nil {
			continue
		}
		// Way gathered from the recovery's speed, the half turn, and on at
		// the top speed: the first of them a second ahead.
		legs := turned["ship"].(map[string]any)["track"].([][]float64)
		if len(c.Track) != 4 || len(legs) != 4 || c.Track[1].Change != ship.Surge || c.Track[1].Start != now+ship.Lead || c.Track[2].Turn == 0 || c.Track[3].Speed != ship.Most {
			t.Fatalf("the turn at the area's end: track %+v, sent %v", c.Track, legs)
		}
	}
}

// TestIslandSailsWithTheShip: the carrier's island is the ship's own solid,
// in its frame, and no longer a building left standing in the sea where the
// map charts the ship.
func TestIslandSailsWithTheShip(t *testing.T) {
	chart, found := chart_load("midway")
	if !found {
		t.Fatal("no midway map")
	}
	c := chart.world.Carrier
	if len(c.Prisms) != 1 || len(c.Posts) != 1 {
		t.Fatalf("the carrier carries %d prisms and %d posts, want its island and its mast", len(c.Prisms), len(c.Posts))
	}
	for _, q := range c.Prisms[0].Outline {
		if math.Abs(q.X) > 170 || math.Abs(q.Z) > 50 {
			t.Fatalf("an island corner at %v is not in the ship's frame", q)
		}
	}
	if p := c.Posts[0].Position; math.Abs(p.X) > 170 || math.Abs(p.Z) > 50 || c.Posts[0].Top < c.Position.Y+20 {
		t.Fatalf("the mast at %v, top %.1f, is not the ship's", p, c.Posts[0].Top)
	}
	near := func(x, z float64) bool { return math.Hypot(x-c.Position.X, z-c.Position.Z) < 400 }
	for _, p := range chart.world.Prisms {
		if near(p.Outline[0].X, p.Outline[0].Z) {
			t.Fatalf("a building stands where the map charts the ship: %+v", p)
		}
	}
	for _, p := range chart.world.Posts {
		if near(p.Position.X, p.Position.Z) {
			t.Fatalf("a mast stands where the map charts the ship: %+v", p)
		}
	}
}

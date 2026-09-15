// Mochi world: the player's own fuel load (#221)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
	"world/games/air/aircraft"
)

// pounds is the wire's unit — the UI speaks it like the IFEI — so every
// expectation here is written in pounds and compared against kilograms.
func kilograms(pounds float64) float64 { return pounds / 2.2046 }

// join adds one player carrying a stores request, and hands back the craft.
func join(t *testing.T, i *instance, slot int, stores map[string]any) *craft {
	t.Helper()
	if _, err := i.Join(game.Player{Name: "p", Slot: slot, Stores: stores}); err != nil {
		t.Fatalf("join %d: %v", slot, err)
	}
	return i.aircraft[slot]
}

// Every player takes the load they asked for, and two players in one match
// take DIFFERENT loads — the whole point of the change (2026-09-15). Before
// it, both spawned on the creator's single match-wide i.tank.
func TestFuelIsPerPlayer(t *testing.T) {
	i := build(t, "furball", nil, 0)
	light := join(t, i, 0, map[string]any{"fuel": 3000.0})
	heavy := join(t, i, 1, map[string]any{"fuel": 10800.0})

	if got, want := light.model.State.Fuel, kilograms(3000); math.Abs(got-want) > 1 {
		t.Errorf("slot 0 spawned with %.0f kg, want %.0f (3,000 lb)", got, want)
	}
	if got, want := heavy.model.State.Fuel, kilograms(10800); math.Abs(got-want) > 1 {
		t.Errorf("slot 1 spawned with %.0f kg, want %.0f (10,800 lb)", got, want)
	}
	if light.model.State.Fuel >= heavy.model.State.Fuel {
		t.Errorf("both jets took the same load (%.0f kg): the request is not reaching the spawn", light.model.State.Fuel)
	}
}

// No request — an old client, or a bot, which has no join message at all —
// takes the match's own default. i.tank stays meaningful; it just stopped
// being the only answer.
func TestFuelWithoutRequestTakesTheMatchDefault(t *testing.T) {
	i := build(t, "furball", map[string]any{"fuel": 6000.0}, 0)
	bare := join(t, i, 0, nil)
	empty := join(t, i, 1, map[string]any{})

	for slot, a := range map[int]*craft{0: bare, 1: empty} {
		if got, want := a.model.State.Fuel, kilograms(6000); math.Abs(got-want) > 1 {
			t.Errorf("slot %d spawned with %.0f kg, want the match default %.0f", slot, got, want)
		}
	}
}

// The bound is the airframe's tank and nothing else: a player may ask for as
// little as they like and be held to it, and asking for more than the jet
// holds is capped rather than refused. No creator cap, no floor - flying a
// load that cannot finish the fight is the player's own trade (2026-09-15).
func TestFuelIsBoundedOnlyByTheTank(t *testing.T) {
	_, airframe := aircraft.Grant("")
	capacity := airframe.Mass.Fuel
	i := build(t, "furball", nil, 0)

	if got := join(t, i, 0, map[string]any{"fuel": 40000.0}).model.State.Fuel; math.Abs(got-capacity) > 1 {
		t.Errorf("a 40,000 lb request spawned %.0f kg, want the tank's %.0f", got, capacity)
	}
	// 800 lb is below the old 500 kg (1,102 lb) floor, which silently handed
	// the player MORE fuel than they chose.
	if got, want := join(t, i, 1, map[string]any{"fuel": 800.0}).model.State.Fuel, kilograms(800); math.Abs(got-want) > 1 {
		t.Errorf("an 800 lb request spawned %.0f kg, want %.0f - nothing may raise a player's chosen load", got, want)
	}
}

// A nonsense request is not a spawn with no fuel: it falls through to the
// default, the same as an absent one. number() already bars NaN and Inf
// centrally (#174), so this pins the <=0 half.
func TestFuelRejectsNonsense(t *testing.T) {
	i := build(t, "furball", nil, 0)
	for slot, bad := range []any{-500.0, 0.0, "full", map[string]any{}} {
		got := join(t, i, slot, map[string]any{"fuel": bad}).model.State.Fuel
		if math.Abs(got-fuel) > 1 {
			t.Errorf("request %#v spawned %.0f kg, want the default %.0f", bad, got, fuel)
		}
	}
}

// A respawn runs through enter(), not Join, so without the craft remembering
// its load a second life silently reverted to the match default. This is the
// regression that the `tank` field on craft exists to stop.
func TestFuelSurvivesRespawn(t *testing.T) {
	i := build(t, "furball", map[string]any{"fuel": 10800.0}, 0)
	a := join(t, i, 0, map[string]any{"fuel": 2500.0})
	join(t, i, 1, nil) // someone to share the sky: an empty room takes a different spawn path

	a.model.State.Fuel = 120 // burnt down over the life it is about to lose
	i.fell(0, -1, "fire", -1)
	a.wait = 0
	i.Step(1, nil)

	if !a.alive {
		t.Fatalf("slot 0 did not respawn")
	}
	if got, want := a.model.State.Fuel, kilograms(2500); math.Abs(got-want) > 1 {
		t.Errorf("respawned with %.0f kg, want the player's own %.0f (2,500 lb) - the match default is %.0f", got, want, i.tank)
	}
}

// The fuel request rides in the SAME map as the loadout, and the loadout
// normalizer walks stations 1..9 and ignores every other key - which is why
// no protocol or wire change was needed to carry it. Pin that: a request
// carrying both must yield both.
func TestFuelRidesWithTheLoadout(t *testing.T) {
	i := build(t, "furball", map[string]any{"weapons": "open"}, 0) // a match with no weapons rule at all is guns-only, and the clamp would strip the round this asserts
	a := join(t, i, 0, map[string]any{
		"fuel": 4000.0,
		"1":    map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	})

	if got, want := a.model.State.Fuel, kilograms(4000); math.Abs(got-want) > 1 {
		t.Errorf("fuel %.0f kg, want %.0f", got, want)
	}
	if got := a.loadout["1"].Stores; len(got) != 1 || got[0] != "9m" {
		t.Errorf("station 1 granted %v, want the requested AIM-9M - the fuel key disturbed the loadout", got)
	}
	if _, found := a.loadout["fuel"]; found {
		t.Errorf("the loadout grew a %q station from the fuel key", "fuel")
	}
}

// Mochi world: air resource bounds
//
// Session creation is unauthenticated and jettison is a player-sent frame; both
// reach server-wide resources and need ceilings of their own.
//
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/game"
	"world/games/air/battle"
	"world/games/air/flight"
)

// TestBotBudgetIsServerWide: the reservation makes the bot cap a server
// ceiling, not a per-session clamp.
func TestBotBudgetIsServerWide(t *testing.T) {
	bots_live.Store(0)
	defer bots_live.Store(0)

	granted, rounds := 0, 0
	for granted < BOTS_MAXIMUM+50 && rounds < 100 {
		got := bots_reserve(99)
		granted += got
		rounds++
		if got == 0 {
			break
		}
	}
	if granted != BOTS_MAXIMUM {
		t.Errorf("reserved %d bots across many sessions, want the ceiling of %d", granted, BOTS_MAXIMUM)
	}
	if got := bots_reserve(10); got != 0 {
		t.Errorf("granted %d more bots past the ceiling", got)
	}
}

// TestBotBudgetPartialGrant: a busy server yields emptier practice matches
// rather than refusing to create them.
func TestBotBudgetPartialGrant(t *testing.T) {
	bots_live.Store(0)
	defer bots_live.Store(0)

	bots_reserve(BOTS_MAXIMUM - 5)
	if got := bots_reserve(20); got != 5 {
		t.Errorf("partial grant was %d, want the 5 that remained", got)
	}
	if live := bots_live.Load(); live != BOTS_MAXIMUM {
		t.Errorf("budget holds %d, want exactly the ceiling %d", live, BOTS_MAXIMUM)
	}
}

// TestBotBudgetReleasedOnClose — a reservation without a release is a leak that
// would refuse practice bots forever after enough matches. Instance had no
// lifecycle at all before game.Closer.
func TestBotBudgetReleasedOnClose(t *testing.T) {
	bots_live.Store(0)
	defer bots_live.Store(0)

	i := &instance{bots: bots_reserve(40)}
	if i.bots != 40 {
		t.Fatalf("reserved %d, want 40", i.bots)
	}
	i.Close()
	if live := bots_live.Load(); live != 0 {
		t.Errorf("%d bots still reserved after Close", live)
	}
	// Close must be safe if the server ever calls it twice.
	i.Close()
	if live := bots_live.Load(); live != 0 {
		t.Errorf("a second Close moved the budget to %d", live)
	}
}

// TestAirImplementsCloser — the release only happens because the server finds
// this interface; losing the method would silently reinstate the leak.
func TestAirImplementsCloser(t *testing.T) {
	var i any = &instance{}
	if _, ok := i.(game.Closer); !ok {
		t.Error("air's instance no longer implements game.Closer, so its bot budget is never released")
	}
}

// loaded gives slot 0 a three-tank loadout, the same fixture the release-strike
// test uses, so the aircraft is a real one with a model behind it.
func loaded(i *instance) *craft {
	a := i.aircraft[0]
	a.loadout = stores_normalize(map[string]any{
		"3": map[string]any{"fixture": "pylon", "stores": []any{"tank"}},
	})
	return a
}

// TestJettisonEmptyStationIsNotADeparture — `changed` counted any well-formed
// request, so nine EMPTY stations passed the changed==0 guard and bought a full
// roster broadcast to every player. That is what made the flood free.
func TestJettisonEmptyStationIsNotADeparture(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true}, 1)
	loaded(i)
	i.Events() // Events() DRAINS, so drain first and then measure from zero

	// Station 4 is well-formed and in range, but holds nothing.
	i.Jettison(0, []game.Departure{{Station: 4, What: "stores"}})
	if got := len(i.Events()); got != 0 {
		t.Errorf("dropping nothing raised %d event(s); an empty station is not a departure", got)
	}
}

// TestJettisonRealDropStillBroadcasts — the guard must not break the feature it
// protects: a genuine drop still re-publishes the loadout.
func TestJettisonRealDropStillBroadcasts(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true}, 1)
	loaded(i)
	i.Events() // drain anything the build raised

	i.Jettison(0, []game.Departure{{Station: 3, What: "stores"}})
	if got := len(i.Events()); got != 1 {
		t.Fatalf("a real drop raised %d event(s), want 1", got)
	}
}

// TestJettisonCooldown — every jettison broadcasts reliably to every player,
// and a client whose reliable queue fills is torn down as "slow", so an
// unthrottled jettison let one player disconnect the whole match.
func TestJettisonCooldown(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true}, 1)
	a := loaded(i)
	i.Events()
	i.stepped = 1000

	i.Jettison(0, []game.Departure{{Station: 3, What: "stores"}})
	if got := len(i.Events()); got != 1 {
		t.Fatalf("the first drop raised %d event(s), want 1", got)
	}

	// Reload and try again inside the cooldown.
	a.loadout = stores_normalize(map[string]any{
		"3": map[string]any{"fixture": "pylon", "stores": []any{"tank"}},
	})
	i.stepped += JETTISON_COOLDOWN - 1
	i.Jettison(0, []game.Departure{{Station: 3, What: "stores"}})
	if got := len(i.Events()); got != 0 {
		t.Errorf("a drop inside the cooldown raised %d event(s)", got)
	}

	// Past it, jettison works again.
	i.stepped += 2
	i.Jettison(0, []game.Departure{{Station: 3, What: "stores"}})
	if got := len(i.Events()); got != 1 {
		t.Errorf("a drop past the cooldown raised %d event(s), want 1", got)
	}
}

// TestRoundsCeiling pins ROUNDS_MAXIMUM (#138). i.rounds was the one per-tick
// collection with no bound, while fly() tests every round against every living
// aircraft: measured at 99 aircraft, 400 rounds costs 289 us a tick and 40,000
// costs 30.1 ms against a 16.7 ms budget. Real play does not approach that --
// the densest roster the clamps permit peaks at 1,729 -- so the ceiling exists
// for the case a roster all holds the trigger, which a human with the
// ammunition cheat can do indefinitely.
func TestRoundsCeiling(t *testing.T) {
	bots_live.Store(0)
	made, err := (&Air{}).Create(game.Session{Identifier: "ceiling", Game: "air", Mode: "furball",
		Capacity: 8, Seed: 4, Parameters: map[string]any{"bots": map[string]any{"pilot": 2.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	over := 500
	for n := 0; n < ROUNDS_MAXIMUM+over; n++ {
		i.rounds = append(i.rounds, battle.Round{Index: uint64(n), Position: flight.Vec3{Y: 4000}})
	}
	i.guns(1.0/60.0, 1)
	if len(i.rounds) > ROUNDS_MAXIMUM {
		t.Fatalf("the in-flight collection is still unbounded: %d rounds held, ceiling %d", len(i.rounds), ROUNDS_MAXIMUM)
	}
	// The OLDEST go, not the newest: an arriving round is the one a defender
	// has already had time to avoid, and dropping the freshest would delete
	// the volley a player just fired.
	if i.rounds[0].Index < uint64(over) {
		t.Errorf("the wrong end was trimmed: oldest surviving round has index %d, want at least %d",
			i.rounds[0].Index, over)
	}
}

// TestJettisonDropsTheRadarMagazine (#132) — arm() seeds TWO magazines from the
// loadout, a.missiles from stores_rounds and a.amraams from stores_amraams, and
// Jettison recomputed only the first. The AIM-120 trigger gates on a.amraams
// alone and fox3() never consults the loadout, so a player who dropped their
// AMRAAM stations kept firing full-fidelity rounds from an aircraft carrying
// none — free ordnance from a client frame, in a module whose stated design is
// server-authoritative weapons. Every AMRAAM ring (4/6, 2/8, 3/7) sits inside
// the stations Jettison accepts, so no station was out of reach.
func TestJettisonDropsTheRadarMagazine(t *testing.T) {
	arm := func(i *instance) *craft {
		a := i.aircraft[0]
		a.loadout = stores_normalize(map[string]any{
			"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"3": map[string]any{"fixture": "pylon", "stores": []any{"9m"}}, // station 3 takes a pylon; a rail there normalizes away
		})
		a.missiles = len(stores_rounds(a.loadout))
		a.amraams = len(stores_amraams(a.loadout))
		return a
	}

	i := build(t, "furball", map[string]any{"missiles": true}, 1)
	a := arm(i)
	if a.amraams != 2 || a.missiles != 1 {
		t.Fatalf("the fixture is wrong: amraams=%d missiles=%d, want 2 and 1", a.amraams, a.missiles)
	}
	i.Jettison(0, []game.Departure{{Station: 4, What: "stores"}})
	if a.amraams != 1 {
		t.Errorf("dropping one AIM-120 station left the radar magazine at %d, want 1", a.amraams)
	}
	if a.missiles != 1 {
		t.Errorf("dropping an AIM-120 station took %d off the HEATER magazine", 1-a.missiles)
	}

	// Dropping the heater must not touch the radar magazine either: the two
	// counts are independent, and the arithmetic for one must not read the
	// other's stations.
	i.stepped += JETTISON_COOLDOWN + 1
	i.Jettison(0, []game.Departure{{Station: 3, What: "stores"}})
	if a.missiles != 0 {
		t.Errorf("dropping the heater rail left the heater magazine at %d, want 0", a.missiles)
	}
	if a.amraams != 1 {
		t.Errorf("dropping a heater station moved the radar magazine to %d, want 1", a.amraams)
	}

	// Rounds already FIRED are not aboard, so they must not be counted as
	// dropped as well. Firing does not rewrite the loadout -- the entry stays
	// and the counter tracks expenditure -- so the arithmetic has to skip the
	// front of the firing order, exactly as the heater path skips `fired`.
	//
	// Station 4 is first in the AMRAAM firing order, so after one shot its
	// round is the one already gone. Dropping that station is dropping an
	// EMPTY rail: the round still aboard at station 6 must survive it. Without
	// the offset this reads the departed round as a second loss and quietly
	// takes away a missile the pilot still has.
	i = build(t, "furball", map[string]any{"missiles": true}, 1)
	a = arm(i)
	a.amraams-- // station 4's round is away down the range
	i.Jettison(0, []game.Departure{{Station: 4, What: "stores"}})
	if a.amraams != 1 {
		t.Errorf("dropping the rail whose round was already fired left the magazine at %d, want 1", a.amraams)
	}

	// ...and dropping what IS still aboard empties it, never past empty.
	i.stepped += JETTISON_COOLDOWN + 1
	i.Jettison(0, []game.Departure{{Station: 6, What: "stores"}})
	if a.amraams != 0 {
		t.Errorf("dropping the last loaded rail left the magazine at %d, want 0", a.amraams)
	}
}

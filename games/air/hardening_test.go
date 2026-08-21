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

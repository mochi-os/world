// Mochi world: air loadout grant tests (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/games/air/aircraft"
)

func fox2() map[string]any {
	rail := func() map[string]any { return map[string]any{"fixture": "rail", "stores": []any{"9m"}} }
	return map[string]any{"1": rail(), "2": rail(), "8": rail(), "9": rail()}
}

// TestGrant: a legal request passes through; missiles strip under a guns-only
// rule while fixtures and tanks survive; junk is dropped.
func TestGrant(t *testing.T) {
	lo := stores_grant(fox2(), true)
	if got := len(stores_rounds(lo)); got != 4 {
		t.Fatalf("fox2 granted %d rounds, want 4", got)
	}
	guns := stores_grant(fox2(), false)
	if got := len(stores_rounds(guns)); got != 0 {
		t.Fatalf("guns-only grant kept %d rounds", got)
	}
	if guns["2"].Fixture != "rail" {
		t.Fatalf("guns-only grant stripped the rail fixture")
	}
	junk := stores_grant(map[string]any{
		"2": map[string]any{"fixture": "catapult", "stores": []any{"brick"}},
		"3": map[string]any{"fixture": "pylon", "stores": []any{"tank", "tank"}},
		"4": map[string]any{"fixture": "pylon", "stores": []any{"tank"}},
	}, true)
	if junk["2"].Fixture != "" {
		t.Fatalf("unknown fixture survived: %q", junk["2"].Fixture)
	}
	if len(junk["3"].Stores) != 1 || junk["3"].Stores[0] != "tank" {
		t.Fatalf("pylon point list wrong: %v", junk["3"].Stores)
	}
	if junk["4"].Fixture != "" {
		t.Fatalf("cheek station accepted a fixture")
	}
}

// TestRounds: the SMS priority order — tips alternating from starboard, then
// outboards, twins outer round first.
func TestRounds(t *testing.T) {
	lo := stores_grant(map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"2": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"8": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	}, true)
	want := []string{"tip9", "tip1", "9m8a", "9m2a", "9m8b", "9m2b"}
	got := stores_rounds(lo)
	if len(got) != len(want) {
		t.Fatalf("rounds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rounds %v, want %v", got, want)
		}
	}
}

// TestMask: the grant's mask matches the catalog bits, clears rounds in
// firing order, and the bots' armed standard equals the legacy tips mask so
// the calibrated doctrine flies exactly what it always flew.
func TestMask(t *testing.T) {
	index := map[string]int{}
	for i, s := range aircraft.Get("fa18c").Stores {
		index[s.Name] = i
	}
	bit := func(name string) uint32 { return 1 << uint(index[name]) }
	lo := stores_grant(fox2(), true)
	full := stores_mask(lo, 0)
	for _, name := range []string{"tip1", "tip9", "rail2", "9m2", "rail8", "9m8"} {
		if full&bit(name) == 0 {
			t.Fatalf("full fox2 mask missing %s", name)
		}
	}
	one := stores_mask(lo, 1)
	if one&bit("tip9") != 0 {
		t.Fatalf("first round fired but tip9 still attached")
	}
	spent := stores_mask(lo, 4)
	if spent&(bit("tip1")|bit("tip9")|bit("9m2")|bit("9m8")) != 0 {
		t.Fatalf("empty magazine still carries rounds")
	}
	if spent&(bit("rail2")|bit("rail8")) == 0 {
		t.Fatalf("empty rails departed with their rounds")
	}
	// The armed bot standard is the six-round Fox 2 fighter (decided
	// 2026-08-06): tips plus outboard twins. The brain's two-round discipline
	// fires from it in SMS order — tips depart first, the twins stay carried.
	if got := len(stores_rounds(bots_loadout(true))); got != 6 {
		t.Fatalf("armed bot standard carries %d rounds, want 6", got)
	}
	if got := stores_mask(bots_loadout(true), 0); got != armed(shots) {
		t.Fatalf("full bot mask %b differs from armed(%d) %b", got, shots, armed(shots))
	}
	reserve := armed(0) // discipline exhausted: both tips fired, twins carried
	if reserve&bit("tip9") != 0 || reserve&bit("tip1") != 0 {
		t.Fatalf("discipline-spent bot still carries a tip: %b", reserve)
	}
	for _, name := range []string{"twin2", "twin8", "9m2a", "9m2b", "9m8a", "9m8b"} {
		if reserve&bit(name) == 0 {
			t.Fatalf("discipline-spent bot lost %s — the reserve rounds must stay carried", name)
		}
	}
	if got := stores_mask(bots_loadout(false), 0); got != 0 {
		t.Fatalf("clean bot standard mask %b, want 0", got)
	}
}

// TestAttach: the craft mask follows the remaining count, and rearm cycles
// through zero so tanks refill.
func TestAttach(t *testing.T) {
	a := &craft{loadout: stores_grant(fox2(), true)}
	a.missiles = 4
	full := a.attach()
	a.missiles = 0
	empty := a.attach()
	if full == empty {
		t.Fatalf("attach ignores the magazine")
	}
	legacy := &craft{missiles: 2}
	if legacy.attach() != armed(2) {
		t.Fatalf("loadout-less craft lost the legacy mapping")
	}
}

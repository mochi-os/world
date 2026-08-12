// Mochi world: air loadout grant tests (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/flight"
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
	bit := func(name string) uint64 { return 1 << uint(index[name]) }
	lo := stores_grant(fox2(), true)
	full := stores_mask(lo, 0, 0)
	for _, name := range []string{"tip1", "tip9", "rail2", "9m2", "rail8", "9m8"} {
		if full&bit(name) == 0 {
			t.Fatalf("full fox2 mask missing %s", name)
		}
	}
	one := stores_mask(lo, 1, 0)
	if one&bit("tip9") != 0 {
		t.Fatalf("first round fired but tip9 still attached")
	}
	spent := stores_mask(lo, 4, 0)
	if spent&(bit("tip1")|bit("tip9")|bit("9m2")|bit("9m8")) != 0 {
		t.Fatalf("empty magazine still carries rounds")
	}
	if spent&(bit("rail2")|bit("rail8")) == 0 {
		t.Fatalf("empty rails departed with their rounds")
	}
	// The armed bot standard is the six-round Fox 2 fighter (decided
	// 2026-08-06): tips plus outboard twins. The brain fires the WHOLE
	// magazine in SMS order (#253 — a joust is the engagement; rounds saved
	// at its end are wasted): tips depart first, then the twin-rack rounds,
	// and the empty launchers stay carried.
	if got := len(stores_rounds(bots_loadout(true))); got != 6 {
		t.Fatalf("armed bot standard carries %d rounds, want 6", got)
	}
	if got := stores_mask(bots_loadout(true), 0, 0); got != armed(shots) {
		t.Fatalf("full bot mask %b differs from armed(%d) %b", got, shots, armed(shots))
	}
	half := armed(shots - 2) // two away: the tips go first in SMS order
	if half&(bit("tip9")|bit("tip1")) != 0 {
		t.Fatalf("two rounds fired but a tip remains: %b", half)
	}
	for _, name := range []string{"9m2a", "9m2b", "9m8a", "9m8b"} {
		if half&bit(name) == 0 {
			t.Fatalf("two rounds fired but the twin round %s left early", name)
		}
	}
	dry := armed(0) // the magazine spent: every round gone, every launcher carried
	for _, name := range []string{"tip1", "tip9", "9m2a", "9m2b", "9m8a", "9m8b"} {
		if dry&bit(name) != 0 {
			t.Fatalf("magazine-dry bot still carries the round %s: %b", name, dry)
		}
	}
	for _, name := range []string{"twin2", "twin8"} { // the twin racks ARE this loadout's launchers; the single rails belong to other fitments
		if dry&bit(name) == 0 {
			t.Fatalf("magazine-dry bot lost its launcher %s — carriage does not depart with the rounds", name)
		}
	}
	if got := stores_mask(bots_loadout(false), 0, 0); got != 0 {
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

// TestAmraamStores (#27): the cheek stations accept only the AIM-120C, the
// guns-only clamp strips it like any missile, and the fa18c catalog carries
// its entries appended after every legacy mask bit.
func TestAmraamStores(t *testing.T) {
	lo := stores_grant(map[string]any{
		"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		"2": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		"8": map[string]any{"fixture": "twin", "stores": []any{"120c", "120c"}},
	}, true)
	if got := lo["4"].Stores[0]; got != "120c" {
		t.Fatalf("cheek AMRAAM dropped: %q", got)
	}
	if got := lo["2"].Stores[0]; got != "120c" {
		t.Fatalf("wing-rail AMRAAM dropped: %q", got)
	}
	if got := lo["8"].Stores; len(got) != 2 || got[0] != "120c" || got[1] != "120c" {
		t.Fatalf("twin AMRAAM pair dropped: %v", got)
	}
	if got := stores_entries(8, lo["8"]); len(got) != 3 || got[0] != "twin8" || got[1] != "120c8a" || got[2] != "120c8b" {
		t.Fatalf("twin pair entries %v", got)
	}
	if got := stores_entries(6, lo["6"]); len(got) != 2 || got[0] != "rail6" || got[1] != "120c6" {
		t.Fatalf("cheek entries %v", got)
	}
	guns := stores_grant(map[string]any{"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}}}, false)
	if got := guns["4"].Stores[0]; got != "" {
		t.Fatalf("guns-only grant kept the AMRAAM: %q", got)
	}
	inboard := stores_grant(map[string]any{
		"3": map[string]any{"fixture": "pylon", "stores": []any{"120c"}},
		"5": map[string]any{"fixture": "pylon", "stores": []any{"120c"}},
	}, true)
	if got := inboard["3"].Stores[0]; got != "120c" {
		t.Fatalf("inboard-pylon AMRAAM dropped: %q", got)
	}
	if got := inboard["5"].Stores[0]; got != "" {
		t.Fatalf("a centreline AMRAAM survived: %q", got)
	}
	if got := stores_entries(3, inboard["3"]); len(got) != 2 || got[1] != "120c3" {
		t.Fatalf("inboard entries %v", got)
	}
	// The ten-round fit (#27): twins on all four wing pylons plus the cheeks.
	// A mixed twin pair is real LAU-115 carriage and survives the grant, and
	// the pair entries sit past bit 31 — the mask chain is uint64 now, and
	// the full fit must carry them.
	spam := stores_grant(map[string]any{
		"2": map[string]any{"fixture": "twin", "stores": []any{"120c", "120c"}},
		"3": map[string]any{"fixture": "twin", "stores": []any{"120c", "9m"}},
		"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		"7": map[string]any{"fixture": "twin", "stores": []any{"120c", "120c"}},
		"8": map[string]any{"fixture": "twin", "stores": []any{"120c", "120c"}},
	}, true)
	if got := spam["3"].Stores; got[0] != "120c" || got[1] != "9m" {
		t.Fatalf("inboard mixed twin pair dropped: %v", got)
	}
	if got := stores_entries(3, spam["3"]); len(got) != 3 || got[1] != "120c3a" || got[2] != "9m3b" {
		t.Fatalf("mixed pair entries %v", got)
	}
	if got := stores_entries(7, spam["7"]); len(got) != 3 || got[1] != "120c7a" || got[2] != "120c7b" {
		t.Fatalf("inboard twin pair entries %v", got)
	}
	index := map[string]int{}
	frame := aircraft.Get("fa18c")
	names := map[string]bool{}
	for i, s := range frame.Stores {
		names[s.Name] = true
		index[s.Name] = i
	}
	for _, want := range []string{"rail4", "120c4", "rail6", "120c6", "120c2", "120c8", "120c3", "120c7", "twin3", "twin7", "120c2a", "120c2b", "120c8a", "120c8b", "120c3a", "120c3b", "120c7a", "120c7b"} {
		if !names[want] {
			t.Fatalf("catalog missing %q", want)
		}
	}
	if frame.Stores[0].Name != "tip1" || frame.Stores[1].Name != "tip9" {
		t.Fatalf("legacy bit order moved: %q %q", frame.Stores[0].Name, frame.Stores[1].Name)
	}
	full := stores_mask(spam, 0, 0)
	for _, name := range []string{"120c3a", "120c7b"} {
		if index[name] < 32 {
			t.Fatalf("expected %q past bit 31 (the uint64 regression tripwire), got bit %d", name, index[name])
		}
		if full&(1<<uint(index[name])) == 0 {
			t.Fatalf("ten-round mask missing %q at bit %d", name, index[name])
		}
	}
}

// TestInboardHeaters (#27 follow-up): the Sparrow-capable inboards carry
// heaters too — singles on the LAU-115C, pairs on its twin rails — and the
// 9M order steps inboard after the outboard ring, starboard seeding.
func TestInboardHeaters(t *testing.T) {
	lo := stores_grant(map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"2": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"3": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"5": map[string]any{"fixture": "pylon", "stores": []any{"9m"}},
		"7": map[string]any{"fixture": "pylon", "stores": []any{"9m"}},
		"8": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	}, true)
	if got := lo["3"].Stores; got[0] != "9m" || got[1] != "9m" {
		t.Fatalf("inboard twin heaters dropped: %v", got)
	}
	if got := lo["7"].Stores[0]; got != "9m" {
		t.Fatalf("inboard single heater dropped: %q", got)
	}
	if got := lo["5"].Stores[0]; got != "" {
		t.Fatalf("a centreline heater survived: %q", got)
	}
	if got := stores_entries(7, lo["7"]); len(got) != 2 || got[0] != "pylon7" || got[1] != "9m7" {
		t.Fatalf("inboard single entries %v", got)
	}
	want := []string{"tip9", "tip1", "9m8a", "9m2a", "9m8b", "9m2b", "9m7", "9m3a", "9m3b"}
	got := stores_rounds(lo)
	if len(got) != len(want) {
		t.Fatalf("rounds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rounds %v, want %v", got, want)
		}
	}
	names := map[string]bool{}
	for _, s := range aircraft.Get("fa18c").Stores {
		names[s.Name] = true
	}
	for _, name := range []string{"9m3", "9m3a", "9m3b", "9m7", "9m7a", "9m7b"} {
		if !names[name] {
			t.Fatalf("catalog missing %q", name)
		}
	}
}

// TestFox3Server (#27 phase 2c): the server's AIM-120 needs the shooter's
// own STT — which is exactly what every RWR hears — and its datalink lives
// or dies with that lock. Turning cold does not kill the round; it drops it
// onto its last prediction until its own seeker wakes. That is the crank.
func TestFox3Server(t *testing.T) {
	build := func() (*instance, *craft, *craft) {
		i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true}
		for _, slot := range []int{0, 1} {
			m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
			m.State = flight.Level(m, flight.Vec3{Y: 8000}, flight.Vec3{X: 1}, 260, 2500)
			a := &craft{model: m, alive: true, lock: -1, loadout: stores_grant(map[string]any{
				"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
				"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			}, true)}
			a.arm()
			i.aircraft[slot] = a
		}
		shooter, target := i.aircraft[0], i.aircraft[1]
		target.model.State.Position = flight.Vec3{X: 25000, Y: 8000}
		target.model.State.Velocity = flight.Vec3{X: -260}
		return i, shooter, target
	}

	// A silent radar has no shot at all.
	i, shooter, _ := build()
	if shooter.amraams != 2 {
		t.Fatalf("cheek pair granted %d AMRAAMs, want 2", shooter.amraams)
	}
	if i.fox3(0, shooter) {
		t.Fatalf("launched with no lock — the shot must cost an STT the RWR can hear")
	}
	shooter.emitter, shooter.lock = 2, 1
	if !i.fox3(0, shooter) || len(i.flying) != 1 {
		t.Fatalf("a locked shooter could not launch")
	}
	if i.flying[0].radar == nil {
		t.Fatalf("the launched round is not an AIM-120")
	}

	// Supported: the estimate tracks the target while the lock holds.
	for step := 0; step < 240; step++ {
		i.pursue(1.0/60, uint64(step))
	}
	if len(i.flying) != 1 {
		t.Fatalf("the supported round died in flight")
	}
	supported := i.flying[0].radar.Stale
	if supported > 0.2 {
		t.Fatalf("a held lock left the datalink stale by %.1f s", supported)
	}

	// Cold: break the lock and the round coasts — still alive, no longer fed.
	shooter.emitter, shooter.lock = 1, -1
	for step := 0; step < 120; step++ {
		i.pursue(1.0/60, uint64(240+step))
	}
	if len(i.flying) != 1 {
		t.Fatalf("breaking the lock killed the round outright — it should coast")
	}
	if cold := i.flying[0].radar.Stale; cold < 1.5 {
		t.Fatalf("the round is still being fed %.1f s after the lock broke", cold)
	}
}

// TestFox3Trigger (#27 phase 2c): the AIM-120's trigger is its own EDGE on
// the wire — a separate flag and a separate magazine from the heater's, so
// one press is one round and a held button is not a stream. This is the
// path from a client's key to a round in the air.
func TestFox3Trigger(t *testing.T) {
	i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true, started: true}
	for _, slot := range []int{0, 1} {
		m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
		m.State = flight.Level(m, flight.Vec3{Y: 8000}, flight.Vec3{X: 1}, 260, 2500)
		a := &craft{model: m, alive: true, lock: -1, loadout: stores_grant(map[string]any{
			"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		}, true)}
		a.arm()
		a.release = 1e9
		i.aircraft[slot] = a
	}
	shooter := i.aircraft[0]
	i.aircraft[1].model.State.Position = flight.Vec3{X: 20000, Y: 8000}
	shooter.emitter, shooter.lock = 2, 1

	press := map[int][]game.Input{0: {{Sequence: 1, Data: map[string]any{"radar": true}}}}
	i.Step(1, press)
	if len(i.flying) != 1 {
		t.Fatalf("the radar trigger launched %d rounds, want 1", len(i.flying))
	}
	if shooter.amraams != 1 {
		t.Fatalf("the shot left %d AMRAAMs, want 1 — the magazine must be its own", shooter.amraams)
	}
	// A HELD trigger is one press: the edge, not the level.
	for tick := 2; tick < 6; tick++ {
		i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: uint32(tick), Data: map[string]any{"radar": true}}}})
	}
	if shooter.amraams != 1 {
		t.Fatalf("a held trigger emptied the magazine to %d — the edge is not being taken", shooter.amraams)
	}
	// Releasing and pressing again is a second round (the cooldown allows it).
	shooter.release = 1e9
	i.Step(6, map[int][]game.Input{0: {{Sequence: 6, Data: map[string]any{"radar": false}}}})
	i.Step(7, map[int][]game.Input{0: {{Sequence: 7, Data: map[string]any{"radar": true}}}})
	if shooter.amraams != 0 {
		t.Fatalf("the second press left %d AMRAAMs, want 0", shooter.amraams)
	}
}

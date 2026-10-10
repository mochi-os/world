// Mochi world: air loadout grant tests (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"math"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/flight"
	"world/games/air/round"
)

func fox2() map[string]any {
	rail := func() map[string]any { return map[string]any{"fixture": "rail", "stores": []any{"9m"}} }
	return map[string]any{"1": rail(), "2": rail(), "8": rail(), "9": rail()}
}

// TestGrant: a legal request passes through; missiles strip under a guns-only
// rule while fixtures and tanks survive; junk is dropped.
func TestGrant(t *testing.T) {
	lo := stores_grant(fox2(), "open")
	if got := len(stores_rounds(lo)); got != 4 {
		t.Fatalf("fox2 granted %d rounds, want 4", got)
	}
	guns := stores_grant(fox2(), "guns")
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
	}, "open")
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
	}, "open")
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
	lo := stores_grant(fox2(), "open")
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
	// The armed bot standard is the six-round Fox 2 fighter: tips plus outboard
	// twins, fired whole in SMS order - tips first, then the twin-rack rounds,
	// with the empty launchers still carried.
	if got := len(stores_rounds(bots_loadout("fox2"))); got != 6 {
		t.Fatalf("armed bot standard carries %d rounds, want 6", got)
	}
	if got := stores_mask(bots_loadout("fox2"), 0, 0); got != armed(shots) {
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
	if got := stores_mask(bots_loadout("guns"), 0, 0); got != 0 {
		t.Fatalf("clean bot standard mask %b, want 0", got)
	}
}

// TestAttach: the craft mask follows the remaining count, and rearm cycles
// through zero so tanks refill.
func TestAttach(t *testing.T) {
	a := &craft{loadout: stores_grant(fox2(), "open")}
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
	}, "open")
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
	guns := stores_grant(map[string]any{"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}}}, "guns")
	if got := guns["4"].Stores[0]; got != "" {
		t.Fatalf("guns-only grant kept the AMRAAM: %q", got)
	}
	inboard := stores_grant(map[string]any{
		"3": map[string]any{"fixture": "pylon", "stores": []any{"120c"}},
		"5": map[string]any{"fixture": "pylon", "stores": []any{"120c"}},
	}, "open")
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
	}, "open")
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
	}, "open")
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
			}, "open")}
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
	if i.fox3(0, shooter, false) {
		t.Fatalf("launched with no lock — the shot must cost an STT the RWR can hear")
	}
	shooter.emitter, shooter.lock = 2, 1
	if !i.fox3(0, shooter, false) || len(i.flying) != 1 {
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
		}, "open")}
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

// TestFox3Visual (#155): a VISUAL shot needs no lock. On the trigger edge
// with the visual flag the round leaves with its seeker live and no estimate,
// after the jet nearest the nose inside the 7.5° field-of-view circle, any
// side, or after nothing - and either way the magazine pays, as the client's
// does. Without the flag and without a lock there is still no shot.
func TestFox3Visual(t *testing.T) {
	build := func(count int) (*instance, *craft) {
		i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true, started: true}
		for slot := 0; slot < count; slot++ {
			m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
			m.State = flight.Level(m, flight.Vec3{Y: 8000}, flight.Vec3{X: 1}, 260, 2500)
			a := &craft{model: m, alive: true, lock: -1, loadout: stores_grant(map[string]any{
				"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
				"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			}, "open")}
			a.arm()
			a.release = 1e9
			i.aircraft[slot] = a
		}
		return i, i.aircraft[0]
	}
	at := func(i *instance, slot int, degrees, distance float64) {
		r := degrees * math.Pi / 180
		i.aircraft[slot].model.State.Position = flight.Vec3{X: distance * math.Cos(r), Y: 8000, Z: distance * math.Sin(r)}
	}
	press := func(i *instance, data map[string]any) {
		i.Step(1, map[int][]game.Input{0: {{Sequence: 1, Data: data}}})
	}

	i, shooter := build(2)
	at(i, 1, 0, 20000)
	press(i, map[string]any{"radar": true})
	if len(i.flying) != 0 || shooter.amraams != 2 {
		t.Fatalf("no lock and no visual flag: %d rounds, %d AMRAAMs left, want 0 and 2", len(i.flying), shooter.amraams)
	}
	i, shooter = build(2)
	at(i, 1, 5, 20000)
	press(i, map[string]any{"radar": true, "visual": true})
	if len(i.flying) != 1 || shooter.amraams != 1 {
		t.Fatalf("a VISUAL shot: %d rounds, %d AMRAAMs left, want 1 and 1", len(i.flying), shooter.amraams)
	}
	if m := i.flying[0]; m.target != 1 || m.radar.Phase != round.Active || m.radar.Loft {
		t.Fatalf("the VISUAL round took %d, phase %d, loft %v; want the jet 5° off the nose, its seeker live, no loft", m.target, m.radar.Phase, m.radar.Loft)
	}

	i, shooter = build(3)
	at(i, 1, 6, 9000)
	at(i, 2, 2, 25000)
	if got := i.visual(0, shooter); got != 2 {
		t.Errorf("two jets in the circle: took %d, want 2, the nearer the nose, not the nearer the jet", got)
	}
	i.mode, shooter.team, i.aircraft[2].team = "teams", "blue", "blue"
	if got := i.visual(0, shooter); got != 2 {
		t.Errorf("a team mate in the circle: took %d, want 2 - the seeker is team-blind, as the circle warns", got)
	}

	i, shooter = build(2)
	at(i, 1, 10, 20000)
	press(i, map[string]any{"radar": true, "visual": true})
	if len(i.flying) != 1 || i.flying[0].target != -1 || shooter.amraams != 1 {
		t.Fatalf("nothing in the circle: %d rounds, target %v, %d AMRAAMs left; want one round after nothing, the magazine paid", len(i.flying), i.flying, shooter.amraams)
	}
	for step := 0; step < 120; step++ {
		i.pursue(1.0/60, uint64(step))
	}
	if len(i.flying) != 1 {
		t.Fatalf("the round after nothing was dropped at once: it should fly on, seeing nothing")
	}
}

// TestChaffServer (#29): a defender who beams and dispenses defeats a server
// round that fuses without the dispense. The bloom is offered through the same
// fields the flare edge sets, so this pins that wiring too.
func TestChaffServer(t *testing.T) {
	fly := func(dispensing bool) float64 {
		i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true, started: true}
		for _, slot := range []int{0, 1} {
			m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
			m.State = flight.Level(m, flight.Vec3{Y: 8000}, flight.Vec3{X: 1}, 280, 2500)
			a := &craft{model: m, alive: true, lock: -1, flared: 1e9, clouded: 1e9, loadout: stores_grant(map[string]any{
				"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			}, "open")}
			a.arm()
			i.aircraft[slot] = a
		}
		shooter, target := i.aircraft[0], i.aircraft[1]
		target.model.State.Position = flight.Vec3{X: 15000, Y: 8000}
		target.model.State.Velocity = flight.Vec3{X: -280}
		shooter.emitter, shooter.lock = 2, 1
		if !i.fox3(0, shooter, false) {
			t.Fatalf("launch refused")
		}
		m := i.flying[0]
		defending := false
		for step := 0; step < 240*90 && len(i.flying) > 0; step++ {
			dt := 1.0 / 240
			// The defender flies the doctrine once the seeker goes terminal,
			// and KEEPS flying it — the beam is held against the round's
			// bearing for the rest of the defence, not set once and
			// abandoned the moment the seduction drops the phase.
			if m.radar.Phase >= 2 {
				defending = true
			}
			if defending {
				sight := target.model.State.Position.Subtract(m.radar.Position).Normalize()
				beam := flight.Vec3{X: -sight.Z, Z: sight.X}
				if beam.Dot(target.model.State.Velocity) < 0 {
					beam = beam.Scale(-1)
				}
				target.model.State.Velocity = beam.Scale(280)
				if dispensing && target.clouded > 1e8 {
					target.flared = 0
					target.cloud = target.model.State.Position
					target.clouded = 0
				}
			}
			target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
			target.clouded += dt
			i.pursue(dt, uint64(step))
			if !target.alive {
				break
			}
		}
		// The round's own closest approach is the honest verdict — whether
		// the warhead's fragment luck killed is battle's stochastic business.
		return m.radar.Least
	}
	if closest := fly(false); closest > 20 {
		t.Fatalf("the control shot (beam, no chaff) passed at %.0f m — an established track must hold through the notch", closest)
	}
	if closest := fly(true); closest < 100 {
		t.Fatalf("the beamed-and-chaffed defender was still passed at %.0f m", closest)
	}
}

// TestJammerServer (#31): the armed jammer radiates only when painted, its
// radiation starves the shooter's datalink outside burnthrough (and not
// inside), and an inbound round homes on the beacon — through the full
// beam-and-chaff defence that saves a quiet defender.
func TestJammerServer(t *testing.T) {
	build := func() (*instance, *craft, *craft) {
		i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true, started: true}
		for _, slot := range []int{0, 1} {
			m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
			m.State = flight.Level(m, flight.Vec3{Y: 8000}, flight.Vec3{X: 1}, 280, 2500)
			a := &craft{model: m, alive: true, lock: -1, flared: 1e9, clouded: 1e9, loadout: stores_grant(map[string]any{
				"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			}, "open")}
			a.arm()
			i.aircraft[slot] = a
		}
		shooter, target := i.aircraft[0], i.aircraft[1]
		target.model.State.Position = flight.Vec3{X: 16000, Y: 8000}
		target.model.State.Velocity = flight.Vec3{X: -280}
		shooter.emitter, shooter.lock = 2, 1
		return i, shooter, target
	}

	// Radiation truth: armed alone is quiet; armed + a hostile lock radiates;
	// disarming silences immediately.
	i, shooter, target := build()
	target.latest.Jammer = true
	shooter.emitter, shooter.lock = 1, -1
	i.Step(1, nil)
	if target.loud {
		t.Fatalf("an unpainted jammer radiated — armed must not mean loud")
	}
	shooter.emitter, shooter.lock = 2, 1
	i.Step(2, nil)
	if !target.loud {
		t.Fatalf("a locked, armed jammer stayed quiet")
	}
	target.latest.Jammer = false
	i.Step(3, nil)
	if target.loud {
		t.Fatalf("a disarmed jammer kept radiating")
	}

	// The toggle is a live decision: a defender who goes quiet at the terminal
	// call, then beams and dispenses, survives; one that keeps radiating dies to
	// HOJ. Jamming trades the denied datalink for the granted beacon.
	i, shooter, target = build()
	target.latest.Jammer = true
	if !i.fox3(0, shooter, false) {
		t.Fatalf("launch refused")
	}
	m := i.flying[0]
	defending := false
	for step := 0; step < 240*90 && len(i.flying) > 0; step++ {
		dt := 1.0 / 240
		// The radiation truth is set directly (case 1 above tests the real
		// computation): a full i.Step here would pursue the round a second
		// time each iteration and fly it against a teleporting target.
		target.loud = target.latest.Jammer && shooter.emitter == 2
		if m.radar.Phase >= 2 || m.radar.Beacon {
			defending = true
		}
		if defending {
			target.latest.Jammer = false // the terminal call: go quiet, beam, dispense
			sight := target.model.State.Position.Subtract(m.radar.Position).Normalize()
			if m.radar.Phase == 3 {
				// The round went stupid: EXTEND. Circling the impact zone
				// is how a ballistic round kills you by accident.
				target.model.State.Velocity = sight.Scale(280)
			} else {
				beam := flight.Vec3{X: -sight.Z, Z: sight.X}
				if beam.Dot(target.model.State.Velocity) < 0 {
					beam = beam.Scale(-1)
				}
				target.model.State.Velocity = beam.Scale(280)
				if target.clouded > 1.5 {
					target.flared = 0
					target.cloud = target.model.State.Position
					target.clouded = 0
				}
			}
		}
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
		target.clouded += dt
		i.pursue(dt, uint64(10+step))
		if !target.alive {
			break
		}
	}
	if m.radar.Least <= round.Fuse {
		t.Fatalf("the defender who went quiet and defended still died (closest %.0f m) — disarming must hand the round back to rules the defence beats", m.radar.Least)
	}

	// The full defence, radiating: HOJ eats the beam and the chaff. The
	// quiet control of this same defence is TestChaffServer's.
	i, shooter, target = build()
	target.latest.Jammer = true
	if !i.fox3(0, shooter, false) {
		t.Fatalf("launch refused")
	}
	m = i.flying[0]
	defending = false
	for step := 0; step < 240*90 && len(i.flying) > 0; step++ {
		dt := 1.0 / 240
		target.loud = target.latest.Jammer && shooter.emitter == 2
		if m.radar.Phase >= 2 || m.radar.Beacon {
			defending = true
		}
		if defending {
			sight := target.model.State.Position.Subtract(m.radar.Position).Normalize()
			beam := flight.Vec3{X: -sight.Z, Z: sight.X}
			if beam.Dot(target.model.State.Velocity) < 0 {
				beam = beam.Scale(-1)
			}
			target.model.State.Velocity = beam.Scale(280)
			if target.clouded > 1e8 {
				target.flared = 0
				target.cloud = target.model.State.Position
				target.clouded = 0
			}
		}
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
		target.clouded += dt
		i.pursue(dt, uint64(100+step))
		if !target.alive {
			break
		}
	}
	if m.radar.Least > round.Fuse {
		t.Fatalf("the radiating defender's beam-and-chaff defence still worked: closest %.0f m — HOJ must override it", m.radar.Least)
	}
}

// TestAmraamAlongTheNose: an AIM-120 leaves along the NOSE, as the client fires
// it - off a wing rail at the jet's speed and the launcher's 30 m/s, off a cheek
// ejector at the speed and 15 m/s punched 8 m/s down - and the DLZ is drawn for
// the next round as it will leave (fired). Drawn for a round leaving along the
// flight path at the jet's own speed, the no-escape range came out 14 km short
// of the round single player flies, and fox3 fired a third round, the flight
// path plus 30 m/s along the nose, that matched neither.
func TestAmraamAlongTheNose(t *testing.T) {
	i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true}
	for _, slot := range []int{0, 1} {
		m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
		m.State = flight.Level(m, flight.Vec3{Y: 7000}, flight.Vec3{X: 1}, 250, 2500)
		a := &craft{model: m, alive: true, lock: -1, loadout: stores_grant(map[string]any{
			"2": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		}, "open")}
		a.arm()
		i.aircraft[slot] = a
	}
	shooter, target := i.aircraft[0], i.aircraft[1]
	pitch := 10 * math.Pi / 180
	nose := flight.Vec3{X: math.Cos(pitch), Y: math.Sin(pitch)}
	shooter.model.State.Velocity = flight.Vec3{X: 250}
	shooter.model.State.Attitude = flight.Basis(nose, flight.Vec3{X: -math.Sin(pitch), Y: math.Cos(pitch)})
	target.model.State.Position, target.model.State.Velocity = flight.Vec3{X: 30000, Y: 7000}, flight.Vec3{X: -250}
	shooter.emitter, shooter.lock = 2, 1
	him := round.Target{Position: target.model.State.Position, Velocity: target.model.State.Velocity}
	honest := round.Ladder(fired(shooter), him, 0)
	for k, want := range []struct {
		station string
		launch  flight.Vec3
	}{
		{"cheek ejector", nose.Scale(265).Add(flight.Vec3{Y: -8})},
		{"wing rail", nose.Scale(280)},
	} {
		drawn := fired(shooter).Velocity
		if !i.fox3(0, shooter, false) || len(i.flying) != k+1 {
			t.Fatalf("%s: a locked shooter could not launch", want.station)
		}
		shooter.amraams--
		launched := i.flying[k].velocity
		if launched.Subtract(want.launch).Length() > 0.5 {
			t.Errorf("%s: the round left at %+v, want %+v", want.station, launched, want.launch)
		}
		if drawn.Subtract(launched).Length() > 1e-6 {
			t.Errorf("%s: the DLZ was drawn for a round leaving at %+v, and fox3 fired one at %+v", want.station, drawn, launched)
		}
	}
	path := round.Ladder(round.Target{Position: shooter.model.State.Position, Velocity: shooter.model.State.Velocity}, him, 0)
	if honest.Escape < path.Escape+5000 {
		t.Errorf("the no-escape range barely moved off the flight path (%.0f m against %.0f m): the control is not a control", honest.Escape, path.Escape)
	}
}

// TestSeparation (#32): the BVR spawn distance is DERIVED from the round's
// own ladder — head-on Rmax at the spawn state plus the fifteen-mile commit
// buffer — so a flight-model retune moves the match geometry automatically.
// This pins the relationship and the sanity band, not a number.
func TestSeparation(t *testing.T) {
	ladder := round.Ladder(
		round.Target{Position: flight.Vec3{Y: bvraltitude}, Velocity: rail(flight.Vec3{X: 1}, bvrspeed)},
		round.Target{Position: flight.Vec3{X: 60000, Y: bvraltitude}, Velocity: flight.Vec3{X: -bvrspeed}},
		0,
	)
	apart := separation()
	if apart != ladder.Max+commit {
		t.Fatalf("separation %.0f is not head-on Rmax (%.0f) plus the commit buffer (%.0f)", apart, ladder.Max, commit)
	}
	if apart < 45000 || apart > 120000 {
		t.Fatalf("separation %.0f m outside the sanity band — the ladder or the spawn state moved a long way", apart)
	}
	// The organisation phase is real: from spawn, the pair closes for tens
	// of seconds before either can be inside Rmax.
	if organise := commit / (2 * bvrspeed); organise < 30 {
		t.Fatalf("the commit buffer gives only %.0f s of closing before Rmax", organise)
	}
}

// TestOpening (#46): a BVR joust's start is drawn from a seed - each block
// inside 15,000-35,000 ft, one speed, a hot start about a third of the time
// and otherwise a 20-45 degree flank, inside the radar's scan - and opens far
// enough apart that neither jet starts inside the other's Rmax.
func TestOpening(t *testing.T) {
	if Draw(7, 0) != Draw(7, 0) {
		t.Fatal("one seed drew two openings: a match and its client would disagree")
	}
	hot, split := 0, 0
	for seed := uint64(1); seed <= 300; seed++ {
		o := Draw(seed, 0)
		for _, h := range o.Altitude {
			if h < 4572 || h > 10668 {
				t.Fatalf("seed %d: block %.0f m outside 15,000-35,000 ft", seed, h)
			}
		}
		if o.Speed < 230 || o.Speed > 290 {
			t.Fatalf("seed %d: block speed %.0f m/s outside 230-290", seed, o.Speed)
		}
		if f := math.Abs(o.Flank); f != 0 && (f < math.Pi/9-1e-9 || f > math.Pi/4+1e-9) {
			t.Fatalf("seed %d: flank %.1f deg, neither hot nor 20-45", seed, f*180/math.Pi)
		}
		// Both turned the same way off the line: their tracks run antiparallel
		// and pass abeam, rather than converging on a point to one side.
		zero, one := o.state(0, o.Apart), o.state(1, o.Apart)
		if zero.Velocity.Add(one.Velocity).Length() > 1e-6 {
			t.Fatalf("seed %d: the pair's tracks are not antiparallel: %+v and %+v", seed, zero.Velocity, one.Velocity)
		}
		for _, end := range [][2]round.Target{{zero, one}, {one, zero}} {
			sight := end[1].Position.Subtract(end[0].Position)
			sight.Y = 0
			if off := math.Acos(math.Min(1, sight.Normalize().Dot(end[0].Velocity.Normalize()))); math.Abs(off-math.Abs(o.Flank)) > 1e-6 {
				t.Fatalf("seed %d: one end sees the other %.1f deg off its nose, not the %.1f flank", seed, off*180/math.Pi, math.Abs(o.Flank)*180/math.Pi)
			}
		}
		if math.Cos(o.Flank) < round.Gimbal {
			t.Fatalf("seed %d: the other jet starts %.0f deg off the nose, outside the radar's scan", seed, math.Abs(o.Flank)*180/math.Pi)
		}
		if o.Flank == 0 {
			hot++
		}
		if math.Abs(o.Altitude[0]-o.Altitude[1]) > 1500 {
			split++
		}
		for slot := 0; slot < 2; slot++ {
			if reach := round.Ladder(o.shooter(slot, o.Apart), o.state(1-slot, o.Apart), 0).Max; o.Apart < reach+commit-1 {
				t.Fatalf("seed %d: slot %d's Rmax %.0f m plus the commit buffer reaches past the %.0f m apart", seed, slot, reach, o.Apart)
			}
		}
	}
	if hot < 70 || hot > 130 {
		t.Errorf("%d of 300 starts hot, want about a third", hot)
	}
	if split < 150 {
		t.Errorf("only %d of 300 starts split the blocks by more than 1,500 m", split)
	}
	if o := Draw(3, 150000); o.Apart > 0.45*150000 {
		t.Errorf("apart %.0f m past the wrap's reach: across a 150 km wrap the pair would meet the short way round", o.Apart)
	}
	if h := headon(); h.Altitude != [2]float64{bvraltitude, bvraltitude} || h.Speed != bvrspeed || h.Flank != 0 || h.Apart != separation() {
		t.Errorf("the pinned head-on start moved: %+v", h)
	}
}

// TestOpeningSpawn: a BVR joust spawns its pair on the drawn opening, or on
// the classic head-on block when "opening": "head" pins it.
func TestOpeningSpawn(t *testing.T) {
	seed := uint64(11)
	for Draw(seed, 0).Flank == 0 { // a flanked start, so the spawn's heading is tested off the line
		seed++
	}
	joust := func(extra map[string]any) (*instance, [2]*flight.State) {
		parameters := map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{"novice": 1.0, "ace": 1.0}}
		for k, v := range extra {
			parameters[k] = v
		}
		made, err := (&Air{}).Create(game.Session{Identifier: "opening", Game: "air", Mode: "joust", Seed: seed, Parameters: parameters})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		t.Cleanup(i.Close)
		var pair [2]*flight.State // by slot parity, as bvr() places them
		for _, s := range i.slots() {
			if a := i.aircraft[s]; a != nil && a.model != nil {
				pair[s%2] = &a.model.State
			}
		}
		if pair[0] == nil || pair[1] == nil {
			t.Fatal("the joust pair does not hold an even and an odd slot")
		}
		return i, pair
	}
	i, pair := joust(nil)
	o := Draw(i.environment.Seed, i.environment.Wrap)
	if i.opening != o || i.apart != o.Apart {
		t.Fatalf("the joust opened on %+v, apart %.0f; the seed draws %+v", i.opening, i.apart, o)
	}
	for slot, st := range pair {
		want := o.state(slot, o.Apart)
		if st.Position.Subtract(want.Position).Length() > 1 {
			t.Errorf("slot %d spawned at %+v, want %+v", slot, st.Position, want.Position)
		}
		if heading := st.Velocity.Normalize(); heading.Dot(want.Velocity.Normalize()) < 0.999 {
			t.Errorf("slot %d spawned heading %+v, want %+v", slot, heading, want.Velocity.Normalize())
		}
		if speed := st.Velocity.Length(); math.Abs(speed-o.Speed) > 0.5 { // the match has no wind
			t.Errorf("slot %d spawned at %.1f m/s, want the drawn %.1f", slot, speed, o.Speed)
		}
	}
	i, pair = joust(map[string]any{"opening": "head"})
	if i.opening != headon() || pair[0].Position.Y != bvraltitude || pair[1].Position.Y != bvraltitude || math.Abs(pair[0].Velocity.Length()-bvrspeed) > 0.5 {
		t.Errorf(`"opening": "head" did not pin the classic start: %+v, blocks %.0f/%.0f`, i.opening, pair[0].Position.Y, pair[1].Position.Y)
	}
}

// TestWeaponsGrant (#32): the three loadout classes clamp at the grant —
// guns strips everything, Fox 2 keeps the heaters and the tank, open keeps
// the lot.
func TestWeaponsGrant(t *testing.T) {
	request := map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"2": map[string]any{"fixture": "twin", "stores": []any{"120c", "9m"}},
		"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
		"5": map[string]any{"fixture": "pylon", "stores": []any{"tank"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	}
	open := stores_grant(request, "open")
	if len(stores_amraams(open)) != 2 || len(stores_rounds(open)) != 3 {
		t.Fatalf("open kept %d AMRAAMs / %d heaters, want 2 / 3", len(stores_amraams(open)), len(stores_rounds(open)))
	}
	fox2 := stores_grant(request, "fox2")
	if len(stores_amraams(fox2)) != 0 {
		t.Fatalf("fox2 kept %d AMRAAMs", len(stores_amraams(fox2)))
	}
	if len(stores_rounds(fox2)) != 3 {
		t.Fatalf("fox2 kept %d heaters, want 3", len(stores_rounds(fox2)))
	}
	if fox2["5"].Stores[0] != "tank" || fox2["2"].Fixture != "twin" {
		t.Fatalf("fox2 stripped the tank or the fixture")
	}
	guns := stores_grant(request, "guns")
	if len(stores_amraams(guns)) != 0 || len(stores_rounds(guns)) != 0 {
		t.Fatalf("guns kept missiles")
	}
	if guns["5"].Stores[0] != "tank" {
		t.Fatalf("guns stripped the tank")
	}
}

// TestBvrJoust (#32): the BVR start spawns the pair head-on across the
// derived separation, organised and WEAPONS FREE from the first tick — the
// distance is the hold. The merge joust keeps its 3/9 gate untouched.
func TestBvrJoust(t *testing.T) {
	make_joust := func(start string) *instance {
		parameters := map[string]any{"missiles": true}
		if start != "" {
			parameters["start"] = start
			parameters["opening"] = "head" // the classic start; TestOpeningSpawn covers the drawn one
		}
		made, err := (&Air{}).Create(game.Session{Mode: "joust", Seed: 7, Parameters: parameters})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		i := made.(*instance)
		if _, err := i.Join(game.Player{Name: "one", Slot: 0}); err != nil {
			t.Fatalf("join one: %v", err)
		}
		if _, err := i.Join(game.Player{Name: "two", Slot: 1}); err != nil {
			t.Fatalf("join two: %v", err)
		}
		return i
	}

	bvr := make_joust("bvr")
	a, b := bvr.aircraft[0], bvr.aircraft[1]
	apart := bvr.span(a.model, b.model)
	if math.Abs(apart-separation()) > 1000 {
		t.Fatalf("BVR pair spawned %.0f m apart, want the derived %.0f", apart, separation())
	}
	if !bvr.free() {
		t.Fatalf("the BVR joust held weapons — the separation is the hold")
	}
	if a.model.State.Position.Y < bvraltitude-200 || a.model.State.Position.Y > bvraltitude+200 {
		t.Fatalf("BVR spawn altitude %.0f, want the block at %.0f", a.model.State.Position.Y, bvraltitude)
	}
	// Head-on: each nose points at the other jet.
	sight, _ := bvr.bearing(a.model.State.Position, b.model.State.Position)
	if sight.Dot(a.model.State.Attitude.Rotate(flight.Vec3{X: 1})) < 0.98 {
		t.Fatalf("the BVR pair did not spawn pointed at each other")
	}

	merge := make_joust("")
	if merge.free() {
		t.Fatalf("the merge joust freed weapons before the 3/9 crossing")
	}
	if d := merge.span(merge.aircraft[0].model, merge.aircraft[1].model); d > 3*ring {
		t.Fatalf("the merge joust spawned %.0f m apart — the BVR change leaked into it", d)
	}
}

// TestSpaced (#32): spaced open respawns re-enter half the separation from
// the fight's centre of mass, approaching it; anchored team spawns hold each
// side at its own anchor, the full separation from the other's.
// TestSpaced: the anchored-sides rule, and what an open match does instead of
// it. The flag once spaced open re-entries at half the derived separation;
// arrivals there are now placed clear of the fight (spawn_test.go covers the
// placement itself, this covers the real Join path and the teams anchors).
func TestSpaced(t *testing.T) {
	made, err := (&Air{}).Create(game.Session{Mode: "furball", Seed: 7, Parameters: map[string]any{"missiles": true, "spaced": true}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	i := made.(*instance)
	if _, err := i.Join(game.Player{Name: "one", Slot: 0}); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := i.Join(game.Player{Name: "two", Slot: 1}); err != nil {
		t.Fatalf("join: %v", err)
	}
	// "spaced" is a TEAMS rule now: it anchors the two walls, and an open
	// match ignores it, because every arrival there is placed clear of the
	// fight and pointed at it whether the flag is set or not. The second
	// joiner therefore enters at the gun clearance, not the half-separation
	// this once asserted - close enough that the fight is a turn away, far
	// enough that nobody arrives inside a pipper.
	a, b := i.aircraft[0], i.aircraft[1]
	d := i.span(a.model, b.model)
	if d < clearance || d > 4*clearance {
		t.Fatalf("the second joiner entered %.0f m from the first, want between %.0f and %.0f", d, clearance, 4*clearance)
	}
	sight, _ := i.bearing(b.model.State.Position, a.model.State.Position)
	if sight.Dot(b.model.State.Attitude.Rotate(flight.Vec3{X: 1})) < 0.9 {
		t.Fatalf("the entry does not approach the fight")
	}

	teams, err := (&Air{}).Create(game.Session{Mode: "teams", Seed: 7, Parameters: map[string]any{"missiles": true, "spaced": true}})
	if err != nil {
		t.Fatalf("create teams: %v", err)
	}
	ti := teams.(*instance)
	if _, err := ti.Join(game.Player{Name: "red", Slot: 0}); err != nil {
		t.Fatalf("join red: %v", err)
	}
	if _, err := ti.Join(game.Player{Name: "blue", Slot: 1}); err != nil {
		t.Fatalf("join blue: %v", err)
	}
	red, blue := ti.aircraft[0], ti.aircraft[1]
	if red.team == blue.team {
		t.Fatalf("the pair landed on one side (%s/%s)", red.team, blue.team)
	}
	if d := ti.span(red.model, blue.model); math.Abs(d-separation()) > 5000 {
		t.Fatalf("anchored walls %.0f m apart, want ~%.0f", d, separation())
	}
}

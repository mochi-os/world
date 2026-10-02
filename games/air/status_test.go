// Mochi world: IFF and Link 16 status relay, flare-only dispense and fire extinguisher tests (air.go).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"reflect"
	"testing"

	"world/game"
)

// told collects the status events a step raised for one slot.
func told(i *instance, slot int) []map[string]any {
	var out []map[string]any
	for _, e := range i.events {
		if e["kind"] == "status" && e["slot"] == slot {
			out = append(out, e)
		}
	}
	return out
}

func sample(data map[string]any) []game.Input {
	return []game.Input{{Data: data}}
}

// TestStatusRelay: a craft's IFF and Link 16 status goes out to the session
// when it first steps, when it changes, and every two seconds for a client that
// joined since; a client that reports none keeps everything on, as the
// pre-flight leaves it.
func TestStatusRelay(t *testing.T) {
	i, slot := hostileSession(t)
	i.events = i.events[:0]
	i.Step(1, map[int][]game.Input{slot: sample(map[string]any{})})
	first := told(i, slot)
	if len(first) != 1 || first[0]["reply"] != true || first[0]["challenge"] != true || first[0]["link"] != true || first[0]["antenna"] != "both" || len(first[0]["tracks"].([]int)) != 0 {
		t.Fatalf("a craft that reports nothing went out as %v; want one status, everything on, both antennas and no tracks", first)
	}
	i.events = i.events[:0]
	i.Step(2, map[int][]game.Input{slot: sample(map[string]any{})})
	if again := told(i, slot); len(again) != 0 {
		t.Fatalf("an unchanged status went out again: %v", again)
	}
	i.Step(3, map[int][]game.Input{slot: sample(map[string]any{"status": []any{uint64(2 | 16), uint64(1), int64(4)}})})
	changed := told(i, slot)
	if len(changed) != 1 || changed[0]["reply"] != false || changed[0]["challenge"] != true || changed[0]["link"] != false || changed[0]["antenna"] != "lower" || !reflect.DeepEqual(changed[0]["tracks"], []int{1, 4}) {
		t.Fatalf("a changed status went out as %v; want reply and link off, challenge on, the lower antenna, tracks [1 4]", changed)
	}
	i.events = i.events[:0]
	i.Step(4, map[int][]game.Input{slot: sample(map[string]any{"status": []any{uint64(2 | 8), uint64(1), int64(4)}})})
	if turned := told(i, slot); len(turned) != 1 || turned[0]["antenna"] != "upper" {
		t.Fatalf("a change of antenna alone went out as %v; want one status with the upper antenna", turned)
	}
	i.events = i.events[:0]
	i.Step(5, map[int][]game.Input{slot: sample(map[string]any{"status": []any{uint64(2 | 8), uint64(1), int64(5)}})})
	if other := told(i, slot); len(other) != 1 || !reflect.DeepEqual(other[0]["tracks"], []int{1, 5}) {
		t.Fatalf("one track changed for another went out as %v; want one status with tracks [1 5]", other)
	}
	i.Step(5, map[int][]game.Input{slot: sample(map[string]any{"status": []any{uint64(2 | 16), uint64(1), int64(4)}})})
	i.events = i.events[:0]
	i.Step(6, map[int][]game.Input{slot: sample(map[string]any{})})
	if kept := told(i, slot); len(kept) != 0 {
		t.Fatalf("a sample with no status changed the one held: %v", kept)
	}
	i.Step(120, map[int][]game.Input{slot: sample(map[string]any{})})
	refreshed := told(i, slot)
	if len(refreshed) != 1 || refreshed[0]["reply"] != false || !reflect.DeepEqual(refreshed[0]["tracks"], []int{1, 4}) {
		t.Fatalf("the two-second refresh sent %v; want the held status once", refreshed)
	}
}

// TestStatusTracks: a status is one packed array - its flags, then the tracks.
// The tracks a client names are whole slots inside the wire's six bits, each
// once, and no more than a radar holds.
func TestStatusTracks(t *testing.T) {
	list := []any{uint64(1), uint64(3), float64(3), float64(2.5), int64(-1), uint64(63), "4", uint64(5)}
	got, ok := reported(map[string]any{"status": list})
	if !ok || !reflect.DeepEqual(got.tracks, []int{3, 5}) {
		t.Fatalf("status %v read with tracks %v (reported %v); want [3 5]", list, got.tracks, ok)
	}
	many := []any{uint64(7)}
	for k := 0; k < 40; k++ {
		many = append(many, uint64(k))
	}
	got, _ = reported(map[string]any{"status": many})
	if len(got.tracks) != tracked {
		t.Fatalf("%d tracks taken of 40 named; want %d", len(got.tracks), tracked)
	}
	for _, none := range []map[string]any{{}, {"status": []any{}}, {"status": "7"}, {"status": []any{"7"}}, {"status": []any{float64(1.5)}}, {"status": []any{int64(-1)}}} {
		if _, ok := reported(none); ok {
			t.Fatalf("a sample with %v read as a status report", none)
		}
	}
	for flags, want := range map[uint64]status{
		0:     {antenna: "both"},
		1:     {reply: true, antenna: "both"},
		2:     {challenge: true, antenna: "both"},
		4:     {link: true, antenna: "both"},
		7 | 8: {reply: true, challenge: true, link: true, antenna: "upper"},
		16:    {antenna: "lower"},
	} {
		if got, _ := reported(map[string]any{"status": []any{flags}}); !got.same(want) {
			t.Fatalf("flags %d read as %+v; want %+v", flags, got, want)
		}
	}
}

// TestBotStatus: a bot's status is its own radar's - everything on, and the
// aircraft it has locked.
func TestBotStatus(t *testing.T) {
	i, _ := hostileSession(t)
	bot := &craft{bot: true, alive: true, emitter: 2, lock: 7}
	i.aircraft[9] = bot
	i.events = i.events[:0]
	i.statuses(1)
	sent := told(i, 9)
	if len(sent) != 1 || sent[0]["link"] != true || sent[0]["reply"] != true || !reflect.DeepEqual(sent[0]["tracks"], []int{7}) {
		t.Fatalf("a locked bot went out as %v; want everything on and tracks [7]", sent)
	}
	bot.emitter = 1
	i.events = i.events[:0]
	i.statuses(2)
	if sent = told(i, 9); len(sent) != 1 || len(sent[0]["tracks"].([]int)) != 0 {
		t.Fatalf("a searching bot went out as %v; want no tracks", sent)
	}
	delete(i.aircraft, 9)
}

// TestFlareSolo: with the dispenser at BYPASS the flare edge releases the
// flare alone - no chaff bundle beside it, and none spent.
func TestFlareSolo(t *testing.T) {
	for _, solo := range []bool{false, true} {
		i, slot := hostileSession(t)
		a := i.aircraft[slot]
		flares, chaff := a.flares, a.chaff
		i.events = i.events[:0]
		i.Step(0, map[int][]game.Input{slot: sample(map[string]any{"flare": true, "solo": solo})})
		kinds := []any{}
		for _, e := range i.events {
			if e["slot"] == slot && (e["kind"] == "chaff" || e["kind"] == "flare") {
				kinds = append(kinds, e["kind"])
			}
		}
		want, bundles := []any{"flare", "chaff"}, chaff-1
		if solo {
			want, bundles = []any{"flare"}, chaff
		}
		if !reflect.DeepEqual(kinds, want) || a.flares != flares-1 || a.chaff != bundles {
			t.Fatalf("solo=%v: events %v, flares %d of %d, chaff %d of %d; want %v and chaff %d", solo, kinds, a.flares, flares, a.chaff, chaff, want, bundles)
		}
	}
}

// TestExtinguishInput: the FIRE EXTGH edge discharges the craft's one bottle
// into the bay whose engine is secured; a second press has no bottle left.
func TestExtinguishInput(t *testing.T) {
	i, slot := hostileSession(t)
	a := i.aircraft[slot]
	press := func(tick uint64, on bool) {
		i.Step(tick, map[int][]game.Input{slot: sample(map[string]any{"throttle": 0.8, "port": true, "extinguish": on})})
	}
	a.condition.Fire[0] = 0.5
	press(0, false)
	if a.condition.Fire[0] <= 0 || a.discharged {
		t.Fatalf("the bottle went with no press: fire %.2f, discharged %v", a.condition.Fire[0], a.discharged)
	}
	press(1, true)
	if a.condition.Fire[0] != 0 || !a.discharged {
		t.Fatalf("the press left the secured bay burning at %.2f (discharged %v)", a.condition.Fire[0], a.discharged)
	}
	press(2, false)
	a.condition.Fire[0] = 0.5
	press(3, true)
	if a.condition.Fire[0] <= 0.4 {
		t.Fatalf("a second press found a bottle: fire %.2f", a.condition.Fire[0])
	}
}

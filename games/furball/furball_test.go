// Mochi world: Furball game module tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package furball

import (
	"testing"

	"github.com/fxamacker/cbor/v2"

	"world/game"
	"world/games/furball/flight"
)

func build(t *testing.T, mode string, parameters map[string]any, players int) *instance {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "test", Game: "furball", Mode: mode, Capacity: 16, Seed: 2, Parameters: parameters})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	i := made.(*instance)
	for slot := 0; slot < players; slot++ {
		if _, err := i.Join(game.Player{Identity: "", Name: "p", Slot: slot}); err != nil {
			t.Fatalf("join %d: %v", slot, err)
		}
	}
	return i
}

// place puts b dead ahead of a, in guns range.
func place(i *instance, a int, b int, distance float64) {
	shooter := &i.aircraft[a].model.State
	target := &i.aircraft[b].model.State
	target.Position = shooter.Position
	forward := shooter.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
	target.Position.X += forward.X * distance
	target.Position.Y += forward.Y * distance
	target.Position.Z += forward.Z * distance
	target.Velocity = shooter.Velocity
	target.Attitude = shooter.Attitude
}

func fire(i *instance, slot int, sample map[string]any) {
	i.Step(0, map[int][]game.Input{slot: {{Sequence: 1, Data: sample}}})
}

// TestJoust: systems damage has no health pool — a tracking burst wrecks
// the target's systems, the wreck falls, and the splash is CREDITED to the
// shooter, finishing the joust. Fought low so the fall is quick.
func TestJoust(t *testing.T) {
	i := build(t, "joust", nil, 2)
	steady := map[string]any{"throttle": 0.85, "fire": true}
	for tick := 0; tick < 60*5; tick++ { // 5 s of held trigger at 250 m astern
		place(i, 0, 1, 250)
		i.aircraft[0].model.State.Position.Y = 400 // fight on the deck: a crippled jet splashes fast
		i.aircraft[1].model.State.Position.Y = 400
		fire(i, 0, steady)
		if done, _ := i.Finished(); done {
			break
		}
	}
	damage := &i.aircraft[1].model.State.Damage
	wounded := damage.Engine[0]+damage.Engine[1] > 0.3 || damage.Leak > 0.2 || total(damage.Element) > 0.5 || i.aircraft[1].condition.Killed
	if done, _ := i.Finished(); !done && !wounded {
		t.Fatalf("5 s of tracking fire left the target untouched: engines %.2f/%.2f leak %.2f elements %.2f",
			damage.Engine[0], damage.Engine[1], damage.Leak, total(damage.Element))
	}
	for tick := 0; tick < 60*90; tick++ { // let the wreck fall
		if done, _ := i.Finished(); done {
			break
		}
		i.Step(uint64(tick), nil)
	}
	done, results := i.Finished()
	if !done {
		t.Fatal("joust did not finish after the target was wrecked")
	}
	if results["winner"] != 0 || results["loser"] != 1 {
		t.Fatalf("wrong outcome: %v", results)
	}
	if i.aircraft[1].alive {
		t.Fatal("loser respawned in a joust")
	}
	if _, err := i.Join(game.Player{Name: "late", Slot: 2}); err == nil {
		t.Fatal("joined a finished joust")
	}
}

func total(element []float64) float64 {
	sum := 0.0
	for _, v := range element {
		sum += v
	}
	return sum
}

// TestJoustLeave: abandoning a live joust hands the win to the stayer.
func TestJoustLeave(t *testing.T) {
	i := build(t, "joust", nil, 2)
	i.Leave(game.Player{Slot: 1})
	done, results := i.Finished()
	if !done || results["winner"] != 0 {
		t.Fatalf("expected slot 0 to win by forfeit: %v %v", done, results)
	}
}

// TestJoustWaiting: the first joiner is held frozen until the pair completes,
// then both merge fresh via match-start respawn events.
func TestJoustWaiting(t *testing.T) {
	i := build(t, "joust", nil, 1)
	before := i.aircraft[0].model.State.Position
	for tick := 0; tick < 120; tick++ {
		i.Step(0, nil)
	}
	if moved := i.aircraft[0].model.State.Position.Subtract(before).Length(); moved > 0.01 {
		t.Fatalf("lone joust player should be held frozen, moved %.2f m", moved)
	}
	welcome, err := i.Join(game.Player{Slot: 1})
	if err != nil {
		t.Fatal(err)
	}
	if waiting, _ := welcome["waiting"].(bool); waiting {
		t.Fatal("second joiner must not be waiting")
	}
	if !i.started {
		t.Fatal("pair complete: the match must have started")
	}
	respawns := 0
	for _, e := range i.events {
		if e["kind"] == "respawn" {
			respawns++
		}
	}
	if respawns != 2 {
		t.Fatalf("expected a match-start respawn per slot, got %d", respawns)
	}
	after := i.aircraft[0].model.State.Position
	for tick := 0; tick < 60; tick++ {
		i.Step(0, nil)
	}
	if moved := i.aircraft[0].model.State.Position.Subtract(after).Length(); moved < 1 {
		t.Fatal("match started: physics should be running")
	}
}

// TestJoustMerge: weapons hold until either aircraft crosses the other's
// 3/9 line; the crossing raises fighton and frees the guns.
func TestJoustMerge(t *testing.T) {
	i := build(t, "joust", nil, 2)
	i.Step(0, nil)
	if i.merged || i.free() {
		t.Fatal("head-on at the ring: weapons must be held before the merge")
	}
	// Park slot 1 directly BEHIND slot 0 (crossed the 3/9 line by any margin).
	a, b := i.aircraft[0], i.aircraft[1]
	forward := a.model.State.Attitude.Rotate(flight.Vec3{X: 1})
	b.model.State.Position = a.model.State.Position.Subtract(forward.Scale(100))
	i.Step(1, nil)
	if !i.merged || !i.free() {
		t.Fatal("crossing the 3/9 line must merge the fight and free the weapons")
	}
	fighton := false
	for _, e := range i.events {
		if e["kind"] == "fighton" {
			fighton = true
		}
	}
	if !fighton {
		t.Fatal("the merge must announce fighton")
	}
}

// BenchmarkStep100: one 60 Hz tick of a full 100-player match (each tick runs
// 4 flight substeps per aircraft plus the battle cascade and gun processing) —
// the server CPU budget check for #81. Run: go test ./games/furball -bench Step100 -run xx
func BenchmarkStep100(b *testing.B) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "bench", Game: "furball", Mode: "furball", Capacity: 100, Seed: 2, Parameters: map[string]any{"bots": 99.0}})
	if err != nil {
		b.Fatal(err)
	}
	i := made.(*instance)
	if _, err := i.Join(game.Player{Name: "human", Slot: 0}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		i.Step(uint64(n), nil)
		i.Snapshot(uint64(n))
	}
}

// TestBotsEndure: the bot autopilot must actually keep bots flying — the
// first open-loop version spiralled every bot into the sea. Twenty bots,
// two simulated minutes: nobody dies, nobody sinks low.
func TestBotsEndure(t *testing.T) {
	if testing.Short() {
		t.Skip("two simulated minutes")
	}
	g := New()
	made, err := g.Create(game.Session{Identifier: "endure", Game: "furball", Mode: "furball", Capacity: 100, Seed: 2, Parameters: map[string]any{"bots": 20.0}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	for tick := uint64(0); tick < 60*120; tick++ {
		i.Step(tick, nil)
	}
	for slot, a := range i.aircraft {
		if a.deaths > 0 {
			t.Fatalf("bot %d died %d times", slot, a.deaths)
		}
		if y := a.model.State.Position.Y; y < 1500 {
			t.Fatalf("bot %d sagging at %.0f m", slot, y)
		}
	}
}

// TestFurballRespawn: open mode respawns after the pause.
func TestFurballRespawn(t *testing.T) {
	i := build(t, "furball", nil, 2)
	i.kill(1, 0)
	if done, _ := i.Finished(); done {
		t.Fatal("open match finished on a kill")
	}
	for tick := 0; tick < 60*6; tick++ {
		i.Step(0, nil)
	}
	if !i.aircraft[1].alive {
		t.Fatal("no respawn in open mode")
	}
}

// TestMissiles: the rule gates launches; an allowed missile tracks and kills.
func TestMissiles(t *testing.T) {
	denied := build(t, "furball", nil, 2)
	place(denied, 0, 1, 2000)
	fire(denied, 0, map[string]any{"missile": true})
	if len(denied.flying) != 0 {
		t.Fatal("missile launched despite the guns-only rule")
	}

	allowed := build(t, "furball", map[string]any{"missiles": true}, 2)
	place(allowed, 0, 1, 2000)
	fire(allowed, 0, map[string]any{"missile": true})
	if len(allowed.flying) != 1 {
		t.Fatal("missile did not launch")
	}
	for tick := 0; tick < 60*12; tick++ {
		allowed.Step(uint64(tick), nil)
		if tick < 60*2 {
			place(allowed, 0, 1, 2000) // hold the geometry while the missile runs in
		}
		if !allowed.aircraft[1].alive {
			return // killed outright by the warhead
		}
		damage := &allowed.aircraft[1].model.State.Damage
		if damage.Engine[0]+damage.Engine[1] > 0.3 || total(damage.Element) > 0.5 || damage.Leak > 0.2 {
			return // fringe burst: fragment damage counts as a score
		}
	}
	t.Fatal("missile neither killed nor damaged the target")
}

// TestDecoy: flares decoy with aspect-graded probability, judged once per
// flare window per missile. A salvo against a flaring target must lose
// SOME missiles to decoys and (with these seeds) keep some — the graded
// model, not a coin fixed for the whole salvo.
func TestDecoy(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true}, 2)
	for n := 0; n < 6; n++ {
		place(i, 0, 1, 3000)
		fire(i, 0, map[string]any{"missile": true})
	}
	if len(i.flying) != 6 {
		t.Fatalf("expected 6 missiles, got %d", len(i.flying))
	}
	place(i, 0, 1, 3000)
	fire(i, 1, map[string]any{"flare": true})
	i.Step(1, nil)
	remaining := len(i.flying)
	if remaining == 6 {
		t.Fatal("no missile decoyed: the flare did nothing")
	}
	if remaining == 0 {
		t.Fatal("every missile decoyed: rear-aspect flares should not be certain")
	}
}

// TestSnapshotSize: the per-recipient snapshot datagram must stay under the
// QUIC datagram MTU — the 106-word core once burst it and snapshots vanished
// silently (SendDatagram discards oversized frames). Guard it forever.
func TestSnapshotSize(t *testing.T) {
	// A FULL 100-player match (#81): every recipient's snapshot datagram —
	// shared body + own core + own interest-managed pose blob — must stay
	// under the QUIC datagram MTU with margin. SendDatagram discards
	// oversized frames silently, so this is the one guard that matters.
	i := build(t, "furball", map[string]any{"missiles": true}, 100)
	for tick := uint64(1); tick <= 8; tick++ { // several ticks so the far-tail rotation is exercised
		snapshot := i.Snapshot(tick)
		cores, _ := snapshot["cores"].(map[int]any)
		poses, _ := snapshot["poses"].(map[int]any)
		delete(snapshot, "cores")
		delete(snapshot, "poses")
		for slot := 0; slot < 100; slot++ {
			envelope := map[string]any{"kind": "snapshot", "tick": tick, "acknowledged": uint32(1000)}
			for k, v := range snapshot {
				envelope[k] = v
			}
			envelope["core"] = cores[slot]
			packed, err := cbor.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if len(packed) > 1250 {
				t.Fatalf("slot %d snapshot datagram %d bytes (budget 1250)", slot, len(packed))
			}
			flock, _ := poses[slot].(map[string]any)
			second := map[string]any{"kind": "poses", "tick": tick}
			for k, v := range flock {
				second[k] = v
			}
			packed, err = cbor.Marshal(second)
			if err != nil {
				t.Fatal(err)
			}
			if len(packed) > 1250 {
				t.Fatalf("slot %d poses datagram %d bytes (budget 1250)", slot, len(packed))
			}
		}
	}
}


// TestEngagement: a scripted fight — sustained fire degrades systems and the
// event stream tells the story; the identical script with the identical seed
// produces the identical outcome (the determinism the SP/MP split relies on).
func TestEngagement(t *testing.T) {
	run := func() (map[string]int, float64, float64) {
		i := build(t, "furball", nil, 2)
		kinds := map[string]int{}
		for tick := 0; tick < 60*8; tick++ {
			place(i, 0, 1, 220)
			i.aircraft[0].model.State.Position.Y = 3000
			i.aircraft[1].model.State.Position.Y = 3000
			i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: uint32(tick + 1), Data: map[string]any{"throttle": 0.85, "fire": true}}}})
			for _, event := range i.Events() {
				kinds[event["kind"].(string)]++
			}
		}
		a := i.aircraft[1]
		if a.model == nil {
			return kinds, 99, 99 // wrecked: pilot killed mid-script
		}
		return kinds, a.model.State.Damage.Engine[0] + a.model.State.Damage.Engine[1], total(a.model.State.Damage.Element)
	}
	first, engines, elements := run()
	if first["hit"] == 0 {
		t.Fatal("8 s of tracking fire produced no hit events")
	}
	if engines < 0.1 && elements < 0.5 && first["pilot"] == 0 && first["fire"] == 0 {
		t.Fatalf("sustained fire degraded nothing: engines %.2f elements %.2f events %v", engines, elements, first)
	}
	second, engines2, elements2 := run()
	if engines != engines2 || elements != elements2 || first["hit"] != second["hit"] || first["fire"] != second["fire"] || first["pilot"] != second["pilot"] {
		t.Fatalf("the same scripted fight diverged: %v/%v vs %v/%v, events %v vs %v", engines, elements, engines2, elements2, first, second)
	}
}

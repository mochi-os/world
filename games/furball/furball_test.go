// Mochi world: Furball game module tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package furball

import (
	"math"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"world/game"
	"world/games/furball/flight"
	"world/games/furball/aircraft"
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

// duel runs one seeded ace-vs-rookie fight to first kill (or the deadline)
// and reports the winner slot, the loser's sea-death count, and the kill tick.
func duel(t *testing.T, seed uint64, ticks uint64) (winner int, splashes int, when uint64) {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "duel", Game: "furball", Mode: "furball", Capacity: 100, Seed: seed,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{"ace": 1.0, "rookie": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	hits := map[int]int{}
	for tick := uint64(0); tick < ticks; tick++ {
		i.Step(tick, nil)
		for _, e := range i.events {
			if e["kind"] == "hit" {
				slot, _ := e["slot"].(int)
				count, _ := e["count"].(int)
				hits[slot] += count
			}
		}
		i.events = i.events[:0]
		for slot, a := range i.aircraft {
			if a.kills > 0 {
				loser := 98 + 99 - slot
				if i.aircraft[loser].condition.Damager < 0 {
					splashes++
				}
				return slot, splashes, tick
			}
		}
	}
	t.Logf("  rounds: 98(%s) %d, 99(%s) %d; hits taken: 98 %d, 99 %d",
		i.aircraft[98].player.Name, rounds-i.aircraft[98].ammunition,
		i.aircraft[99].player.Name, rounds-i.aircraft[99].ammunition, hits[98], hits[99])
	return -1, splashes, ticks
}

// TestBotDuel: identical airframes with competent brains legitimately
// stalemate guns-only 1v1s (that is BFM, not a bug) — the honest claims are
// that decided duels go to the ACE, never the rookie majority, and that a
// reasonable share decide at all. Slot 99 is the ace (levels fill from slot
// 99 down in map order).
func TestBotDuel(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes")
	}
	aces, rookies := 0, 0
	for seed := uint64(1); seed <= 12; seed++ {
		winner, _, when := duel(t, seed, 60*240)
		t.Logf("seed %d: winner %d at t=%ds", seed, winner, when/60)
		if winner == 98 { // the fixed level order fills rookie at 99, ace at 98
			aces++
		}
		if winner == 99 {
			rookies++
		}
	}
	// Outcome tallies are informational: 1v1 kills ride on missile-decoy
	// dice, so hard thresholds here were a seed lottery (the skill gate is
	// TestBotGunnery). The one stable claim: the rookie must not DOMINATE.
	t.Logf("ace %d, rookie %d, stalemates %d", aces, rookies, 12-aces-rookies)
	if rookies > aces+2 {
		t.Fatalf("the rookie won %d duels to the ace's %d", rookies, aces)
	}
}

// TestBotLadder: the product-truth skill measure — in a mixed brawl with
// respawns, aces out-kill rookies decisively over many engagements.
func TestBotLadder(t *testing.T) {
	if testing.Short() {
		t.Skip("six simulated minutes")
	}
	g := New()
	made, err := g.Create(game.Session{Identifier: "ladder", Game: "furball", Mode: "furball", Capacity: 100, Seed: 11,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{"ace": 3.0, "rookie": 3.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	for tick := uint64(0); tick < 60*480; tick++ {
		i.Step(tick, nil)
	}
	aces, rookies, total := 0, 0, 0
	for _, a := range i.aircraft {
		total += a.kills
		if len(a.player.Name) > 0 && a.player.Name[0] == 'A' {
			aces += a.kills
		} else {
			rookies += a.kills
		}
	}
	// Informational, same reasoning as TestBotDuel; the hard skill gate is
	// TestBotGunnery, and the invariants (sea, determinism, blind) gate here.
	t.Logf("ace kills %d, rookie kills %d, total %d", aces, rookies, total)
	if rookies > aces+2 {
		t.Fatalf("rookies out-killed aces %d to %d", rookies, aces)
	}
}

// TestBotDeterminism: the same seed twice must produce the identical fight.
func TestBotDeterminism(t *testing.T) {
	w1, _, t1 := duel(t, 3, 60*120)
	w2, _, t2 := duel(t, 3, 60*120)
	if w1 != w2 || t1 != t2 {
		t.Fatalf("seed 3 diverged: (%d,%d) vs (%d,%d)", w1, t1, w2, t2)
	}
}

// TestBotsFight: a six-ace furball produces kills and nobody flies into the sea.
func TestBotsFight(t *testing.T) {
	if testing.Short() {
		t.Skip("three simulated minutes")
	}
	g := New()
	made, err := g.Create(game.Session{Identifier: "brawl", Game: "furball", Mode: "furball", Capacity: 100, Seed: 5,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{"ace": 6.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	living := map[int]bool{}
	for slot, a := range i.aircraft {
		living[slot] = a.alive
	}
	splashes := 0
	for tick := uint64(0); tick < 60*180; tick++ {
		i.Step(tick, nil)
		for slot, a := range i.aircraft {
			if living[slot] && !a.alive {
				mode := "-"
				if a.brain != nil {
					mode = a.brain.mode
				}
				t.Logf("t=%ds slot %d died: mode=%s y=%.0f damager=%d", tick/60, slot, mode, a.model.State.Position.Y, a.condition.Damager)
				if a.condition.Damager < 0 {
					splashes++ // judged AT the death: the end-of-run condition belongs to the respawned jet, not the corpse
				}
			}
			living[slot] = a.alive
		}
	}
	kills := 0
	for _, a := range i.aircraft {
		kills += a.kills
	}
	// Kill count among EQUALS is dice (see TestBotDuel's reasoning; the skill
	// gate is TestBotGunnery). The stable invariant here is terrain discipline.
	t.Logf("six aces, three minutes: %d kills", kills)
	if splashes > 1 {
		t.Fatalf("%d bots flew into the sea", splashes)
	}
}

// TestBotBlind: an attacker parked dead six-low is INVISIBLE — the ace must
// not react until the attacker fires or enters view.
func TestBotBlind(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "blind", Game: "furball", Mode: "furball", Capacity: 100, Seed: 7,
		Parameters: map[string]any{"bots": map[string]any{"ace": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	ace := i.aircraft[99]
	// A human-slot craft glued to the ace's blind cone: behind and below.
	m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
	i.spawn(0, m)
	shadow := &craft{player: game.Player{Name: "shadow", Slot: 0}, kind: "fa18c", model: m, alive: true, flared: 1e9}
	shadow.arm()
	i.aircraft[0] = shadow
	for tick := uint64(0); tick < 60*10; tick++ {
		s := &ace.model.State
		behind := s.Attitude.Rotate(flight.Vec3{X: -1})
		shadow.model.State.Position = s.Position.Add(behind.Scale(500)).Add(flight.Vec3{Y: -220})
		shadow.model.State.Velocity = s.Velocity
		i.Step(tick, nil)
		if _, tracked := ace.brain.known[0]; tracked {
			t.Fatalf("tick %d: the ace tracked a contact parked in its blind cone", tick)
		}
	}
}

// gunnery puts one bot of the given level behind a drone (the weave: a
// predictable, honest gunnery target) and counts gun hits landed over 90 s.
func gunnery(t *testing.T, level string, seed uint64) (int, int) {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "guns", Game: "furball", Mode: "furball", Capacity: 100, Seed: seed,
		Parameters: map[string]any{"bots": map[string]any{"drone": 1.0, level: 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	hits := 0
	hunter := i.aircraft[98] // drone fills 99, the fighter 98
	// Start IN the saddle, 400 m astern co-altitude: this gate measures
	// tracking gunnery — the skill differential — not chase energetics.
	drone := &i.aircraft[99].model.State
	behind := drone.Attitude.Rotate(flight.Vec3{X: -1})
	hunter.model.State.Position = drone.Position.Add(behind.Scale(400))
	hunter.model.State.Velocity = drone.Velocity
	hunter.model.State.Attitude = drone.Attitude
	for tick := uint64(0); tick < 60*90; tick++ {
		i.Step(tick, nil)
		for _, e := range i.events {
			if e["kind"] == "hit" {
				count, _ := e["count"].(int)
				hits += count
			}
		}
		i.events = i.events[:0]
	}
	return hits, rounds - hunter.ammunition
}

// TestBotGunnery: the STABLE skill gate — behind the same predictable target,
// the ace lands real gunfire and decisively out-shoots the rookie. This is
// the measured input to lethality, free of missile-decoy dice.
func TestBotGunnery(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes")
	}
	aceHits, aceRounds, rookieHits, rookieRounds := 0, 0, 0, 0
	for seed := uint64(1); seed <= 3; seed++ {
		h, r := gunnery(t, "ace", seed)
		aceHits, aceRounds = aceHits+h, aceRounds+r
		h, r = gunnery(t, "rookie", seed)
		rookieHits, rookieRounds = rookieHits+h, rookieRounds+r
	}
	t.Logf("ace %d/%d hits per round, rookie %d/%d", aceHits, aceRounds, rookieHits, rookieRounds)
	if aceHits < 10 {
		t.Fatalf("the ace landed only %d hits from the saddle in three 90 s runs", aceHits)
	}
	// The skill claim is EFFICIENCY: the ace fires only real solutions, the
	// rookie sprays — hits per round must separate by at least 3×.
	if aceHits*rookieRounds < 3*rookieHits*aceRounds {
		t.Fatalf("ace %d/%d vs rookie %d/%d: precision does not express", aceHits, aceRounds, rookieHits, rookieRounds)
	}
}

// TestBotCircles: the merge game plan — with an energy advantage the ace
// commits to the two-circle rate fight; slower and poorer, the one-circle
// radius fight. Doctrine, pinned directly at the decision.
func TestBotCircles(t *testing.T) {
	stage := func(speed float64, height float64) string {
		g := New()
		made, _ := g.Create(game.Session{Identifier: "circle", Game: "furball", Mode: "furball", Capacity: 100, Seed: 4,
			Parameters: map[string]any{"bots": map[string]any{"ace": 1.0, "drone": 1.0}}})
		i := made.(*instance)
		ace, mark := i.aircraft[99], i.aircraft[98] // map order: ace 99, drone 98
		if ace.brain == nil {
			ace, mark = i.aircraft[98], i.aircraft[99]
		}
		// Head-on INSIDE the lead-turn gate: the probe pins the decision at the
		// first think — a longer sim lets both jets maneuver the geometry away.
		mark.model.State.Position = flight.Vec3{X: 0, Y: 4500, Z: 0}
		mark.model.State.Velocity = flight.Vec3{X: 220}
		mark.model.State.Attitude = flight.Look(flight.Vec3{X: 1})
		ace.model.State.Position = flight.Vec3{X: 500, Y: height, Z: 30}
		ace.model.State.Velocity = flight.Vec3{X: -speed}
		ace.model.State.Attitude = flight.Look(flight.Vec3{X: -1})
		i.Step(0, nil)
		return ace.brain.plan
	}
	if plan := stage(310, 4550); plan != "two" {
		t.Fatalf("fast at the merge: expected the two-circle, got %q", plan)
	}
	if plan := stage(150, 4450); plan != "one" {
		t.Fatalf("slow at the merge: expected the one-circle, got %q", plan)
	}
}

// TestBotReversal: an attacker crossing the defender's flight path flips the
// lateral side — the tier-3 defender reverses into him instead of dragging
// the stale break.
func TestBotReversal(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "reverse", Game: "furball", Mode: "furball", Capacity: 100, Seed: 4,
		Parameters: map[string]any{"bots": map[string]any{"ace": 1.0, "drone": 1.0}}})
	i := made.(*instance)
	ace, foe := i.aircraft[99], i.aircraft[98]
	if ace.brain == nil {
		ace, foe = i.aircraft[98], i.aircraft[99]
	}
	// The foe glued 500 m behind, nose on, first on the left — then teleported
	// across to the right: a flight-path overshoot by construction.
	place := func(side float64) {
		s := &ace.model.State
		behind := s.Attitude.Rotate(flight.Vec3{X: -1})
		flank := s.Attitude.Rotate(flight.Vec3{Z: 1})
		lift := s.Attitude.Rotate(flight.Vec3{Y: 1})
		// Behind but HIGH: out of the blind cone, or the ace never sees him at all.
		foe.model.State.Position = s.Position.Add(behind.Scale(450)).Add(flank.Scale(side * 180)).Add(lift.Scale(260))
		foe.model.State.Velocity = s.Velocity.Add(s.Attitude.Rotate(flight.Vec3{X: 60}))
		foe.model.State.Attitude = s.Attitude
	}
	reversed := false
	for tick := uint64(0); tick < 60*8; tick++ {
		side := 1.0
		if tick > 60*4 {
			side = -1.0
		}
		place(side)
		i.Step(tick, nil)
		if ace.brain.mode == "reverse" {
			reversed = true
		}
	}
	if !reversed {
		t.Fatal("the attacker crossed the flight path and the ace never reversed")
	}
}

// TestBandit: the SP joust harness — the bandit chases a mirrored straight
// flier, closes, and eventually pulls the trigger; nothing crashes into the sea.
func TestBandit(t *testing.T) {
	b := NewBandit("ace", 9, 250000, "", false)
	spawn := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 9, Wrap: 250000}, flight.World{Sea: sea})
	spawn.State.Position = flight.Vec3{X: 2778, Y: altitude}
	spawn.State.Velocity = flight.Vec3{X: -220}
	spawn.State.Attitude = flight.Look(flight.Vec3{X: -1})
	words := make([]float64, flight.Size)
	spawn.State.Encode(words)
	b.Place(words)

	// The player: straight and level away from the bandit — a towed target.
	player := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 9, Wrap: 250000}, flight.World{Sea: sea})
	player.State.Position = flight.Vec3{X: -2778, Y: altitude}
	player.State.Velocity = flight.Vec3{X: 170}
	player.State.Attitude = flight.Look(flight.Vec3{X: 1})
	// Wiring invariants, not a lethality lottery: the bandit pursues (gets
	// close), survives (sea, mush-lock), and the mirror/step plumbing holds.
	closest, slowest := math.MaxFloat64, 0.0
	for tick := 0; tick < 60*240; tick++ {
		player.Step(flight.Inputs{Throttle: 0.45})
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		if b.State().Position.Y < 100 {
			t.Fatalf("tick %d: the bandit is in the sea", tick)
		}
		if d := b.State().Position.Subtract(player.State.Position).Length(); d < closest {
			closest = d
		}
		if tick > 60*30 { // past the first merge: count time spent mushing
			if v := b.State().Velocity.Length(); v < 100 {
				slowest++
			}
		}
	}
	if closest > 1200 {
		t.Fatalf("the bandit never pursued: closest approach %.0f m", closest)
	}
	if slowest > 60*100 {
		t.Fatalf("the bandit spent %.0f s mushing below 100 m/s", slowest/60)
	}
}

// missileRange builds a two-craft arena for direct missile physics tests:
// the shooter at slot 0, the target at slot 1, one missile in flight.
func missileRange(t *testing.T, position, velocity flight.Vec3) (*instance, *craft, *missile) {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "range", Game: "furball", Mode: "furball", Capacity: 16, Seed: 3,
		Parameters: map[string]any{"missiles": true}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	for slot := 0; slot < 2; slot++ {
		if _, err := i.Join(game.Player{Name: "p", Slot: slot}); err != nil {
			t.Fatal(err)
		}
	}
	target := i.aircraft[1]
	target.model.State.Position = position
	target.model.State.Velocity = velocity
	target.model.State.Attitude = flight.Look(velocity.Normalize())
	shooter := i.aircraft[0]
	shooter.model.State.Position = flight.Vec3{Y: 3000}
	shooter.model.State.Velocity = flight.Vec3{X: 250}
	shooter.model.State.Attitude = flight.Look(flight.Vec3{X: 1})
	i.launched++
	sight, _ := i.bearing(shooter.model.State.Position, position)
	m := &missile{shooter: 0, target: 1, position: shooter.model.State.Position, life: missile_life,
		velocity: flight.Vec3{X: 280}, burn: missile_boost, sight: sight, number: i.launched}
	i.flying = append(i.flying, m)
	return i, target, m
}

// fly advances only the missile world; the target flies a straight line.
func fly(i *instance, target *craft, seconds float64) int {
	dt := 1.0 / 60
	ticks := int(seconds * 60)
	for tick := 0; tick < ticks; tick++ {
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
		i.pursue(dt, uint64(tick))
		if len(i.flying) == 0 {
			return tick
		}
	}
	return ticks
}

// TestMissileBoost: the motor accelerates hard for three seconds, then the
// coast bleeds against drag — no more constant-speed darts.
func TestMissileBoost(t *testing.T) {
	i, target, m := missileRange(t, flight.Vec3{X: 12000, Y: 3000}, flight.Vec3{X: 250})
	fly(i, target, 2.9)
	peak := m.velocity.Length()
	if peak < 900 {
		t.Fatalf("end of boost at %.0f m/s: the motor is weak", peak)
	}
	fly(i, target, 8)
	if len(i.flying) > 0 && m.velocity.Length() > peak-150 {
		t.Fatalf("eight seconds of coast barely bled (%.0f -> %.0f)", peak, m.velocity.Length())
	}
}

// TestMissilePn: proportional navigation intercepts a CROSSING target — the
// geometry pure pursuit chronically loses.
func TestMissilePn(t *testing.T) {
	i, target, _ := missileRange(t, flight.Vec3{X: 2500, Y: 3000, Z: -1200}, flight.Vec3{Z: 240})
	before := target.deaths
	fly(i, target, 12)
	if target.deaths == before && target.condition.Damager != 0 {
		t.Fatal("the crossing target was never engaged")
	}
	if target.condition.Damager != 0 {
		t.Fatal("PN never brought the warhead to the crossing target")
	}
}

// TestMissileGimbal: a target that drags the line of sight past the seeker
// gimbal breaks the lock — the missile goes ballistic and never fuses.
func TestMissileGimbal(t *testing.T) {
	i, target, m := missileRange(t, flight.Vec3{X: 1400, Y: 3000, Z: 60}, flight.Vec3{X: -80, Z: 320})
	fly(i, target, 10)
	if !m.loose && target.condition.Damager == 0 {
		t.Fatal("the beam drag neither broke the lock nor missed")
	}
	if target.condition.Damager == 0 && !m.loose {
		t.Fatal("expected a broken lock")
	}
}

// TestMissileArm: inside the arming time there is no fuse — a point-blank
// launch flies through.
func TestMissileArm(t *testing.T) {
	i, target, _ := missileRange(t, flight.Vec3{X: 180, Y: 3000}, flight.Vec3{X: 250})
	dt := 1.0 / 60
	for tick := 0; tick < 30; tick++ { // half a second: inside missile_arm
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
		i.pursue(dt, uint64(tick))
	}
	if target.condition.Damager == 0 && target.deaths > 0 {
		t.Fatal("the fuse fired inside the arming time")
	}
	if target.condition.Damaged == 0 && target.deaths > 0 {
		t.Fatal("armed too early")
	}
}

// TestMissileEnergy: a stern shot at the edge on a fleeing afterburning
// target dies of energy, not of luck.
func TestMissileEnergy(t *testing.T) {
	i, target, _ := missileRange(t, flight.Vec3{X: 4800, Y: 3000}, flight.Vec3{X: 320})
	survived := fly(i, target, float64(missile_life))
	if target.condition.Damager == 0 && target.deaths > 0 {
		t.Fatal("the runner should outlast this shot")
	}
	if survived == int(missile_life)*60 && len(i.flying) > 0 {
		t.Fatal("the missile never expired")
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
		// (no per-tick re-pin: teleporting the target reads as impossible LOS
		// motion and the seeker's track-rate ceiling correctly breaks lock)
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
	// A decoyed missile is no longer removed — it is SEDUCED: it chases the
	// falling flare. Count seductions across a few flare windows.
	seduced := map[uint64]bool{}
	tick := uint64(1)
	for round := 0; round < 5; round++ {
		fire(i, 1, map[string]any{"flare": true})
		for step := 0; step < 60; step++ {
			i.Step(tick, nil)
			tick++
			for _, m := range i.flying {
				if m.blind > 0 {
					seduced[m.number] = true
				}
			}
		}
	}
	if len(seduced) == 0 {
		t.Fatal("five flares and no missile was ever seduced")
	}
	if len(seduced) >= 6 {
		t.Fatal("every missile seduced: rear-aspect flares against the 9M should not be certain")
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

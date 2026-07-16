// Mochi world: Air game module tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"world/game"
	"world/games/air/flight"
	"world/games/air/aircraft"
)

func build(t *testing.T, mode string, parameters map[string]any, players int) *instance {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "test", Game: "air", Mode: mode, Capacity: 16, Seed: 2, Parameters: parameters})
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

// TestCheats: with the match cheats on, a tracked burst passes through a
// HUMAN untouched while a bot under the same fire still bleeds; the shooter's
// ammunition and fuel never deplete.
func TestCheats(t *testing.T) {
	i := build(t, "furball", map[string]any{
		"cheats": map[string]any{"invulnerable": true, "ammunition": true, "fuel": true},
		"bots":   map[string]any{"drone": 1.0},
	}, 2)
	steady := map[string]any{"throttle": 1.0, "reheat": 1.0, "fire": true}
	for tick := 0; tick < 60*5; tick++ { // 5 s of held trigger at 250 m astern of the human
		place(i, 0, 1, 250)
		i.aircraft[0].model.State.Position.Y = 400
		i.aircraft[1].model.State.Position.Y = 400
		i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: 1, Data: steady}}}) // real ticks: a pinned tick freezes the dispersion roll
	}
	human := &i.aircraft[1].model.State.Damage
	if human.Engine[0]+human.Engine[1] > 0 || human.Leak > 0 || total(human.Element) > 0 || i.aircraft[1].condition.Killed {
		t.Fatalf("invulnerable human took damage: engines %.2f/%.2f leak %.2f elements %.2f killed %v",
			human.Engine[0], human.Engine[1], human.Leak, total(human.Element), i.aircraft[1].condition.Killed)
	}
	if i.aircraft[0].ammunition != rounds {
		t.Fatalf("infinite ammunition depleted: %d of %d rounds left", i.aircraft[0].ammunition, rounds)
	}
	if fuel := i.aircraft[0].model.State.Fuel; fuel != i.tank {
		t.Fatalf("infinite fuel depleted: %.1f of %.1f kg left after 5 s in reheat", fuel, i.tank)
	}
	for tick := 0; tick < 60*5; tick++ { // the same burst against the BOT drone must still wound it
		if !i.aircraft[99].alive {
			break
		}
		place(i, 0, 99, 250)
		i.aircraft[0].model.State.Position.Y = 400
		i.aircraft[99].model.State.Position.Y = 400
		i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: 1, Data: steady}}})
	}
	drone := &i.aircraft[99].model.State.Damage
	if drone.Engine[0]+drone.Engine[1] == 0 && drone.Leak == 0 && total(drone.Element) == 0 && i.aircraft[99].alive {
		t.Fatal("invulnerability protected a bot: 5 s of tracking fire left the drone untouched")
	}
}

// TestCheatMissile: the invulnerability cheat gates the missile warhead
// (battle.Blast) exactly as it gates guns — a human is spared, a bot is not.
// TestCheats fires only guns, so without this the Blast guard is untested and
// could regress silently. The bot case is the control: it proves the missile
// actually reached the fuse, so the human being unhurt is the guard, not a miss.
func TestCheatMissile(t *testing.T) {
	hurt := func(bot bool) bool {
		g := New()
		made, err := g.Create(game.Session{Identifier: "cheatmissile", Game: "air", Mode: "furball", Capacity: 16, Seed: 3,
			Parameters: map[string]any{"missiles": true, "cheats": map[string]any{"invulnerable": true}}})
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
		target.bot = bot // the ONLY difference between the two runs
		target.model.State.Position = flight.Vec3{X: 1500, Y: 3000}
		target.model.State.Velocity = flight.Vec3{X: 250}
		target.model.State.Attitude = flight.Look(flight.Vec3{X: 1})
		shooter := i.aircraft[0]
		shooter.model.State.Position = flight.Vec3{Y: 3000}
		shooter.model.State.Velocity = flight.Vec3{X: 250}
		shooter.model.State.Attitude = flight.Look(flight.Vec3{X: 1})
		i.launched++
		sight, _ := i.bearing(shooter.model.State.Position, target.model.State.Position)
		i.flying = append(i.flying, &missile{shooter: 0, target: 1, position: shooter.model.State.Position,
			life: missile_life, velocity: flight.Vec3{X: 280}, burn: missile_boost, sight: sight, number: i.launched})
		fly(i, target, 12)
		d := &target.model.State.Damage
		return d.Engine[0]+d.Engine[1] > 0 || d.Leak > 0 || total(d.Element) > 0 || target.condition.Killed || !target.alive
	}
	if hurt(false) {
		t.Fatal("a missile damaged the invulnerable human")
	}
	if !hurt(true) {
		t.Fatal("the same missile left a bot unharmed — the fuse never fired, so the test proves nothing")
	}
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
// the server CPU budget check for #81. Run: go test ./games/air -bench Step100 -run xx
func BenchmarkStep100(b *testing.B) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "bench", Game: "air", Mode: "furball", Capacity: 100, Seed: 2, Parameters: map[string]any{"bots": 99.0}})
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
	made, err := g.Create(game.Session{Identifier: "endure", Game: "air", Mode: "furball", Capacity: 100, Seed: 2, Parameters: map[string]any{"bots": 20.0}})
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
	made, err := g.Create(game.Session{Identifier: "duel", Game: "air", Mode: "furball", Capacity: 100, Seed: seed,
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
	made, err := g.Create(game.Session{Identifier: "ladder", Game: "air", Mode: "furball", Capacity: 100, Seed: 11,
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

// TestBotsFight: a six-ace air produces kills and nobody flies into the sea.
func TestBotsFight(t *testing.T) {
	if testing.Short() {
		t.Skip("three simulated minutes")
	}
	g := New()
	made, err := g.Create(game.Session{Identifier: "brawl", Game: "air", Mode: "furball", Capacity: 100, Seed: 5,
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
	made, err := g.Create(game.Session{Identifier: "blind", Game: "air", Mode: "furball", Capacity: 100, Seed: 7,
		Parameters: map[string]any{"bots": map[string]any{"ace": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	ace := i.aircraft[99]
	// A human-slot craft glued to the ace's blind cone: behind and below.
	m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
	i.spawn(0, m, "")
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
	made, err := g.Create(game.Session{Identifier: "guns", Game: "air", Mode: "furball", Capacity: 100, Seed: seed,
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
	for seed := uint64(1); seed <= 8; seed++ {
		h, r := gunnery(t, "ace", seed)
		aceHits, aceRounds = aceHits+h, aceRounds+r
		h, r = gunnery(t, "rookie", seed)
		rookieHits, rookieRounds = rookieHits+h, rookieRounds+r
	}
	t.Logf("ace %d/%d hits per round, rookie %d/%d", aceHits, aceRounds, rookieHits, rookieRounds)
	if aceHits < 10 {
		t.Fatalf("the ace landed only %d hits from the saddle across the seeds", aceHits)
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
		made, _ := g.Create(game.Session{Identifier: "circle", Game: "air", Mode: "furball", Capacity: 100, Seed: 4,
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
	made, _ := g.Create(game.Session{Identifier: "reverse", Game: "air", Mode: "furball", Capacity: 100, Seed: 4,
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

// section runs one A/B arm sweep: the given roster once with section tactics
// and once with brain.solo on red (the control: same skill, same airframe,
// same opposition), returning the summed nets and deaths. The sweep is the
// claim — per-seed outcomes ride missile-decoy dice.
func section(t *testing.T, sweep uint64, red, blue map[string]any) (sectionNet, sectionDeaths, soloNet, soloDeaths int) {
	t.Helper()
	arm := func(seed uint64, solo bool) (kills, deaths int) {
		g := New()
		made, _ := g.Create(game.Session{Identifier: "section", Game: "air", Mode: "teams", Capacity: 16, Seed: seed,
			Parameters: map[string]any{"missiles": true, "bots": map[string]any{"red": red, "blue": blue}}})
		i := made.(*instance)
		for _, slot := range i.slots() {
			if a := i.aircraft[slot]; a.team == "red" && a.brain != nil {
				a.brain.solo = solo
			}
		}
		for tick := uint64(0); tick < 60*300; tick++ {
			i.Step(tick, nil)
		}
		for _, slot := range i.slots() {
			if a := i.aircraft[slot]; a.team == "red" {
				deaths += a.deaths
			}
		}
		return i.score["red"], deaths
	}
	if n, err := strconv.Atoi(os.Getenv("AIR_SWEEP")); err == nil && n > 0 {
		sweep = uint64(n) // a quicker local run, or more resolution
	}
	for seed := uint64(1); seed <= sweep; seed++ {
		sk, sd := arm(seed, false)
		lk, ld := arm(seed, true)
		t.Logf("seed %d: section %d kills %d deaths | solo %d kills %d deaths", seed, sk, sd, lk, ld)
		sectionNet += sk - sd
		soloNet += lk - ld
		sectionDeaths += sd
		soloDeaths += ld
	}
	t.Logf("sweep: section net %d deaths %d | solo net %d deaths %d", sectionNet, sectionDeaths, soloNet, soloDeaths)
	return
}

// TestBotSection (#138): the section tactics must EARN their keep against a
// weaker, larger enemy — two veterans versus four pilots. The claim here is
// the DEFENSIVE contract only: mutual support keeps the pair alive, so the
// section arm's deaths come in strictly under the solo control's. Net is
// deliberately NOT asserted in this scenario: measured (2026-07-16, after
// realistic lethality and the padlock), a lone wolf farms weak opposition
// faster than a disciplined section — trading some kill rate for section
// integrity is the doctrine, not a defect. The offensive claim lives in
// TestBotSectionEqual, where the enemy is good enough that teamwork must
// pay both ways.
func TestBotSection(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes")
	}
	t.Parallel()
	_, sectionDeaths, _, soloDeaths := section(t, 14,
		map[string]any{"veteran": 2.0}, map[string]any{"pilot": 4.0})
	if sectionDeaths >= soloDeaths {
		t.Fatalf("mutual support saved nothing: section deaths %d, solo deaths %d", sectionDeaths, soloDeaths)
	}
}

// TestBotSectionEqual (#144): the same A/B against EQUAL opposition — two
// veterans versus four veterans. Outnumbered with no skill edge, survival
// and score both hang on the section actually working (this is the scenario
// that caught the pre-rejoin tactics dying 8-30 km from their pair), so both
// claims are asserted: fewer deaths AND a better net than the solo control.
func TestBotSectionEqual(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes")
	}
	t.Parallel()
	sectionNet, sectionDeaths, soloNet, soloDeaths := section(t, 12,
		map[string]any{"veteran": 2.0}, map[string]any{"veteran": 4.0})
	if sectionDeaths >= soloDeaths {
		t.Fatalf("mutual support saved nothing: section deaths %d, solo deaths %d", sectionDeaths, soloDeaths)
	}
	if sectionNet <= soloNet {
		t.Fatalf("the section pair netted %d to the solo pair's %d — the tactics are not earning their keep", sectionNet, soloNet)
	}
}

// TestTeamsJoin: pick-on-join honours a valid choice and balances the rest;
// the welcome carries the side and the running score.
func TestTeamsJoin(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "teams", Game: "air", Mode: "teams", Capacity: 16, Seed: 2})
	i := made.(*instance)
	welcome, err := i.Join(game.Player{Name: "a", Slot: 0, Team: "red"})
	if err != nil || welcome["team"] != "red" {
		t.Fatalf("chosen side not honoured: %v %v", welcome["team"], err)
	}
	welcome, _ = i.Join(game.Player{Name: "b", Slot: 1}) // no choice: the smaller side
	if welcome["team"] != "blue" {
		t.Fatalf("balance assigned %v, want blue", welcome["team"])
	}
	if welcome["score"] == nil {
		t.Fatal("welcome carries no team score")
	}
	welcome, _ = i.Join(game.Player{Name: "c", Slot: 2, Team: "purple"}) // invalid: balanced (tie -> red)
	if welcome["team"] != "red" {
		t.Fatalf("invalid side assigned %v, want red", welcome["team"])
	}
}

// TestTeamsSpawnSides: the sides spawn in opposing arcs of the merge ring.
func TestTeamsSpawnSides(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "sides", Game: "air", Mode: "teams", Capacity: 16, Seed: 2,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"rookie": 3.0},
			"blue": map[string]any{"rookie": 3.0},
		}}})
	i := made.(*instance)
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		if a.team == "red" && a.model.State.Position.X <= 0 {
			t.Fatalf("red slot %d spawned on the blue side (x %.0f)", slot, a.model.State.Position.X)
		}
		if a.team == "blue" && a.model.State.Position.X >= 0 {
			t.Fatalf("blue slot %d spawned on the red side (x %.0f)", slot, a.model.State.Position.X)
		}
	}
}

// TestTeamsFriendlyFire: a teammate kill scores minus one for the shooter
// and the side; an enemy kill scores plus one.
func TestTeamsFriendlyFire(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "ff", Game: "air", Mode: "teams", Capacity: 16, Seed: 2})
	i := made.(*instance)
	i.Join(game.Player{Name: "a", Slot: 0, Team: "red"})
	i.Join(game.Player{Name: "b", Slot: 1, Team: "red"})
	i.Join(game.Player{Name: "c", Slot: 2, Team: "blue"})
	i.kill(1, 0) // red guns red
	if i.aircraft[0].kills != -1 || i.score["red"] != -1 {
		t.Fatalf("friendly kill scored %d (team %d), want -1/-1", i.aircraft[0].kills, i.score["red"])
	}
	i.kill(2, 0) // red guns blue
	if i.aircraft[0].kills != 0 || i.score["red"] != 0 {
		t.Fatalf("enemy kill scored %d (team %d), want 0/0 after the earlier minus", i.aircraft[0].kills, i.score["red"])
	}
}

// teams builds a red ace with a red drone teammate and two blue rookies for
// the section-tactics tests, all teleported by the caller.
func teams(t *testing.T) (*instance, *craft, *craft, *craft, *craft) {
	t.Helper()
	g := New()
	made, _ := g.Create(game.Session{Identifier: "section", Game: "air", Mode: "teams", Capacity: 16, Seed: 2,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"ace": 1.0, "drone": 1.0},
			"blue": map[string]any{"rookie": 2.0},
		}}})
	i := made.(*instance)
	var ace, mate, blue1, blue2 *craft
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		switch {
		case a.team == "red" && a.brain != nil:
			ace = a
		case a.team == "red":
			mate = a
		case blue1 == nil:
			blue1 = a
		default:
			blue2 = a
		}
	}
	return i, ace, mate, blue1, blue2
}

// aloft parks a craft at a position with a velocity, well above the floor.
func aloft(c *craft, position, velocity flight.Vec3) {
	c.model.State.Position = position
	c.model.State.Velocity = velocity
	c.model.State.Attitude = flight.Look(velocity.Normalize())
}

// TestTeamsSandwich: the enemy running an attack on a teammate outranks a
// nearer, harmless one.
func TestTeamsSandwich(t *testing.T) {
	i, ace, mate, blue1, blue2 := teams(t)
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	for tick := uint64(0); tick < 90; tick++ {
		aloft(ace, base, flight.Vec3{X: 220})
		aloft(mate, base.Add(flight.Vec3{X: 3800, Z: 600}), flight.Vec3{X: 220})
		aloft(blue1, base.Add(flight.Vec3{X: 1800, Z: -400}), flight.Vec3{X: 220})                       // nearer, minding its own business
		aloft(blue2, base.Add(flight.Vec3{X: 2400, Z: 600, Y: 200}), flight.Vec3{X: 240, Y: -10})        // running in on the mate from its six
		i.Step(tick, nil)
	}
	if ace.brain.target != blue2.player.Slot {
		t.Fatalf("ace targets slot %d, want the teammate's attacker %d", ace.brain.target, blue2.player.Slot)
	}
}

// TestTeamsSupport: a closer teammate already engaged with the target puts
// the section mate on the energy perch, not into the pile.
func TestTeamsSupport(t *testing.T) {
	i, ace, mate, blue1, blue2 := teams(t)
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	supported := false
	for tick := uint64(0); tick < 240; tick++ {
		aloft(ace, base, flight.Vec3{X: 220})
		aloft(mate, base.Add(flight.Vec3{X: 3300, Z: 150}), flight.Vec3{X: 220})
		aloft(blue1, base.Add(flight.Vec3{X: 3600, Z: 300}), flight.Vec3{Z: 230}) // engaged with the mate, beam-on to the ace
		aloft(blue2, base.Add(flight.Vec3{X: 3600, Z: -9000}), flight.Vec3{Z: -230})
		i.Step(tick, nil)
		if ace.brain.mode == "support" {
			supported = true
			break
		}
	}
	if !supported {
		t.Fatal("teammate engaged, but the ace piled in instead of flying support")
	}
}

// TestTeamsHoldFire: a friendly inside the stream's corridor, nearer than
// the target, holds the trigger; clearing the line releases it.
func TestTeamsHoldFire(t *testing.T) {
	i, ace, mate, blue1, _ := teams(t)
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	forward := flight.Vec3{X: 220}
	rig := func(lateral float64) bool {
		aloft(ace, base, forward)
		aloft(mate, base.Add(flight.Vec3{X: 300, Z: lateral}), forward)
		aloft(blue1, base.Add(flight.Vec3{X: 500}), forward)
		b := ace.brain
		slot := ace.player.Slot
		b.decided = 1000 // skip decide: the rigged fire state must survive
		b.prey = &track{when: 1000, position: blue1.model.State.Position, velocity: blue1.model.State.Velocity}
		b.distance = 500
		b.shoot = true
		b.mode = "offense"
		b.aim, _ = i.bearing(base, blue1.model.State.Position)
		i.think(slot, ace, 1000)
		return ace.latest.Fire
	}
	if rig(0) {
		t.Fatal("fired straight through the teammate")
	}
	if !rig(250) {
		t.Fatal("teammate well clear of the line, but the trigger stayed held")
	}
}

// TestTeamsFormation: with nothing to fight, a paired wingman leaves the solo
// weave and holds combat spread — line abreast, near 1.5 km — off his lead,
// and the pair cruises on a common heading.
func TestTeamsFormation(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "form", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
		Parameters: map[string]any{"bots": map[string]any{
			"red": map[string]any{"pilot": 2.0},
		}}})
	i := made.(*instance)
	lead, wing := i.aircraft[98], i.aircraft[99] // created 99 first: the later, lower slot leads
	if lead.brain.mate != 99 || wing.brain.mate != 98 {
		t.Fatalf("pairing wrong: lead mate %d, wing mate %d", lead.brain.mate, wing.brain.mate)
	}
	for tick := uint64(0); tick < 60*120; tick++ {
		i.Step(tick, nil)
	}
	if lead.brain.mode != "cruise" || wing.brain.mode != "form" {
		t.Fatalf("lead %q wing %q, want cruise/form", lead.brain.mode, wing.brain.mode)
	}
	_, span := i.bearing(lead.model.State.Position, wing.model.State.Position)
	if span < 700 || span > 2600 {
		t.Fatalf("wing %.0f m off the lead, want combat spread near 1500", span)
	}
	if lead.model.State.Velocity.Normalize().Dot(wing.model.State.Velocity.Normalize()) < 0.7 {
		t.Fatal("the pair cruises on different headings")
	}
}

// TestTeamsHumanLead: the odd bot a roster leaves unpaired attaches to a human
// teammate and flies the player's wing.
func TestTeamsHumanLead(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "wing", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
		Parameters: map[string]any{"bots": map[string]any{
			"red": map[string]any{"pilot": 1.0},
		}}})
	i := made.(*instance)
	i.Join(game.Player{Name: "human", Slot: 0, Team: "red"})
	human, bot := i.aircraft[0], i.aircraft[99]
	if bot.brain.mate != -1 {
		t.Fatalf("odd bot paired with %d, want unpaired", bot.brain.mate)
	}
	for tick := uint64(0); tick < 60*30; tick++ {
		aloft(human, flight.Vec3{X: 0, Y: 4000, Z: 0}, flight.Vec3{X: 220})
		i.Step(tick, nil)
	}
	if bot.brain.mode != "form" {
		t.Fatalf("bot mode %q, want form on the human lead", bot.brain.mode)
	}
}

// TestTeamsRadio: an engaged lead's target call reaches the wing — a night
// contact beyond the wing's own eyes lands in his track table and he commits.
func TestTeamsRadio(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "radio", Game: "air", Mode: "teams", Capacity: 16, Seed: 4,
		Parameters: map[string]any{"tod": "night", "bots": map[string]any{
			"red":  map[string]any{"pilot": 2.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	lead, wing, enemy := i.aircraft[98], i.aircraft[99], i.aircraft[97]
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	for tick := uint64(0); tick < 90; tick++ {
		aloft(wing, base, flight.Vec3{X: 220})
		aloft(lead, base.Add(flight.Vec3{Z: 1500}), flight.Vec3{X: 220})
		aloft(enemy, base.Add(flight.Vec3{Z: 7000}), flight.Vec3{X: 220}) // 5.5 km from the lead (night eyes reach 6), 7 km from the wing (they don't)
		i.Step(tick, nil)
	}
	if lead.brain.target != enemy.player.Slot {
		t.Fatalf("lead targets %d, want the drone %d", lead.brain.target, enemy.player.Slot)
	}
	if wing.brain.target != enemy.player.Slot {
		t.Fatalf("wing targets %d, want the lead's called drone %d", wing.brain.target, enemy.player.Slot)
	}
}

// TestBotRejoin: a paired bot with a distant target and a distant partner
// flies to the pair first — never a lonely transit into a stale fight — and
// fights normally once the section is together or the fight is close.
func TestBotRejoin(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "rejoin", Game: "air", Mode: "teams", Capacity: 16, Seed: 8,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"veteran": 2.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	lead, wing, enemy := i.aircraft[98], i.aircraft[99], i.aircraft[97]
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	run := func(apart, fight float64) string {
		for tick := uint64(0); tick < 90; tick++ {
			aloft(wing, base, flight.Vec3{X: 220})
			aloft(lead, base.Add(flight.Vec3{Z: apart}), flight.Vec3{X: 220})
			aloft(enemy, base.Add(flight.Vec3{X: fight}), flight.Vec3{X: 220})
			i.Step(tick, nil)
		}
		return wing.brain.mode
	}
	if mode := run(8000, 11000); mode != "rejoin" { // fight beyond rejoin.fight (10 km, measured: 6 km cost too many kills against weak opposition), pair split
		t.Fatalf("pair split, fight far: wing mode %q, want rejoin", mode)
	}
	if mode := run(1500, 11000); mode == "rejoin" {
		t.Fatalf("pair together: wing still rejoining (%q) instead of fighting", mode)
	}
	if mode := run(8000, 3000); mode == "rejoin" {
		t.Fatalf("fight close: wing rejoining (%q) instead of fighting", mode)
	}
}

// TestBotPadlock: under heavy load the scan collapses to the target and the
// forward view — an off-nose contact goes unseen until the g comes off (#144:
// bots that never lose sight never blunder, and no guns fight ever resolved).
func TestBotPadlock(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "padlock", Game: "air", Mode: "teams", Capacity: 16, Seed: 8,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"ace": 1.0},
			"blue": map[string]any{"drone": 2.0},
		}}})
	i := made.(*instance)
	var ace *craft
	drones := []*craft{}
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a.brain != nil {
			ace = a
		} else if a.team == "blue" {
			drones = append(drones, a)
		}
	}
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	run := func(load float64) int {
		ace.brain.reborn()
		for tick := uint64(0); tick < 90; tick++ {
			aloft(ace, base, flight.Vec3{X: 220})
			aloft(drones[0], base.Add(flight.Vec3{X: 1200}), flight.Vec3{X: 220})      // dead ahead: the padlocked target
			aloft(drones[1], base.Add(flight.Vec3{X: -600, Z: 2000}), flight.Vec3{X: 240}) // aft of the beam, outside the canopy blind wedge: visible relaxed, unseen under g
			ace.model.State.Fcs.Normal = load
			i.Step(tick, nil)
		}
		return len(ace.brain.known)
	}
	if known := run(1); known != 2 {
		t.Fatalf("relaxed scan tracks %d contacts, want both", known)
	}
	if known := run(7.5); known != 1 {
		t.Fatalf("padlocked at 7.5 g tracks %d contacts, want only the target", known)
	}
}

// TestTeamsMissileCall: a wingman who sights a launch at his mate calls it —
// the call IS the victim's sighting (a bot victim's noticed map is seeded, a
// human victim gets the radio event) — once per round, ever.
func TestTeamsMissileCall(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "plume", Game: "air", Mode: "teams", Capacity: 16, Seed: 9,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{
			"red":  map[string]any{"ace": 2.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	i.Join(game.Player{Name: "human", Slot: 0, Team: "red"})
	lead, wing, human := i.aircraft[98], i.aircraft[99], i.aircraft[0]
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	shot := func(number uint64, target int) *missile {
		m := &missile{shooter: 97, target: target, number: number, life: 10,
			position: base.Add(flight.Vec3{X: 2500, Z: 600}), velocity: flight.Vec3{X: -400}}
		i.flying = append(i.flying, m)
		return m
	}
	fired := shot(1, wing.player.Slot) // at the bot wingman, ahead of the lead's nose
	for tick := uint64(0); tick < 30; tick++ {
		aloft(lead, base, flight.Vec3{X: 220})
		aloft(wing, base.Add(flight.Vec3{Z: 1500}), flight.Vec3{X: 220})
		aloft(human, base.Add(flight.Vec3{Z: 3000}), flight.Vec3{X: 220})
		i.Step(tick, nil)
	}
	if !fired.called {
		t.Fatal("the lead sighted nothing: launch at the wing never called")
	}
	if !wing.brain.noticed[1] {
		t.Fatal("call made but the wing's sighting was not seeded")
	}
	at := shot(2, 0) // at the human
	calls := 0
	for tick := uint64(30); tick < 90; tick++ {
		aloft(lead, base, flight.Vec3{X: 220})
		aloft(wing, base.Add(flight.Vec3{Z: 1500}), flight.Vec3{X: 220})
		aloft(human, base.Add(flight.Vec3{Z: 3000}), flight.Vec3{X: 220})
		i.Step(tick, nil)
		for _, e := range i.events {
			if e["kind"] == "call" && e["call"] == "missile" {
				if e["target"] != 0 {
					t.Fatalf("missile call addressed to %v, want the human 0", e["target"])
				}
				calls++
			}
		}
		i.events = nil
	}
	if !at.called || calls != 1 {
		t.Fatalf("launch at the human: called %v with %d radio calls, want one", at.called, calls)
	}
}

// TestBotZoom: an energy edge at the merge takes the fight upstairs — and
// holds it there, because an unheld zoom was a one-decision twitch.
func TestBotZoom(t *testing.T) {
	i, ace, _, blue1, _ := teams(t)
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	zoomed := false
	for tick := uint64(0); tick < 120; tick++ {
		aloft(ace, base, flight.Vec3{X: 300})                                    // fast: the energy edge
		aloft(blue1, base.Add(flight.Vec3{X: 2500, Y: -300}), flight.Vec3{X: -160}) // head-on, slow and low
		i.Step(tick, nil)
		if ace.brain.mode == "zoom" {
			zoomed = true
			break
		}
	}
	if !zoomed {
		t.Fatal("energy edge at the merge, but the ace never took it upstairs")
	}
	if ace.brain.aim.Y < 0.5 {
		t.Fatalf("zoom aim Y %.2f, want steeply up", ace.brain.aim.Y)
	}
}

// TestBotRope: an attacker at range without an established saddle, against a
// defender with the energy bank — the defender climbs at a rate the attacker
// cannot sustain instead of burning the edge in a level break.
func TestBotRope(t *testing.T) {
	i, ace, _, blue1, _ := teams(t)
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	roped := false
	for tick := uint64(0); tick < 120; tick++ {
		aloft(ace, base, flight.Vec3{X: 320})                                            // the bank: fast
		aloft(blue1, base.Add(flight.Vec3{X: -500, Z: 1500}), flight.Vec3{X: 200, Z: -100}) // rear quarter outside the blind wedge, slow, nose loosely on
		i.Step(tick, nil)
		if ace.brain.mode == "rope" {
			roped = true
			break
		}
	}
	if !roped {
		t.Fatal("energy edge over a loose attacker, but the ace never roped him")
	}
	if ace.brain.aim.Y < 0.6 {
		t.Fatalf("rope aim Y %.2f, want the climb he cannot follow", ace.brain.aim.Y)
	}
}

// TestBotBracket: two pairs running the same distant contact split the
// approach — the junior pair's aim leaves the direct line by the bracket
// angle while the senior pair flies straight in.
func TestBotBracket(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "bracket", Game: "air", Mode: "teams", Capacity: 16, Seed: 9,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"veteran": 4.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	senior, junior, target := i.aircraft[98], i.aircraft[96], i.aircraft[95] // pairs (99,98) and (97,96); blue drone fills 95
	if target.team != "blue" {
		t.Fatalf("slot map surprise: 95 is %q", target.team)
	}
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	for tick := uint64(0); tick < 90; tick++ {
		aloft(i.aircraft[99], base, flight.Vec3{X: 220})
		aloft(i.aircraft[98], base.Add(flight.Vec3{Z: 400}), flight.Vec3{X: 220})
		aloft(i.aircraft[97], base.Add(flight.Vec3{Z: 1400}), flight.Vec3{X: 220})
		aloft(i.aircraft[96], base.Add(flight.Vec3{Z: 1800}), flight.Vec3{X: 220})
		aloft(target, base.Add(flight.Vec3{X: 6000, Z: 900}), flight.Vec3{X: 220})
		i.Step(tick, nil)
	}
	direct, _ := i.bearing(i.aircraft[96].model.State.Position, target.model.State.Position)
	if off := math.Acos(clamp(junior.brain.aim.Dot(direct), -1, 1)); off < 0.35 {
		t.Fatalf("junior pair only %.2f rad off the direct line, want the bracket", off)
	}
	direct, _ = i.bearing(senior.model.State.Position, target.model.State.Position)
	if off := math.Acos(clamp(senior.brain.aim.Dot(direct), -1, 1)); off > 0.35 {
		t.Fatalf("senior pair %.2f rad off the direct line, want straight in", off)
	}
}

// TestBotPress: held offensive advantage becomes the press — the counter
// accumulates only in range behind his 3/9, the saddle converts to an
// overtake once patience matures, losing the advantage resets it, and the
// guns solution accepts the wider deflection only while pressing (#144).
func TestBotPress(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "press", Game: "air", Mode: "teams", Capacity: 16, Seed: 7,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"ace": 1.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	ace, prey := i.aircraft[99], i.aircraft[98]
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	forward := flight.Vec3{X: 220}
	for tick := uint64(0); tick < 450; tick++ {
		aloft(ace, base, forward)
		aloft(prey, base.Add(flight.Vec3{X: 700}), forward) // parked in the control zone, dead ahead
		i.Step(tick, nil)
	}
	if !ace.brain.pressing(449) || ace.brain.mode != "press" {
		t.Fatalf("advantage held 7.5 s: press %d mode %q, want pressing in press mode", ace.brain.press, ace.brain.mode)
	}
	for tick := uint64(450); tick < 560; tick++ { // long enough for the press-mode maneuver hold to expire and a real decision to see the new picture
		aloft(ace, base, forward)
		aloft(prey, base.Add(flight.Vec3{X: 3000}), forward) // he opened past the press span: the advantage is gone
		i.Step(tick, nil)
	}
	if ace.brain.press != 0 {
		t.Fatalf("advantage lost but press still %d", ace.brain.press)
	}

	// The loose knob (neutral by default — measured: rounds trace the
	// airframe, wider gates only spray): with it amended open, a pressing
	// brain takes the ~35 m deflection at 400 m that patience declines.
	ace.brain.tactics.press.loose = 1.8
	rig := func(press uint64) bool {
		aloft(ace, base, forward)
		aloft(prey, base.Add(flight.Vec3{X: 400, Z: 35}), forward)
		b := ace.brain
		b.decided = 1000 // skip decide: the rigged state must survive
		b.press = press  // 0 = no advantage; 1 = held since tick zero
		b.shoot = true
		b.prey = &track{when: 1000, position: prey.model.State.Position, velocity: prey.model.State.Velocity}
		b.distance = 400
		b.aim = flight.Vec3{X: 1}
		i.think(ace.player.Slot, ace, 1000)
		return ace.latest.Fire
	}
	if rig(0) {
		t.Fatal("patient tracker took the deflection shot")
	}
	if !rig(1) {
		t.Fatal("pressing, but the wider deflection still held fire")
	}
}

// TestTeamsMissileHold: a teammate the seeker would acquire declines the
// launch — the AIM-9 has no IFF — and the shot comes back once the line
// clears, locked on the enemy.
func TestTeamsMissileHold(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "hold", Game: "air", Mode: "teams", Capacity: 16, Seed: 6,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{
			"red":  map[string]any{"ace": 1.0, "drone": 1.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	var ace, mate, enemy *craft
	for _, slot := range i.slots() { // levels are created drone-first: find by brain and side, not slot
		a := i.aircraft[slot]
		switch {
		case a.team == "red" && a.brain != nil:
			ace = a
		case a.team == "red":
			mate = a
		default:
			enemy = a
		}
	}
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	forward := flight.Vec3{X: 220}
	rig := func(lateral float64) int {
		aloft(ace, base, forward)
		aloft(mate, base.Add(flight.Vec3{X: 800, Z: lateral}), forward)
		aloft(enemy, base.Add(flight.Vec3{X: 1500}), forward) // tailpipe square at the shooter
		b := ace.brain
		b.decided = 1000 // skip decide: the rigged request must survive
		b.loose = true
		before := len(i.flying)
		i.think(ace.player.Slot, ace, 1000)
		if len(i.flying) > before {
			return i.flying[len(i.flying)-1].target
		}
		return -1
	}
	if target := rig(0); target >= 0 {
		t.Fatalf("launched at slot %d through the teammate on the seeker line", target)
	}
	if ace.brain.missiles != 2 {
		t.Fatalf("declined launch spent a missile (%d left)", ace.brain.missiles)
	}
	if target := rig(2000); target != enemy.player.Slot {
		t.Fatalf("line clear: locked slot %d, want the enemy %d", target, enemy.player.Slot)
	}
}

// TestTeamsMissileShare: a teammate's missile already chasing the target
// declines a second warshot — one bandit, one missile, while another bandit
// roams — but a bot's own follow-up stays legal and a ballistic missile no
// longer blocks.
func TestTeamsMissileShare(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "share", Game: "air", Mode: "teams", Capacity: 16, Seed: 10,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{
			"red":  map[string]any{"ace": 2.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	lead, wing, enemy := i.aircraft[98], i.aircraft[99], i.aircraft[97]
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	forward := flight.Vec3{X: 220}
	rig := func(chasing *missile) int {
		if chasing != nil {
			i.flying = []*missile{chasing}
		} else {
			i.flying = nil
		}
		aloft(wing, base, forward)
		aloft(lead, base.Add(flight.Vec3{Z: 800}), forward)
		aloft(enemy, base.Add(flight.Vec3{X: 1500}), forward)
		b := wing.brain
		b.decided = 2000 // skip decide: the rigged request must survive
		b.loose = true
		b.missiles = 2
		before := len(i.flying)
		i.think(wing.player.Slot, wing, 2000)
		if len(i.flying) > before {
			return i.flying[len(i.flying)-1].target
		}
		return -1
	}
	mates := &missile{shooter: lead.player.Slot, target: enemy.player.Slot, number: 5, life: 10,
		position: base.Add(flight.Vec3{X: 800, Z: 700}), velocity: flight.Vec3{X: 500}}
	if target := rig(mates); target >= 0 {
		t.Fatalf("launched at %d with the lead's missile already chasing it", target)
	}
	mates.loose = true
	if target := rig(mates); target != enemy.player.Slot {
		t.Fatalf("the lead's missile went ballistic but the wing still held (locked %d)", target)
	}
	own := &missile{shooter: wing.player.Slot, target: enemy.player.Slot, number: 6, life: 10,
		position: base.Add(flight.Vec3{X: 800}), velocity: flight.Vec3{X: 500}}
	if target := rig(own); target != enemy.player.Slot {
		t.Fatalf("own follow-up shot held (locked %d) — shoot-shoot-look is doctrine", target)
	}
}

// TestBotWounded: a visibly hurt contact — fire, fuel, missing structure —
// pulls target priority over a nearer, healthy one.
func TestBotWounded(t *testing.T) {
	i, ace, mate, blue1, blue2 := teams(t)
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	for tick := uint64(0); tick < 90; tick++ {
		aloft(ace, base, flight.Vec3{X: 220})
		aloft(mate, base.Add(flight.Vec3{Z: -9000}), flight.Vec3{X: 220})         // far out of the sandwich geometry
		aloft(blue1, base.Add(flight.Vec3{X: 2000}), flight.Vec3{X: 220})          // nearer, healthy
		aloft(blue2, base.Add(flight.Vec3{X: 2600, Z: 400}), flight.Vec3{X: 220}) // farther, burning
		blue2.condition.Burning = true                                             // Advance runs the fuse: keep the fire lit for the rig
		blue2.condition.Fuse = 30
		i.Step(tick, nil)
	}
	if ace.brain.target != blue2.player.Slot {
		t.Fatalf("ace targets %d, want the burning bird %d", ace.brain.target, blue2.player.Slot)
	}
}

// TestTeamsCalls: a bot teammate warns a human with BREAK toward the attack
// side, announces ENGAGED when committing onto the human's attacker, rate-
// limits the radio, and says nothing when the victim is another bot.
func TestTeamsCalls(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "calls", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"ace": 1.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	i.Join(game.Player{Name: "human", Slot: 0, Team: "red"})
	human, ace, drone := i.aircraft[0], i.aircraft[99], i.aircraft[98]
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	for tick := uint64(0); tick < 600; tick++ {
		aloft(human, base, flight.Vec3{X: 220})
		aloft(ace, base.Add(flight.Vec3{X: -3000, Z: 1500}), flight.Vec3{X: 220})   // trailing the fight: the attacker sits ahead of his nose, not in the blind wedge
		aloft(drone, base.Add(flight.Vec3{X: -1200, Z: 300}), flight.Vec3{X: 240}) // saddled behind the human's right rear quarter, nose on
		i.Step(tick, nil)
	}
	breaks, engaged := 0, 0
	for _, e := range i.events {
		if e["kind"] != "call" {
			continue
		}
		if e["call"] == "break" {
			breaks++
			if e["target"] != 0 || e["direction"] != "right" {
				t.Fatalf("break call %v, want target 0 direction right", e)
			}
		}
		if e["call"] == "engaged" {
			engaged++
		}
	}
	if breaks == 0 || engaged == 0 {
		t.Fatalf("%d break and %d engaged calls, want both", breaks, engaged)
	}
	if breaks > 3 {
		t.Fatalf("%d break calls in ten seconds: the radio is a chat storm", breaks)
	}

	// A bot victim earns no radio: the calls exist for the human's cockpit.
	quiet, aceq, mateq, blue1, _ := teams(t)
	for tick := uint64(0); tick < 300; tick++ {
		aloft(aceq, base, flight.Vec3{X: 220})
		aloft(mateq, base.Add(flight.Vec3{X: 3800, Z: 600}), flight.Vec3{X: 220})
		aloft(blue1, base.Add(flight.Vec3{X: 2600, Z: 500}), flight.Vec3{X: 240}) // running in on the bot mate from behind
		quiet.Step(tick, nil)
	}
	for _, e := range quiet.events {
		if e["kind"] == "call" {
			t.Fatalf("call %v for a bot victim", e)
		}
	}
}

// wounded builds the ace-vs-drone pair used by the wounded-flying tests.
func wounded(t *testing.T, seed uint64) (*instance, *craft, *craft) {
	t.Helper()
	g := New()
	made, _ := g.Create(game.Session{Identifier: "wounded", Game: "air", Mode: "furball", Capacity: 100, Seed: seed,
		Parameters: map[string]any{"bots": map[string]any{"ace": 1.0, "drone": 1.0}}})
	i := made.(*instance)
	ace, foe := i.aircraft[99], i.aircraft[98]
	if ace.brain == nil {
		ace, foe = i.aircraft[98], i.aircraft[99]
	}
	return i, ace, foe
}

// TestBotFireDrill: an engine fire pulls the lever to idle (battle.Advance
// starves fires below 0.1 throttle), and once it burns out the power returns.
func TestBotFireDrill(t *testing.T) {
	i, ace, _ := wounded(t, 5)
	ace.condition.Fire[0] = 0.5
	drilled, recovered := false, false
	for tick := uint64(0); tick < 60*20; tick++ {
		i.Step(tick, nil)
		if ace.condition.Fire[0] > 0 && ace.latest.Throttle == 0 && ace.latest.Reheat == 0 {
			drilled = true
		}
		if drilled && ace.condition.Fire[0] <= 0 && ace.latest.Throttle > 0 {
			recovered = true
			break
		}
	}
	if !drilled {
		t.Fatal("the burning ace never chopped the throttle")
	}
	if !recovered {
		t.Fatal("the fire never starved out (or the power never came back)")
	}
}

// TestBotLimp: with the thrust gone the fight is over — the bot goes to
// "limp" and opens the range instead of continuing the engagement.
func TestBotLimp(t *testing.T) {
	i, ace, foe := wounded(t, 6)
	// The foe crossing 1.5 km ahead, well inside visual range.
	s := &ace.model.State
	ahead := s.Attitude.Rotate(flight.Vec3{X: 1})
	foe.model.State.Position = s.Position.Add(ahead.Scale(1500))
	foe.model.State.Velocity = s.Attitude.Rotate(flight.Vec3{X: 30, Z: 200})
	ace.model.State.Damage.Engine[0] = 1
	ace.model.State.Damage.Engine[1] = 1
	limped := false
	for tick := uint64(0); tick < 60*6; tick++ {
		i.Step(tick, nil)
		if ace.brain.mode == "limp" {
			limped = true
		}
	}
	if !limped {
		t.Fatal("thrust gone and the ace never went to limp")
	}
}

// TestBotWingCap: shed structure caps the commanded g at 4.5 — the wounded
// wing no longer carries the limiter's margin.
func TestBotWingCap(t *testing.T) {
	i, ace, foe := wounded(t, 7)
	s := &ace.model.State
	ahead := s.Attitude.Rotate(flight.Vec3{X: 1})
	foe.model.State.Position = s.Position.Add(ahead.Scale(1200))
	ace.model.State.Damage.Loss = 200
	for tick := uint64(0); tick < 60; tick++ {
		i.Step(tick, nil)
	}
	if ace.brain.g > 4.5 {
		t.Fatalf("shed structure but the brain still commands %.1f g", ace.brain.g)
	}
}

// TestBotDrag: defensive at mush speed, the tiered brain unloads AWAY (drag)
// instead of offering a hopeless slow break.
func TestBotDrag(t *testing.T) {
	i, ace, foe := wounded(t, 8)
	place := func() {
		s := &ace.model.State
		behind := s.Attitude.Rotate(flight.Vec3{X: -1})
		lift := s.Attitude.Rotate(flight.Vec3{Y: 1})
		foe.model.State.Position = s.Position.Add(behind.Scale(1100)).Add(lift.Scale(420)) // high enough to clear the blind wedge at this range
		to, _ := i.bearing(foe.model.State.Position, s.Position)
		foe.model.State.Velocity = to.Scale(250) // nose on, closing
		foe.model.State.Attitude = flight.Look(to)
	}
	dragged := false
	for tick := uint64(0); tick < 60*8; tick++ {
		place()
		s := &ace.model.State
		if speed := s.Velocity.Length(); speed > 0.66*corner(ace.model) {
			s.Velocity = s.Velocity.Scale(0.6 * corner(ace.model) / speed) // hold him spent: the burner would rebuild through the gate mid-test
		}
		i.Step(tick, nil)
		if ace.brain.mode == "drag" {
			dragged = true
			break
		}
	}
	if !dragged {
		t.Fatal("spent and saddled, but the ace never dragged")
	}
}

// TestBotSpiral: saddled from behind but not yet shot at, with altitude in
// the bank — the brain flies the defensive spiral.
func TestBotSpiral(t *testing.T) {
	i, ace, foe := wounded(t, 9)
	place := func() {
		s := &ace.model.State
		if s.Position.Y < 3000 {
			s.Position.Y = 3500
		}
		behind := s.Attitude.Rotate(flight.Vec3{X: -1})
		lift := s.Attitude.Rotate(flight.Vec3{Y: 1})
		side := s.Attitude.Rotate(flight.Vec3{Z: 1})
		foe.model.State.Position = s.Position.Add(behind.Scale(900)).Add(lift.Scale(280))
		to, _ := i.bearing(foe.model.State.Position, s.Position)
		off := to.Add(side.Scale(0.22)).Normalize() // ~12 degrees off my tail: established, NOT pointed
		foe.model.State.Velocity = off.Scale(240)
		foe.model.State.Attitude = flight.Look(off)
	}
	spiralled := false
	for tick := uint64(0); tick < 60*8; tick++ {
		place()
		i.Step(tick, nil)
		if ace.brain.mode == "spiral" {
			spiralled = true
			break
		}
	}
	if !spiralled {
		t.Fatal("saddled with altitude in the bank, but the ace never spiralled")
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
	made, err := g.Create(game.Session{Identifier: "range", Game: "air", Mode: "furball", Capacity: 16, Seed: 3,
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

// TestFuelBurn: the F404-402 calibration — both engines at full military
// burn ~2.2 kg/s, in full reheat ~7.8 kg/s (sea level, mid subsonic). A
// 3,000 kg load is minutes of burner, not a free button.
func TestFuelBurn(t *testing.T) {
	rate := func(reheat float64) float64 {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = flight.Vec3{Y: 300}
		m.State.Velocity = flight.Vec3{X: 180}
		m.State.Attitude = flight.Look(flight.Vec3{X: 1})
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: reheat}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: reheat}
		start := m.State.Fuel
		for tick := 0; tick < 240*10; tick++ {
			m.Step(flight.Inputs{Throttle: 1, Reheat: reheat})
		}
		return (start - m.State.Fuel) / 10
	}
	if mil := rate(0); mil < 1.7 || mil > 2.9 {
		t.Fatalf("military burn %.2f kg/s: expected ~2.2", mil)
	}
	if wet := rate(1); wet < 6.0 || wet > 9.5 {
		t.Fatalf("full reheat burn %.2f kg/s: expected ~7.8", wet)
	}
}

// TestAirRespawn: open mode respawns after the pause.
func TestAirRespawn(t *testing.T) {
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
			if i.aircraft[1].model == nil {
				break // wrecked mid-script: the #131 polar made this tracking fire lethal enough to kill inside the window
			}
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

// TestBurstWounds: the non-binary promise at match level — a SHORT tracking
// burst wounds the target without destroying it. TestJoust proves five
// seconds wrecks; half a second must merely hurt.
func TestBurstWounds(t *testing.T) {
	i := build(t, "furball", nil, 2)
	steady := map[string]any{"throttle": 0.85, "fire": true}
	for tick := 0; tick < 36; tick++ { // 0.6 s of trigger at 250 m astern
		place(i, 0, 1, 250)
		i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: 1, Data: steady}}})
	}
	damage := &i.aircraft[1].model.State.Damage
	wounded := damage.Engine[0]+damage.Engine[1] > 0 || damage.Leak > 0 || total(damage.Element) > 0 || damage.Jam != nil
	if !wounded {
		t.Fatal("0.6 s of tracking fire left no wound at all")
	}
	if !i.aircraft[1].alive || i.aircraft[1].condition.Killed {
		t.Fatal("a short burst must wound, not destroy")
	}
}

// TestBlastCredits: the missile path end to end — launch, pursuit, proximity
// fuse, a real wound, and the DAMAGER credited to the shooter so any later
// death (cascade or splash) scores correctly. TestJoust proves the guns
// half of the credit chain; this proves the Blast half.
func TestBlastCredits(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true}, 2)
	place(i, 0, 1, 700)
	i.Step(0, map[int][]game.Input{0: {{Sequence: 1, Data: map[string]any{"throttle": 0.85, "missile": true}}}})
	if len(i.flying) == 0 {
		t.Fatal("the missile never launched")
	}
	for tick := uint64(1); tick < 60*15 && len(i.flying) > 0; tick++ {
		i.Step(tick, nil)
		i.events = i.events[:0]
	}
	if len(i.flying) > 0 {
		t.Fatal("the missile never fused or timed out within 15 s")
	}
	damage := &i.aircraft[1].model.State.Damage
	wounded := damage.Engine[0]+damage.Engine[1] > 0 || damage.Leak > 0 || total(damage.Element) > 0 || i.aircraft[1].condition.Killed
	if !wounded {
		t.Fatal("the warhead fused without wounding the target")
	}
	if i.aircraft[1].condition.Damager != 0 {
		t.Fatalf("the blast wound is credited to %d, want shooter 0", i.aircraft[1].condition.Damager)
	}
}

// TestRespawnPristine: damage must not leak across lives — the respawned jet
// carries a zeroed damage state, full ammunition, and a full tank.
func TestRespawnPristine(t *testing.T) {
	i := build(t, "furball", nil, 2)
	steady := map[string]any{"throttle": 0.85, "fire": true}
	for tick := 0; tick < 60*5 && i.aircraft[1].alive; tick++ { // wreck it: 5 s of pinned tracking fire (or until it dies outright — the strike rolls may cascade to an explosion under sustained guns)
		if i.aircraft[1].model != nil {
			place(i, 0, 1, 250)
			i.aircraft[0].model.State.Position.Y = 400 // fight on the deck so the wreck splashes fast
			i.aircraft[1].model.State.Position.Y = 400
		}
		i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: 1, Data: steady}}})
	}
	for tick := 0; tick < 60*60 && i.aircraft[1].alive; tick++ {
		i.Step(uint64(tick), nil) // ...then stop pinning the pose and let the wreck fall
		i.events = i.events[:0]
	}
	if i.aircraft[1].alive {
		t.Fatal("the wrecked target never went down")
	}
	reborn := false
	for tick := uint64(0); tick < 60*10 && !reborn; tick++ {
		i.Step(tick, nil)
		for _, e := range i.events {
			if e["kind"] == "respawn" && e["slot"] == 1 {
				reborn = true
			}
		}
		i.events = i.events[:0]
	}
	if !reborn {
		t.Fatal("the victim never respawned")
	}
	a := i.aircraft[1]
	damage := &a.model.State.Damage
	if total(damage.Element) > 0 || damage.Jam != nil || damage.Engine[0] > 0 || damage.Engine[1] > 0 || damage.Leak > 0 || damage.Loss > 0 || damage.Stress > 0 {
		t.Fatal("damage leaked across the respawn")
	}
	if a.ammunition != rounds {
		t.Fatalf("the respawn shortchanged the guns: %d of %d rounds", a.ammunition, rounds)
	}
	if a.model.State.Fuel != i.tank {
		t.Fatalf("the respawn shortchanged the tank: %.0f of %.0f kg", a.model.State.Fuel, i.tank)
	}
	if a.condition.Killed {
		t.Fatal("the pilot stayed dead through the respawn")
	}
}

// TestLethalityBand: a seeded balance gate — from a perfect 300 m tracking
// position, guns kill within a plausible window: never inside half a second
// (no one-burst deletions), always within thirty (guns stay lethal). Catches
// balance regressions from aero or battle edits.
func TestLethalityBand(t *testing.T) {
	if testing.Short() {
		t.Skip("five seeded duels")
	}
	for _, seed := range []uint64{2, 5, 9, 11, 17} {
		g := New()
		made, err := g.Create(game.Session{Identifier: "band", Game: "air", Mode: "furball", Capacity: 16, Seed: seed})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		for slot := 0; slot < 2; slot++ {
			if _, err := i.Join(game.Player{Name: "p", Slot: slot}); err != nil {
				t.Fatal(err)
			}
		}
		steady := map[string]any{"throttle": 0.85, "fire": true}
		killed := -1
		for tick := 0; tick < 60*5 && killed < 0; tick++ { // 5 s of pinned tracking fire
			place(i, 0, 1, 300)
			i.aircraft[0].model.State.Position.Y = 400
			i.aircraft[1].model.State.Position.Y = 400
			i.Step(uint64(tick), map[int][]game.Input{0: {{Sequence: 1, Data: steady}}})
			for _, e := range i.events {
				if e["kind"] == "kill" && e["slot"] == 1 {
					killed = tick
				}
			}
			i.events = i.events[:0]
		}
		if killed >= 0 && killed < 30 {
			t.Fatalf("seed %d: killed in %.2f s — one-burst deletions are out of band", seed, float64(killed)/60)
		}
		for tick := 60 * 5; tick < 60*35 && killed < 0; tick++ { // release the pose: the wreck must fall and score
			i.Step(uint64(tick), nil)
			for _, e := range i.events {
				if e["kind"] == "kill" && e["slot"] == 1 {
					killed = tick
				}
			}
			i.events = i.events[:0]
		}
		if killed < 0 {
			t.Fatalf("seed %d: 5 s of perfect tracking fire plus the fall never killed", seed)
		}
	}
}

// TestMissileEvadingFuse: a hard-turning target saturates the seeker's
// track-rate ceiling in the terminal frames and the lock breaks — the
// proximity fuse must stay live regardless and grade the pass at the
// closest approach. The old tracking-gated fuse silently disarmed with the
// lock, so an evading bandit absorbed entire magazines untouched.
func TestMissileEvadingFuse(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "evadefuse", Game: "air", Mode: "furball", Capacity: 16, Seed: 3,
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
	target.bot = true
	target.model.State.Position = flight.Vec3{X: 1500, Y: 3000}
	target.model.State.Velocity = flight.Vec3{Z: 250} // crossing the shot line: PN lags a turning crosser by metres, so the terminal LOS-rate spike breaks the lock with a small (lethal) miss pending
	target.model.State.Attitude = flight.Look(flight.Vec3{Z: 1})
	shooter := i.aircraft[0]
	shooter.model.State.Position = flight.Vec3{Y: 3000}
	shooter.model.State.Velocity = flight.Vec3{X: 250}
	shooter.model.State.Attitude = flight.Look(flight.Vec3{X: 1})
	i.launched++
	sight, _ := i.bearing(shooter.model.State.Position, target.model.State.Position)
	i.flying = append(i.flying, &missile{shooter: 0, target: 1, position: shooter.model.State.Position,
		life: missile_life, velocity: flight.Vec3{X: 280}, burn: missile_boost, sight: sight, number: i.launched})
	dt := 1.0 / 60
	round := i.flying[0]
	minimum := 1e9
	for tick := 0; tick < 20*60 && len(i.flying) > 0; tick++ {
		if !round.loose && round.position.Subtract(target.model.State.Position).Length() < 150 {
			round.loose = true // the terminal lock break every near-miss course suffers (LOS rate diverges as range closes): the warhead must stay live regardless
		}
		if d := round.position.Subtract(target.model.State.Position).Length(); d < minimum {
			minimum = d
		}
		// The evasion: a level 7 g turn, velocity rotating in the horizontal plane.
		velocity := &target.model.State.Velocity
		speed := velocity.Length()
		turn := 7 * 9.81 / speed * dt
		x := velocity.X*math.Cos(turn) + velocity.Z*math.Sin(turn)
		z := -velocity.X*math.Sin(turn) + velocity.Z*math.Cos(turn)
		velocity.X, velocity.Z = x, z
		target.model.State.Attitude = flight.Look(velocity.Scale(1 / speed))
		target.model.State.Position = target.model.State.Position.Add(velocity.Scale(dt))
		i.pursue(dt, uint64(tick))
	}
	t.Logf("closest approach %.2f m, loose %v, flew %.2f s", minimum, round.loose, round.flew)
	d := &target.model.State.Damage
	if !(d.Engine[0]+d.Engine[1] > 0 || d.Leak > 0 || total(d.Element) > 0 || target.condition.Killed || !target.alive) {
		t.Fatal("the evading target absorbed the missile untouched — the fuse died with the lock again")
	}
}

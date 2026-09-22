// Mochi world: the decision journal's tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"encoding/json"
	"math"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// duel flies a guns bandit against a player in a steady level turn - close
// enough, and turning, so the arbiter re-plans and its forecasts have
// something to be wrong about - and hands every tick to watch.
func skirmish(t *testing.T, journalled bool, seconds int, watch func(tick uint64, b *Bandit)) *Bandit {
	t.Helper()
	b := NewBandit("ace", 7, 250000, "", false, false, "guns", 0, false)
	if !journalled {
		b.craft.brain.journal = nil
	}
	b.Spawn(flight.Vec3{X: 2500, Y: 4000}, flight.Vec3{X: -230})
	environment := flight.Environment{Seed: 7, Wrap: 250000}
	player := flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea})
	player.State = flight.Level(player, flight.Vec3{Y: 4000}, flight.Vec3{X: 1}, 220, fuel)
	words := make([]float64, flight.Size)
	for tick := uint64(1); tick <= uint64(60*seconds); tick++ {
		// A level 3 g turn by hand: the phantom is a constant-rate arc, so this
		// is the opponent it predicts BEST. Whatever error shows is its floor.
		rate := 9.81 * math.Sqrt(3*3-1) / 220
		heading := rate * float64(tick) / 60
		player.State.Velocity = flight.Vec3{X: 220 * math.Cos(heading), Z: 220 * math.Sin(heading)}
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(nil)
		b.Step()
		if watch != nil {
			watch(tick, b)
		}
	}
	return b
}

// The journal is an instrument. If allocating one moved the bot by a single
// bit, every developer recording would describe a different pilot from the one
// players fight, and every conclusion drawn from it would be about that other
// pilot.
func TestJournalIsInert(t *testing.T) {
	var with, without []float64
	skirmish(t, true, 40, func(tick uint64, b *Bandit) {
		if tick%60 == 0 {
			s := b.craft.model.State
			with = append(with, s.Position.X, s.Position.Y, s.Position.Z, s.Velocity.X, s.Velocity.Y, s.Velocity.Z)
		}
	})
	skirmish(t, false, 40, func(tick uint64, b *Bandit) {
		if tick%60 == 0 {
			s := b.craft.model.State
			without = append(without, s.Position.X, s.Position.Y, s.Position.Z, s.Velocity.X, s.Velocity.Y, s.Velocity.Z)
		}
	})
	if len(with) == 0 || len(with) != len(without) {
		t.Fatalf("the two runs sampled %d and %d words", len(with), len(without))
	}
	for n := range with {
		if with[n] != without[n] {
			t.Fatalf("the journalled bandit diverged at word %d (second %d): %v against %v", n, n/6+1, with[n], without[n])
		}
	}
}

func TestJournalRecordsTheArbiter(t *testing.T) {
	var decisions []decision
	var forecasts []forecast
	var stack Demand
	b := skirmish(t, true, 40, func(tick uint64, b *Bandit) {
		if tick%30 != 0 {
			return
		}
		drained := b.Journal()
		if drained == nil {
			t.Fatal("a journalled bandit drained nil")
		}
		decisions = append(decisions, drained.Decisions...)
		forecasts = append(forecasts, drained.Forecasts...)
		stack = drained.Demand
	})
	if len(decisions) < 5 {
		t.Fatalf("forty seconds of fight journalled %d re-plans", len(decisions))
	}
	for _, d := range decisions {
		if len(d.Scores) < 2 {
			t.Fatalf("a re-plan at tick %d weighed %d candidates: the arbiter compares, so there must be several", d.Tick, len(d.Scores))
		}
		chosen, found := d.Scores[d.Play]
		if !found {
			t.Fatalf("the winner %q is not among its own candidates %v", d.Play, d.Scores)
		}
		for name, score := range d.Scores {
			if score > chosen+1e-9 {
				t.Fatalf("%q won at %.4f while %q scored %.4f: the journal is not recording the number the arbiter compared", d.Play, chosen, name, score)
			}
		}
		if d.Horizon < 2 {
			t.Fatalf("a %q judged over %.1f s: no tier's horizon is under two seconds", d.Play, d.Horizon)
		}
	}
	// Both subjects, and the error must GROW with the lookahead: a forecast that
	// is as good at twelve seconds as at two is not being measured.
	mean := map[string]map[float64][]float64{"his": {}, "own": {}}
	for _, f := range forecasts {
		mean[f.Subject][f.Span] = append(mean[f.Subject][f.Span], f.Error)
	}
	average := func(list []float64) float64 {
		total := 0.0
		for _, v := range list {
			total += v
		}
		return total / float64(len(list))
	}
	if len(mean["his"][2]) == 0 || len(mean["his"][8]) == 0 {
		t.Fatalf("no opponent forecasts came due: %v", mean["his"])
	}
	if len(mean["own"][2]) == 0 {
		t.Fatal("no own-track forecasts came due: the winner's rehearsed path was never captured")
	}
	if near, far := average(mean["his"][2]), average(mean["his"][8]); far <= near {
		t.Fatalf("the opponent forecast read %.1f m at 2 s and %.1f m at 8 s: it must get worse with distance", near, far)
	}
	if stack.Law <= 0 || stack.Capped <= 0 || stack.Capped > stack.Law+1e-9 {
		t.Fatalf("the demand stack read law %.2f corner %.2f capped %.2f: each stage can only take g away", stack.Law, stack.Corner, stack.Capped)
	}
	// And it crosses the wasm boundary as JSON, with single-word keys.
	payload, err := json.Marshal(Journal{Decisions: decisions[:1], Forecasts: forecasts[:1], Demand: stack})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(payload, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"decisions", "forecasts", "bypasses", "demand"} {
		if _, found := back[key]; !found {
			t.Fatalf("the payload lacks %q: %s", key, payload)
		}
	}
	if b.Journal() == nil {
		t.Fatal("a drained journal must still drain")
	}
}

func TestJournalNoticesABypassOnItsEdge(t *testing.T) {
	j := &journal{}
	for tick := uint64(1); tick <= 5; tick++ {
		j.notice(tick, "rebuild", 95, 110)
	}
	j.notice(6, "high", 130, 110)
	j.notice(7, "rebuild", 100, 110)
	j.notice(8, "press", 100, 110) // an arbiter play is not a bypass
	if len(j.bypasses) != 2 {
		t.Fatalf("two entries into rebuild journalled %d bypasses", len(j.bypasses))
	}
	if j.bypasses[0].Tick != 1 || j.bypasses[1].Tick != 7 {
		t.Fatalf("bypasses at ticks %d and %d, want the edges 1 and 7", j.bypasses[0].Tick, j.bypasses[1].Tick)
	}
	if j.bypasses[0].Speed != 95 || j.bypasses[0].Gate != 110 {
		t.Fatalf("the trigger values were not kept: %+v", j.bypasses[0])
	}
}

func TestJournalIsBoundedWhenNobodyDrains(t *testing.T) {
	j := &journal{}
	for tick := uint64(0); tick < 1000; tick++ {
		j.notice(tick, "rebuild", 1, 2)
		j.notice(tick, "high", 1, 2)
	}
	if len(j.bypasses) > kept {
		t.Fatalf("an undrained journal grew to %d entries", len(j.bypasses))
	}
	if j.bypasses[len(j.bypasses)-1].Tick != 999 {
		t.Fatal("the bound must keep the NEWEST entries")
	}
}

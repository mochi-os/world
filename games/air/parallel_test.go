// Mochi world: a tick's jets are stepped in parallel, and fly exactly as one after another
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"world/game"
	"world/games/air/flight"
)

// parallel_match is a seeded furball of roster expert bots.
func parallel_match(t testing.TB, roster float64, seed uint64) *instance {
	made, err := New().Create(game.Session{Identifier: fmt.Sprintf("parallel%v", seed), Game: "air", Mode: "furball", Capacity: 16, Seed: seed,
		Parameters: map[string]any{"bots": map[string]any{"expert": roster}}})
	if err != nil {
		t.Fatal(err)
	}
	return made.(*instance)
}

// parallel_digest is every aircraft's encoded state, kills, deaths and
// whether it flies, and the counts of weapons and wrecks aloft.
func parallel_digest(i *instance) []float64 {
	out := []float64{}
	buffer := make([]float64, flight.Size+64)
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		alive := 0.0
		if a.alive {
			alive = 1
		}
		out = append(out, float64(slot), float64(a.kills), float64(a.deaths), alive)
		if a.model != nil {
			a.model.State.Encode(buffer)
			out = append(out, buffer[:flight.Size]...)
		}
	}
	return append(out, float64(len(i.flying)), float64(len(i.rounds)), float64(len(i.wrecks)))
}

// TestParallelIdentical flies two identical seeded 99-expert furballs side by
// side, one stepping and judging each jet as it comes, as the loop always
// did, and one stepping them in parallel and judging them after, and
// compares every aircraft's state, kills, deaths and the weapons aloft after
// every tick, bit for bit, and the events each raised. AIR_PARALLEL_TICKS
// flies longer (3,600 is a minute).
func TestParallelIdentical(t *testing.T) {
	ticks, _ := strconv.Atoi(os.Getenv("AIR_PARALLEL_TICKS"))
	if ticks == 0 {
		ticks = 900 // past the first respawns, which read every jet's position
	}
	threshold := step_parallel
	defer func() { step_parallel = threshold }()
	for _, seed := range []uint64{5, 11} {
		bots_live.Store(0)
		serial := parallel_match(t, 99, seed)
		parallel := parallel_match(t, 99, seed)
		events := [2]int{}
		spent := [2]time.Duration{}
		for tick := uint64(0); tick < uint64(ticks); tick++ {
			step_parallel = math.MaxInt
			start := time.Now()
			serial.Step(tick, nil)
			spent[0] += time.Since(start)
			events[0] += len(serial.Events())
			step_parallel = 1
			start = time.Now()
			parallel.Step(tick, nil)
			spent[1] += time.Since(start)
			events[1] += len(parallel.Events())
			a, b := parallel_digest(serial), parallel_digest(parallel)
			if len(a) != len(b) {
				t.Fatalf("seed %d tick %d: digests of %d and %d words", seed, tick, len(a), len(b))
			}
			for k := range a {
				if math.Float64bits(a[k]) != math.Float64bits(b[k]) && !(math.IsNaN(a[k]) && math.IsNaN(b[k])) {
					t.Fatalf("seed %d tick %d: word %d is %v one after another, %v in parallel", seed, tick, k, a[k], b[k])
				}
			}
		}
		if events[0] != events[1] {
			t.Fatalf("seed %d: %d events one after another, %d in parallel", seed, events[0], events[1])
		}
		t.Logf("seed %d: %d ticks identical, %d events; mean tick %.2f ms one after another, %.2f ms in parallel",
			seed, ticks, events[0], float64(spent[0].Microseconds())/1000/float64(ticks), float64(spent[1].Microseconds())/1000/float64(ticks))
		serial.Close()
		parallel.Close()
	}
}

// TestStepRaisesAHelpersPanic: a panic stepping a jet, on a helper or the
// tick goroutine, reaches the tick goroutine, where the session's guard meets
// it - and only once every helper has stopped, so the session's next tick
// cannot fly a jet a helper is still flying.
func TestStepRaisesAHelpersPanic(t *testing.T) {
	threshold := step_parallel
	defer func() { step_parallel = threshold }()
	step_parallel = 1
	batch := make([]stepping, 16)
	for k := range batch {
		batch[k].craft = &craft{} // no model: Step dereferences nil
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("a panic stepping a jet was lost")
			}
		}()
		step(batch)
	}()
	if held := len(steppers); held != 0 {
		t.Fatalf("%d helper places still held after the tick", held)
	}
}

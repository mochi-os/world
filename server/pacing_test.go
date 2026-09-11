// Mochi world: input pacing tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"testing"

	"world/game"
)

// TestDrainSpendsOneTick pins #176. The client integrates each control sample
// for a whole number of fixed 1/60 steps and records that count against its
// prediction ring; the server must fly the sample for the same number, or the
// state it acknowledges is not the state the prediction reached and the client
// reconciles the difference away as if it were a prediction error. Measured on
// the live server before this: 23.8-38.8 m of correction per 110 ms frame at
// 583 kt, none of it a real divergence.
func TestDrainSpendsOneTick(t *testing.T) {
	// Steps are the client's own units - fixed model sub-steps, four to a
	// server tick - because that is what its counter counts and what its
	// prediction ring replays. A sample worth one TICK is worth four of them.
	sample := func(sequence uint32, ticks int) game.Input {
		return game.Input{Sequence: sequence, Steps: ticks * substeps, Data: map[string]any{}}
	}
	sequences := func(list []game.Input) []uint32 {
		out := []uint32{}
		for _, in := range list {
			out = append(out, in.Sequence)
		}
		return out
	}
	same := func(t *testing.T, got []uint32, want ...uint32) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("drained %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("drained %v, want %v", got, want)
			}
		}
	}

	// The steady case: one sample per tick, each worth one step. Unchanged
	// from before the fix, and it must stay that way.
	t.Run("one step is one tick", func(t *testing.T) {
		p := &player{queue: []game.Input{sample(1, 1), sample(2, 1)}}
		same(t, sequences(player_drain(p)), 1)
		if p.sequence != 1 {
			t.Errorf("acknowledged %d after the first tick, want 1: a snapshot may only acknowledge what it has flown", p.sequence)
		}
		same(t, sequences(player_drain(p)), 2)
		if p.sequence != 2 {
			t.Errorf("acknowledged %d, want 2", p.sequence)
		}
	})

	// Two steps is two ticks. The game keeps its last input when nothing new
	// arrives, so the hold is the second tick of that sample.
	t.Run("two steps is two ticks", func(t *testing.T) {
		p := &player{queue: []game.Input{sample(1, 2), sample(2, 1)}}
		same(t, sequences(player_drain(p)), 1)
		if got := player_drain(p); len(got) != 0 {
			t.Fatalf("delivered %v on the held tick: sample 1 still owes a tick, so nothing new may fly", sequences(got))
		}
		_ = p.credit
		if p.sequence != 1 {
			t.Errorf("acknowledged %d while still flying sample 1, want 1", p.sequence)
		}
		same(t, sequences(player_drain(p)), 2)
	})

	// No steps is no time: the sample is acknowledged, its edges still reach
	// the game, and the tick goes to the sample behind it.
	t.Run("no steps costs no tick", func(t *testing.T) {
		p := &player{queue: []game.Input{sample(1, 0), sample(2, 1), sample(3, 1)}}
		same(t, sequences(player_drain(p)), 1, 2)
		if p.sequence != 2 {
			t.Errorf("acknowledged %d, want 2: the zero-step sample was flown for no time, so the tick belonged to sample 2", p.sequence)
		}
		same(t, sequences(player_drain(p)), 3)
	})

	// A client that predates the field sends nothing, which the decoder reads
	// as one step - so an old client is metered exactly as it always was.
	t.Run("a sample with no count is worth a tick", func(t *testing.T) {
		p := &player{queue: []game.Input{{Sequence: 1, Steps: substeps}, {Sequence: 2, Steps: substeps}}}
		same(t, sequences(player_drain(p)), 1)
		same(t, sequences(player_drain(p)), 2)
	})

	// A sample worth less than a whole tick cannot be rounded away: two of
	// them make one tick, and the remainder has to survive between ticks or
	// the player is quietly given time he never asked for.
	t.Run("a part tick is carried, not rounded", func(t *testing.T) {
		p := &player{queue: []game.Input{
			{Sequence: 1, Steps: 2}, {Sequence: 2, Steps: 2}, {Sequence: 3, Steps: 2}, {Sequence: 4, Steps: 2},
		}}
		same(t, sequences(player_drain(p)), 1, 2)
		same(t, sequences(player_drain(p)), 3, 4)
	})

	// A backlog after a stall is caught up rather than trailing the player by
	// the depth of the queue: metering 30 queued samples one per tick would
	// put him half a second behind his own stick.
	t.Run("a backlog is caught up, not metered", func(t *testing.T) {
		queue := []game.Input{}
		for n := uint32(1); n <= 20; n++ {
			queue = append(queue, sample(n, 1))
		}
		p := &player{queue: queue}
		got := player_drain(p)
		if len(got) != 20 {
			t.Fatalf("drained %d of a 20-deep backlog, want all 20", len(got))
		}
		if p.sequence != 20 || len(p.queue) != 0 || p.credit != 0 {
			t.Errorf("after the catch-up: acknowledged %d, %d queued, %d sub-steps carried; want 20, 0, 0", p.sequence, len(p.queue), p.credit)
		}
	})

	// Nothing queued is nothing delivered, and the acknowledgement holds.
	t.Run("an empty queue acknowledges nothing new", func(t *testing.T) {
		p := &player{sequence: 7}
		if got := player_drain(p); len(got) != 0 {
			t.Fatalf("drained %v from an empty queue", sequences(got))
		}
		if p.sequence != 7 {
			t.Errorf("acknowledgement moved to %d with nothing to fly, want 7", p.sequence)
		}
	})
}

// TestInputStepsDecode pins the wire reading of the step count (#176). ABSENT
// and ZERO are different claims and the decoder must not conflate them: a
// client that predates the field sends nothing and every one of its samples is
// worth a tick, exactly as before, while a client that sends zero is saying
// its accumulator produced no step for that frame.
func TestInputStepsDecode(t *testing.T) {
	decode := func(samples ...map[string]any) []game.Input {
		list := []any{}
		for _, s := range samples {
			list = append(list, s)
		}
		return connection_inputs(map[string]any{"inputs": list})
	}

	got := decode(
		map[string]any{"sequence": uint64(1)},                      // an old client: no field at all
		map[string]any{"sequence": uint64(2), "steps": uint64(0)},  // a frame that produced no step
		map[string]any{"sequence": uint64(3), "steps": uint64(2)},  // a frame that produced two sub-steps, half a tick
		map[string]any{"sequence": uint64(4), "steps": uint64(99)}, // and a client asking for the moon
	)
	if len(got) != 4 {
		t.Fatalf("decoded %d samples, want 4", len(got))
	}
	if got[0].Steps != substeps {
		t.Errorf("a sample with no steps field decoded as %d, want %d - a whole TICK. The count is in the client core's fixed sub-steps, four to a tick, so defaulting to one would starve an old client four to one", got[0].Steps, substeps)
	}
	if got[1].Steps != 0 {
		t.Errorf("an explicit zero decoded as %d, want 0", got[1].Steps)
	}
	if got[2].Steps != 2 {
		t.Errorf("two steps decoded as %d, want 2", got[2].Steps)
	}
	if got[3].Steps != 30 {
		t.Errorf("99 steps decoded as %d, want it clamped to 30 - the same cap the client core applies - because the wire promises nothing and one sample may not buy a second of physics", got[3].Steps)
	}
}

// Mochi world: Air game module tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
)

// TestMachineDefendsLikeTheAce bounds how much of a heater fight the machine
// spends inside the defensive programme (#216).
//
// The machine carries no reaction delay, and react was doing a second job that
// nothing had named: b.alert resets whenever the threat test flickers off, so
// react was also the only debounce on that test. At zero the machine broke,
// cut its burner and cancelled its press for rounds that were in the window for
// a single tick - measured against the ace over these seeds, 7.82% of the fight
// against 0.13%, arriving 41 kt slower and surviving 2 of 16 against 10.
// threat_confirm is the shared floor that fixed it; post-fix this reads 3.01%
// against the ace's 0.18%, and the machine is now the FASTER of the two.
//
// The outcome gates (TestLadderDuel, TestMissileLadderWide) already catch the
// consequence. This catches the CAUSE, which is what took a day to find: when
// those rungs go red again, run this first.
func TestMachineDefendsLikeTheAce(t *testing.T) {
	heavy(t)
	evading, flying := map[bool]int{}, map[bool]int{}
	speed := map[bool]float64{}
	for seed := uint64(1); seed <= 16; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("defence%d", seed),
			Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": true, "weapons": "fox2",
				"bots": map[string]any{"superhuman": 1.0, "ace": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		var fighters []*craft
		for _, slot := range i.slots() {
			if c := i.aircraft[slot]; c != nil && c.brain != nil {
				fighters = append(fighters, c)
			}
		}
		if len(fighters) != 2 {
			t.Fatalf("seed %d: wanted two bots, got %d", seed, len(fighters))
		}
		done := false
		for tick := uint64(0); tick < 60*240 && !done; tick++ {
			i.Step(tick, nil)
			for _, c := range fighters {
				if c.model == nil || !c.alive {
					done = true
					continue
				}
				machine := c.brain.skill.machine
				flying[machine]++
				speed[machine] += c.model.State.Velocity.Length()
				if c.brain.mode == "evade" {
					evading[machine]++
				}
			}
		}
		i.Close()
	}
	share := func(machine bool) float64 {
		return 100 * float64(evading[machine]) / math.Max(1, float64(flying[machine]))
	}
	for _, m := range []bool{false, true} {
		who := "ace"
		if m {
			who = "superhuman"
		}
		fmt.Printf("  %-11s evading %5.2f%% of its fight | mean speed %3.0f kt\n",
			who, share(m), speed[m]/math.Max(1, float64(flying[m]))*1.944)
	}
	if flying[true] == 0 || flying[false] == 0 {
		t.Fatal("one tier never flew: the scenario is not driving the path under test")
	}
	// The line sits between the defect (7.82%) and the repair (3.01%), nearer the
	// repair: a regression that re-opens this reaches for the whole aircraft, it
	// does not drift a fraction of a percent.
	if s := share(true); s > 5 {
		t.Errorf("the machine spends %.2f%% of a heater fight evading against the ace's %.2f%%: "+
			"the threat test has lost its confirmation window again (#216)", s, share(false))
	}
}

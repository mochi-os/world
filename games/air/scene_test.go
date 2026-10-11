// Mochi world: scenes — moments a human pilot created, put back in front of the brain
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Outcome gates on recorded human flying (footage_test.go explains the reader,
// the open-loop limit, and where a scene's recording lives). Each is a moment
// the brain got wrong when it was flown. A scene that judges where the fight
// ends up flies the scripted doctrine single player gives the bandit; one that
// pins the arbiter's own choices flies the arbiter, which a teamless bot still
// flies in a furball. The four scenes from recording 01a0b090 (joust, ace,
// 2026-09-17) went when the store pruned it: they judged the arbiter's rebuild
// and high in front of a gun, which single player's bandit no longer flies.

package air

import (
	"fmt"
	"testing"
)

// seeds is how many bandits fly each scene. Selection noise and personality
// vary by seed, so a scene passes on a majority, never on one lucky pilot.
const seeds = 8

// majority fails the test unless at least six of the eight seeds pass.
func majority(t *testing.T, what string, passed int, detail []string) {
	t.Helper()
	for _, line := range detail {
		t.Log(line)
	}
	if passed < 6 {
		t.Fatalf("%s: %d of %d seeds", what, passed, seeds)
	}
}

// The first turn the user won against the scripted ace (recording 01a10e335a,
// t=12.5-20.7): both jets came out of the merge at 460 kt and the pilot, at 33
// degrees of alpha, fired a heater on tone at 20.7 s that killed it. The ace
// had its nose 2.5-27 degrees off him at 1.0-1.3 km for the last 2.7 s and
// fired nothing, because its zone flew a round off its flight path, 30 degrees
// below the nose, and came out empty (launcher). With the zone drawn along the
// nose it must have its own heater away before his.
func TestSceneFirstTurnTone(t *testing.T) {
	reel := view(t, "01a10e335a2173cd8285943513b220b0")
	passed := 0
	var detail []string
	for seed := uint64(1); seed <= seeds; seed++ {
		first, rack := -1.0, -1
		replay(t, reel, "ace", seed, 12.5, 8.2, true, func(g glimpse) {
			if rack < 0 {
				rack = g.missiles
			}
			if first < 0 && g.missiles < rack {
				first = 12.5 + g.second
			}
		})
		ok := first > 0
		if ok {
			passed++
		}
		detail = append(detail, fmt.Sprintf("seed %d: first heater at %5.1f s (the pilot's at 20.7)  %v", seed, first, ok))
	}
	majority(t, "the ace must fire its heater before the pilot's tone shot in the first turn", passed, detail)
}

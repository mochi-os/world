// Mochi world: the BVR and missile ladders' upper rungs at wide seed counts
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"testing"

	"world/game"
)

// magazine summarises a per-fight expenditure: the range, the middle,
// and how many fights emptied the rack, which is the behaviour the
// magazine-discipline gate is really about.
func magazine(fights []int) string {
	if len(fights) == 0 {
		return "none"
	}
	sorted := append([]int{}, fights...)
	sort.Ints(sorted)
	empty, total := 0, 0
	for _, v := range sorted {
		total += v
		if v == 8 {
			empty++
		}
	}
	mean := float64(total) / float64(len(sorted))
	spread := 0.0
	for _, v := range sorted {
		spread += (float64(v) - mean) * (float64(v) - mean)
	}
	spread = math.Sqrt(spread / float64(len(sorted)))
	return fmt.Sprintf("low %d, median %d, high %d, mean %.2f, deviation %.2f | %d of %d fights spent all eight",
		sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1], mean, spread, empty, len(sorted))
}

func TestBvrWide(t *testing.T) {
	wide(t)
	// 48, not 24 (#106): at 24 seeds this rung passed while the same code failed
	// at 48 by eight fights. The gate's own tolerance is +-2 at 24 seeds and the
	// true margin was four times that, so a green run at 24 was never evidence
	// about this pair. Costs about seven extra minutes in the doctrine battery.
	seeds := uint64(48)
	if v := os.Getenv("AIR_BVR_SEEDS"); v != "" {
		fmt.Sscanf(v, "%d", &seeds)
	}
	// ace v ace is here for its MAGAZINE, not its order (#225): the six-seed
	// gate in battery_bvr_test.go fires above 44 of 48 and this pairing sits at
	// exactly 44, so a threshold nobody derived is one round from red on a
	// rung whose spend had never been sampled deep. Two aces are the same
	// jet, so no ordering is asserted of it below.
	pairs := [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}, {"ace", "ace"}}
	if os.Getenv("AIR_BVR_END") != "" {
		pairs = [][2]string{{"superhuman", "novice"}} // the end rung alone, on request
	}
	if os.Getenv("AIR_BVR_MIRROR") != "" {
		pairs = [][2]string{{"ace", "ace"}} // the magazine rung alone, on request
	}
	for _, pair := range pairs {
		strong, weak := pair[0], pair[1]
		wins, losses, draws, spent := 0, 0, 0, 0
		mirror := strong == weak
		// The mirror rung is here for a SUM over 2n jets, not a win-loss margin,
		// and a sum converges faster than a margin: 24 seeds already puts the
		// 48-round ceiling 4.5 deviations above the mean, which is all the
		// magazine gate below needs. Half the depth, half the sixteen minutes it
		// adds to a doctrine battery already running near its own timeout.
		count := seeds
		if mirror {
			count = seeds / 2
		}
		expenditure := []int{} // per fight, so the total can be read as a distribution rather than a number
		for seed := uint64(1); seed <= count; seed++ {
			bots := map[string]any{strong: 1.0, weak: 1.0}
			if mirror {
				bots = map[string]any{strong: 2.0} // one tier twice, not a 1.0 that overwrites itself
			}
			// The identifier carries BOTH tiers: on the strong name alone, ace v ace
			// and ace v pilot would name the same sessions.
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("bvrwide%s%s%d", strong, weak, seed),
				Game: "air", Mode: "joust", Seed: seed,
				Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": bots}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var top, low *craft
			for _, s := range i.slots() {
				a := i.aircraft[s]
				if a == nil || !a.bot {
					continue
				}
				if top == nil {
					top = a
				} else {
					low = a
				}
			}
			if !mirror && top.brain.skill.library <= low.brain.skill.library && !(top.brain.skill.machine && !low.brain.skill.machine) {
				top, low = low, top
			}
			decided := false
			for tick := uint64(0); tick < 60*600 && !decided; tick++ {
				i.Step(tick, nil)
				switch {
				case low.model == nil || !low.alive:
					wins++
					decided = true
				case top.model == nil || !top.alive:
					losses++
					decided = true
				}
			}
			if !decided {
				draws++
			}
			fight := 0
			for _, a := range []*craft{top, low} {
				fight += 4 - a.amraams
			}
			spent += fight
			expenditure = append(expenditure, fight)
			i.Close()
		}
		fmt.Printf("bvr wide %-11s v %-7s %d-%d, %d no result | AMRAAMs spent %d of %d\n", strong, weak, wins, losses, draws, spent, 8*count)
		// The distribution, not just the total: a six-seed gate is a sample of
		// this, and whether 44 of 48 is typical or a tail is exactly what the
		// total cannot say (#225).
		fmt.Printf("         spend per fight: %s\n", magazine(expenditure))
		// THE GATE (#46): symmetric competent BVR neutralises, so order is not
		// demanded of these rungs - losing them beyond the seed band is.
		//
		// The band SCALES with the sample (#106). A win-loss margin wanders as
		// the square root of the fights, so the +3 that is right at 24 seeds is
		// about +4 at 48; a fixed number would mean raising the seed count
		// silently TIGHTENED the gate rather than only resolving it better.
		// THE MAGAZINE GATE (#225), which lived at six seeds and could not.
		// Measured at HEAD over 48 seeds: two aces spend 6.69 AMRAAMs a fight
		// with a deviation of 1.43, so n fights centre on 6.7n and wander by
		// 1.43*sqrt(n). The allowance is three of those deviations.
		//
		// WHY IT MOVED. The quantity is capped at 8n, and at six seeds the cap
		// sits only 2.2 deviations above the mean - so the old gate at 44 of 48
		// stood at 1.25, tripping about one run in nine on nothing but the draw,
		// and even a gate at the ceiling itself would have tripped on one in
		// eighty. No threshold was both meaningful and quiet at that sample; the
		// ladder now prints the number and this judges it. At 24 seeds the cap is
		// 4.5 deviations up and the three-deviation line clears it comfortably:
		// the rung reads 164 of 192 against an allowance of 182, a margin of 2.6
		// deviations - about one false red in 190 runs, against the old gate's
		// one in nine.
		//
		// The novice ripples by design and the pilot's long fights legitimately
		// empty the rack (measured 7.42 a fight against the ace's 6.69), so only
		// pairings where BOTH sides hold rounds are judged.
		if weak != "novice" && weak != "pilot" {
			if allowed := 6.7*float64(count) + 4.3*math.Sqrt(float64(count)); float64(spent) > allowed {
				t.Errorf("bvr wide: %s v %s dumped magazines: %d of %d AMRAAMs spent over %d seeds, past an allowance of %.0f",
					strong, weak, spent, 8*count, count, allowed)
			}
		}
		band := 3 * math.Sqrt(float64(count)/24)
		// A mirror rung has no order to invert - the loser is whichever identical
		// jet drew the short seed - so only the pairings with a real ladder are
		// gated. Asserting a coin flip here would fail on its own variance.
		if !mirror && count >= 24 && float64(losses) > float64(wins)+band {
			t.Errorf("bvr wide: %s lost to %s %d-%d over %d seeds: the rung is inverted", strong, weak, losses, wins, count)
		}
	}
}

// TestMissileLadderWide is TestLadderDuel's missiles arm over 48 seeds.
func TestMissileLadderWide(t *testing.T) {
	wide(t)
	for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
		strong, weak := pair[0], pair[1]
		wins, losses, draws := 0, 0, 0
		for seed := uint64(1); seed <= 48; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("missilewide%s%d", strong, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{strong: 1.0, weak: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var top, low *craft
			for _, slot := range i.slots() {
				c := i.aircraft[slot]
				if c == nil || c.brain == nil {
					continue
				}
				if c.brain.skill.library == skills[strong].library && c.brain.skill.wander == skills[strong].wander {
					top = c
				} else {
					low = c
				}
			}
			done := false
			for tick := uint64(0); tick < 60*240 && !done; tick++ {
				i.Step(tick, nil)
				switch {
				case low.model == nil || !low.alive:
					wins++
					done = true
				case top.model == nil || !top.alive:
					losses++
					done = true
				}
			}
			if !done {
				draws++
			}
			i.Close()
		}
		fmt.Printf("wide missiles %-11s vs %-7s  won %d  lost %d  no result %d  (of 48)\n", strong, weak, wins, losses, draws)
		// THE GATE (#46): the chaotic arm. Re-centred for #69: the threshold was
		// set when this rung read 20-18 positive, but the rung has drifted
		// intrinsically slightly negative (measured 2026-08-22: 19-21 at HEAD on
		// these seeds, 37-41 at 96 seeds where sigma is ~9), so four fights of
		// daylight left NO headroom — any change that re-rolls a handful of
		// fights tripped it on luck (#69's gate read 17-22 while a 96-seed
		// paired run showed a 2-fight delta). Six still catches the 13-27-class
		// inversions this gate exists for.
		if losses > wins+6 {
			t.Errorf("wide missiles: %s lost to %s %d-%d over 48 seeds: the ladder is inverted", strong, weak, losses, wins)
		}
	}
}

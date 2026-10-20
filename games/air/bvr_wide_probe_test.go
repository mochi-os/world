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
// magazine-discipline gate is really about. full is what both jets carried
// between them.
func magazine(fights []int, full int) string {
	if len(fights) == 0 {
		return "none"
	}
	sorted := append([]int{}, fights...)
	sort.Ints(sorted)
	empty, total := 0, 0
	for _, v := range sorted {
		total += v
		if v == full {
			empty++
		}
	}
	mean := float64(total) / float64(len(sorted))
	spread := 0.0
	for _, v := range sorted {
		spread += (float64(v) - mean) * (float64(v) - mean)
	}
	spread = math.Sqrt(spread / float64(len(sorted)))
	return fmt.Sprintf("low %d, median %d, high %d, mean %.2f, deviation %.2f | %d of %d fights spent all %d",
		sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1], mean, spread, empty, len(sorted), full)
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
	// expert v expert is here for its MAGAZINE, not its order (#225): the six-seed
	// gate in battery_bvr_test.go fires above 44 of 48 and this pairing sits at
	// exactly 44, so a threshold nobody derived is one round from red on a
	// rung whose spend had never been sampled deep. Two experts are the same
	// jet, so no ordering is asserted of it below.
	pairs := [][2]string{{"expert", "intermediate"}, {"superhuman", "expert"}, {"expert", "expert"}}
	if os.Getenv("AIR_BVR_END") != "" {
		pairs = [][2]string{{"superhuman", "novice"}} // the end rung alone, on request
	}
	if os.Getenv("AIR_BVR_MIRROR") != "" {
		pairs = [][2]string{{"expert", "expert"}} // the magazine rung alone, on request
	}
	for _, pair := range pairs {
		strong, weak := pair[0], pair[1]
		wins, losses, trades, draws, spent, carried, full := 0, 0, 0, 0, 0, 0, 0
		mirror := strong == weak
		// The mirror rung is here for a SUM over 2n jets, not a win-loss margin,
		// and a sum converges faster than a margin: 24 seeds already puts the
		// 288-round ceiling 9 deviations above the mean, which is all the
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
			// The identifier carries BOTH tiers: on the strong name alone, expert v expert
			// and expert v intermediate would name the same sessions.
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
				switch ending(top, low) {
				case "won":
					wins++
					decided = true
				case "lost":
					losses++
					decided = true
				case "traded":
					trades++
					decided = true
				}
			}
			if !decided {
				draws++
			}
			fight := 0
			full = 0
			for _, a := range []*craft{top, low} {
				rack := len(stores_amraams(a.loadout))
				fight += rack - a.amraams
				full += rack
			}
			spent += fight
			carried += full
			expenditure = append(expenditure, fight)
			i.Close()
		}
		fmt.Printf("bvr wide %-11s v %-7s %d-%d, %d traded, %d no result | AMRAAMs spent %d of %d\n", strong, weak, wins, losses, trades, draws, spent, carried)
		// The distribution, not just the total: a six-seed gate is a sample of
		// this, and whether 44 of 48 is typical or a tail is exactly what the
		// total cannot say (#225).
		fmt.Printf("         spend per fight: %s\n", magazine(expenditure, full))
		// THE GATE (#46): symmetric competent BVR neutralises, so order is not
		// demanded of these rungs - losing them beyond the seed band is.
		//
		// The band SCALES with the sample (#106), and it is two deviations of
		// the draw (2026-10-16). The margin between evenly matched jets wanders
		// by about the square root of the fights: two experts read 28-18 over 48
		// seeds, and the superhuman flying the expert's doctrine 19-27 on seeds
		// 1-48 and 22-21 on 49-96. The old band, 3*sqrt(n/24) or 4.2 at 48
		// seeds, was under one deviation, so an even pair failed it about one
		// run in four. Two deviations still catch the inversion this gate
		// exists for: 11-35 when the superhuman jammed under every seeker.
		//
		// THE MAGAZINE GATE (#225), which lived at six seeds and could not.
		// Measured over 48 seeds on the Fox 3 fit, six rounds a jet: two experts
		// spend 8.08 AMRAAMs a fight with a deviation of 2.04, so n fights
		// centre on 8.1n and wander by 2.04*sqrt(n). The allowance is three of
		// those deviations.
		//
		// WHY IT MOVED. On the four-round fit the six-seed cap sat only 2.2
		// deviations above the mean, so the old gate at 44 of 48 tripped about
		// one run in nine on nothing but the draw. No threshold was both
		// meaningful and quiet at that sample; the ladder now prints the number
		// and this judges it, at 24 seeds, with the cap far above the line.
		//
		// The novice ripples by design and the intermediate's long fights legitimately
		// empty the rack (measured 8.90 a fight against the expert's 8.08), so only
		// pairings where BOTH sides hold rounds are judged.
		if weak != "novice" && weak != "intermediate" {
			if allowed := 8.1*float64(count) + 6.1*math.Sqrt(float64(count)); float64(spent) > allowed {
				t.Errorf("bvr wide: %s v %s dumped magazines: %d of %d AMRAAMs spent over %d seeds, past an allowance of %.0f",
					strong, weak, spent, carried, count, allowed)
			}
		}
		band := 2 * math.Sqrt(float64(count))
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
	for _, pair := range [][2]string{{"expert", "intermediate"}, {"superhuman", "expert"}} {
		strong, weak := pair[0], pair[1]
		wins, losses, trades, draws := 0, 0, 0, 0
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
				switch ending(top, low) {
				case "won":
					wins++
					done = true
				case "lost":
					losses++
					done = true
				case "traded":
					trades++
					done = true
				}
			}
			if !done {
				draws++
			}
			i.Close()
		}
		fmt.Printf("wide missiles %-11s vs %-7s  won %d  lost %d  traded %d  no result %d  (of 48)\n", strong, weak, wins, losses, trades, draws)
		// THE GATE (#46): the chaotic arm, banded at two deviations of the draw
		// as TestBvrWide is (2026-10-16). A trade, both jets down in one step,
		// is neither side's: scored as the superhuman's win, this rung read
		// 87-53 over 144 seeds before the 2026-10-12 fuel change, 30 of them
		// trades, where the decided fights stood 57-53. On the lighter jets it
		// reads 61-79 with 2 trades, 19-28 on these seeds. The fixed six it
		// replaces was under one deviation of a 48-seed margin; two deviations
		// still catch the 13-27-class inversion this gate exists for.
		if float64(losses) > float64(wins)+2*math.Sqrt(48) {
			t.Errorf("wide missiles: %s lost to %s %d-%d over 48 seeds: the ladder is inverted", strong, weak, losses, wins)
		}
	}
}

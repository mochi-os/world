// Mochi world: The BVR battery — bot-versus-bot jousts over seeds (#22).
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

// TestBvrLadder is the BVR tuning battery: full jousts at every gated pairing,
// kept separate from the fox2-pinned dogfight suites. The gates assert order
// and health - the ladder holds, fights close, nobody dumps a rack - never
// tallies.
func TestBvrLadder(t *testing.T) {
	heavy(t)
	type tally struct {
		strong, weak, draws int
		closest             float64
		spent               int
	}
	pairs := [][2]string{
		{"superhuman", "novice"},
		{"ace", "pilot"},
		{"superhuman", "ace"},
		{"ace", "ace"},
	}
	results := map[string]*tally{}
	for _, pair := range pairs {
		strong, weak := pair[0], pair[1]
		key := strong + " v " + weak
		r := &tally{closest: math.MaxFloat64}
		results[key] = r
		for seed := uint64(1); seed <= 6; seed++ {
			bots := map[string]any{strong: 1.0, weak: 1.0}
			if strong == weak {
				bots = map[string]any{strong: 2.0}
			}
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("bvrladder%s%s%d", strong, weak, seed),
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
			if strong != weak && top.brain.skill.library <= low.brain.skill.library &&
				!(top.brain.skill.machine && !low.brain.skill.machine) {
				top, low = low, top
			}
			if top == nil || low == nil {
				t.Fatal("ladder roster wrong")
			}
			decided := false
			for tick := uint64(0); tick < 60*600 && !decided; tick++ {
				i.Step(tick, nil)
				if top.model != nil && low.model != nil {
					if span := i.span(top.model, low.model); span < r.closest {
						r.closest = span
					}
				}
				switch {
				case low.model == nil || !low.alive:
					r.strong++
					decided = true
				case top.model == nil || !top.alive:
					r.weak++
					decided = true
				}
			}
			if !decided {
				r.draws++
			}
			for _, a := range []*craft{top, low} {
				if a != nil {
					r.spent += 4 - a.amraams
				}
			}
			i.Close() // return this fight's share of the server-wide bot budget: leaked reservations starve every later bot-spawning test in the binary
		}
	}
	for _, pair := range pairs {
		key := pair[0] + " v " + pair[1]
		r := results[key]
		fmt.Printf("bvr %-22s %d-%d, %d no result | closest %6.0f m | AMRAAMs spent %2d of 48\n",
			key, r.strong, r.weak, r.draws, r.closest, r.spent)
	}
	// THE GATES. Order and health, not tallies: see the header.
	end := results["superhuman v novice"]
	if end.weak > end.strong {
		t.Fatalf("the BVR ladder inverted end to end: novice beat superhuman %d-%d", end.weak, end.strong)
	}
	// The top rung: symmetric competent BVR neutralizes, so ordering is not
	// demanded, only a sweep. Slack of two - six seeds cannot carry a strict gate
	// (#46); TestBvrWide is the armed detector for a real inversion.
	top := results["superhuman v ace"]
	if top.weak > top.strong+2 {
		t.Fatalf("the top rung inverted: ace beat superhuman %d-%d", top.weak, top.strong)
	}
	for _, pair := range pairs {
		r := results[pair[0]+" v "+pair[1]]
		if r.closest > 20000 {
			t.Fatalf("%s v %s never closed inside 20 km: degenerate never-closing play (closest %.0f m)", pair[0], pair[1], r.closest)
		}
		// Raw spend only gates pairings where both sides have the discipline to hold
		// rounds (0.7 and up). The novice's full ripple is its authentic toolkit, and
		// the pilot's long fights legitimately empty the rack.
		if pair[1] != "novice" && pair[1] != "pilot" && r.spent > 44 {
			t.Fatalf("%s v %s dumped magazines: %d of 48 AMRAAMs spent", pair[0], pair[1], r.spent)
		}
	}
}

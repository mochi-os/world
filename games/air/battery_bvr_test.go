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

// TestBvrLadder is the BVR tuning battery: full jousts at every pairing the
// gates care about, judged on decisions — deliberately separate from the
// dogfight suites, whose fox2-pinned numbers this battery must never
// disturb. Gates, per the settled design: the ladder holds end to end
// (superhuman over novice), equal pairings produce genuine fights (they
// merge or resolve — never-closing is the degenerate play), and nobody
// dumps a magazine. Honest physics bounds what may be demanded: stern
// chases are marginal, head-on first exchanges are the high-Pk moment, and
// a flown defence deterministically defeats shots outside burn-through —
// so kill counts at these sample sizes are REPORTED, and the gates assert
// order and health rather than precise tallies.
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
	// The top rung, armed 2026-08-14 with counter-fire (ruling 9): the
	// machine must not LOSE to the ace beyond one-seed noise. Before
	// counter-fire this rung sat at 7-11 over twenty seeds — the machine
	// died with a full rack whenever the ace's first credible shot forced
	// it defensive early — and a 0-6 inversion passed this battery unseen
	// because only the end pair was gated. The rung's measured state is
	// level (7-7 at twenty seeds, 6 draws): symmetric competent BVR
	// neutralizes, so ordering is not demanded, but losing it again is a
	// regression this gate now catches.
	top := results["superhuman v ace"]
	if top.weak > top.strong+1 {
		t.Fatalf("the top rung inverted: ace beat superhuman %d-%d", top.weak, top.strong)
	}
	for _, pair := range pairs {
		r := results[pair[0]+" v "+pair[1]]
		if r.closest > 20000 {
			t.Fatalf("%s v %s never closed inside 20 km: degenerate never-closing play (closest %.0f m)", pair[0], pair[1], r.closest)
		}
		// The novice's full ripple is the authentic incomplete toolkit, so
		// raw spend only gates the pairings where both sides carry the
		// discipline to hold rounds.
		if pair[1] != "novice" && r.spent > 44 {
			t.Fatalf("%s v %s dumped magazines: %d of 48 AMRAAMs spent", pair[0], pair[1], r.spent)
		}
	}
}

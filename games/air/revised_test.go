// Mochi world: the revised brain against the current one
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The doctrine battery's gates were fitted, over months, to the brain as it
// stands: several measure doctrine PROXIES (perch donated, time lingered
// beneath, share tracked) against hand-scripted opponents, and a constant has
// more than once been held "where the gates are green" by a comment that also
// records it costing kills. A structural change cannot be judged only by
// instruments tuned to what it replaces. This is the comparison that does not
// lean on them: the same tier, the same airframe, the same seeds, one bot
// flying tactics.stage and the other not, and the only score is who dies.

package air

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// evaluating is the structural stage in flight (claude/plans/air-bot-arbiter.md,
// Part II), from AIR_STAGE, or 0 when none is. TestMain makes it the DEFAULT for
// every brain in the package, so the whole doctrine battery can be flown by the
// revised brain without touching a line of it:
//
//	AIR_STAGE=7 make test-doctrine-quick
//
// and versus() below pins the "current" bot back to 0.
var evaluating = 0

func TestMain(m *testing.M) {
	if stage, err := strconv.Atoi(os.Getenv("AIR_STAGE")); err == nil && stage > 0 {
		evaluating = stage
		doctrine.stage = stage
	}
	for _, knob := range []string{"omit", "futures", "hedge", "peril", "steady", "startled", "gravity", "truth.cap", "truth.parts", "truth.stack", "truth.keen", "truth.point", "truth.offence", "truth.overtake", "truth.threat", "truth.closing", "span.high", "span.pitch", "span.climb"} {
		if value, err := strconv.ParseFloat(os.Getenv("AIR_"+strings.ToUpper(strings.ReplaceAll(knob, ".", "_"))), 64); err == nil {
			amend(&doctrine, knob, value) // the stage's own sweeps: AIR_HEDGE=0.5, AIR_FUTURES=2, AIR_SPAN_HIGH=8
		}
	}
	os.Exit(m.Run())
}

// versus flies `seeds` furballs of one revised ace against one current ace and
// returns the revised bot's wins, losses and undecided fights. Seats alternate
// by seed, so neither brain owns whatever the spawn ring favours.
func versus(t *testing.T, stage int, missiles bool, seeds int) (wins, losses, draws int) {
	t.Helper()
	weapons := map[bool]string{false: "guns", true: "fox2"}[missiles]
	for seed := uint64(1); seed <= uint64(seeds); seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("revised%s%d", weapons, seed), Game: "air", Mode: "furball",
			Capacity: 8, Seed: seed, Parameters: map[string]any{"missiles": missiles, "weapons": weapons, "bots": map[string]any{"ace": 2.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		var pair []*craft
		for _, slot := range i.slots() {
			if c := i.aircraft[slot]; c != nil && c.brain != nil {
				pair = append(pair, c)
			}
		}
		if len(pair) != 2 {
			t.Fatalf("seed %d seated %d aces", seed, len(pair))
		}
		revised, current := pair[seed%2], pair[1-seed%2]
		revised.brain.tactics.stage = stage
		current.brain.tactics = standard() // the brain as it stands, whatever knobs the environment set for the one under evaluation
		decided := false
		for tick := uint64(0); tick < 60*240 && !decided; tick++ {
			i.Step(tick, nil)
			switch {
			case current.model == nil || !current.alive: // a detonation tears the model down in the death tick: nil IS a kill
				wins++
				decided = true
			case revised.model == nil || !revised.alive:
				losses++
				decided = true
			}
		}
		if !decided {
			draws++
		}
	}
	return
}

func TestRevisedAgainstCurrent(t *testing.T) {
	heavy(t)
	if evaluating == 0 && doctrine == standard() {
		t.Skip("nothing is under evaluation: set AIR_STAGE, or a knob such as AIR_GRAVITY=1")
	}
	for _, missiles := range []bool{false, true} {
		arm := map[bool]string{false: "guns", true: "heaters"}[missiles]
		wins, losses, draws := versus(t, evaluating, missiles, 48)
		decided := wins + losses
		t.Logf("stage %d, %s: revised %d - %d current, %d undecided of 48", evaluating, arm, wins, losses, draws)
		// Not worse than the brain it replaces, beyond what 48 seeds can tell
		// apart. Cutover asks for more (wins >= n/2 + sqrt n of n decided); a
		// stage only has to not lose ground.
		if float64(losses) > float64(wins)+math.Sqrt(float64(max(decided, 1))) {
			t.Errorf("stage %d loses the %s ladder to the current brain %d-%d", evaluating, arm, wins, losses)
		}
	}
}

// The control the comparison rests on: with BOTH bots at stage 0 the harness
// must be a mirror, or a seat or a seed is doing the winning.
func TestRevisedMirrorIsFair(t *testing.T) {
	heavy(t)
	wins, losses, draws := versus(t, 0, false, 24)
	t.Logf("mirror, guns: %d - %d, %d undecided of 24", wins, losses, draws)
	if decided := wins + losses; math.Abs(float64(wins-losses)) > 2*math.Sqrt(float64(max(decided, 1))) {
		t.Errorf("two identical aces split %d-%d: the seating is not fair", wins, losses)
	}
}

// TestHedgeControl: with ONE future and no hedge, stage 7 must choose exactly
// what stage 6 chooses, to the last digit of the promise. The re-ranking is an
// addition on top of the old argmax; if it moved a pick with nothing to hedge
// against, every ladder result for the stage would be measuring that instead.
func TestHedgeControl(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "hedgecontrol", Game: "air", Mode: "furball", Capacity: 8, Seed: 3,
		Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 1.0, "pilot": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	compared, hedged, moved := 0, 0, 0
	for tick := uint64(0); tick < 60*90; tick++ {
		i.Step(tick, nil)
		if tick%45 != 0 {
			continue
		}
		for _, slot := range i.slots() {
			a := i.aircraft[slot]
			if a == nil || a.brain == nil || a.model == nil || !a.alive || a.brain.prey == nil || a.brain.target < 0 {
				continue
			}
			pick := func(stage, futures int, hedge, peril float64) (string, float64) {
				shadow := *a.brain // choose() learns and casts: never on the live brain
				shadow.tactics.stage, shadow.tactics.futures, shadow.tactics.hedge, shadow.tactics.peril = stage, futures, hedge, peril
				sim := flight.New(a.model.Airframe, a.model.Environment, a.model.World)
				return i.choose(slot, a, &shadow, sim, shadow.prey, tick, shadow.distance, nil)
			}
			// Stage 7 is two changes: the futures, and `peril` priced in every
			// rehearsed instant. The control switches both off. With peril left
			// on it matches stage 6 only while no rollout samples a gun solution
			// on the ace, which is luck, not a control.
			plain, promise := pick(6, 4, 0.25, 1.3)
			single, same := pick(7, 1, 0, 0)
			if plain != single || promise != same {
				t.Fatalf("tick %d: stage 6 chose %s (%.6f), stage 7 with one future and no hedge chose %s (%.6f)", tick, plain, promise, single, same)
			}
			compared++
			if a.brain.distance < 2500 {
				hedged++
				if full, _ := pick(7, 4, 0.25, 1.3); full != plain {
					moved++
				}
			}
		}
	}
	if compared < 20 || hedged == 0 {
		t.Fatalf("only %d decision points compared, %d inside the hedging range: the control proved nothing", compared, hedged)
	}
	t.Logf("%d decision points identical; with four futures the pick moved at %d of %d inside 2,500 m", compared, moved, hedged)
}

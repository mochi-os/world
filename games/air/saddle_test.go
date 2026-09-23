// Mochi world: Saddle finishing
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestSaddleFinish: a slow ace parked in gun parameters behind a compliant
// target must finish the kill, not starve into rebuild and unload away. The
// energy floor must yield to the shot, not only to a close menace.
func TestSaddleFinish(t *testing.T) {
	g := New()
	made, _ := g.Create(game.Session{Identifier: "saddle", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
		Parameters: map[string]any{"bots": map[string]any{
			"red":  map[string]any{"ace": 1.0},
			"blue": map[string]any{"drone": 1.0},
		}}})
	i := made.(*instance)
	ace, prey := i.aircraft[99], i.aircraft[98]
	ace.brain.plan, ace.brain.planned = "two", 1 // the merge is history: the energy floor only arms once a merge plan exists, and this test starts mid-fight
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	// BELOW the ace's energy floor (154 m/s), dead astern at 400 m: the gift.
	slow := flight.Vec3{X: 140}
	fired, starved := false, false
	for tick := uint64(0); tick < 60*12; tick++ {
		aloft(ace, base, slow)
		aloft(prey, base.Add(flight.Vec3{X: 400}), slow)
		i.Step(tick, nil)
		if tick < 60 {
			continue // let the picture and the merge plan form
		}
		if ace.latest.Fire {
			fired = true
		}
		if ace.brain.mode == "rebuild" {
			starved = true
		}
	}
	if starved {
		t.Fatal("the ace starved out of the saddle: rebuild with a compliant target 400 m ahead")
	}
	if !fired {
		t.Fatal("twelve seconds in gun parameters behind a compliant target and the trigger never came")
	}
}

// The duel-path twins of TestSaddleFinish. That test runs in TEAM mode at
// 140 m/s, which is above the older "too slow to fight" reflex
// (speed < 0.55 of corner) that sits upstream of everything a TEAMLESS bot
// does - so nothing ever tested that reflex, and a recorded human fight
// (01a0b090, 2026-09-17) found it firing with the human 250 m off its tail:
// it unloaded in front of the gun and died. The first twin is the saddle,
// where the reflex would walk away from a kill; the second is that gun. Both
// hold the ace BELOW the gate.

// duellist builds a furball of one ace against one other bot, and returns the
// ace (the subject) and the other.
func duellist(t *testing.T, other string) (*instance, *craft, *craft) {
	t.Helper()
	g := New()
	bots := map[string]any{"ace": 1.0, other: 1.0}
	if other == "ace" {
		bots = map[string]any{"ace": 2.0}
	}
	made, err := g.Create(game.Session{Identifier: "duellist", Game: "air", Mode: "furball", Capacity: 16, Seed: 5,
		Parameters: map[string]any{"bots": bots}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	var subject, opposite *craft
	for _, slot := range i.slots() {
		c := i.aircraft[slot]
		if c == nil || !c.bot {
			continue
		}
		if subject == nil && c.brain != nil && c.team == "" {
			subject = c
		} else {
			opposite = c
		}
	}
	if subject == nil || opposite == nil {
		t.Fatal("the furball did not seat an ace and an opponent")
	}
	return i, subject, opposite
}

// TestDuelSaddleFinish: a slow teamless ace parked in gun parameters behind a
// compliant target must finish the kill, not unload away to rebuild.
func TestDuelSaddleFinish(t *testing.T) {
	i, ace, prey := duellist(t, "drone")
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	slow := flight.Vec3{X: 100} // 194 kt: under 0.55 of corner at this height
	if gate := 0.55 * corner(ace.model); slow.X >= gate {
		t.Fatalf("the twin must sit under the reflex's gate: 100 m/s against %.0f", gate)
	}
	fired, starved := false, false
	for tick := uint64(0); tick < 60*12; tick++ {
		aloft(ace, base, slow)
		aloft(prey, base.Add(flight.Vec3{X: 400}), slow)
		i.Step(tick, nil)
		if tick < 60 {
			continue // let the picture form
		}
		if ace.latest.Fire {
			fired = true
		}
		if ace.brain.mode == "rebuild" {
			starved = true
		}
	}
	if starved {
		t.Fatal("the teamless ace starved out of the saddle: rebuild with a compliant target 400 m ahead")
	}
	if !fired {
		t.Fatal("twelve seconds in gun parameters behind a compliant target and the trigger never came")
	}
}

// TestDuelBleedKeepsTheArbiter: a bleed (stage 9) is slow on purpose, so the
// "too slow to fight" reflex must leave it to the arbiter. When the bleed's
// commitment runs out, the arbiter re-plans before the reflex is asked, and the
// jet keeps whatever it chooses. The reflex still takes a slow jet committed to
// anything else. Here the target is 1.6 km ahead and flying away: no threat and
// no saddle to excuse the reflex on its own terms.
func TestDuelBleedKeepsTheArbiter(t *testing.T) {
	i, ace, prey := duellist(t, "drone")
	b := ace.brain
	b.tactics.stage, b.tactics.omit = 9, 1<<7|1<<8 // bleed on stage 6 alone
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	ahead := base.Add(flight.Vec3{X: 1600})
	tick := uint64(0)
	for ; tick < 90; tick++ { // the picture forms at a fighting speed
		aloft(ace, base, flight.Vec3{X: 200})
		aloft(prey, ahead, flight.Vec3{X: 200})
		i.Step(tick, nil)
	}
	slow := flight.Vec3{X: 100}
	if gate := 0.55 * corner(ace.model); slow.X >= gate {
		t.Fatalf("the jet must sit under the reflex's gate: 100 m/s against %.0f", gate)
	}
	decide := func(play string, until uint64) {
		b.play, b.until, b.spent, b.decided = play, until, false, 0 // decided 0: this tick is a decision
		aloft(ace, base, slow)
		aloft(prey, ahead, slow.Scale(2))
		i.Step(tick, nil)
		tick++
	}
	decide("bleed", tick+60)
	if b.mode == "rebuild" {
		t.Fatal("the reflex took a jet in the middle of a committed bleed")
	}
	decide("bleed", tick)
	if b.mode == "rebuild" || b.picked != tick-1 {
		t.Fatalf("the bleed's commitment ran out and the reflex answered before the arbiter: mode %q, last re-plan at tick %d, this tick %d", b.mode, b.picked, tick-1)
	}
	decide("high", tick+60)
	if b.mode != "rebuild" {
		t.Fatalf("a jet 100 m/s under the gate, committed to high with nothing near it, was not rebuilt: mode %q", b.mode)
	}
}

// TestDuelThreatenedNeverUnloads: a slow teamless ace with a gun 300 m behind it
// must fight, not fly a wings-level energy recovery in front of the muzzle.
func TestDuelThreatenedNeverUnloads(t *testing.T) {
	i, ace, hunter := duellist(t, "ace")
	base := flight.Vec3{X: 0, Y: 4000, Z: 0}
	slow := flight.Vec3{X: 100}
	unloaded := 0
	for tick := uint64(0); tick < 60*10; tick++ {
		aloft(ace, base, slow)
		aloft(hunter, base.Add(flight.Vec3{X: -300}), slow) // dead astern, nose on
		hunter.latest.Fire = true                           // and shooting: tracers are how a blind-cone attacker is perceived
		i.Step(tick, nil)
		if tick >= 120 && ace.brain.mode == "rebuild" {
			unloaded++
		}
	}
	if unloaded > 0 {
		t.Fatalf("the teamless ace flew rebuild for %.1f s with a gun 300 m behind it", float64(unloaded)/60)
	}
}

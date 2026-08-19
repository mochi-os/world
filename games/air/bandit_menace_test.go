// Mochi world: single-player menace wire — the beaten-round sentinel
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
	"world/games/air/round"
)

// menace builds one round's eight words for the wire: position, velocity,
// shooter, phase.
func menace(position, velocity flight.Vec3, shooter int, phase float64) []float64 {
	return []float64{position.X, position.Y, position.Z, velocity.X, velocity.Y, velocity.Z, float64(shooter), phase}
}

// TestMenaceBeaten pins the sentinel the single-player wire carries for a
// heater that has already lost. `beaten` (bot.go) reads `loose`/`blind`, and
// the client is the only source of those in single player — it flies the
// player's rounds itself. Before the sentinel existed the stubs were built
// with both flags at their zero values, so the instructor tiers' refusal to
// abandon a fight for a defeated round was dead in the one place a human ever
// sees it.
func TestMenaceBeaten(t *testing.T) {
	bandit := NewBandit("ace", 1, 250000, "", false, true, "fox2", 0)
	position, velocity := flight.Vec3{X: 1000}, flight.Vec3{X: -300}

	for _, c := range []struct {
		name   string
		phase  float64
		loose  bool
		radar  bool
		beaten bool
	}{
		{name: "a live heater", phase: -1, loose: false, radar: false, beaten: false},
		{name: "a beaten heater", phase: -2, loose: true, radar: false, beaten: true},
		{name: "a radar round mid-flight", phase: float64(round.Active), loose: false, radar: true, beaten: false},
	} {
		bandit.Menace(menace(position, velocity, 0, c.phase))
		if len(bandit.arena.flying) != 1 {
			t.Fatalf("%s: declared %d rounds, want 1", c.name, len(bandit.arena.flying))
		}
		m := bandit.arena.flying[0]
		if m.loose != c.loose {
			t.Errorf("%s: loose = %v, want %v", c.name, m.loose, c.loose)
		}
		if (m.radar != nil) != c.radar {
			t.Errorf("%s: radar round = %v, want %v", c.name, m.radar != nil, c.radar)
		}
		if m.target != 1 || m.shooter != 0 {
			t.Errorf("%s: round is %d -> %d, want the player (0) at the bandit (1)", c.name, m.shooter, m.target)
		}
		// The point of the sentinel: what the brain concludes from it.
		if got := beaten(bandit.craft.brain, m); got != c.beaten {
			t.Errorf("%s: beaten = %v, want %v", c.name, got, c.beaten)
		}
	}

	// Rebuilt every frame, so nothing accumulates: a round that has left the
	// air simply stops being declared.
	bandit.Menace(nil)
	if len(bandit.arena.flying) != 0 {
		t.Errorf("an empty declaration left %d rounds in the air", len(bandit.arena.flying))
	}
}

// TestMenaceBeatenDefence is the behavioural half: the bandit must go
// defensive for a live round and must NOT for one it can see has already been
// beaten. This is the whole point of the wire change — a bandit that breaks
// for spent missiles hands the fight to anyone willing to waste rounds.
func TestMenaceBeatenDefence(t *testing.T) {
	// The machine tier: no sighting roll and no reaction delay, so the response
	// is deterministic and the test measures doctrine rather than dice.
	evading := func(phase float64) bool {
		bandit := NewBandit("superhuman", 1, 250000, "", false, true, "fox2", 0)
		bandit.Spawn(flight.Vec3{Y: 5000}, flight.Vec3{X: 220})

		environment := flight.Environment{Seed: 1, Wrap: 250000}
		player := flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea})
		player.State = flight.Level(player, flight.Vec3{X: 2000, Y: 5000}, flight.Vec3{X: -1}, 220, fuel)
		words := make([]float64, flight.Size)

		for tick := 0; tick < 60; tick++ {
			player.State.Encode(words)
			bandit.Mirror(words, false, true) // not firing: the tracer cue must not be what trips this
			// One of the player's rounds, a kilometre from the bandit and
			// closing — well inside the 4,500 m the evade logic watches.
			bandit.Menace(menace(flight.Vec3{X: 1000, Y: 5000}, flight.Vec3{X: -300}, 0, phase))
			bandit.Step()
			if bandit.Mode() == "evade" {
				return true
			}
		}
		return false
	}

	if !evading(-1) {
		t.Error("a live heater inside 4.5 km did not put the bandit into evade: the defence is not reachable in single player at all")
	}
	if evading(-2) {
		t.Error("the bandit went defensive for a round it could see was already beaten: the -2 sentinel is not reaching `beaten`")
	}
}

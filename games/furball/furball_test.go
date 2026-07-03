// Mochi world: Furball game module tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package furball

import (
	"testing"

	"world/game"
	"world/games/furball/flight"
)

func build(t *testing.T, mode string, parameters map[string]any, players int) *instance {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "test", Game: "furball", Mode: mode, Capacity: 16, Seed: 2, Parameters: parameters})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	i := made.(*instance)
	for slot := 0; slot < players; slot++ {
		if _, err := i.Join(game.Player{Identity: "", Name: "p", Slot: slot}); err != nil {
			t.Fatalf("join %d: %v", slot, err)
		}
	}
	return i
}

// place puts b dead ahead of a, in guns range.
func place(i *instance, a int, b int, distance float64) {
	shooter := &i.aircraft[a].model.State
	target := &i.aircraft[b].model.State
	target.Position = shooter.Position
	forward := shooter.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
	target.Position.X += forward.X * distance
	target.Position.Y += forward.Y * distance
	target.Position.Z += forward.Z * distance
	target.Velocity = shooter.Velocity
	target.Attitude = shooter.Attitude
}

func fire(i *instance, slot int, sample map[string]any) {
	i.Step(0, map[int][]game.Input{slot: {{Sequence: 1, Data: sample}}})
}

// TestJoust: the first kill ends the match with the shooter as winner, and
// nobody respawns.
func TestJoust(t *testing.T) {
	i := build(t, "joust", nil, 2)
	place(i, 0, 1, 300)
	steady := map[string]any{"throttle": 0.85, "fire": true}
	for tick := 0; tick < 60*10; tick++ {
		fire(i, 0, steady)
		if done, _ := i.Finished(); done {
			break
		}
		place(i, 0, 1, 300) // hold the target on the nose against drift
	}
	done, results := i.Finished()
	if !done {
		t.Fatal("joust did not finish on a kill")
	}
	if results["winner"] != 0 || results["loser"] != 1 {
		t.Fatalf("wrong outcome: %v", results)
	}
	for tick := 0; tick < 60*7; tick++ { // past the respawn pause
		i.Step(0, nil)
	}
	if i.aircraft[1].alive {
		t.Fatal("loser respawned in a joust")
	}
	if _, err := i.Join(game.Player{Name: "late", Slot: 2}); err == nil {
		t.Fatal("joined a finished joust")
	}
}

// TestJoustLeave: abandoning a live joust hands the win to the stayer.
func TestJoustLeave(t *testing.T) {
	i := build(t, "joust", nil, 2)
	i.Leave(game.Player{Slot: 1})
	done, results := i.Finished()
	if !done || results["winner"] != 0 {
		t.Fatalf("expected slot 0 to win by forfeit: %v %v", done, results)
	}
}

// TestFurballRespawn: open mode respawns after the pause.
func TestFurballRespawn(t *testing.T) {
	i := build(t, "furball", nil, 2)
	i.kill(1, 0)
	if done, _ := i.Finished(); done {
		t.Fatal("open match finished on a kill")
	}
	for tick := 0; tick < 60*6; tick++ {
		i.Step(0, nil)
	}
	if !i.aircraft[1].alive {
		t.Fatal("no respawn in open mode")
	}
}

// TestMissiles: the rule gates launches; an allowed missile tracks and kills.
func TestMissiles(t *testing.T) {
	denied := build(t, "furball", nil, 2)
	place(denied, 0, 1, 2000)
	fire(denied, 0, map[string]any{"missile": true})
	if len(denied.flying) != 0 {
		t.Fatal("missile launched despite the guns-only rule")
	}

	allowed := build(t, "furball", map[string]any{"missiles": true}, 2)
	place(allowed, 0, 1, 2000)
	fire(allowed, 0, map[string]any{"missile": true})
	if len(allowed.flying) != 1 {
		t.Fatal("missile did not launch")
	}
	for tick := 0; tick < 60*12; tick++ {
		allowed.Step(0, nil)
		place(allowed, 0, 1, 2000-float64(tick)) // target closing, missile faster
		if !allowed.aircraft[1].alive {
			return // splashed
		}
	}
	t.Fatal("missile never scored")
}

// TestDecoy: with seed 2 the first launch decoys on a fresh flare
// ((2+1)%3 == 0 keeps lock — use launch 2 which breaks), and the decoyed
// missile disappears.
func TestDecoy(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true}, 2)
	place(i, 0, 1, 3000)
	fire(i, 0, map[string]any{"missile": true}) // launch 1: (2+1)%3==0 → immune to flares
	fire(i, 0, map[string]any{"missile": true}) // launch 2: (2+2)%3==1 → decoys
	if len(i.flying) != 2 {
		t.Fatalf("expected 2 missiles, got %d", len(i.flying))
	}
	fire(i, 1, map[string]any{"flare": true})
	i.Step(0, nil)
	if len(i.flying) != 1 {
		t.Fatalf("expected the flare to decoy exactly one missile, %d flying", len(i.flying))
	}
}

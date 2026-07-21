// Mochi world: timed-out join / ghost player handling (#176)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"testing"

	"world/game"
)

type fakeInstance struct{ joined, left int }

func (f *fakeInstance) Join(game.Player) (map[string]any, error) { f.joined++; return map[string]any{}, nil }
func (f *fakeInstance) Leave(game.Player)                        { f.left++ }
func (f *fakeInstance) Step(uint64, map[int][]game.Input)        {}
func (f *fakeInstance) Snapshot(uint64) map[string]any           { return nil }
func (f *fakeInstance) Events() []map[string]any                 { return nil }
func (f *fakeInstance) Finished() (bool, map[string]any)         { return false, nil }

// joinCancels closes the order's cancel channel from inside Join, to hit the
// post-Join rollback window deterministically.
type joinCancels struct {
	fakeInstance
	cancel chan struct{}
}

func (j *joinCancels) Join(p game.Player) (map[string]any, error) {
	j.joined++
	close(j.cancel)
	return map[string]any{}, nil
}

func bareSession(inst game.Instance, capacity int) *session {
	return &session{identifier: "t", spec: game.Session{Capacity: capacity}, instance: inst, players: map[int]*player{}}
}

// TestJoinCancelledBeforeJoin: a caller that already gave up (cancel closed)
// is refused before Instance.Join runs — no ghost, no game-side join.
func TestJoinCancelledBeforeJoin(t *testing.T) {
	inst := &fakeInstance{}
	s := bareSession(inst, 4)
	cancel := make(chan struct{})
	close(cancel)
	if a := session_join(s, order{kind: "join", cancel: cancel}); a.err == nil {
		t.Fatal("cancelled join was admitted")
	}
	if len(s.players) != 0 {
		t.Fatalf("ghost created: %d players", len(s.players))
	}
	if inst.joined != 0 {
		t.Fatalf("Instance.Join ran for a pre-cancelled join: %d", inst.joined)
	}
}

// TestJoinCancelledDuringJoin: the caller times out WHILE Instance.Join runs;
// the game-side join must be rolled back and no player committed.
func TestJoinCancelledDuringJoin(t *testing.T) {
	inst := &joinCancels{cancel: make(chan struct{})}
	s := bareSession(inst, 4)
	if a := session_join(s, order{kind: "join", cancel: inst.cancel}); a.err == nil {
		t.Fatal("join admitted despite a mid-join cancel")
	}
	if len(s.players) != 0 {
		t.Fatalf("ghost created: %d players", len(s.players))
	}
	if inst.joined != 1 || inst.left != 1 {
		t.Fatalf("rollback missing: joined=%d left=%d (want 1/1)", inst.joined, inst.left)
	}
}

// TestRemoveGhost: session_remove reaps a nil-link ghost, tells the game, frees
// the slot, and is idempotent (a later duplicate leave is a no-op).
func TestRemoveGhost(t *testing.T) {
	inst := &fakeInstance{}
	s := bareSession(inst, 2)
	s.players[0] = &player{Player: game.Player{Name: "ghost", Slot: 0}, link: nil}
	session_remove(s, 0)
	if len(s.players) != 0 {
		t.Fatalf("ghost survived removal: %d players", len(s.players))
	}
	if inst.left != 1 {
		t.Fatalf("Instance.Leave not called on removal: %d", inst.left)
	}
	session_remove(s, 0) // a real reader's late leave for the same slot
	if inst.left != 1 {
		t.Fatalf("double removal leaked a Leave: %d", inst.left)
	}
}

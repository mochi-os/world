// Mochi world: panic containment
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"testing"
	"time"

	"world/game"
)

// faultyInstance panics on its nth Step, standing in for the arithmetic edges
// real game modules hit on client-chosen input.
type faultyInstance struct {
	fakeInstance
	after int
	steps int
}

func (f *faultyInstance) Step(tick uint64, in map[int][]game.Input) {
	f.steps++
	if f.steps >= f.after {
		panic("simulated game fault")
	}
}

// countingInstance just records that it is still being ticked.
type countingInstance struct {
	fakeInstance
	steps int
}

func (c *countingInstance) Step(uint64, map[int][]game.Input) { c.steps++ }

type fakeGame struct {
	name     string
	instance game.Instance
}

func (f *fakeGame) Name() string                               { return f.name }
func (f *fakeGame) Rate() (int, int)                           { return 60, 20 }
func (f *fakeGame) Create(game.Session) (game.Instance, error) { return f.instance, nil }

// TestGuardRecovers: a panic inside the guarded function must not escape, and
// the cleanup must run.
func TestGuardRecovers(t *testing.T) {
	cleaned := false
	guard("unit", func() { cleaned = true }, func() { panic("boom") })
	if !cleaned {
		t.Fatal("guard did not run its cleanup after a panic")
	}
}

// TestGuardSurvivesFaultyCleanup: a second panic while cleaning up must not
// escape either, or the guard defeats itself exactly when it is needed.
func TestGuardSurvivesFaultyCleanup(t *testing.T) {
	guard("unit", func() { panic("cleanup boom") }, func() { panic("boom") })
}

// TestGuardPassesThrough: no panic, no cleanup.
func TestGuardPassesThrough(t *testing.T) {
	cleaned, ran := false, false
	guard("unit", func() { cleaned = true }, func() { ran = true })
	if !ran {
		t.Fatal("guard did not run f")
	}
	if cleaned {
		t.Fatal("guard ran cleanup without a panic")
	}
}

// TestSessionPanicIsContained: a game module that panics mid-tick must end ONLY
// its own session. Before the guard this killed the process, taking every other
// match on the host with it — which is what made a one-line divide-by-zero in
// the air snapshot a whole-server outage.
func TestSessionPanicIsContained(t *testing.T) {
	faulty := &faultyInstance{after: 2}
	healthy := &countingInstance{}
	games["faulty"] = &fakeGame{name: "faulty", instance: faulty}
	games["healthy"] = &fakeGame{name: "healthy", instance: healthy}
	defer func() { delete(games, "faulty"); delete(games, "healthy") }()

	good, err := sessions_make("healthy", "m", "M", 4, nil, false)
	if err != nil {
		t.Fatalf("healthy session: %v", err)
	}
	defer session_close(good, "test")
	bad, err := sessions_make("faulty", "m", "M", 4, nil, false)
	if err != nil {
		t.Fatalf("faulty session: %v", err)
	}

	// The faulty session must end itself. If the guard were missing the process
	// would be gone and this test would never report at all.
	select {
	case <-bad.done:
	case <-time.After(5 * time.Second):
		t.Fatal("faulty session never ended after its game panicked")
	}

	sessions_lock.RLock()
	_, lingering := sessions[bad.identifier]
	_, survived := sessions[good.identifier]
	sessions_lock.RUnlock()
	if lingering {
		t.Error("faulted session left in the session map")
	}
	if !survived {
		t.Fatal("the healthy session was removed when its neighbour faulted")
	}

	// And it must still be ticking, not merely present.
	before := healthy.steps
	time.Sleep(200 * time.Millisecond)
	if healthy.steps <= before {
		t.Fatalf("healthy session stopped ticking: %d steps", healthy.steps-before)
	}
}

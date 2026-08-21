// Mochi world: Game interface
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Package game defines the contract between the world server and game
// modules. The server owns sessions, connections, ticking, and the wire;
// a game module owns the simulation. Every Instance method is called only
// from its session's tick goroutine, so implementations need no locking.
package game

// Player identifies one participant. Identity is self-asserted; Slot is
// assigned by the instance at Join and stable for the session; Team is the
// game-defined join choice, passed through verbatim.
type Player struct {
	Identity string
	Name     string
	Slot     int
	Team     string
	Stores   map[string]any // game-defined loadout request (#17), passed through from the join message verbatim; the game validates and clamps it
}

// Session carries the parameters a session was created with. Parameters is
// game-defined and opaque to the server, and READ-ONLY from Create onward: it
// is shared with the server's records, and a concurrent map write is fatal.
type Session struct {
	Identifier string
	Game       string
	Mode       string
	Label      string
	Capacity   int
	Seed       uint64
	Parameters map[string]any
}

// Input is one sequenced control sample from a player. Data is game-defined.
type Input struct {
	Sequence uint32
	Data     map[string]any
}

// Departure is one station's jettison request (#18): What is "stores" (the
// mounts leave, the fixture stays) or "rack" (the fixture with everything on
// it). The game instance validates — the wire promises nothing.
type Departure struct {
	Station int
	What    string
}

// Game is a registered module: a factory for per-session instances plus its
// fixed simulation and snapshot rates in Hz.
type Game interface {
	Name() string
	Rate() (tick int, snapshot int)
	Create(session Session) (Instance, error)
}

// Instance is one running match.
type Instance interface {
	// Join adds a player and returns the game-defined spawn/welcome payload,
	// or an error (session full, wrong parameters) that refuses the join.
	Join(player Player) (map[string]any, error)
	Leave(player Player)
	// Step advances the simulation one fixed tick. inputs maps slot to the
	// inputs received since the previous step, in arrival order.
	Step(tick uint64, inputs map[int][]Input)
	// Snapshot returns the shared state payload broadcast to every player;
	// the server wraps it in a per-recipient envelope.
	Snapshot(tick uint64) map[string]any
	// Events returns and clears the events raised since the last call
	// (kills, respawns, game-defined happenings).
	Events() []map[string]any
	// Finished reports whether the match has ended, with its results.
	Finished() (bool, map[string]any)
}

// Occupancy is the optional half of Instance for games that place non-player
// entities in the slot space. Without it the server assigns slots from its own
// player map, which cannot see them, and Join overwrites them.
type Occupancy interface {
	// Occupied reports whether a slot already holds something the game placed
	// there itself. It is consulted only while choosing a slot for a join.
	Occupied(slot int) bool
}

// Closer is the optional half of Instance for games holding a resource that
// outlives a tick and is shared across sessions (air's practice-bot budget).
// The server calls Close exactly once, when the session ends.
type Closer interface {
	Close()
}

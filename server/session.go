// Mochi world: Session registry and lifecycle
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"world/game"
)

// games holds the registered game modules by name.
var games = map[string]game.Game{}

func games_register(g game.Game) { games[g.Name()] = g }

// player is one participant in a session. All fields after link are owned by
// the session's tick goroutine.
type player struct {
	game.Player
	link     link         // nil once disconnected
	sequence uint32       // highest input sequence applied (acknowledged in snapshots)
	queue    []game.Input // inputs since the last step
	seen     time.Time    // last input received — the application-level liveness signal
	talked   []time.Time  // recent chat sends (#84): the flood limiter's window
}

// order is a control message from a connection to a session's tick goroutine.
type order struct {
	kind   string // join, leave, input, chat
	player game.Player
	link   link
	inputs []game.Input
	slot   int
	text   string        // chat only
	scope  string        // chat only: "team" or "all"
	reply  chan answer   // join only
	cancel chan struct{} // join only: closed when the caller stops waiting, so a stalled tick's late join rolls back instead of committing a ghost
}

// answer is the tick goroutine's response to a join order (the welcome
// itself is sent by the tick goroutine, keeping per-link message order).
type answer struct {
	slot int
	err  error
}

// spoken is one delivered chat line: the event as sent, plus the team it was
// scoped to ("" = everyone) so the replay-on-join respects the same audience.
type spoken struct {
	event map[string]any
	team  string
}

// sided is the optional game-instance interface the chat scoping asks: which
// side a slot flies for ("" outside team modes). Called only from the
// session's tick goroutine, which owns the instance.
type sided interface {
	Team(slot int) string
}

type session struct {
	identifier string
	spec       game.Session
	instance   game.Instance
	created    time.Time
	inbox      chan order
	done       chan struct{} // closed when the session ends

	// tick-goroutine-owned after start:
	tick    uint64
	players map[int]*player
	empty   time.Time // since when no player has been connected
	chats   []spoken  // the recent-chat ring (#84): replayed to joiners so a late arrival sees the conversation

	permanent bool // a standing match: exempt from the idle sweep, recreated at startup

	// Offer ownership (#77). A player may hold ONE live offer at a time: an
	// open match nobody has joined yet, kept alive by its owner's presence on
	// the server page. owner is the pilot token the creator sent (stable
	// across reconnects, unlike an address); offered is its last heartbeat —
	// the page's own match-list poll — and withdrawn is set by the lobby
	// goroutine for the tick loop to act on, since only the tick goroutine may
	// close a session. joined latches once anyone actually connects: from then
	// on it is a match like any other and the offer rules stop applying.
	owner     string
	offered   time.Time
	joined    bool
	withdrawn bool

	// registry-owned counters mirrored for the lobby (updated under sessions_lock):
	connected int
	names     []map[string]any
	state     string // open, playing, finished
}

var (
	sessions      = map[string]*session{}
	sessions_lock sync.RWMutex
)

// identifier returns a fresh random session identifier.
func identifier() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err // a zero identifier collides with every other zero identifier
	}
	return hex.EncodeToString(bytes), nil
}

// sessions_create makes a session for the named game, starts its tick loop,
// and registers it. Enforced limits: known game, total session cap.
func sessions_create(name string, mode string, label string, capacity int, parameters map[string]any) (*session, error) {
	return sessions_make(name, mode, label, capacity, parameters, false)
}

// sessions_standing creates the permanent matches: one always-open session
// per game listed in [world] standing (default: every registered game).
// Anyone may join or leave at any time; the idle sweep never ends them.
func sessions_standing() {
	names := ini_string("world", "standing", "")
	list := []string{}
	if names == "" {
		for name := range games {
			if name != "echo" {
				list = append(list, name)
			}
		}
	} else {
		for _, name := range strings.Fields(strings.ReplaceAll(names, ",", " ")) {
			list = append(list, name)
		}
	}
	for _, name := range list {
		if games[name] == nil {
			warn("standing session: unknown game %q", name)
			continue
		}
		// The standing session is the always-on free-for-all: it carries the MODE's
		// name ("Furball", the dogfight term) in the match list, not the game's.
		mode := "furball"
		label := strings.ToUpper(mode[:1]) + mode[1:]
		if _, err := sessions_make(name, mode, label, 0, nil, true); err != nil {
			warn("standing session %s: %v", name, err)
		}
	}
}

func sessions_make(name string, mode string, label string, capacity int, parameters map[string]any, permanent bool) (*session, error) {
	g, found := games[name]
	if !found {
		return nil, errors.New("unknown")
	}
	limit := ini_int("limits", "sessions", 100)
	sessions_lock.Lock()
	defer sessions_lock.Unlock()
	if len(sessions) >= limit {
		return nil, errors.New("full")
	}
	most := ini_int("limits", "players", 100)
	if capacity <= 0 || capacity > most {
		capacity = most
	}
	token, err := identifier()
	if err != nil {
		return nil, err
	}
	random, err := seed()
	if err != nil {
		return nil, err
	}
	spec := game.Session{Identifier: token, Game: name, Mode: mode, Label: label, Capacity: capacity, Seed: random, Parameters: parameters}
	instance, err := g.Create(spec)
	if err != nil {
		return nil, err
	}
	s := &session{
		permanent:  permanent,
		identifier: spec.Identifier,
		spec:       spec,
		instance:   instance,
		created:    time.Now(),
		inbox:      make(chan order, 256),
		done:       make(chan struct{}),
		players:    map[int]*player{},
		empty:      time.Now(),
		names:      []map[string]any{},
		state:      "open",
	}
	sessions[s.identifier] = s
	go session_run(s, g)
	info("session %s created: %s %s %q capacity %d", s.identifier, name, mode, label, capacity)
	return s, nil
}

// OFFER_GRACE is how long an unjoined offer outlives its owner's last
// heartbeat. The server page polls the match list every few seconds, so this
// covers a closed tab, a lost connection, or simply walking away; leaving the
// page deliberately withdraws at once through sessions_withdraw.
const OFFER_GRACE = 25 * time.Second

// sessions_own records the creator and starts the offer clock.
func sessions_own(s *session, owner string) {
	if owner == "" {
		return
	}
	sessions_lock.Lock()
	s.owner = owner
	s.offered = time.Now()
	sessions_lock.Unlock()
}

// sessions_withdraw flags every live offer held by this pilot. Creating a new
// match calls it first — one offer at a time, and the new one replaces the
// old — as does leaving the server page or joining somebody else's match.
func sessions_withdraw(owner string) int {
	if owner == "" {
		return 0
	}
	count := 0
	sessions_lock.Lock()
	for _, s := range sessions {
		if s.owner == owner && !s.joined && !s.permanent && !s.withdrawn {
			s.withdrawn = true
			count++
		}
	}
	sessions_lock.Unlock()
	return count
}

// sessions_touch refreshes the offer clock for this pilot's own offers: the
// match-list poll IS the heartbeat, so a player browsing the server page keeps
// their offer alive without a second request.
//
// A token holding no offer costs only the read lock. The poll is
// unauthenticated and its token unvalidated, so taking the write lock on the
// way in would let any caller contend with the tick goroutines, which need it
// for every mirror update. The match list itself is deliberately not rate
// limited: players cluster behind one address far more than attackers do.
func sessions_touch(owner string) {
	if owner == "" {
		return
	}
	sessions_lock.RLock()
	held := false
	for _, s := range sessions {
		if s.owner == owner && !s.joined {
			held = true
			break
		}
	}
	sessions_lock.RUnlock()
	if !held {
		return
	}
	now := time.Now()
	sessions_lock.Lock()
	for _, s := range sessions {
		if s.owner == owner && !s.joined {
			s.offered = now
		}
	}
	sessions_lock.Unlock()
}

// sessions_stale flags offers whose owner has stopped heartbeating.
func sessions_stale() {
	now := time.Now()
	sessions_lock.Lock()
	for _, s := range sessions {
		if s.owner != "" && !s.joined && !s.permanent && !s.withdrawn && !s.offered.IsZero() && now.Sub(s.offered) > OFFER_GRACE {
			s.withdrawn = true
		}
	}
	sessions_lock.Unlock()
}

func seed() (uint64, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return 0, err // a zero seed makes every match predictable
	}
	var v uint64
	for _, b := range bytes {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

func sessions_get(identifier string) *session {
	sessions_lock.RLock()
	defer sessions_lock.RUnlock()
	return sessions[identifier]
}

// sessions_list summarises sessions for the lobby, optionally filtered by game.
// pilot is the CALLER's own token, matched here and never published: the token
// is the capability /withdraw and the heartbeat accept, so a listing that
// carried it would let any reader retire anybody's offer. The client only ever
// needs to know which offer is its own, which is what mine answers.
func sessions_list(name string, pilot string) []map[string]any {
	sessions_lock.RLock()
	defer sessions_lock.RUnlock()
	list := []map[string]any{}
	for _, s := range sessions {
		if name != "" && s.spec.Game != name {
			continue
		}
		list = append(list, map[string]any{
			"mine":      pilot != "" && s.owner == pilot,
			"offer":     s.owner != "" && !s.joined,
			"session":   s.identifier,
			"game":      s.spec.Game,
			"mode":      s.spec.Mode,
			"label":     s.spec.Label,
			"capacity":  s.spec.Capacity,
			"players":   s.names,
			"created":   s.created.Unix(),
			"state":     s.state,
			"permanent": s.permanent,
		})
	}
	sort.Slice(list, func(a int, b int) bool {
		if list[a]["permanent"] != list[b]["permanent"] {
			return list[a]["permanent"].(bool)
		}
		return list[a]["created"].(int64) < list[b]["created"].(int64)
	})
	return list
}

// sessions_counts returns total sessions and connected players for /status.
func sessions_counts() (int, int) {
	sessions_lock.RLock()
	defer sessions_lock.RUnlock()
	players := 0
	for _, s := range sessions {
		players += s.connected
	}
	return len(sessions), players
}

// session_mirror refreshes the registry-owned lobby counters from the tick
// goroutine's player set.
func session_mirror(s *session, state string) {
	names := []map[string]any{}
	connected := 0
	for _, p := range s.players {
		names = append(names, map[string]any{"name": p.Name, "slot": p.Slot})
		if p.link != nil {
			connected++
		}
	}
	sessions_lock.Lock()
	s.names = names
	s.connected = connected
	if connected > 0 {
		s.joined = true // somebody flew it: this is a match now, not an offer
	}
	if state != "" {
		s.state = state
	}
	sessions_lock.Unlock()
}

// session_end removes the session from the registry and releases its loop.
func session_end(s *session, reason string) {
	sessions_lock.Lock()
	delete(sessions, s.identifier)
	sessions_lock.Unlock()
	close(s.done)
	info("session %s ended: %s", s.identifier, reason)
}

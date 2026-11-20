// Mochi world: lobby status tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLobbyPresence: the status counts the players HERE - the pilots sighted
// on the server page inside the window plus those in a match - separately
// from the players flying, so the server page can say "2 players · 0 flying"
// instead of repeating the server's name beside a count of nobody.
func TestLobbyPresence(t *testing.T) {
	presence_lock.Lock()
	presence = map[string]map[string]time.Time{}
	presence_lock.Unlock()
	poll := func(pilot string) {
		r := httptest.NewRequest("GET", "/sessions?game=air&pilot="+pilot, nil)
		r.RemoteAddr = "198.51.100.7:4433"
		lobby_sessions(httptest.NewRecorder(), r)
	}
	status := func() (present, players float64) {
		r := httptest.NewRequest("GET", "/status", nil)
		r.RemoteAddr = "198.51.100.8:4433"
		w := httptest.NewRecorder()
		lobby_status(w, r)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("status body: %v", err)
		}
		present, _ = body["present"].(float64)
		players, _ = body["players"].(float64)
		return
	}
	poll("alpha")
	poll("bravo")
	poll("alpha") // the same player polling again is still one player
	poll("")      // no token: a browser without a pilot is not a player
	_, flying := status()
	if present, _ := status(); present != 2+flying {
		t.Errorf("present %v, want %v: two pilots sighted plus the %v flying", present, 2+flying, flying)
	}
	// A pilot gone quiet past the window is no longer here.
	presence_lock.Lock()
	presence["198.51.100.7"]["bravo"] = time.Now().Add(-presence_window - time.Second)
	presence_lock.Unlock()
	if present, _ := status(); present != 1+flying {
		t.Errorf("present %v after bravo went quiet, want %v", present, 1+flying)
	}
}

// TestPresenceCountsAnAddressOnce: the pilot token is whatever the caller puts
// in the query, so one address cycling tokens inside its read budget stood for
// forty pilots and made an empty server look busy.
func TestPresenceCountsAnAddressOnce(t *testing.T) {
	presence_lock.Lock()
	presence = map[string]map[string]time.Time{}
	presence_lock.Unlock()
	for n := range 40 {
		presence_touch("198.51.100.30:1", fmt.Sprintf("crowd-%d", n))
	}
	presence_touch("198.51.100.31:1", "neighbour")
	if got := presence_count(); got != HOST_CONNECTIONS_MAXIMUM+1 {
		t.Errorf("40 tokens from one address and one from another counted as %d present, want %d", got, HOST_CONNECTIONS_MAXIMUM+1)
	}
	// A pilot already counted keeps being sighted at the cap.
	presence_lock.Lock()
	presence["198.51.100.30"]["crowd-0"] = time.Now().Add(-time.Second)
	presence_lock.Unlock()
	presence_touch("198.51.100.30:1", "crowd-0")
	presence_lock.Lock()
	fresh := time.Since(presence["198.51.100.30"]["crowd-0"]) < time.Second
	presence_lock.Unlock()
	if !fresh {
		t.Error("a pilot inside the address's share stopped being sighted once it was full")
	}
}

// TestPresenceIsPrunedOnItsOwnClock: only a /status request pruned the map, so
// with nobody reading the status it grew with every token anyone sent.
func TestPresenceIsPrunedOnItsOwnClock(t *testing.T) {
	presence_lock.Lock()
	presence = map[string]map[string]time.Time{"198.51.100.40": {"gone": time.Now().Add(-presence_window - time.Second)}}
	presence_lock.Unlock()
	presence_prune()
	presence_lock.Lock()
	left := len(presence)
	presence_lock.Unlock()
	if left != 0 {
		t.Errorf("%d addresses left after the prune, want none: the only sighting was past the window", left)
	}
}

// TestPollAnswersThePageInOneRead: the server page asked for the status, the
// match list and the chat separately, 44 reads a minute, so three players
// behind one address ran the budget dry. One poll carries all three, spends
// one read, and is the heartbeat and the sighting the match-list read is.
func TestPollAnswersThePageInOneRead(t *testing.T) {
	browses = map[string][]time.Time{}
	presence_lock.Lock()
	presence = map[string]map[string]time.Time{}
	presence_lock.Unlock()
	stale := time.Now().Add(-2 * OFFER_GRACE)
	offer := &session{identifier: "poll-heartbeat", owner: "poller", offered: stale}
	sessions_lock.Lock()
	sessions[offer.identifier] = offer
	sessions_lock.Unlock()
	t.Cleanup(func() {
		sessions_lock.Lock()
		delete(sessions, offer.identifier)
		sessions_lock.Unlock()
	})
	chat_append(map[string]any{"name": "poll", "text": "hello"})
	_, latest := chat_since(0)
	r := httptest.NewRequest("GET", fmt.Sprintf("/poll?game=air&pilot=poller&since=%d", latest-1), nil)
	r.RemoteAddr = "198.51.100.50:1"
	w := httptest.NewRecorder()
	lobby_poll(w, r)
	if w.Code != 200 {
		t.Fatalf("poll answered %d", w.Code)
	}
	var body struct {
		Status   map[string]any   `json:"status"`
		Sessions []map[string]any `json:"sessions"`
		Chat     struct {
			Lines    []map[string]any `json:"lines"`
			Sequence uint64           `json:"sequence"`
		} `json:"chat"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status["protocol"] == nil || body.Sessions == nil || len(body.Chat.Lines) != 1 || body.Chat.Sequence != latest {
		t.Errorf("poll carried status %v, %d sessions, %d chat lines to %d; want the status, the list and the one line to %d", body.Status, len(body.Sessions), len(body.Chat.Lines), body.Chat.Sequence, latest)
	}
	creates_lock.Lock()
	spent := len(browses["198.51.100.50"])
	creates_lock.Unlock()
	if spent != 1 {
		t.Errorf("one poll spent %d reads of the budget, want 1", spent)
	}
	if presence_count() != 1 {
		t.Error("the poll was not counted as a sighting of its pilot")
	}
	sessions_lock.RLock()
	beat := offer.offered.After(stale)
	sessions_lock.RUnlock()
	if !beat {
		t.Error("the poll did not keep its pilot's offer alive")
	}
	bad := httptest.NewRequest("GET", "/poll?since=nope", nil)
	bad.RemoteAddr = "198.51.100.50:1"
	refused := httptest.NewRecorder()
	lobby_poll(refused, bad)
	if refused.Code != 400 {
		t.Errorf("a rubbish cursor answered %d, want 400", refused.Code)
	}
}

// TestTransportOriginFollowsTheCaller: with no [transport] address - the
// packaged default - every caller was told the game was at
// https://127.0.0.1:<port>, and a remote player dialled their own machine.
func TestTransportOriginFollowsTheCaller(t *testing.T) {
	t.Setenv("MOCHI_TRANSPORT_ADDRESS", "")
	t.Setenv("MOCHI_TRANSPORT_PORT", "4433")
	asked := func(host string) string {
		r := httptest.NewRequest("GET", "/status", nil)
		r.Host = host
		return transport_origin(r)
	}
	for host, want := range map[string]string{
		"game.example.org:4433": "https://game.example.org:4433",
		"game.example.org":      "https://game.example.org:4433",
		"[2001:db8::1]:4433":    "https://[2001:db8::1]:4433",
		"203.0.113.9:8080":      "https://203.0.113.9:4433",
		"evil/path?x=1":         "https://127.0.0.1:4433",
	} {
		if got := asked(host); got != want {
			t.Errorf("a caller who reached the lobby at %q was sent to %q, want %q", host, got, want)
		}
	}
	t.Setenv("MOCHI_TRANSPORT_ADDRESS", "https://configured.example:4433/")
	if got := asked("game.example.org"); got != "https://configured.example:4433" {
		t.Errorf("a configured address gave way to the caller's host: %q", got)
	}
}

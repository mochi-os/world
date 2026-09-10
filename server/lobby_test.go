// Mochi world: lobby status tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/json"
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
	presence = map[string]time.Time{}
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
	presence["bravo"] = time.Now().Add(-presence_window - time.Second)
	presence_lock.Unlock()
	if present, _ := status(); present != 1+flying {
		t.Errorf("present %v after bravo went quiet, want %v", present, 1+flying)
	}
}

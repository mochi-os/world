// Mochi world: the lobby serves map geometry
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"world/games/air"
)

// TestLobbyMap: GET /maps/<name> serves a carried map as JSON from any origin,
// and nothing for a name the game does not carry or admit.
func TestLobbyMap(t *testing.T) {
	games["air"] = air.New()
	get := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "198.51.100.9:4433"
		w := httptest.NewRecorder()
		lobby_map(w, r)
		return w
	}
	w := get("GET", "/maps/midway")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("midway: %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Access-Control-Allow-Origin"))
	}
	if body := w.Body.String(); !strings.Contains(body, `"prisms"`) || !strings.Contains(body, `"posts"`) {
		t.Errorf("midway body carries no obstacles: %.80s", body)
	}
	for _, path := range []string{"/maps/nowhere", "/maps/../midway", "/maps/Midway", "/maps/"} {
		if w := get("GET", path); w.Code != 404 {
			t.Errorf("%s: %d, want 404", path, w.Code)
		}
	}
	if w := get("OPTIONS", "/maps/midway"); w.Code != 204 {
		t.Errorf("preflight: %d, want 204", w.Code)
	}
}

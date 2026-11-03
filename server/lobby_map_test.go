// Mochi world: the lobby serves map geometry
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"bytes"
	"compress/gzip"
	"io"
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

// TestLobbyMapGzip: a request that accepts gzip gets the map gzipped, which
// unpacks to the very bytes served raw, and every answer says it varies by
// Accept-Encoding; one that refuses gzip, or names none, gets the raw file.
func TestLobbyMapGzip(t *testing.T) {
	games["air"] = air.New()
	get := func(accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/maps/midway", nil)
		r.RemoteAddr = "198.51.100.10:4433"
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		w := httptest.NewRecorder()
		lobby_map(w, r)
		return w
	}
	raw := get("")
	if raw.Code != 200 || raw.Header().Get("Content-Encoding") != "" || raw.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("plain: %d encoding %q vary %q", raw.Code, raw.Header().Get("Content-Encoding"), raw.Header().Get("Vary"))
	}
	packed := get("gzip, deflate, br")
	if packed.Code != 200 || packed.Header().Get("Content-Encoding") != "gzip" || packed.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzip: %d encoding %q vary %q", packed.Code, packed.Header().Get("Content-Encoding"), packed.Header().Get("Vary"))
	}
	if packed.Body.Len()*4 > raw.Body.Len() {
		t.Errorf("gzipped map is %d bytes against %d raw", packed.Body.Len(), raw.Body.Len())
	}
	z, err := gzip.NewReader(bytes.NewReader(packed.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err := io.ReadAll(z)
	if err != nil || !bytes.Equal(unpacked, raw.Body.Bytes()) {
		t.Errorf("the gzipped map does not unpack to the raw file (err %v)", err)
	}
	if w := get("gzip;q=0, identity"); w.Header().Get("Content-Encoding") != "" {
		t.Errorf("gzip refused by q=0 was sent gzipped")
	}
}

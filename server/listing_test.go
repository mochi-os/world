// Mochi world: listing unit tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listing_test_conf points the loaded configuration at a throwaway file and
// restores the previous configuration afterwards.
func listing_test_conf(t *testing.T, content string) {
	t.Helper()
	previous := ini_file
	path := filepath.Join(t.TempDir(), "world.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ini_load(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ini_file = previous })
}

// The stable id survives restarts: minted once, read back identical.
func TestListingIDStable(t *testing.T) {
	dir := t.TempDir()
	listing_test_conf(t, "[world]\ndata = "+dir+"\n")
	first := listing_id()
	if len(first) != 32 {
		t.Fatalf("minted id %q", first)
	}
	second := listing_id()
	if second != first {
		t.Fatalf("id changed across reads: %q then %q", first, second)
	}
}

// Per-service sections override the inherited name and visibility; an
// unlisted service is absent from the payload entirely, not marked.
func TestListingServices(t *testing.T) {
	listing_test_conf(t, `[world]
name = Duc's World
public = true
[air]
name = Duc's Dogfight Den
[echo]
public = false
`)
	services := listing_services()
	if len(services) != 1 {
		t.Fatalf("expected air only, got %+v", services)
	}
	if services[0].Service != "air" || services[0].Name != "Duc's Dogfight Den" {
		t.Fatalf("air service wrong: %+v", services[0])
	}
}

// The payload carries the flight version — the number the join page judges
// compatibility by — and round-trips through JSON with services sorted, so
// unchanged state compares byte-equal for the change debounce.
func TestListingPayload(t *testing.T) {
	dir := t.TempDir()
	listing_test_conf(t, "[world]\ndata = "+dir+"\nname = Test World\npublic = true\n[lobby]\naddress = https://example.com:4433\n")
	one, err := listing_payload("abcdefghij0123456789abcdefghij01", "Test World")
	if err != nil {
		t.Fatal(err)
	}
	two, err := listing_payload("abcdefghij0123456789abcdefghij01", "Test World")
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) {
		t.Fatal("identical state produced different payloads — the change debounce would push every tick")
	}
	var parsed struct {
		World struct {
			Version int64  `json:"version"`
			Address string `json:"address"`
		} `json:"world"`
		Services []listing_service `json:"services"`
	}
	if err := json.Unmarshal(one, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.World.Version == 0 {
		t.Fatal("payload carries no flight version")
	}
	if parsed.World.Address == "" {
		t.Fatal("payload carries no advertised address")
	}
	// The listing advertises the base origin a player would paste into a join
	// page, not the WebTransport connect URL — a /play suffix breaks the
	// client's server-address handling.
	if strings.HasSuffix(parsed.World.Address, "/play") {
		t.Fatalf("advertised address %q carries the /play path", parsed.World.Address)
	}
}

// biased answers every bulk read with one repeated byte and every single-byte
// read (the rejection loop's) with zero, so the two mappings are told apart by
// the character they produce.
type biased struct{ value byte }

func (b biased) Read(p []byte) (int, error) {
	if len(p) == 1 {
		p[0] = 0
		return 1, nil
	}
	for i := range p {
		p[i] = b.value
	}
	return len(p), nil
}

// TestListingIDHasNoModuloBias — 256 is not a multiple of the 36-character
// alphabet, so a plain `raw[i] % 36` made 0-3 turn up 8/7 as often as the rest.
// Byte 253 is one of the four that has to be resampled: mapped it gives '1',
// resampled it gives '0'.
func TestListingIDHasNoModuloBias(t *testing.T) {
	previous := entropy
	entropy = biased{value: 253}
	t.Cleanup(func() { entropy = previous })

	listing_test_conf(t, "[world]\ndata = "+t.TempDir()+"\n")
	id := listing_id()
	if id != strings.Repeat("0", 32) {
		t.Errorf("id %q: byte 253 was mapped rather than resampled", id)
	}
}

// TestListingPublishesTheLobby: the listing carried the transport's address,
// and a join page fetches <address>/status from it - so a server whose lobby
// listened on another port was listed where nothing answers HTTP.
func TestListingPublishesTheLobby(t *testing.T) {
	t.Setenv("MOCHI_WORLD_PUBLIC", "true")
	t.Setenv("MOCHI_WORLD_NAME", "Test world")
	t.Setenv("MOCHI_TRANSPORT_ADDRESS", "https://game.example:4433")
	t.Setenv("MOCHI_TRANSPORT_PORT", "4433")
	t.Setenv("MOCHI_LOBBY_PORT", "4433")
	if got := lobby_origin(); got != "https://game.example:4433" {
		t.Errorf("one port for both: published %q, want the transport's address, which is the lobby's too", got)
	}
	t.Setenv("MOCHI_LOBBY_PORT", "443")
	if _, ok := listing_ready(); ok {
		t.Error("published a lobby on port 443 under the transport's address on 4433")
	}
	t.Setenv("MOCHI_LOBBY_ADDRESS", "https://lobby.example/")
	if _, ok := listing_ready(); !ok || lobby_origin() != "https://lobby.example" {
		t.Errorf("with [lobby] address set: ready %v, publishing %q; want it published", ok, lobby_origin())
	}
	payload, err := listing_payload(strings.Repeat("a", 32), "Test world")
	if err != nil || !strings.Contains(string(payload), `"address":"https://lobby.example"`) {
		t.Errorf("the payload carries no lobby address: %s", payload)
	}
}

// TestListingRefusesWhatCoreRefuses: the world server checked only that a name
// and an address were set; core refused anything longer than its bounds on
// every push, and the operator saw an unreachable server.
func TestListingRefusesWhatCoreRefuses(t *testing.T) {
	t.Setenv("MOCHI_WORLD_PUBLIC", "true")
	t.Setenv("MOCHI_LOBBY_ADDRESS", "https://lobby.example")
	for _, c := range []struct {
		what, key, value string
	}{
		{"a 65-character name", "MOCHI_WORLD_NAME", strings.Repeat("n", LISTING_NAME_MOST+1)},
		{"a name holding a bidi override", "MOCHI_WORLD_NAME", "Den\u202eden"},
		{"a game name too long", "MOCHI_AIR_NAME", strings.Repeat("g", LISTING_NAME_MOST+1)},
		{"an address that is not http or https", "MOCHI_LOBBY_ADDRESS", "ftp://lobby.example"},
		{"a 256-byte address", "MOCHI_LOBBY_ADDRESS", "https://" + strings.Repeat("a", LISTING_ADDRESS_MOST-7)},
	} {
		t.Run(c.what, func(t *testing.T) {
			t.Setenv("MOCHI_WORLD_NAME", "Fine")
			t.Setenv(c.key, c.value)
			if _, ok := listing_ready(); ok {
				t.Errorf("published with %s", c.what)
			}
		})
	}
	t.Setenv("MOCHI_WORLD_NAME", strings.Repeat("n", LISTING_NAME_MOST))
	if _, ok := listing_ready(); !ok {
		t.Error("refused a name of exactly the most core takes")
	}
}

// TestListingIdCoreRefusesIsReplaced: any 32 bytes were kept as the id, and
// core takes only 32 of [0-9a-z], so a hand-edited or damaged world.id refused
// every push for the life of the install.
func TestListingIdCoreRefusesIsReplaced(t *testing.T) {
	dir := t.TempDir()
	listing_test_conf(t, "[world]\ndata = "+dir+"\n")
	os.WriteFile(filepath.Join(dir, "world.id"), []byte(strings.Repeat("A", 32)+"\n"), 0644)
	if id := listing_id(); !match_listing_id.MatchString(id) {
		t.Errorf("kept the id %q, which core refuses", id)
	}
}

// refusing answers every request with a fixed status and body.
type refusing struct {
	status int
	body   string
}

func (r refusing) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: r.status, Status: fmt.Sprintf("%d Refused", r.status), Body: io.NopCloser(strings.NewReader(r.body)), Header: http.Header{}}, nil
}

type unreachable struct{}

func (unreachable) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connect: no such file or directory")
}

// TestListingRefusalIsNotAnOutage: core refusing the status was logged at
// debug level only, and the one line the operator saw said the Mochi server
// could not be reached. A refusal is said once, with the setting to check.
func TestListingRefusalIsNotAnOutage(t *testing.T) {
	listing_once.Do(func() {})
	previous := listing_client
	t.Cleanup(func() { listing_client = previous })
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	listing_client = &http.Client{Transport: refusing{400, `{"error":"invalid status","field":"name"}`}}
	var told listing_told
	for range 3 {
		if !told.note(listing_push([]byte("{}"))) {
			t.Fatal("a refusal was taken for an outage")
		}
	}
	if strings.Count(logged.String(), "WARN") != 1 || !strings.Contains(logged.String(), "[world] name") || strings.Contains(logged.String(), "cannot push") {
		t.Errorf("three refusals logged %q; want one warning naming [world] name and nothing about reaching the server", logged.String())
	}

	logged.Reset()
	listing_client = &http.Client{Transport: unreachable{}}
	if told.note(listing_push([]byte("{}"))) || !strings.Contains(logged.String(), "cannot push") {
		t.Errorf("an unreachable server logged %q", logged.String())
	}
	logged.Reset()
	listing_client = &http.Client{Transport: refusing{200, "{}"}}
	if !told.note(listing_push([]byte("{}"))) || !strings.Contains(logged.String(), "reachable again") {
		t.Errorf("a push taken after an outage logged %q", logged.String())
	}
}

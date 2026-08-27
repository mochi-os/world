// Mochi world: listing unit tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/json"
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
	ini_load(path)
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
	listing_test_conf(t, "[world]\ndata = "+dir+"\nname = Test World\npublic = true\n")
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

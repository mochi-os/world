// Mochi world: public listing - pushing this server's status to a co-located
// Mochi server for the network-wide join list (#14).
//
// A listed server pushes name, address, flight version and player counts over
// the Mochi server's local world socket; a private server never pushes. Listing
// is discovery, never capability. Pushes go on change (debounced) and on an
// idle floor that doubles as the liveness signal; a failed push is dropped, not
// queued.
//
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"world/games/air/flight"
)

const (
	listing_minimum = 60  // seconds between pushes: the change debounce
	listing_floor   = 600 // seconds between idle pushes: the liveness floor
)

// listing_service is one hosted game's slice of the pushed status.
type listing_service struct {
	Service string `json:"service"`
	Players int64  `json:"players"`
	Name    string `json:"name,omitempty"`
}

// listing_id returns this server's stable id, minting and persisting it on
// first use. The id is what makes a rename UPDATE the listing rather than
// create a second one, so it must survive restarts; it lives beside the
// ACME cache in the data directory.
func listing_id() string {
	directory := ini_string("world", "data", "/var/lib/mochi-world")
	path := filepath.Join(directory, "world.id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) == 32 {
			return id
		}
	}
	alphabet := "0123456789abcdefghijklmnopqrstuvwxyz"
	raw := make([]byte, 32)
	if _, err := entropy.Read(raw); err != nil {
		warn("listing: cannot generate id: %v", err)
		return ""
	}
	// Rejection sampling: 256 is not a multiple of 36, so a plain modulo would
	// make the first four characters of the alphabet likelier than the rest.
	limit := byte(256 - (256 % len(alphabet)))
	for i := range raw {
		for raw[i] >= limit {
			var one [1]byte
			if _, err := entropy.Read(one[:]); err != nil {
				warn("listing: cannot generate id: %v", err)
				return ""
			}
			raw[i] = one[0]
		}
		raw[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	id := string(raw)
	if err := os.MkdirAll(directory, 0755); err != nil {
		warn("listing: cannot create %s: %v", directory, err)
		return ""
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0644); err != nil {
		warn("listing: cannot persist id to %s: %v", path, err)
		return ""
	}
	return id
}

// listing_services builds the announced service list: every registered game
// whose per-service `public` (inheriting [world] public, which is true by
// the time this runs) is not switched off, with its optional per-service
// display name and its live player count.
func listing_services() []listing_service {
	out := []listing_service{}
	for name := range games {
		if !ini_bool(name, "public", true) {
			continue
		}
		out = append(out, listing_service{
			Service: name,
			Players: sessions_players(name),
			Name:    ini_string(name, "name", ""),
		})
	}
	return out
}

// listing_payload builds the full status push.
func listing_payload(id, name string) ([]byte, error) {
	services := listing_services()
	// Deterministic order, so unchanged state compares equal.
	for i := 0; i < len(services); i++ {
		for j := i + 1; j < len(services); j++ {
			if services[j].Service < services[i].Service {
				services[i], services[j] = services[j], services[i]
			}
		}
	}
	return json.Marshal(map[string]any{
		"world": map[string]any{
			"id":      id,
			"name":    name,
			"address": transport_origin(),
			// The flight model version is what decides whether a client can
			// fly here (the wasm and the server must agree), so it is the
			// compatibility number the join page judges by.
			"version": flight.Version,
		},
		"services": services,
	})
}

// listing_start launches the status pusher when this server is public.
// Called from main_serve; a private server returns before spawning anything.
func listing_start() {
	name, ok := listing_ready()
	if !ok {
		return
	}
	id := listing_id()
	if id == "" {
		return
	}
	go listing_push_manager(id, name)
}

// listing_ready reports whether this server may publish, and the name it
// publishes under. Both refusals are operator mistakes caught here rather than
// downstream: an unnamed listing would put an infrastructure name on a
// player-facing join page, and a server with no [transport] address would
// publish transport_origin's https://127.0.0.1:<port> fallback - right for a
// client on this machine, wrong for the address gossiped to every server on the
// network. Core accepts that one (world_address_valid passes any https URL) and
// republishes it, so nothing downstream catches it either.
func listing_ready() (string, bool) {
	if !ini_bool("world", "public", false) {
		return "", false
	}
	name := ini_string("world", "name", "")
	if name == "" {
		warn("listing: [world] public is true but no name is set - not publishing")
		return "", false
	}
	if ini_string("transport", "address", "") == "" {
		warn("listing: [world] public is true but no [transport] address is set - not publishing")
		return "", false
	}
	return name, true
}

// listing_push_manager pushes on change and on the idle floor, forever.
func listing_push_manager(id, name string) {
	var last []byte
	var pushed time.Time
	var down bool
	for ; ; time.Sleep(15 * time.Second) {
		payload, err := listing_payload(id, name)
		if err != nil {
			warn("listing: payload: %v", err)
			continue
		}
		changed := !bytes.Equal(payload, last)
		since := time.Since(pushed)
		if !(changed && since >= listing_minimum*time.Second) && since < listing_floor*time.Second {
			continue
		}
		if listing_push(payload) {
			if down {
				info("listing: Mochi server reachable again")
				down = false
			}
			last = payload
			pushed = time.Now()
		} else if !down {
			// One warning per outage, then silence: the game must not fill
			// its log because its listing endpoint is down.
			warn("listing: cannot push status to the Mochi server (will keep trying quietly)")
			down = true
		}
	}
}

// listing_client is built once, at the first push. A Transport per push
// allocated a connection pool nobody kept and could not age out (IdleConnTimeout
// is 0), and it only failed to strand connections because core answers with a
// body this side never reads, which makes net/http close the socket rather than
// pool it. A bodiless 2xx from core would have started leaking a connection and
// its goroutines per push, on both sides.
var (
	listing_client *http.Client
	listing_once   sync.Once
)

// listing_push delivers one status payload over the local socket. Latest
// state only: a failure is reported, never retried out of band.
func listing_push(payload []byte) bool {
	listing_once.Do(func() {
		listing_client = &http.Client{Timeout: 10 * time.Second, Transport: listing_transport()}
	})
	response, err := listing_client.Post("http://mochi/_/world/status", "application/json", bytes.NewReader(payload))
	if err != nil {
		debug("listing: push: %v", err)
		return false
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		debug("listing: push refused: %s", response.Status)
		return false
	}
	// Drained so the connection is reused rather than closed and redialled:
	// net/http only pools a connection whose body was read to EOF.
	_, _ = io.Copy(io.Discard, response.Body)
	return true
}

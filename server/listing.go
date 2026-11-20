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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
		// Core takes only 32 of [0-9a-z], and refuses every push carrying
		// anything else, so a file holding another id is replaced.
		id := strings.TrimSpace(string(data))
		if match_listing_id.MatchString(id) {
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

var match_listing_id = regexp.MustCompile(`^[0-9a-z]{32}$`)

// LISTING_NAME_MOST and LISTING_ADDRESS_MOST are core's bounds on a listing's
// names and its address (world_name_most, world_address_most): core refuses a
// push past them, so a server is told here instead of never being listed.
const (
	LISTING_NAME_MOST    = 64
	LISTING_ADDRESS_MOST = 255
)

// listing_name_valid is core's test of a display name: no control or format
// character - the bidi overrides and zero-width joiners among them - and no
// more than LISTING_NAME_MOST runes.
func listing_name_valid(name string) bool {
	for _, character := range name {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return false
		}
	}
	return utf8.RuneCountInString(name) <= LISTING_NAME_MOST
}

// listing_address_valid is whether core will take an address: an http or https
// URL of no more than LISTING_ADDRESS_MOST bytes.
func listing_address_valid(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && len(address) <= LISTING_ADDRESS_MOST && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
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
			"address": lobby_origin(),
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
// publishes under. Each refusal is an operator mistake caught here, naming the
// setting, rather than downstream: an unnamed listing would put an
// infrastructure name on a player-facing join page; a server with no address
// set has nothing to publish that reaches it from elsewhere; and a name or
// address past core's bounds is refused by core on every push, which the
// operator would otherwise see only as a listing that never appears.
func listing_ready() (string, bool) {
	if !ini_bool("world", "public", false) {
		return "", false
	}
	name := ini_string("world", "name", "")
	if name == "" {
		warn("listing: [world] public is true but no name is set - not publishing")
		return "", false
	}
	if !listing_name_valid(name) {
		warn("listing: [world] name is longer than %d characters or holds a control or format character - not publishing", LISTING_NAME_MOST)
		return "", false
	}
	for game := range games {
		if label := ini_string(game, "name", ""); label != "" && !listing_name_valid(label) {
			warn("listing: [%s] name is longer than %d characters or holds a control or format character - not publishing", game, LISTING_NAME_MOST)
			return "", false
		}
	}
	address := lobby_origin()
	if address == "" {
		if ini_string("transport", "address", "") != "" {
			warn("listing: [lobby] port differs from [transport] port, so [lobby] address must be set - not publishing")
		} else {
			warn("listing: [world] public is true but no [transport] address is set - not publishing")
		}
		return "", false
	}
	if !listing_address_valid(address) {
		warn("listing: the lobby address %q is not an http or https URL of at most %d bytes - check [lobby] address and [transport] address - not publishing", address, LISTING_ADDRESS_MOST)
		return "", false
	}
	return name, true
}

// listing_push_manager pushes on change and on the idle floor, forever.
func listing_push_manager(id, name string) {
	var last []byte
	var pushed time.Time
	var told listing_told
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
		if told.note(listing_push(payload)) {
			// Taken, or refused - which waits for a change or the floor as a
			// push does, since sending the same refused status again changes
			// nothing.
			last = payload
			pushed = time.Now()
		}
	}
}

// listing_told is what the push manager has said to the operator, so each
// outage and each distinct refusal is said once.
type listing_told struct {
	down    bool
	refusal string
}

// note logs one push's outcome if the operator has not been told it yet, and
// reports whether core answered - took the status or refused it - rather than
// being out of reach.
func (t *listing_told) note(err error) bool {
	var refused *listing_refusal
	switch {
	case err == nil:
		if t.down {
			info("listing: Mochi server reachable again")
		}
		t.down, t.refusal = false, ""
		return true
	case errors.As(err, &refused):
		// Core is there and said no: a setting to change, not an outage.
		if refused.Error() != t.refusal {
			warn("listing: %v", refused)
			t.refusal = refused.Error()
		}
		t.down = false
		return true
	default:
		if !t.down {
			// One warning per outage, then silence: the game must not fill
			// its log because its listing endpoint is down.
			warn("listing: cannot push status to the Mochi server (will keep trying quietly)")
			t.down = true
		}
		return false
	}
}

// listing_refusal is core refusing a push it was able to read.
type listing_refusal struct {
	status string // the HTTP status line
	reason string // core's error
	field  string // the field core named, "" from a core that names none
}

// listing_settings is the setting behind each field core may name.
var listing_settings = map[string]string{
	"id":       "the listing id in world.id under [world] data",
	"name":     "[world] name",
	"address":  "[lobby] address or [transport] address",
	"label":    "a game's own name ([<game>] name)",
	"service":  "the games this server hosts",
	"services": "the games this server hosts",
}

func (r *listing_refusal) Error() string {
	message := "the Mochi server refused the status: " + r.status
	if r.reason != "" {
		message += " " + r.reason
	}
	if setting, found := listing_settings[r.field]; found {
		return message + " - check " + setting
	}
	return message + " - check [world] name, [lobby] address, [transport] address and each game's name"
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
// state only: a failure is reported, never retried out of band. Core refusing
// the status is a *listing_refusal; any other error is core out of reach.
func listing_push(payload []byte) error {
	listing_once.Do(func() {
		listing_client = &http.Client{Timeout: 10 * time.Second, Transport: listing_transport()}
	})
	response, err := listing_client.Post("http://mochi/_/world/status", "application/json", bytes.NewReader(payload))
	if err != nil {
		debug("listing: push: %v", err)
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 == 4 {
		var body struct {
			Error string `json:"error"`
			Field string `json:"field"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
		return &listing_refusal{status: response.Status, reason: body.Error, field: body.Field}
	}
	if response.StatusCode/100 != 2 {
		debug("listing: push failed: %s", response.Status)
		return fmt.Errorf("push: %s", response.Status)
	}
	// Drained so the connection is reused rather than closed and redialled:
	// net/http only pools a connection whose body was read to EOF.
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

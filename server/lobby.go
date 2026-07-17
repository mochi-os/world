// Mochi world: Lobby API (public HTTP: status, session list and create)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// lobby_start serves the public lobby API. World servers are open: there is
// no authentication, only rate and capacity limits.
func lobby_start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", lobby_status)
	mux.HandleFunc("/sessions", lobby_sessions)
	mux.HandleFunc("/chat", lobby_chat)
	address := fmt.Sprintf("%s:%d", ini_string("lobby", "listen", ""), ini_int("lobby", "port", 4433))
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	info("lobby listening on %s", address)
	var err error
	if certificate_file != "" {
		err = server.ListenAndServeTLS(certificate_file, key_file)
	} else {
		err = server.ListenAndServe()
	}
	warn("lobby: %v", err)
}

// lobby_cors marks every lobby response as callable from any origin — the
// API is public by design. Returns true when the request was a preflight.
func lobby_cors(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

func lobby_respond(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func lobby_status(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	count, players := sessions_counts()
	names := []string{}
	for name := range games {
		names = append(names, name)
	}
	body := map[string]any{
		"name":     ini_string("world", "name", "Mochi world"),
		"version":  build_version,
		"protocol": protocol,
		"started":  started.Unix(),
		"games":    names,
		"sessions": count,
		"players":  players,
		"address":  transport_address(),
	}
	if hash, expires := certificate_hash(); hash != "" {
		body["certificate"] = map[string]any{"hash": hash, "expires": expires}
	}
	lobby_respond(w, http.StatusOK, body)
}

func lobby_sessions(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		lobby_respond(w, http.StatusOK, map[string]any{"sessions": sessions_list(r.URL.Query().Get("game"))})
	case http.MethodPost:
		if !lobby_allow(r) {
			lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
			return
		}
		var request struct {
			Game       string         `json:"game"`
			Mode       string         `json:"mode"`
			Label      string         `json:"label"`
			Name       string         `json:"name"`
			Capacity   int            `json:"capacity"`
			Parameters map[string]any `json:"parameters"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&request); err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "request"})
			return
		}
		if len(request.Label) > 64 {
			request.Label = request.Label[:64]
		}
		s, err := sessions_create(request.Game, request.Mode, request.Label, request.Capacity, request.Parameters)
		if err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if name := clean(request.Name, 32); name != "" {
			chat_made(name, s.spec.Label) // the lobby's system line: who just made what (#84)
		}
		body := map[string]any{
			"session": s.identifier, "game": s.spec.Game, "mode": s.spec.Mode,
			"label": s.spec.Label, "capacity": s.spec.Capacity, "address": transport_address(),
		}
		if hash, expires := certificate_hash(); hash != "" {
			body["certificate"] = map[string]any{"hash": hash, "expires": expires}
		}
		lobby_respond(w, http.StatusOK, body)
	default:
		lobby_respond(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
	}
}

// The server-wide lobby chat (#84): one ring of recent lines for players
// browsing the match list — where "what shall we fly?" happens. Plain HTTP
// polling beside the other lobby calls: the audience has no game connection
// yet. Lines are either player chat ({name, text}) or STRUCTURED system
// events ({event, name, label}) the client renders in its own language —
// baked English sentences would defeat every locale.
var (
	chat_lines    []map[string]any
	chat_sequence uint64
	chat_lock     sync.Mutex
)

// chat_append stores one line under the next sequence number.
func chat_append(line map[string]any) {
	chat_lock.Lock()
	defer chat_lock.Unlock()
	chat_sequence++
	line["sequence"] = chat_sequence
	line["time"] = time.Now().Unix()
	chat_lines = append(chat_lines, line)
	if len(chat_lines) > 100 {
		chat_lines = chat_lines[len(chat_lines)-100:]
	}
}

// chat_made records the match-creation system event.
func chat_made(name string, label string) {
	chat_append(map[string]any{"event": "made", "name": name, "label": label})
}

func lobby_chat(w http.ResponseWriter, r *http.Request) {
	if lobby_cors(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		since := uint64(0)
		fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
		chat_lock.Lock()
		lines := []map[string]any{}
		for _, line := range chat_lines {
			if line["sequence"].(uint64) > since {
				lines = append(lines, line)
			}
		}
		latest := chat_sequence
		chat_lock.Unlock()
		lobby_respond(w, http.StatusOK, map[string]any{"lines": lines, "sequence": latest})
	case http.MethodPost:
		if !lobby_voice(r) {
			lobby_respond(w, http.StatusTooManyRequests, map[string]any{"error": "rate"})
			return
		}
		var request struct {
			Name string `json:"name"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "request"})
			return
		}
		name := clean(request.Name, 32)
		if name == "" {
			name = "pilot"
		}
		words := clean(request.Text, 200)
		if words == "" {
			lobby_respond(w, http.StatusBadRequest, map[string]any{"error": "empty"})
			return
		}
		chat_append(map[string]any{"name": name, "text": words})
		lobby_respond(w, http.StatusOK, map[string]any{"sent": true})
	default:
		lobby_respond(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
	}
}

// clean strips control characters and caps a user string at limit runes.
func clean(words string, limit int) string {
	words = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, words)
	words = strings.TrimSpace(words)
	if runes := []rune(words); len(runes) > limit {
		words = string(runes[:limit])
	}
	return words
}

// lobby_allow rate-limits session creation per client address.
var (
	creates      = map[string][]time.Time{}
	creates_lock sync.Mutex
)

func lobby_allow(r *http.Request) bool {
	return lobby_permit(creates, r, ini_int("limits", "creates", 10))
}

// lobby_voice rate-limits lobby chat per client address — its own budget, so
// conversation never spends the match-creation allowance (#84).
var says = map[string][]time.Time{}

func lobby_voice(r *http.Request) bool {
	return lobby_permit(says, r, ini_int("limits", "chats", 20))
}

// lobby_permit is the shared per-host sliding-minute limiter.
func lobby_permit(table map[string][]time.Time, r *http.Request, limit int) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	creates_lock.Lock()
	defer creates_lock.Unlock()
	recent := []time.Time{}
	for _, t := range table[host] {
		if time.Since(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		table[host] = recent
		return false
	}
	table[host] = append(recent, time.Now())
	if len(table) > 10000 { // prune the table itself under address churn
		for h, list := range table {
			if len(list) == 0 || time.Since(list[len(list)-1]) > time.Minute {
				delete(table, h)
			}
		}
	}
	return true
}

// transport_address is the WebTransport URL advertised to clients.
func transport_address() string {
	address := ini_string("transport", "address", "")
	if address == "" {
		address = fmt.Sprintf("https://127.0.0.1:%d", ini_int("transport", "port", 4433))
	}
	return strings.TrimSuffix(address, "/") + "/play"
}

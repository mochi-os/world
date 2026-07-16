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

// lobby_allow rate-limits session creation per client address.
var (
	creates      = map[string][]time.Time{}
	creates_lock sync.Mutex
)

func lobby_allow(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	limit := ini_int("limits", "creates", 10)
	creates_lock.Lock()
	defer creates_lock.Unlock()
	recent := []time.Time{}
	for _, t := range creates[host] {
		if time.Since(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		creates[host] = recent
		return false
	}
	creates[host] = append(recent, time.Now())
	if len(creates) > 10000 { // prune the table itself under address churn
		for h, list := range creates {
			if len(list) == 0 || time.Since(list[len(list)-1]) > time.Minute {
				delete(creates, h)
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

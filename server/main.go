// Mochi world: Main
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// mochi-world is a standalone realtime game server for the Mochi ecosystem:
// many simultaneous sessions, each with many players, over WebTransport.
// It is crash-only — sessions live in memory, nothing durable is stored —
// and open: anyone may run one, players choose which server to play on, and
// no Mochi server authentication is involved. Durable concerns (identity,
// settings, match history, assets) belong to Mochi apps on Mochi servers.

package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"world/games/air"
	"world/games/echo"
)

var (
	build_version = "dev"
	started       = time.Now()
	shutdown      = make(chan struct{})    // closed once at exit; session loops watch it
	stopping      = make(chan struct{}, 1) // pushed by the Windows SCM handler to request shutdown
)

func main() {
	windows_service_redirect_logs()
	if windows_service_run() {
		return
	}
	os.Exit(main_serve(nil))
}

// main_serve runs the server until an OS signal or a service stop request
// arrives. ready, when non-nil, is called once serving begins (the Windows
// SCM watches it); the return value is the process exit code.
func main_serve(ready func()) int {
	path := flag.String("f", "/etc/mochi/world.conf", "configuration file")
	flag.Parse()
	ini_load(*path)
	log_debug = ini_bool("log", "debug", false)
	info("mochi-world %s starting", build_version)

	games_register(echo.New())
	games_register(air.New())

	sessions_standing()
	if err := certificate_start(); err != nil {
		warn("startup: %v", err)
		return 1
	}
	// Both listeners bind synchronously and report a terminal serve error to
	// fatal. A bind failure (taken port, bad address, unreadable cert) is a
	// non-zero exit so systemd restarts, rather than a live-but-deaf process
	// (#175). The channel is buffered so a serve goroutine dying during
	// shutdown never blocks on a main that has already stopped reading.
	fatal := make(chan error, 2)
	if err := lobby_start(fatal); err != nil {
		warn("startup: %v", err)
		return 1
	}
	if err := transport_start(fatal); err != nil {
		warn("startup: %v", err)
		return 1
	}
	if ready != nil {
		ready()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	code := 0
	select {
	case <-signals:
	case <-stopping:
	case err := <-fatal:
		warn("listener exited: %v", err) // a required listener died under us
		code = 1
	}
	info("shutting down")
	close(shutdown)
	time.Sleep(500 * time.Millisecond) // one beat for session loops to notify players
	return code
}

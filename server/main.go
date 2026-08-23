// Mochi world: Main
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// mochi-world is a standalone realtime game server: many simultaneous sessions
// over WebTransport, crash-only (sessions live in memory, nothing is stored)
// and open (no Mochi authentication). Durable concerns belong to Mochi apps.

package main

import (
	"flag"
	"os"
	"os/signal"
	stack "runtime/debug" // aliased: this package already has a debug() logger
	"syscall"
	"time"

	"world/games/air"
)

var (
	build_version = "dev"
	started       = time.Now()
	shutdown      = make(chan struct{})    // closed once at exit; session loops watch it
	stopping      = make(chan struct{}, 1) // pushed by the Windows SCM handler to request shutdown
)

// guard runs f and turns a panic into a logged fault instead of a dead process.
// recover() sees only panics on its OWN goroutine, so every `go` reachable from
// client input needs its own guard. after runs during recovery, itself guarded.
func guard(name string, after func(), f func()) {
	defer func() {
		fault := recover()
		if fault == nil {
			return
		}
		warn("panic in %s: %v\n%s", name, fault, stack.Stack())
		if after != nil {
			defer func() { _ = recover() }()
			after()
		}
	}()
	f()
}

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

	games_register(air.New())

	sessions_standing()
	go sessions_stale_manager()
	if err := certificate_start(); err != nil {
		warn("startup: %v", err)
		return 1
	}
	// Both listeners bind synchronously; a bind failure is a non-zero exit so
	// systemd restarts rather than leaving a live-but-deaf process (#175). The
	// channel is buffered so a serve goroutine dying during shutdown never blocks.
	fatal := make(chan error, 2)
	if err := lobby_start(fatal); err != nil {
		warn("startup: %v", err)
		return 1
	}
	if err := transport_start(fatal); err != nil {
		warn("startup: %v", err)
		return 1
	}
	listing_start()
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

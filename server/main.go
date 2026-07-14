// Mochi world: Main
// Copyright © 2026 Mochi OÜ
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

	"world/games/echo"
	"world/games/air"
)

var (
	build_version = "dev"
	started       = time.Now()
	shutdown      = make(chan struct{}) // closed once at exit; session loops watch it
)

func main() {
	path := flag.String("f", "/etc/mochi/world.conf", "configuration file")
	flag.Parse()
	ini_load(*path)
	log_debug = ini_bool("log", "debug", false)
	info("mochi-world %s starting", build_version)

	games_register(echo.New())
	games_register(air.New())

	sessions_standing()
	certificate_start()
	go lobby_start()
	go transport_start()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
	info("shutting down")
	close(shutdown)
	time.Sleep(500 * time.Millisecond) // one beat for session loops to notify players
}

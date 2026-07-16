// Mochi world: Logging
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"fmt"
	"log"
	"time"
)

type log_writer struct{}

func (w *log_writer) Write(bytes []byte) (int, error) {
	return fmt.Printf("%s %s", time.Now().Format("2006-01-02 15:04:05.000000"), string(bytes))
}

func init() {
	log.SetFlags(0)
	log.SetOutput(new(log_writer))
}

var log_debug = false

func debug(message string, values ...any) {
	if !log_debug {
		return
	}
	out := fmt.Sprintf(message, values...)
	if len(out) > 1000 {
		out = out[:1000] + "..."
	}
	log.Print(out + "\n")
}

func info(message string, values ...any) {
	log.Printf(message+"\n", values...)
}

func warn(message string, values ...any) {
	log.Printf("WARN "+message+"\n", values...)
}

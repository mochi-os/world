// Mochi world: Unix listing transport — dials the co-located Mochi server's
// world socket. The socket's group permission (mochi-world) is the entire
// credential; there is no TLS and no token on a same-machine channel the OS
// already gates.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build !windows

package main

import (
	"context"
	"net"
	"net/http"
)

// listing_socket returns the Mochi server's world socket path: the standard
// native data directory unless [mochi] socket points somewhere else (a
// relocated data dir, a second instance).
func listing_socket() string {
	return ini_string("mochi", "socket", "/var/lib/mochi/run/world.sock")
}

// listing_transport dials the world UDS whatever host the URL names.
func listing_transport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", listing_socket())
		},
	}
}

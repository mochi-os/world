// Mochi world: Windows listing transport - dials the co-located Mochi server's
// world pipe. The pipe's security descriptor is the credential and admits
// LocalSystem and Administrators only, so a listed server must run under one of
// those.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

//go:build windows

package main

import (
	"context"
	"net"
	"net/http"

	"github.com/Microsoft/go-winio"
)

// listing_socket returns the Mochi server's world pipe name, overridable as
// [mochi] socket for unusual layouts.
func listing_socket() string {
	return ini_string("mochi", "socket", `\\.\pipe\mochi-world`)
}

// listing_transport dials the world pipe whatever host the URL names.
func listing_transport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, listing_socket())
		},
	}
}

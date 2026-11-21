// Mochi world: where the server keeps its state off Windows.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build !windows

package main

// state_directory is the default for [world] data and [acme] cache: the
// stable listing id, the ACME account key and the certificates.
func state_directory() string {
	return "/var/lib/mochi-world"
}

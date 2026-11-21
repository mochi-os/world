// Mochi world: where the server keeps its state on Windows.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// state_directory is the default for [world] data and [acme] cache: the
// directory the installer makes for them, under ProgramData. The Unix path
// would resolve against the service's current drive to C:\var\lib\mochi-world,
// a folder that inherits the drive root's access list, which lets any local
// account change it, and with it the certificate private keys.
func state_directory() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "Mochi", "world")
}

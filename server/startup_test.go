// Mochi world: startup failure handling (#175, #179)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TestLobbyBindFatal: a taken port must make lobby_start return an error
// (fatal at startup) rather than swallow it into a background warn (#175).
func TestLobbyBindFatal(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	_, port, _ := net.SplitHostPort(blocker.Addr().String())

	os.Setenv("MOCHI_LOBBY_LISTEN", "127.0.0.1")
	os.Setenv("MOCHI_LOBBY_PORT", port)
	defer os.Unsetenv("MOCHI_LOBBY_LISTEN")
	defer os.Unsetenv("MOCHI_LOBBY_PORT")

	fatal := make(chan error, 1)
	if err := lobby_start(fatal); err == nil {
		t.Fatal("lobby_start accepted a taken port instead of failing")
	}
}

// TestOperatorMissingKey: operator TLS mode selected on a certificate path
// whose key is absent must fail at startup, not on the first handshake (#179).
func TestOperatorMissingKey(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	// A present (if bogus) certificate file, and a key path that does not exist.
	if err := os.WriteFile(cert, []byte("-----BEGIN CERTIFICATE-----\nnot a real cert\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Setenv("MOCHI_TLS_CERTIFICATE", cert)
	os.Setenv("MOCHI_TLS_KEY", filepath.Join(dir, "absent.key"))
	defer os.Unsetenv("MOCHI_TLS_CERTIFICATE")
	defer os.Unsetenv("MOCHI_TLS_KEY")
	// Reset the package state the previous certificate_start left behind.
	certificate_file, key_file, operator = "", "", nil

	if err := certificate_start(); err == nil {
		t.Fatal("certificate_start accepted a missing key instead of failing")
	}
	// Leave the operator-mode globals cleared so later tests aren't affected.
	certificate_file, key_file, operator = "", "", nil
}

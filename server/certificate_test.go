// Mochi world: Certificate tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// certificate_write creates a self-signed pair with the given serial.
func certificate_write(t *testing.T, certificate string, key string, serial int64) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &private.PublicKey, private)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	marshalled, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: marshalled}), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// certificate_serial reads the served leaf's serial number.
func certificate_serial(t *testing.T, pair *tls.Certificate) int64 {
	t.Helper()
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return leaf.SerialNumber.Int64()
}

// TestCertificateReload: file mode serves the [tls] pair and picks up an
// in-place rotation (new mtime) without a restart — the mechanism that lets
// an external ACME renewal apply live.
func TestCertificateReload(t *testing.T) {
	directory := t.TempDir()
	certificate := filepath.Join(directory, "certificate.pem")
	key := filepath.Join(directory, "key.pem")
	certificate_write(t, certificate, key, 1)

	held_file, held_key := certificate_file, key_file
	held_pair, held_time := operator, operator_time
	defer func() {
		certificate_file, key_file = held_file, held_key
		operator, operator_time = held_pair, held_time
	}()
	certificate_file, key_file = certificate, key
	operator, operator_time = nil, time.Time{}

	pair, err := certificate_get(nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if serial := certificate_serial(t, pair); serial != 1 {
		t.Fatalf("serial %d, want 1", serial)
	}

	certificate_write(t, certificate, key, 2)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(certificate, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	pair, err = certificate_get(nil)
	if err != nil {
		t.Fatalf("get after rotation: %v", err)
	}
	if serial := certificate_serial(t, pair); serial != 2 {
		t.Fatalf("serial %d after rotation, want 2", serial)
	}
}

// TestResponderDeadlines pins the request deadlines on the ACME responder, a
// public listener with no rate limiter. It asserts configuration, not
// behaviour: what it catches is a listener losing one of the four.
func TestResponderDeadlines(t *testing.T) {
	responder := certificate_responder(nil)

	deadlines := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadHeaderTimeout", responder.ReadHeaderTimeout, TIMEOUT_HEADER},
		{"ReadTimeout", responder.ReadTimeout, TIMEOUT_READ},
		{"WriteTimeout", responder.WriteTimeout, TIMEOUT_WRITE},
		{"IdleTimeout", responder.IdleTimeout, TIMEOUT_IDLE},
	}
	for _, deadline := range deadlines {
		if deadline.got == 0 {
			t.Errorf("%s is unset: the responder is a public listener and every deadline it drops is one a slow client can hold open", deadline.name)
			continue
		}
		if deadline.got != deadline.want {
			t.Errorf("%s = %v, want %v (the lobby's value; both listeners share these constants)", deadline.name, deadline.got, deadline.want)
		}
	}

	if responder.Addr != ":80" {
		t.Errorf("Addr = %q, want :80 — ACME HTTP-01 validates on port 80 only", responder.Addr)
	}
}

// The deadlines bound how long ONE connection holds a goroutine, not how many
// exist; listener_limit is what closes socket exhaustion. This asserts
// behaviour: a wrapper returning its argument unchanged would satisfy a
// structural check.
func TestListenerLimit(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	limited := listener_limit(raw, 1)
	defer limited.Close()

	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			connection, err := limited.Accept()
			if err != nil {
				return
			}
			accepted <- connection
		}
	}()

	// Dialling always succeeds — the kernel completes the handshake into the
	// accept queue regardless of the cap. What the cap governs is whether
	// Accept hands the connection to the server, so that is what is measured.
	first, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	defer first.Close()

	var held net.Conn
	select {
	case held = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the first connection was never accepted, so the cap is not the thing being measured")
	}

	second, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}
	defer second.Close()

	select {
	case <-accepted:
		t.Fatal("a second connection was accepted while the cap of 1 was already held: the listener is not limited")
	case <-time.After(250 * time.Millisecond):
	}

	// Releasing the held slot must free the waiting connection, otherwise the
	// cap is a permanent ceiling rather than a concurrency limit.
	held.Close()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the second connection was not accepted after the first closed: the cap never releases")
	}
}

// broken is an entropy source that always fails, standing in for a machine
// whose getrandom(2) is unavailable.
type broken struct{}

func (broken) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

// TestEphemeralGenerationFailureIsFatal — certificate_generate warned and
// returned, leaving `ephemeral` nil; certificate_get then answered (nil, nil)
// and BOTH listeners failed every handshake while the process stayed up and
// looked healthy. That is the failure mode #175 and #179 removed from the bind
// and operator paths, left in place on the third.
func TestEphemeralGenerationFailureIsFatal(t *testing.T) {
	certificate_file, key_file, operator = "", "", nil
	previous := entropy
	entropy = broken{}
	t.Cleanup(func() {
		entropy = previous
		certificate_file, key_file, operator = "", "", nil
	})

	if err := certificate_generate(); err == nil {
		t.Error("certificate_generate reported success with no entropy")
	}
	if err := certificate_start(); err == nil {
		t.Error("certificate_start came up deaf instead of failing")
	}

	// The control: with entropy restored the same call succeeds and publishes
	// a pair, so the test above is not merely asserting that startup is broken.
	entropy = previous
	if err := certificate_generate(); err != nil {
		t.Fatalf("certificate_generate failed with working entropy: %v", err)
	}
	ephemeral_lock.RLock()
	pair := ephemeral
	ephemeral_lock.RUnlock()
	if pair == nil {
		t.Error("no ephemeral pair published after a successful generation")
	}
}

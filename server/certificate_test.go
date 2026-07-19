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
	"math/big"
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

// Mochi world: TLS certificates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// QUIC mandates TLS even on localhost. With [tls] certificate/key set, the
// operator's certificate serves both the lobby and the transport, and clients
// validate normally. Without it, an ephemeral self-signed ECDSA P-256
// certificate is generated: 12 days validity (the WebTransport
// serverCertificateHashes rules cap acceptable certificates at 14), rotated
// at day 10, its SHA-256 advertised through the lobby so clients can pin it.

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/url"
	"sync"
	"time"
)

var (
	certificate_file string // [tls] certificate — set when operator-provided
	key_file         string // [tls] key

	ephemeral         *tls.Certificate
	ephemeral_hash    string
	ephemeral_expires time.Time
	ephemeral_lock    sync.RWMutex
)

func certificate_start() {
	certificate_file = ini_string("tls", "certificate", "")
	key_file = ini_string("tls", "key", "")
	if certificate_file != "" {
		return
	}
	certificate_generate()
	go certificate_manager()
}

// certificate_tls returns the TLS configuration for the QUIC listener.
func certificate_tls() (*tls.Config, error) {
	if certificate_file != "" {
		pair, err := tls.LoadX509KeyPair(certificate_file, key_file)
		if err != nil {
			return nil, err
		}
		return &tls.Config{Certificates: []tls.Certificate{pair}}, nil
	}
	return &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			ephemeral_lock.RLock()
			defer ephemeral_lock.RUnlock()
			return ephemeral, nil
		},
	}, nil
}

// certificate_hash returns the base64 SHA-256 of the active ephemeral
// certificate for WebTransport pinning, or "" when an operator certificate
// is in use (normal chain validation applies).
func certificate_hash() (string, int64) {
	if certificate_file != "" {
		return "", 0
	}
	ephemeral_lock.RLock()
	defer ephemeral_lock.RUnlock()
	return ephemeral_hash, ephemeral_expires.Unix()
}

func certificate_generate() {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		warn("certificate: %v", err)
		return
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	names := []string{"localhost"}
	addresses := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if u, err := url.Parse(ini_string("transport", "address", "")); err == nil && u != nil && u.Hostname() != "" {
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			addresses = append(addresses, ip)
		} else {
			names = append(names, u.Hostname())
		}
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "mochi-world"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(12 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
		IPAddresses:  addresses,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		warn("certificate: %v", err)
		return
	}
	sum := sha256.Sum256(der)
	pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ephemeral_lock.Lock()
	ephemeral = &pair
	ephemeral_hash = base64.StdEncoding.EncodeToString(sum[:])
	ephemeral_expires = template.NotAfter
	ephemeral_lock.Unlock()
	info("certificate: ephemeral, sha-256 %s, expires %s", ephemeral_hash, template.NotAfter.Format("2006-01-02"))
}

// certificate_manager rotates the ephemeral certificate before expiry. New
// joins fetch the fresh hash from the lobby; established QUIC connections
// keep their completed handshake.
func certificate_manager() {
	for range time.Tick(time.Hour) {
		ephemeral_lock.RLock()
		remaining := time.Until(ephemeral_expires)
		ephemeral_lock.RUnlock()
		if remaining < 2*24*time.Hour {
			certificate_generate()
		}
	}
}

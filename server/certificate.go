// Mochi world: TLS certificates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// QUIC mandates TLS even on localhost. Three modes, in order of precedence:
//
//  1. [tls] certificate/key - operator files, reloaded when the certificate's
// modification time changes, so an external renewal needs no restart.
//  2. [acme] hosts - built-in Let's Encrypt, cached under [acme] cache;
// HTTP-01 validation, so port 80 must be reachable.
//  3. Neither - an ephemeral self-signed certificate, 12 days validity (the
// WebTransport serverCertificateHashes cap is 14), rotated at day 10, its
// SHA-256 advertised through the lobby for clients to pin.
//
// Mode 3 is encrypted but unauthenticated: the pin rides the plaintext lobby,
// so an on-path attacker can substitute both. It is for LAN / no-DNS / dev;
// public deployments use [tls] or [acme].

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
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

var (
	certificate_file string // [tls] certificate — set when operator-provided
	key_file         string // [tls] key

	acme_manager *autocert.Manager // non-nil in [acme] mode

	operator      *tls.Certificate // file mode: the loaded pair
	operator_time time.Time        // certificate file's mtime at load
	operator_lock sync.RWMutex

	ephemeral         *tls.Certificate
	ephemeral_hash    string
	ephemeral_expires time.Time
	ephemeral_lock    sync.RWMutex
)

func certificate_start() error {
	certificate_file = ini_string("tls", "certificate", "")
	key_file = ini_string("tls", "key", "")
	if certificate_file != "" {
		// Validate the operator pair NOW, not on the first handshake (#179):
		// a missing or unreadable key was selected into TLS mode purely on the
		// certificate path being set, so the process came up and then failed
		// every handshake. This eager load also warms the reload cache.
		if _, err := certificate_operator(); err != nil {
			return fmt.Errorf("operator certificate: %w", err)
		}
		return nil // reloaded on change per handshake in certificate_get
	}
	if hosts := ini_string("acme", "hosts", ""); hosts != "" {
		names := strings.Split(hosts, ",")
		for i := range names {
			names[i] = strings.TrimSpace(names[i])
		}
		acme_manager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(names...),
			Cache:      autocert.DirCache(ini_string("acme", "cache", "/var/lib/mochi-world")),
		}
		// The HTTP-01 responder: ACME validates on port 80 only. A taken port
		// is a warning, not fatal — cached certificates keep serving, though
		// renewals will fail until it frees up.
		go func() {
			// Bound here rather than through ListenAndServe so the accepted
			// connections can be capped. This one answers strangers on port 80
			// with no rate limiter in front of it, so it is the listener that
			// least tolerates an unbounded connection count.
			listener, err := net.Listen("tcp", ":80")
			if err != nil {
				warn("acme responder: %v", err)
				return
			}
			warn("acme responder: %v", certificate_responder(acme_manager.HTTPHandler(nil)).
				Serve(listener_limit(listener, CONNECTIONS_MAXIMUM)))
		}()
		info("certificate: acme, hosts %s", strings.Join(names, " "))
		return nil
	}
	certificate_generate()
	go certificate_manager()
	return nil
}

// certificate_responder builds the HTTP-01 validation listener. It carries the
// lobby's deadlines: it answers strangers on port 80 with no rate limiter in
// front of it. Split out from certificate_start so a test can assert them.
func certificate_responder(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":80",
		Handler:           handler,
		ReadHeaderTimeout: TIMEOUT_HEADER,
		ReadTimeout:       TIMEOUT_READ,
		WriteTimeout:      TIMEOUT_WRITE,
		IdleTimeout:       TIMEOUT_IDLE,
	}
}

// certificate_tls returns the TLS configuration for the QUIC and lobby
// listeners. Every mode resolves the certificate per handshake through
// certificate_get, so renewals and rotations never need a restart.
func certificate_tls() (*tls.Config, error) {
	return &tls.Config{GetCertificate: certificate_get}, nil
}

// certificate_get resolves the certificate for one handshake.
func certificate_get(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if certificate_file != "" {
		return certificate_operator()
	}
	if acme_manager != nil {
		return acme_manager.GetCertificate(hello)
	}
	ephemeral_lock.RLock()
	defer ephemeral_lock.RUnlock()
	return ephemeral, nil
}

// certificate_operator returns the [tls] pair, reloading it when the
// certificate file changes on disk — a stat per handshake is cheap, and an
// in-place renewal (mochi-server's ACME cache, certbot, ...) then applies
// without a restart. A pair that fails to parse keeps the previous one.
func certificate_operator() (*tls.Certificate, error) {
	var when time.Time
	if status, err := os.Stat(certificate_file); err == nil {
		when = status.ModTime()
	}
	operator_lock.RLock()
	current, loaded := operator, operator_time
	operator_lock.RUnlock()
	if current != nil && when.Equal(loaded) {
		return current, nil
	}
	pair, err := tls.LoadX509KeyPair(certificate_file, key_file)
	if err != nil {
		if current != nil {
			warn("certificate: reload %s: %v", certificate_file, err)
			return current, nil
		}
		return nil, err
	}
	operator_lock.Lock()
	operator = &pair
	operator_time = when
	operator_lock.Unlock()
	info("certificate: loaded %s", certificate_file)
	return &pair, nil
}

// certificate_hash returns the base64 SHA-256 of the active ephemeral
// certificate for WebTransport pinning, or "" when an operator or ACME
// certificate is in use (normal chain validation applies).
func certificate_hash() (string, int64) {
	if certificate_file != "" || acme_manager != nil {
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

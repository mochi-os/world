// Mochi world: wire-number validation
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import "testing"

// TestNumberNonFinite: a hostile client can CBOR-encode NaN or ±Inf at any
// float width. decode() accepts them (they are valid CBOR), so number() must
// be the wall — every non-finite wire value degrades to the safe neutral 0.
func TestNumberNonFinite(t *testing.T) {
	// {"pitch": X} — the map+key prefix, then the float payload per case.
	prefix := []byte{0xa1, 0x65, 'p', 'i', 't', 'c', 'h'}
	cases := []struct {
		name    string
		payload []byte
	}{
		{"float16 NaN", []byte{0xf9, 0x7e, 0x00}},
		{"float16 +Inf", []byte{0xf9, 0x7c, 0x00}},
		{"float16 -Inf", []byte{0xf9, 0xfc, 0x00}},
		{"float32 NaN", []byte{0xfa, 0x7f, 0xc0, 0x00, 0x00}},
		{"float32 +Inf", []byte{0xfa, 0x7f, 0x80, 0x00, 0x00}},
		{"float64 NaN", []byte{0xfb, 0x7f, 0xf8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"float64 -Inf", []byte{0xfb, 0xff, 0xf0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
	}
	for _, c := range cases {
		message, err := decode(append(append([]byte{}, prefix...), c.payload...))
		if err != nil {
			t.Fatalf("%s: decode: %v", c.name, err)
		}
		if got := number(message, "pitch"); got != 0 {
			t.Fatalf("%s: number = %v, want 0 (non-finite must degrade)", c.name, got)
		}
	}
	// A finite value still passes through unchanged (the guard is not a blanket zero).
	message, _ := decode([]byte{0xa1, 0x65, 'p', 'i', 't', 'c', 'h', 0xf9, 0x3c, 0x00}) // float16 1.0
	if got := number(message, "pitch"); got != 1 {
		t.Fatalf("finite 1.0 became %v", got)
	}
}

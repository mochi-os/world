// Mochi world: Wire messages (CBOR envelopes)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Every wire message, in both directions, is a CBOR map with a "kind"
// discriminator. Client→server: join, input, leave. Server→client: welcome,
// refuse, snapshot, event, end. All encoding and decoding lives here so a
// future quantised snapshot format touches one file per side.

package main

import (
	"math"
	"reflect"

	"github.com/fxamacker/cbor/v2"
)

// decoder maps nested CBOR maps to map[string]any (the library's default is
// map[any]any, which silently fails string-keyed assertions).
var decoder, _ = cbor.DecOptions{DefaultMapType: reflect.TypeOf(map[string]any(nil))}.DecMode()

// protocol is the wire protocol version, carried in /status and welcome;
// clients refuse politely on mismatch.
const protocol = 1

func encode(message map[string]any) ([]byte, error) {
	return cbor.Marshal(message)
}

func decode(bytes []byte) (map[string]any, error) {
	message := map[string]any{}
	err := decoder.Unmarshal(bytes, &message)
	return message, err
}

// text reads a string field from a decoded message.
func text(message map[string]any, key string) string {
	if v, found := message[key].(string); found {
		return v
	}
	return ""
}

// number reads a numeric field from a decoded message (CBOR integers decode
// as uint64 or int64, JSON-sourced values as float64).
func number(message map[string]any, key string) float64 {
	var v float64
	switch n := message[key].(type) {
	case uint64:
		v = float64(n)
	case int64:
		v = float64(n)
	case float64:
		v = n
	case float32:
		v = float64(n)
	case int:
		v = float64(n)
	default:
		return 0
	}
	// Reject non-finite wire numbers (#174): a client can CBOR-encode NaN/±Inf,
	// and NaN propagates through clamp() into the authoritative simulation.
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

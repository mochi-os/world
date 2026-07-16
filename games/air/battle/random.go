// Mochi world: Battle randomness
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// All battle randomness flows through one stateless hash: the same
// (seed, slot, tick, counter) always rolls the same number, native or wasm,
// so both hosts judge identical outcomes and replays cannot diverge.

package battle

// mix is a SplitMix64-style integer mixer (the flight package's wind noise
// uses the same construction).
func mix(v uint64) uint64 {
	v += 0x9E3779B97F4A7C15
	v = (v ^ (v >> 30)) * 0xBF58476D1CE4E5B9
	v = (v ^ (v >> 27)) * 0x94D049BB133111EB
	return v ^ (v >> 31)
}

// Roll is the exported form for hosts that need battle-consistent rolls
// (the server's flare-decoy judgement).
func Roll(parts ...uint64) float64 { return roll(parts...) }

// roll hashes the parts together and maps the result to [0, 1).
func roll(parts ...uint64) float64 {
	h := uint64(0x8C5FB1B7A024FE9D)
	for _, p := range parts {
		h = mix(h ^ p)
	}
	return float64(h>>11) / float64(1<<53)
}

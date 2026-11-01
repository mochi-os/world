// Mochi world: Air map geometry
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The scenery a match collides against - the sea, the islands and their
// paved strips, the buildings as prisms, the masts as posts, the carrier with
// its island - as the world server carries it: one file per map under maps/,
// exported from the client's own map build (claude/scripts/air/geometry.py)
// and served to clients on the lobby, so the prediction core flies the very
// geometry the server collides it with. The welcome names the map and its
// hash, and a client whose own build differs says so.

package air

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"

	"world/games/air/flight"
)

//go:embed maps/*.json
var charts embed.FS

// chart is a loaded map: the world the flight core flies against, the file
// as served, and its hash.
type chart struct {
	name  string
	world flight.World
	bytes []byte
	hash  string
}

// chart_load reads a map by name. The file is decoded with the same rules the
// browser core applies to the client's payload - case-insensitive keys into
// flight.World - so both hosts hold one geometry.
func chart_load(name string) (*chart, bool) {
	// The embedded tree does its own gatekeeping: a name with a path element
	// that is not a plain file name is refused, and the lookup is exact.
	bytes, err := charts.ReadFile("maps/" + name + ".json")
	if err != nil {
		return nil, false
	}
	var world flight.World
	if err := json.Unmarshal(bytes, &world); err != nil {
		return nil, false
	}
	sum := sha256.Sum256(bytes)
	return &chart{name: name, world: world, bytes: bytes, hash: hex.EncodeToString(sum[:])}, true
}

// Map serves a map's geometry, as the lobby's mapper interface asks.
func (f *Air) Map(name string) ([]byte, bool) {
	c, found := chart_load(name)
	if !found {
		return nil, false
	}
	return c.bytes, true
}

// Mochi world: Air land tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"

	"world/games/air/flight"
)

// landSum is land.bin's hash as the client records it in its own copy's
// manifest (apps/air/web/public/maps/world/world.json, "land": {"sha256"}),
// both written by apps/air/tools/world.py: the server and the browser must
// collide against the one outline. Rebuilding the land updates both.
const landSum = "c81d5748b32548d5533878025d46f2fe208f3e0cae7a9f27f349b3ad73a4b9d5"

func TestTheServerEmbedsTheClientsLand(t *testing.T) {
	sum := sha256.Sum256(outline)
	if got := hex.EncodeToString(sum[:]); got != landSum {
		t.Fatalf("maps/land.bin hashes %s, the client's world.json records %s: rebuild with apps/air/tools/world.py, which writes both", got, landSum)
	}
}

func TestEveryChartCarriesTheEarthsLand(t *testing.T) {
	c, found := chart_load("midway")
	if !found {
		t.Fatal("midway chart missing")
	}
	if c.world.Land == nil {
		t.Fatal("the chart's world has no land")
	}
	paris := flight.Place{Latitude: 48.86 * math.Pi / 180, Longitude: 2.35 * math.Pi / 180}
	if !c.world.Land.At(paris.Latitude, paris.Longitude) {
		t.Error("Paris is not land")
	}
	if c.world.Land.At(30*math.Pi/180, -40*math.Pi/180) {
		t.Error("the mid-Atlantic is land")
	}
}

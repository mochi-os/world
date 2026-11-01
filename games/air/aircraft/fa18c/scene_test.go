// Mochi world: the scene these flight tests are laid in
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package fa18c

import (
	"math"
	"os"
	"strconv"
	"strings"

	"world/games/air/flight"
)

// scene is the tangent frame these tests lay their scenes in, and chart their
// maps and ships in: Midway's, or the place AIR_PLACE names
// ("latitude,longitude" in degrees), so the suite can run anywhere on the
// earth and must pass the same there.
var scene = func() flight.Frame {
	place := os.Getenv("AIR_PLACE")
	if place == "" {
		return flight.Midway
	}
	parts := strings.Split(place, ",")
	if len(parts) != 2 {
		panic("AIR_PLACE is latitude,longitude in degrees")
	}
	latitude, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		panic(err)
	}
	longitude, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		panic(err)
	}
	return flight.Tangent(latitude*math.Pi/180, longitude*math.Pi/180)
}()

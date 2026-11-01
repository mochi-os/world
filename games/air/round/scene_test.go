// Mochi world: the scene the round tests are laid in
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package round

import (
	"math"
	"os"
	"strconv"
	"strings"

	"world/games/air/flight"
)

// scene is the tangent frame the package's tests lay their scenes in, and
// chart their weather in: Midway's, or the place AIR_PLACE names
// ("latitude,longitude" in degrees). Nothing a round does may depend on where
// on the earth it flies, so the whole suite must pass anywhere.
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

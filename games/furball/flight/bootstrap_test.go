// Mochi world: Test bootstrap
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The package's gates fly the F/A-18C — the shipping aircraft. This
// external-package bootstrap wires it before any test runs (the flight
// package cannot import the catalogue itself: aircraft/fa18c imports flight).

package flight_test

import (
	"os"
	"testing"

	"world/games/furball/aircraft/fa18c"
	"world/games/furball/flight"
)

func TestMain(m *testing.M) {
	flight.Fighter = fa18c.Airframe
	os.Exit(m.Run())
}

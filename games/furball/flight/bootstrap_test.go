// Mochi world: Test bootstrap
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The package's gates fly the F/A-18F dataset, which lives outside the core
// (aircraft/fa18f imports flight, so internal tests cannot import it back).
// This external-package bootstrap wires the shared test airframe before any
// test runs.

package flight_test

import (
	"os"
	"testing"

	"world/games/furball/aircraft/fa18f"
	"world/games/furball/flight"
)

func TestMain(m *testing.M) {
	flight.Fighter = fa18f.Airframe
	os.Exit(m.Run())
}

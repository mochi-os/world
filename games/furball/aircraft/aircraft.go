// Mochi world: Aircraft catalogue
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The catalogue of flyable airframes: one subdirectory per aircraft, each
// exporting its dataset for the flight core. Hosts resolve a name from the
// wire to an airframe here; an empty name means the default.

package aircraft

import (
	"world/games/furball/aircraft/fa18c"
	"world/games/furball/aircraft/fa18f"
	"world/games/furball/flight"
)

// Get resolves an aircraft name to its airframe; nil for unknown names.
// The empty name is the default aircraft.
func Get(name string) *flight.Airframe {
	switch name {
	case "", "fa18f":
		return fa18f.Airframe
	case "fa18c":
		return fa18c.Airframe
	}
	return nil
}

// Names lists the catalogue for lobbies and pickers.
func Names() []string { return []string{"fa18c", "fa18f"} }

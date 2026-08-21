// Mochi world: Aircraft catalogue
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The catalogue of flyable airframes: one subdirectory per aircraft, each
// exporting its dataset for the flight core. Hosts resolve a name from the
// wire to an airframe here; an empty name means the default.

package aircraft

import (
	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

// Get resolves an aircraft name to its airframe; nil for unknown names, and the
// empty name is the default. Validation paths want the nil; spawning paths use
// Grant, since flight.New dereferences immediately.
func Get(name string) *flight.Airframe {
	switch name {
	case "", "fa18c":
		return fa18c.Airframe
	}
	return nil
}

// Grant resolves a requested airframe name to one guaranteed to exist,
// honouring a valid choice and defaulting anything else. The returned name is
// canonical. A nil airframe must never reach flight.New - it dereferences
// immediately.
func Grant(name string) (string, *flight.Airframe) {
	if a := Get(name); a != nil && name != "" {
		return name, a
	}
	return "fa18c", fa18c.Airframe
}

// Names lists the catalogue for lobbies and pickers.
func Names() []string { return []string{"fa18c"} }

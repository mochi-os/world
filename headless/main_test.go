// Mochi world: headless analysis smoke test
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"math"
	"strings"
	"testing"

	"world/games/air/flight"
)

// TestFlightIsAtTheAltitudeAsked: the analysis model was built with a zero
// Environment, which since the one global co-ordinate system is the
// Earth-centred frame, so `fly -altitude 3000` placed the jet 3 km from the
// earth's centre and printed sea-level numbers for every condition asked. A
// second of command_fly's loop at 3,000 m must stay at 3,000 m in the air of
// 3,000 m.
func TestFlightIsAtTheAltitudeAsked(t *testing.T) {
	m := model(0.5)
	m.State.Position = flight.Vec3{Y: 3000}
	m.State.Velocity = flight.Vec3{X: 200}
	m.State.Engine[0] = flight.EngineState{Spool: 0.85}
	m.State.Engine[1] = flight.EngineState{Spool: 0.85}
	for range int(1 / flight.Dt) {
		m.Step(flight.Inputs{Throttle: 0.85})
	}
	if altitude := m.Altitude(); !(math.Abs(altitude-3000) <= 50) { // NaN fails too: the zero frame's flight goes to NaN

		t.Fatalf("a second's flight from 3,000 m is at %.0f m", altitude)
	}
	air := flight.Atmosphere(3000, flight.Environment{})
	if want := m.State.Velocity.Length() / air.Sound; !(math.Abs(m.Mach()-want) <= 0.01) {
		t.Errorf("Mach %.3f, want %.3f: the model is not flying in the air of 3,000 m", m.Mach(), want)
	}
}

// TestAircraftHelpNamesWhatIsCarried: the help said "default fa18f", an
// airframe the build does not carry.
func TestAircraftHelpNamesWhatIsCarried(t *testing.T) {
	help := aircraft_help()
	if !strings.Contains(help, "default fa18c") || strings.Contains(help, "fa18f") {
		t.Errorf("help %q", help)
	}
}

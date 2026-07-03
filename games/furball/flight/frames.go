// Mochi world: Frame conventions
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// THE frame convention, defined once. Body: x out the nose, y out the canopy
// (up), z out the right wing. World: y up, gravity (0, -g, 0); the x/z plane
// is the map. This deliberately deviates from the aerospace x-fwd/y-right/
// z-down convention (the handover doc §11) because every existing boundary —
// the THREE.js client, the wire format, the map data, the tuned carrier
// poses — is y-up; conversion bugs at those boundaries are caught by
// nothing, while sign errors here are caught by the trim/modes harness.
//
// Consequences, written once so no other file re-derives a sign:
//   - pitch rate q is about body +z (right wing), positive nose-UP
//   - roll rate p is about body +x, positive roll RIGHT
//   - aerospace yaw rate r is about body -y (down), so r = -Omega.Y
//   - the inertia cross-term coupling roll and yaw (aerospace Ixz) couples
//     body x and body y here: it lives at Mat3[0][1]/[1][0]
//   - alpha is positive with the nose above the flight path; beta is
//     positive with the wind from the right

package flight

import (
	"math"
)

// alpha is the angle of attack from the body-frame air velocity (the
// aircraft's velocity relative to the air mass): positive when the flow
// comes from below the nose.
func alpha(v Vec3) float64 {
	return math.Atan2(-v.Y, v.X)
}

// beta is the sideslip angle: positive when the flow comes from the right.
func beta(v Vec3) float64 {
	l := v.Length()
	if l < 1e-9 {
		return 0
	}
	return math.Asin(clamp(v.Z/l, -1, 1))
}

// rates maps body Omega to aerospace (p, q, r): roll right, pitch up, yaw
// right positive.
func rates(omega Vec3) (p float64, q float64, r float64) {
	return omega.X, omega.Z, -omega.Y
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

// Derived instruments — pure accessors for HUDs, tooling, and the wasm
// out-buffer. All angles in radians, speeds in m/s.

// Alpha is the body angle of attack from the current state.
func (m *Model) Alpha() float64 {
	return alpha(m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust)))
}

// Beta is the sideslip angle.
func (m *Model) Beta() float64 {
	return beta(m.State.Attitude.Unrotate(m.State.Velocity.Subtract(m.gust)))
}

// Nz is the sensed normal load factor (the g meter) from the last step.
func (m *Model) Nz() float64 { return m.State.Fcs.Normal }

// Mach is the flight Mach number.
func (m *Model) Mach() float64 {
	return m.State.Velocity.Length() / air(m.State.Position.Y, m.Environment).Sound
}

// Cas approximates calibrated airspeed as equivalent airspeed.
func (m *Model) Cas() float64 {
	local := air(m.State.Position.Y, m.Environment)
	sea := air(0, m.Environment)
	return m.State.Velocity.Length() * math.Sqrt(local.Density/sea.Density)
}

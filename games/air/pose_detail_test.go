// Mochi world: the pose record's debrief detail (#164)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// A recipient's recording can only carry what the server chose to send it, and
// alpha and g are the two channels it cannot recover from the pose stream:
// #44 measured a derived nose disagreeing with the recorded AOA by up to 80
// degrees. They ride bytes 37 and 38.
func TestPoseCarriesAlphaAndLoad(t *testing.T) {
	i := build(t, "furball", nil, 2)
	a := i.aircraft[0]

	// Fly the state to a known alpha by pitching the body away from the flow:
	// Alpha() reads the velocity in the body frame, so a nose-up attitude over
	// a level velocity IS the angle of attack.
	want := 12.0
	a.model.State.Velocity = flight.Vec3{X: 200}
	a.model.State.Attitude = flight.Axis(flight.Vec3{Z: 1}, want*math.Pi/180)
	a.model.State.Fcs.Normal = 6.4

	b := pose(0, a)
	if len(b) != pose_record {
		t.Fatalf("pose is %d bytes, want pose_record %d", len(b), pose_record)
	}
	if alpha := float64(int8(b[37])); math.Abs(alpha-want) > 1.5 {
		t.Errorf("byte 37 alpha = %.0f deg, want about %.0f", alpha, want)
	}
	if load := float64(int8(b[38])) / 10; math.Abs(load-6.4) > 0.05 {
		t.Errorf("byte 38 load = %.2f g, want 6.40", load)
	}
}

// Both fields SATURATE. A wrapped counter reads as a manoeuvre that never
// happened — the same reason the gun expenditure above it saturates — and a
// departure past the int8 range is exactly when a debrief is being read.
func TestPoseDetailSaturates(t *testing.T) {
	i := build(t, "furball", nil, 2)
	a := i.aircraft[0]
	a.model.State.Velocity = flight.Vec3{X: 60}
	a.model.State.Attitude = flight.Axis(flight.Vec3{Z: 1}, 150*math.Pi/180) // deep departure
	a.model.State.Fcs.Normal = 40                                            // far past any limiter

	b := pose(0, a)
	if alpha := int8(b[37]); alpha < 0 {
		t.Errorf("byte 37 alpha = %d, a wrapped departure reading as a negative alpha", alpha)
	}
	if load := int8(b[38]); load != 127 {
		t.Errorf("byte 38 load = %d, want a saturated 127 rather than a wrap", load)
	}
}

// The stride is the contract with apps/air/web/src/game/net.ts POSE_RECORD.
// The protocol byte gates the wire VERSION, not the stride, so nothing at the
// join refuses a peer that disagrees here — it simply misreads every pose.
func TestPoseRecordStride(t *testing.T) {
	if pose_record != 39 {
		t.Errorf("pose_record = %d; net.ts POSE_RECORD must be changed to match, in the same release", pose_record)
	}
}

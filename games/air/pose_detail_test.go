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
	pitch := 11.7 // to the nearest degree, 12: a truncating byte reads 11
	want := 12.0
	a.model.State.Velocity = flight.Vec3{X: 200}
	a.model.State.Attitude = flight.Axis(flight.Vec3{Z: 1}, pitch*math.Pi/180)
	a.model.State.Fcs.Normal = 6.37 // to the nearest tenth, 6.4: a truncating byte reads 6.3

	b := pose(0, a)
	if len(b) != pose_record {
		t.Fatalf("pose is %d bytes, want pose_record %d", len(b), pose_record)
	}
	if alpha := float64(int8(b[37])); math.Abs(alpha-want) > 0.5 {
		t.Errorf("byte 37 alpha = %.0f deg, want about %.0f", alpha, want)
	}
	if load := float64(int8(b[38])) / 10; math.Abs(load-6.4) > 0.001 {
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

// Sideslip rides byte 30 in whole degrees, positive with the flow from the
// right, which is the side the jet is moving toward when its nose points left
// of its path. The recipient needs the core's own angle, taken against the
// air: the ground track the direction bytes carry reads a crab as a slip.
func TestPoseCarriesSideslip(t *testing.T) {
	i := build(t, "furball", nil, 2)
	a := i.aircraft[0]
	for _, want := range []float64{8, -8, 0} {
		// A yaw about the vertical swings the nose off a level velocity: that
		// angle IS the sideslip, the nose to the left of the path for positive.
		a.model.State.Velocity = flight.Vec3{X: 200}
		a.model.State.Attitude = flight.Axis(flight.Vec3{Y: 1}, want*math.Pi/180)
		b := pose(0, a)
		core := a.model.Beta() * 180 / math.Pi
		if got := float64(int8(b[30])); math.Abs(got-core) > 0.5 || math.Abs(got-want) > 0.5 {
			t.Errorf("nose %.0f deg off the path: byte 30 sideslip = %.0f deg, want about %.0f (the core reads %.1f)", want, got, want, core)
		}
	}
}

// The engine fires share byte 29 in fifteenths, rounded UP: the pilot's own
// cockpit lights FIRE on anything above zero, so the faintest fire must still
// read as one.
func TestPoseEngineFires(t *testing.T) {
	i := build(t, "furball", nil, 2)
	a := i.aircraft[0]
	for _, c := range []struct {
		left, right float64
		want        byte
	}{
		{0, 0, 0x00},
		{0.01, 0, 0x10},
		{0, 0.01, 0x01},
		{0.2, 1, 0x3f},
		{1, 0.6, 0xf9},
		{2, -1, 0xf0}, // out of range either way stays inside its four bits
	} {
		a.condition.Fire = [2]float64{c.left, c.right}
		if got := pose(0, a)[29]; got != c.want {
			t.Errorf("fires %.2f/%.2f: byte 29 = %#02x, want %#02x", c.left, c.right, got, c.want)
		}
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

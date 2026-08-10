// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import "testing"

// TestRadarPoseWire: byte 34 of the pose record carries the emitter state —
// high two bits the mode, low six the locked slot, 63 for none (#30). A craft
// that never reported must encode silent/none, not a phantom lock on slot 0.
func TestRadarPoseWire(t *testing.T) {
	i := build(t, "furball", nil, 2)

	fresh := pose(0, i.aircraft[0])
	if len(fresh) != 35 {
		t.Fatalf("pose record %d bytes, want 35", len(fresh))
	}
	if fresh[34] != 63 {
		t.Errorf("unreported emitter byte %d, want 63 (silent, no lock)", fresh[34])
	}

	i.Radar(0, 2, 1)
	locked := pose(0, i.aircraft[0])
	if locked[34] != 2<<6|1 {
		t.Errorf("STT-on-1 emitter byte %d, want %d", locked[34], 2<<6|1)
	}

	i.Radar(0, 1, -1)
	searching := pose(0, i.aircraft[0])
	if searching[34] != 1<<6|63 {
		t.Errorf("searching emitter byte %d, want %d", searching[34], 1<<6|63)
	}
}

// TestRadarValidation: the wire promises nothing — a lock may only name a
// present aircraft, only STT carries one, nonsense modes die, and bots (which
// never send the message) cannot be puppeted through it.
func TestRadarValidation(t *testing.T) {
	i := build(t, "furball", nil, 2)

	i.Radar(0, 2, 9) // slot 9 holds nothing
	if a := i.aircraft[0]; a.emitter != 2 || a.lock != -1 {
		t.Errorf("lock on an absent slot: emitter %d lock %d, want 2/-1", a.emitter, a.lock)
	}

	i.Radar(0, 1, 1) // search never carries a target
	if a := i.aircraft[0]; a.emitter != 1 || a.lock != -1 {
		t.Errorf("search with a target: emitter %d lock %d, want 1/-1", a.emitter, a.lock)
	}

	i.Radar(0, 7, 1) // nonsense mode: state unchanged
	if a := i.aircraft[0]; a.emitter != 1 || a.lock != -1 {
		t.Errorf("nonsense mode moved state: emitter %d lock %d", a.emitter, a.lock)
	}

	i.Radar(0, 2, 63) // beyond the six wire bits
	if a := i.aircraft[0]; a.lock != -1 {
		t.Errorf("lock %d accepted beyond the wire's slot space", a.lock)
	}
}

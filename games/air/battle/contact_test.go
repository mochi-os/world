// Mochi world: Battle midair contact geometry
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"
	"testing"
	"time"

	"world/games/air/flight"
)

func near(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s: %.6f, want %.6f", label, got, want)
	}
}

// TestSegments: the segment-segment distance the sweep rests on, across the
// cases that matter for capsules - crossing, parallel, skew, an endpoint
// nearest, and degenerate points.
func TestSegments(t *testing.T) {
	v := func(x, y, z float64) flight.Vec3 { return flight.Vec3{X: x, Y: y, Z: z} }
	near(t, "crossing at a height", segments(v(-1, 0, 0), v(1, 0, 0), v(0, 2, -1), v(0, 2, 1)), 2)
	near(t, "parallel abreast", segments(v(0, 0, 0), v(4, 0, 0), v(0, 0, 3), v(4, 0, 3)), 3)
	near(t, "collinear with a gap", segments(v(0, 0, 0), v(1, 0, 0), v(3, 0, 0), v(5, 0, 0)), 2)
	near(t, "endpoint nearest", segments(v(0, 0, 0), v(1, 0, 0), v(4, 4, 0), v(4, 8, 0)), 5)
	near(t, "two points", segments(v(0, 0, 0), v(0, 0, 0), v(3, 4, 0), v(3, 4, 0)), 5)
	near(t, "point and segment", segments(v(2, 1, 0), v(2, 1, 0), v(0, 0, 0), v(4, 0, 0)), 1)
	near(t, "touching", segments(v(0, 0, 0), v(2, 0, 0), v(1, 0, 0), v(1, 5, 0)), 0)
	near(t, "symmetric", segments(v(0, 0, 0), v(1, 0, 0), v(4, 4, 0), v(4, 8, 0)), segments(v(4, 4, 0), v(4, 8, 0), v(0, 0, 0), v(1, 0, 0)))
}

// A two-capsule airframe: a 10 m fuselage along X and a 12 m wing along Z.
func cross() []Part {
	return []Part{
		{Kind: Fuselage, Index: 0, Surface: -1, A: flight.Vec3{X: -5}, B: flight.Vec3{X: 5}, Radius: 0.8},
		{Kind: Structure, Index: 0, Surface: 0, A: flight.Vec3{Z: -6}, B: flight.Vec3{Z: 6}, Radius: 0.3},
	}
}

func TestExtent(t *testing.T) {
	near(t, "the wing tip plus its radius", Extent(cross()), 6.3)
	if Extent(nil) != 0 {
		t.Errorf("no parts, no extent")
	}
}

// TestContactSweep: two crosses passing wingtip through wingtip inside one
// tick meet only if the sweep looks back along the motion - at the end of the
// tick they are already clear of each other.
func TestContactSweep(t *testing.T) {
	still := flight.Quat{W: 1}
	a := Mover{Parts: cross(), Position: flight.Vec3{X: 5}, Attitude: still, Velocity: flight.Vec3{X: 300}}
	b := Mover{Parts: cross(), Position: flight.Vec3{X: 0, Z: 11.8}, Attitude: still, Velocity: flight.Vec3{X: -300}}
	metA, metB := Contact(a, b, 1.0/60, 0)
	if len(metA) != 1 || metA[0] != 1 || len(metB) != 1 || metB[0] != 1 {
		t.Errorf("wingtips crossing inside the tick: met %v %v, want the wings only", metA, metB)
	}
	if metA, metB := Contact(a, b, 0, 0); len(metA) != 0 || len(metB) != 0 {
		t.Errorf("without the sweep the end poses are clear: met %v %v", metA, metB)
	}
	b.Position.Z = 12.8
	if metA, metB := Contact(a, b, 1.0/60, 0); len(metA) != 0 || len(metB) != 0 {
		t.Errorf("a 0.2 m gap at the tips is a miss: met %v %v", metA, metB)
	}
}

// TestContactBounded: a flight state that has run away asks the sweep to look
// back along a relative motion of 1e11 m/s, over a million kilometres in one
// tick. The server's session goroutine runs that tick, so the answer has to
// come back at once; and the wingtips that overlap at the end of the tick must
// still be seen.
func TestContactBounded(t *testing.T) {
	still := flight.Quat{W: 1}
	a := Mover{Parts: cross(), Attitude: still, Velocity: flight.Vec3{X: 1e11}}
	b := Mover{Parts: cross(), Position: flight.Vec3{Z: 11.8}, Attitude: still}
	done := make(chan [2][]int, 1)
	go func() {
		metA, metB := Contact(a, b, 1.0/60, 0)
		done <- [2][]int{metA, metB}
	}()
	select {
	case met := <-done:
		if len(met[0]) != 1 || met[0][0] != 1 || len(met[1]) != 1 || met[1][0] != 1 {
			t.Errorf("the wingtips overlap at the end of the tick, but the sweep met %v %v", met[0], met[1])
		}
	case <-time.After(time.Second):
		t.Fatal("a sweep at 1e11 m/s relative has not returned after a second: the sample count follows the speed")
	}
}

// TestContactFrames: b's capsules are carried through both attitudes, and the
// world's wrap - a jet across the seam is as close as it really is.
func TestContactFrames(t *testing.T) {
	yaw := func(angle float64) flight.Quat { return flight.Quat{W: math.Cos(angle / 2), Y: math.Sin(angle / 2)} }
	a := Mover{Parts: cross(), Attitude: yaw(math.Pi / 2)}
	// a's wing, in the world, now lies along X; b nose-on to it along Z at 4 m
	// from a's centre runs its fuselage into that wing.
	b := Mover{Parts: cross(), Position: flight.Vec3{Z: 4}, Attitude: yaw(0)}
	metA, metB := Contact(a, b, 1.0/60, 0)
	if len(metA) == 0 || len(metB) == 0 {
		t.Fatalf("rotated wing across b's nose: met %v %v", metA, metB)
	}
	// Across the seam: 1,000 m short of the wrap is 11.8 m from the origin.
	c := Mover{Parts: cross(), Position: flight.Vec3{X: 0, Z: 1000 - 11.8}, Attitude: yaw(0)}
	d := Mover{Parts: cross(), Position: flight.Vec3{}, Attitude: yaw(0)}
	if metC, metD := Contact(c, d, 1.0/60, 1000); len(metC) == 0 || len(metD) == 0 {
		t.Errorf("wingtips overlapping across the seam: met %v %v", metC, metD)
	}
	if metC, metD := Contact(c, d, 1.0/60, 0); len(metC) != 0 || len(metD) != 0 {
		t.Errorf("without a wrap the same pair is 988 m apart: met %v %v", metC, metD)
	}
}

// TestContactStores: a round on a rail is hit geometry only while the rail is
// loaded.
func TestContactStores(t *testing.T) {
	still := flight.Quat{W: 1}
	rail := []Part{{Kind: Ordnance, Index: 3, Surface: -1, A: flight.Vec3{X: -1}, B: flight.Vec3{X: 1}, Radius: 0.35, Warhead: 9}}
	a := Mover{Parts: cross(), Attitude: still}
	b := Mover{Parts: rail, Position: flight.Vec3{Z: 6.2}, Attitude: still, Stores: 1 << 3}
	if metA, metB := Contact(a, b, 0, 0); len(metA) != 1 || len(metB) != 1 {
		t.Errorf("a loaded rail at the wingtip meets it: %v %v", metA, metB)
	}
	b.Stores = 0
	if metA, metB := Contact(a, b, 0, 0); len(metA) != 0 || len(metB) != 0 {
		t.Errorf("an empty rail is empty air: %v %v", metA, metB)
	}
}

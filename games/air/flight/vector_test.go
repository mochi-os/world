// Mochi world: Vector and quaternion tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"math/rand"
	"testing"
)

// TestBasis proves the frame constructor round-trips: the built attitude
// rotates body +X onto the requested forward, keeps body +Y in the
// forward/up plane on the up side, and stays orthonormal — including when the
// caller's up is nearly parallel to forward.
func TestBasis(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	vector := func() Vec3 {
		return Vec3{random.Float64()*2 - 1, random.Float64()*2 - 1, random.Float64()*2 - 1}
	}
	for i := 0; i < 200; i++ {
		forward := vector()
		up := vector()
		if forward.Length() < 1e-3 || forward.Normalize().Cross(up).Length() < 1e-3 {
			continue
		}
		q := Basis(forward, up)
		f := q.Rotate(Vec3{X: 1})
		u := q.Rotate(Vec3{Y: 1})
		s := q.Rotate(Vec3{Z: 1})
		if f.Subtract(forward.Normalize()).Length() > 1e-9 {
			t.Fatalf("case %d: forward off by %v", i, f.Subtract(forward.Normalize()).Length())
		}
		if math.Abs(f.Dot(u)) > 1e-9 || math.Abs(f.Dot(s)) > 1e-9 || math.Abs(u.Dot(s)) > 1e-9 {
			t.Fatalf("case %d: axes not orthogonal", i)
		}
		if u.Dot(up) < 0 {
			t.Fatalf("case %d: up landed on the wrong side", i)
		}
		if f.Cross(u).Subtract(s).Length() > 1e-9 {
			t.Fatalf("case %d: left-handed frame", i)
		}
	}
	// Degenerate up: parallel to forward must still return a valid frame.
	q := Basis(Vec3{X: 1}, Vec3{X: 1})
	if math.Abs(q.Rotate(Vec3{X: 1}).Subtract(Vec3{X: 1}).Length()) > 1e-9 {
		t.Fatal("degenerate up broke the forward axis")
	}
}

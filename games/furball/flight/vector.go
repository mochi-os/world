// Mochi world: Vector and quaternion mathematics
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
)

type Vec3 struct {
	X, Y, Z float64
}

func (v Vec3) add(o Vec3) Vec3        { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) scale(s float64) Vec3   { return Vec3{v.X * s, v.Y * s, v.Z * s} }
func (v Vec3) length() float64        { return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z) }

func (v Vec3) normalize() Vec3 {
	l := v.length()
	if l < 1e-12 {
		return Vec3{1, 0, 0}
	}
	return v.scale(1 / l)
}

func (v Vec3) lerp(o Vec3, t float64) Vec3 {
	return Vec3{v.X + (o.X-v.X)*t, v.Y + (o.Y-v.Y)*t, v.Z + (o.Z-v.Z)*t}
}

func (v Vec3) component(axis int) float64 {
	switch axis {
	case 0:
		return v.X
	case 1:
		return v.Y
	}
	return v.Z
}

func (v Vec3) replace(axis int, value float64) Vec3 {
	switch axis {
	case 0:
		v.X = value
	case 1:
		v.Y = value
	default:
		v.Z = value
	}
	return v
}

// Quat is a body->world unit quaternion.
type Quat struct {
	W, X, Y, Z float64
}

// axis builds the rotation of angle radians about a unit axis.
func axis(a Vec3, angle float64) Quat {
	half := angle / 2
	s := math.Sin(half)
	return Quat{math.Cos(half), a.X * s, a.Y * s, a.Z * s}
}

// multiply composes rotations: (q.multiply(p)) applies p first, then q —
// matching the client's quaternion.premultiply usage.
func (q Quat) multiply(p Quat) Quat {
	return Quat{
		q.W*p.W - q.X*p.X - q.Y*p.Y - q.Z*p.Z,
		q.W*p.X + q.X*p.W + q.Y*p.Z - q.Z*p.Y,
		q.W*p.Y - q.X*p.Z + q.Y*p.W + q.Z*p.X,
		q.W*p.Z + q.X*p.Y - q.Y*p.X + q.Z*p.W,
	}
}

func (q Quat) normalize() Quat {
	l := math.Sqrt(q.W*q.W + q.X*q.X + q.Y*q.Y + q.Z*q.Z)
	if l < 1e-12 {
		return Quat{1, 0, 0, 0}
	}
	return Quat{q.W / l, q.X / l, q.Y / l, q.Z / l}
}

// rotate applies the rotation to a vector.
func (q Quat) Rotate(v Vec3) Vec3 {
	// t = 2 * cross(q.xyz, v); v' = v + w*t + cross(q.xyz, t)
	tx := 2 * (q.Y*v.Z - q.Z*v.Y)
	ty := 2 * (q.Z*v.X - q.X*v.Z)
	tz := 2 * (q.X*v.Y - q.Y*v.X)
	return Vec3{
		v.X + q.W*tx + q.Y*tz - q.Z*ty,
		v.Y + q.W*ty + q.Z*tx - q.X*tz,
		v.Z + q.W*tz + q.X*ty - q.Y*tx,
	}
}

// Look builds the attitude whose forward axis points along a horizontal
// heading vector (used for spawns).
func Look(direction Vec3) Quat {
	f := Vec3{direction.X, 0, direction.Z}.normalize()
	angle := math.Atan2(-f.Z, f.X) // rotation about +Y taking +X to f (R_y: x'=x cosθ + z sinθ)
	return axis(Vec3{0, 1, 0}, angle)
}

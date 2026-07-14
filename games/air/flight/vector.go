// Mochi world: Vector, quaternion, and matrix mathematics
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
)

// Vec3 is a 3-vector; world or body frame by context (see frames.go).
type Vec3 struct {
	X, Y, Z float64
}

func (v Vec3) Add(o Vec3) Vec3      { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) Subtract(o Vec3) Vec3 { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v Vec3) Scale(s float64) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }
func (v Vec3) Dot(o Vec3) float64   { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }
func (v Vec3) Length() float64      { return math.Sqrt(v.Dot(v)) }

func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{v.Y*o.Z - v.Z*o.Y, v.Z*o.X - v.X*o.Z, v.X*o.Y - v.Y*o.X}
}

func (v Vec3) Normalize() Vec3 {
	l := v.Length()
	if l < 1e-12 {
		return Vec3{X: 1}
	}
	return v.Scale(1 / l)
}

func (v Vec3) Lerp(o Vec3, t float64) Vec3 {
	return Vec3{v.X + (o.X-v.X)*t, v.Y + (o.Y-v.Y)*t, v.Z + (o.Z-v.Z)*t}
}

// Quat is a body->world unit quaternion.
type Quat struct {
	W, X, Y, Z float64
}

// Axis builds the rotation of angle radians about a unit axis.
func Axis(a Vec3, angle float64) Quat {
	half := angle / 2
	s := math.Sin(half)
	return Quat{math.Cos(half), a.X * s, a.Y * s, a.Z * s}
}

// Multiply composes rotations: q.Multiply(p) applies p first, then q.
func (q Quat) Multiply(p Quat) Quat {
	return Quat{
		q.W*p.W - q.X*p.X - q.Y*p.Y - q.Z*p.Z,
		q.W*p.X + q.X*p.W + q.Y*p.Z - q.Z*p.Y,
		q.W*p.Y - q.X*p.Z + q.Y*p.W + q.Z*p.X,
		q.W*p.Z + q.X*p.Y - q.Y*p.X + q.Z*p.W,
	}
}

func (q Quat) Normalize() Quat {
	l := math.Sqrt(q.W*q.W + q.X*q.X + q.Y*q.Y + q.Z*q.Z)
	if l < 1e-12 {
		return Quat{W: 1}
	}
	return Quat{q.W / l, q.X / l, q.Y / l, q.Z / l}
}

// Conjugate is the inverse rotation for a unit quaternion.
func (q Quat) Conjugate() Quat { return Quat{q.W, -q.X, -q.Y, -q.Z} }

// Rotate applies the rotation to a vector (body->world for an attitude).
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

// Unrotate applies the inverse rotation (world->body for an attitude).
func (q Quat) Unrotate(v Vec3) Vec3 { return q.Conjugate().Rotate(v) }

// Derivative is the quaternion kinematic rate q̇ = ½ q ⊗ (0, ω) for body
// rates ω.
func (q Quat) Derivative(omega Vec3) Quat {
	return Quat{
		-0.5 * (q.X*omega.X + q.Y*omega.Y + q.Z*omega.Z),
		0.5 * (q.W*omega.X + q.Y*omega.Z - q.Z*omega.Y),
		0.5 * (q.W*omega.Y + q.Z*omega.X - q.X*omega.Z),
		0.5 * (q.W*omega.Z + q.X*omega.Y - q.Y*omega.X),
	}
}

// Look builds the attitude whose forward axis points along a horizontal
// heading vector, wings level (used for spawns).
func Look(direction Vec3) Quat {
	f := Vec3{X: direction.X, Z: direction.Z}.Normalize()
	angle := math.Atan2(-f.Z, f.X) // rotation about +Y taking +X to f
	return Axis(Vec3{Y: 1}, angle)
}

// Mat3 is a row-major 3×3 matrix (inertia tensors and their inverses).
type Mat3 [3][3]float64

func (m Mat3) Apply(v Vec3) Vec3 {
	return Vec3{
		m[0][0]*v.X + m[0][1]*v.Y + m[0][2]*v.Z,
		m[1][0]*v.X + m[1][1]*v.Y + m[1][2]*v.Z,
		m[2][0]*v.X + m[2][1]*v.Y + m[2][2]*v.Z,
	}
}

func (m Mat3) Add(o Mat3) Mat3 {
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			m[r][c] += o[r][c]
		}
	}
	return m
}

func (m Mat3) Scale(s float64) Mat3 {
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			m[r][c] *= s
		}
	}
	return m
}

// Inverse inverts the matrix by cofactor expansion; inertia tensors are
// well-conditioned so no pivoting is needed.
func (m Mat3) Inverse() Mat3 {
	a := m[1][1]*m[2][2] - m[1][2]*m[2][1]
	b := m[1][2]*m[2][0] - m[1][0]*m[2][2]
	c := m[1][0]*m[2][1] - m[1][1]*m[2][0]
	determinant := m[0][0]*a + m[0][1]*b + m[0][2]*c
	if determinant == 0 {
		return Mat3{}
	}
	d := 1 / determinant
	return Mat3{
		{a * d, (m[0][2]*m[2][1] - m[0][1]*m[2][2]) * d, (m[0][1]*m[1][2] - m[0][2]*m[1][1]) * d},
		{b * d, (m[0][0]*m[2][2] - m[0][2]*m[2][0]) * d, (m[0][2]*m[1][0] - m[0][0]*m[1][2]) * d},
		{c * d, (m[0][1]*m[2][0] - m[0][0]*m[2][1]) * d, (m[0][0]*m[1][1] - m[0][1]*m[1][0]) * d},
	}
}

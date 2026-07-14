// Mochi world: Contact world geometry
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The host supplies world geometry as data at model creation — never as
// callbacks — so the surface query lives inside the core and is identical
// native and wasm, allocation-free, and correct under prediction replay.
// Strut/tyre/catapult/wire force models land in phase F (gear.go,
// carrier.go); this file owns the types and the surface query.

package flight

import (
	"math"
)

// World is the contact geometry for a session.
type World struct {
	Sea     float64 // water surface height; open sea has no landable surface
	Fields  []Field
	Carrier *Carrier // nil for pure-airfield sessions
}

// Field is one island group: paved strips over soft ground.
type Field struct {
	Height float64
	Strips []Strip // runways/taxiways/aprons as capsules
	Coast  []Vec3  // island outline polygon (y ignored); inside = soft ground
}

// Strip is a paved capsule: the segment A-B swept by half Width.
type Strip struct {
	A, B  Vec3
	Width float64
}

// Carrier is the ship: pose, deck outline, catapults, and arrestor wires,
// all in the carrier frame. The pose is a pure function of sim time so
// prediction replay reproduces the deck exactly.
type Carrier struct {
	Position  Vec3    // deck reference at Time 0 (Y = deck height)
	Heading   float64 // rad
	Speed     float64 // m/s along heading (0 in the current phase)
	Deck      []Vec3  // deck outline polygon, carrier frame
	Catapults []Catapult
	Wires     []Wire
}

// Catapult is one launch track in the carrier frame.
type Catapult struct {
	Position Vec3
	Heading  float64 // rad, relative to the ship
	Stroke   float64 // m
	Speed    float64 // m/s end speed relative to the deck
}

// Wire is one arrestor cable segment in the carrier frame.
type Wire struct {
	A, B Vec3
}

// Surface kinds, shared vocabulary with the hosts.
const (
	Paved = 1 // runway, taxiway, apron
	Soft  = 2 // unpaved island ground: heavy rolling drag
	Deck  = 3 // the carrier
)

// pose is the carrier reference position at a sim time (deterministic under
// prediction replay: a pure function of time).
func (c *Carrier) pose(t float64) Vec3 {
	return c.Position.Add(c.direction().Scale(c.Speed * t))
}

// direction is the ship's forward unit vector in the world.
func (c *Carrier) direction() Vec3 {
	return Vec3{X: math.Cos(c.Heading), Z: -math.Sin(c.Heading)}
}

// local transforms a world point into the carrier deck frame at time t
// (minimum-image, so ops near a wrap seam stay correct).
func (c *Carrier) local(p Vec3, t float64, wrap float64) Vec3 {
	at := c.pose(t)
	dx := Shortest(at.X, p.X, wrap)
	dz := Shortest(at.Z, p.Z, wrap)
	sin, cos := math.Sin(c.Heading), math.Cos(c.Heading)
	return Vec3{X: dx*cos - dz*sin, Y: p.Y - at.Y, Z: dx*sin + dz*cos}
}

// world transforms a carrier-frame point back to world coordinates.
func (c *Carrier) world(p Vec3, t float64) Vec3 {
	at := c.pose(t)
	sin, cos := math.Sin(c.Heading), math.Cos(c.Heading)
	return Vec3{X: at.X + p.X*cos + p.Z*sin, Y: at.Y + p.Y, Z: at.Z - p.X*sin + p.Z*cos}
}

// inside is a 2D point-in-polygon test on the x/z plane.
func inside(p Vec3, polygon []Vec3) bool {
	in := false
	for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
		a, b := polygon[i], polygon[j]
		if (a.Z > p.Z) != (b.Z > p.Z) &&
			p.X < (b.X-a.X)*(p.Z-a.Z)/(b.Z-a.Z)+a.X {
			in = !in
		}
	}
	return in
}

// surface finds the contact surface under a world point: carrier deck, then
// paved strips, then island ground, else none (open sea — the hosts treat
// water impact as a crash, not a contact). Returns height, kind, and the
// surface's own velocity (a parked jet rides the ship).
func (w *World) surface(p Vec3, t float64, wrap float64) (float64, int, Vec3, bool) {
	if w.Carrier != nil {
		local := w.Carrier.local(p, t, wrap)
		if local.Y > -12 && local.Y < 25 && math.Abs(local.X) < 180 && math.Abs(local.Z) < 60 {
			if inside(local, w.Carrier.Deck) {
				return w.Carrier.pose(t).Y, Deck, w.Carrier.direction().Scale(w.Carrier.Speed), true
			}
		}
	}
	for fi := range w.Fields {
		field := &w.Fields[fi]
		for si := range field.Strips {
			strip := &field.Strips[si]
			dx := p.X - strip.A.X
			dz := p.Z - strip.A.Z
			ex := strip.B.X - strip.A.X
			ez := strip.B.Z - strip.A.Z
			length := ex*ex + ez*ez
			f := 0.0
			if length > 0 {
				f = clamp((dx*ex+dz*ez)/length, 0, 1)
			}
			ox := dx - f*ex
			oz := dz - f*ez
			if ox*ox+oz*oz <= strip.Width*strip.Width/4 {
				return field.Height, Paved, Vec3{}, true
			}
		}
		if len(field.Coast) > 2 && inside(p, field.Coast) {
			return field.Height, Soft, Vec3{}, true
		}
	}
	return 0, 0, Vec3{}, false
}

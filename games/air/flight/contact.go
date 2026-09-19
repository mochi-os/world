// Mochi world: Contact world geometry
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The host supplies world geometry as data at model creation - never as
// callbacks - so the surface query lives inside the core: identical native and
// wasm, allocation-free, correct under prediction replay.

package flight

import (
	"math"
)

// World is the contact geometry for a session.
type World struct {
	Sea     float64 // water surface height; open sea has no landable surface
	Fields  []Field
	Carrier *Carrier // nil for pure-airfield sessions
	Prisms  []Prism  // buildings, the carrier's island: solids the probes must not meet
	Posts   []Post   // masts and lights
}

// Prism is a building: a solid over its footprint polygon up to Top, from
// whatever ground it stands on. Post is a mast or a light: a cylinder about
// its axis. Neither is a surface anything rolls on - meeting one is a crash,
// which the hosts read off the crash probes.
type Prism struct {
	Outline []Vec3 // footprint polygon, x/z
	Top     float64
}

type Post struct {
	Position Vec3 // the axis, x/z
	Radius   float64
	Top      float64
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

// Reach is how far above the world's highest surface a lookup can still find
// one. Every contact caller - the gear, the belly skids, touch and the crash
// probes - reads a surface below the point as no contact, so above the top
// there is nothing for them to find. Ground effect (aero.go) asks from up to six
// wingspans; every airframe's six spans must fit inside this
// (aircraft.TestGroundEffectInsideReach).
const Reach = 100.0

// top is the highest surface a lookup can return: the fields and the
// carrier's deck, over the sea.
func (w *World) top() float64 {
	top := w.Sea
	for fi := range w.Fields {
		if w.Fields[fi].Height > top {
			top = w.Fields[fi].Height
		}
	}
	if c := w.Carrier; c != nil && c.Position.Y > top {
		top = c.Position.Y
	}
	return top
}

// surface finds the contact surface under a world point: carrier deck, then
// paved strips, then island ground, else none (open sea — the hosts treat
// water impact as a crash, not a contact). Returns height, kind, and the
// surface's own velocity (a parked jet rides the ship).
func (w *World) surface(p Vec3, t float64, wrap float64) (float64, int, Vec3, bool) {
	if p.Y > w.top()+Reach {
		// Far above anything in this world. Once every server model flew the
		// match's map, a jet at altitude asked this about 160 times a tick
		// between its gear, skids and crash probes, and every answer walked
		// every island's coastline: two thirds of a 16-ace furball's tick.
		return 0, 0, Vec3{}, false
	}
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

// struck reports whether a world point is inside a prism or a post, each
// taken at its minimum image so a seam hides nothing.
func (w *World) struck(p Vec3, wrap float64) bool {
	for pi := range w.Prisms {
		prism := &w.Prisms[pi]
		if p.Y > prism.Top || len(prism.Outline) < 3 {
			continue
		}
		at := prism.Outline[0]
		if inside(Vec3{X: at.X + Shortest(at.X, p.X, wrap), Z: at.Z + Shortest(at.Z, p.Z, wrap)}, prism.Outline) {
			return true
		}
	}
	for pi := range w.Posts {
		post := &w.Posts[pi]
		if p.Y > post.Top {
			continue
		}
		dx, dz := Shortest(post.Position.X, p.X, wrap), Shortest(post.Position.Z, p.Z, wrap)
		if dx*dx+dz*dz <= post.Radius*post.Radius {
			return true
		}
	}
	return false
}

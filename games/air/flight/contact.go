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

// Carrier is the ship: pose, deck outline, catapults, arrestor wires and its
// island, all in the carrier frame. The pose is a pure function of sim time
// so prediction replay reproduces the deck exactly; every host gives its
// aircraft the session's clock (State.Time), so all of them meet the ship in
// the same place. Its track is charted in the world frame, Home: the ship
// sails where the chart puts it, on the curved sea (Pose).
type Carrier struct {
	Position  Vec3    // deck reference at Time 0 (Y = deck height above the sea, which the ship keeps)
	Heading   float64 // rad
	Speed     float64 // m/s along heading
	Track     []Leg   // the ship's path in time order; set, it replaces Position, Heading and Speed
	Deck      []Vec3  // deck outline polygon, carrier frame
	Catapults []Catapult
	Wires     []Wire
	Prisms    []Prism // the island's solids: outline in the carrier frame, Top a world height
	Posts     []Post  // its mast: position in the carrier frame
}

// Leg is one stretch of the ship's track: from Start (session time, s) the
// reference point leaves Position on Heading at Speed, the bow turning at
// Turn rad/s (0 holds the heading; + turns it as Heading counts, to port). A
// straight leg may gather or shed way at Change m/s²; a turn holds its speed,
// and a leg that changes speed is followed by one holding the speed reached.
type Leg struct {
	Start    float64
	Position Vec3
	Heading  float64
	Speed    float64
	Turn     float64
	Change   float64
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

// leg is the stretch of the track the ship is on at time t: the last that has
// started, or the first for a time before it. Without a track the ship holds
// Position, Heading and Speed from time zero.
func (c *Carrier) leg(t float64) Leg {
	if len(c.Track) == 0 {
		return Leg{Position: c.Position, Heading: c.Heading, Speed: c.Speed}
	}
	i := len(c.Track) - 1
	for i > 0 && c.Track[i].Start > t {
		i--
	}
	return c.Track[i]
}

// At is the carrier's reference point and heading at time t (deterministic
// under prediction replay: a pure function of time). A turning leg sweeps an
// arc of radius Speed/Turn.
func (c *Carrier) At(t float64) (Vec3, float64) {
	l := c.leg(t)
	dt := t - l.Start
	heading := l.Heading + l.Turn*dt
	at := l.Position
	if math.Abs(l.Turn) < 1e-9 {
		at = at.Add(direction(l.Heading).Scale(l.Speed*dt + 0.5*l.Change*dt*dt))
	} else {
		r := l.Speed / l.Turn
		at.X += r * (math.Sin(heading) - math.Sin(l.Heading))
		at.Z += r * (math.Cos(heading) - math.Cos(l.Heading))
	}
	at.Y = c.Position.Y
	return at, heading
}

// Pose is the ship's deck reference point and its attitude (the carrier frame
// to Home) at time t: the track's chart position carried straight down onto
// the curved sea, the deck at its height above the water there, level on it,
// and the bow on its heading.
func (c *Carrier) Pose(t float64) (Vec3, Quat) {
	at, heading := c.At(t)
	sea, level := Home.Sea(at.X, at.Z)
	up := level.Rotate(Vec3{Y: 1})
	// The bow points the way the chart moves her, laid level on the water:
	// her heading counts in Home's axes, which lean a little against the
	// level ones away from Midway.
	bow := direction(heading)
	bow = bow.Subtract(up.Scale(bow.Dot(up)))
	return sea.Add(up.Scale(c.Position.Y)), Basis(bow, up)
}

// track is the unit level direction, in the caller's frame, of a heading
// relative to the ship's bow: a catapult's line. place carries the caller's
// frame to Home.
func (c *Carrier) track(relative float64, t float64, place Rigid) Vec3 {
	_, attitude := c.Pose(t)
	d := place.Inverse().Vector(attitude.Rotate(direction(relative)))
	return Vec3{X: d.X, Z: d.Z}.Normalize()
}

// direction is a heading's forward unit vector in the world.
func direction(heading float64) Vec3 {
	return Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
}

// Way is the ship's speed through the water at a sim time.
func (c *Carrier) Way(t float64) float64 {
	l := c.leg(t)
	if l.Turn != 0 {
		return l.Speed
	}
	return l.Speed + l.Change*(t-l.Start)
}

// Known is the earliest time the ship's track covers. Before it, At carries
// the first leg back in time whatever that leg does, which is where she was
// only if she held it: a host drawing where she has been (her wake) takes
// her as steaming steadily before then. Without a track, she always held
// her course.
func (c *Carrier) Known() float64 {
	if len(c.Track) == 0 {
		return math.Inf(-1)
	}
	return c.Track[0].Start
}

// velocity is the deck's own velocity at a point at time t, in the caller's
// frame (place carries it to Home): the reference point's along the bow, plus
// the turn's sweep about it.
func (c *Carrier) velocity(p Vec3, t float64, place Rigid) Vec3 {
	l := c.leg(t)
	at, attitude := c.Pose(t)
	_, heading := c.At(t)
	up := attitude.Rotate(Vec3{Y: 1})
	// The chart moves her reference point along her heading at her way, and
	// the sea she floats on slopes away under the chart as Pose lays her on
	// it: her own velocity rises and falls with that slope, and her deck
	// stands its height up the normal, which tips forward as she goes.
	along := direction(heading).Scale(c.Way(t))
	v := Vec3{X: along.X, Y: -(up.X*along.X + up.Z*along.Z) / up.Y, Z: along.Z}
	spin := up.Scale(l.Turn).Add(up.Cross(v).Scale(1 / curve)) // her turn about her up, and the curve the sea carries her round
	v = v.Add(spin.Cross(up.Scale(c.Position.Y)))
	return place.Inverse().Vector(v.Add(spin.Cross(place.Point(p).Subtract(at))))
}

// curve is the sea's radius of curvature, m: the mean of the WGS84 radii,
// which differ from it by a third of a percent at most, for a term that is
// itself a part in a million.
const curve = 6371008.8

// Aboard reports whether a point is on the carrier's deck at time t: over the
// deck's outline, and within a few metres of its height. place carries the
// caller's frame to Home.
func (c *Carrier) Aboard(p Vec3, t float64, place Rigid) bool {
	local := c.local(p, t, place)
	return local.Y > -2 && local.Y < 8 && inside(local, c.Deck)
}

// local transforms a point of the caller's frame into the carrier deck frame
// at time t.
func (c *Carrier) local(p Vec3, t float64, place Rigid) Vec3 {
	at, attitude := c.Pose(t)
	return attitude.Unrotate(place.Point(p).Subtract(at))
}

// world transforms a carrier-frame point to the caller's frame.
func (c *Carrier) world(p Vec3, t float64, place Rigid) Vec3 {
	at, attitude := c.Pose(t)
	return place.Inverse().Point(at.Add(attitude.Rotate(p)))
}

// Where is the deck reference point at time t in the caller's frame, and the
// carrier's attitude there: where a host draws the ship or spawns a jet on it.
func (c *Carrier) Where(t float64, place Rigid) (Vec3, Quat) {
	at, attitude := c.Pose(t)
	back := place.Inverse()
	return back.Point(at), back.Attitude(attitude)
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

// surface finds the contact surface under a point: carrier deck, then paved
// strips, then island ground, else none (open sea — the hosts treat water
// impact as a crash, not a contact). Returns height, kind, and the surface's
// own velocity (a parked jet rides the ship), in the caller's frame: place
// carries it to Home, where the map and the ship are. The height is the
// point's own height less its clearance above the surface, so a caller
// working level under itself reads the surface beneath it.
func (w *World) surface(p Vec3, t float64, place Rigid) (float64, int, Vec3, bool) {
	if p.Y > w.top()+Reach {
		// Far above anything in this world. Once every server model flew the
		// match's map, a jet at altitude asked this about 160 times a tick
		// between its gear, skids and crash probes, and every answer walked
		// every island's coastline: two thirds of a 16-expert furball's tick.
		return 0, 0, Vec3{}, false
	}
	if w.Carrier != nil {
		local := w.Carrier.local(p, t, place)
		if local.Y > -12 && local.Y < 25 && math.Abs(local.X) < 180 && math.Abs(local.Z) < 60 {
			if inside(local, w.Carrier.Deck) {
				return p.Y - local.Y, Deck, w.Carrier.velocity(p, t, place), true
			}
		}
	}
	q := place.Point(p) // the point in Home, where the map is charted
	for fi := range w.Fields {
		field := &w.Fields[fi]
		for si := range field.Strips {
			strip := &field.Strips[si]
			dx := q.X - strip.A.X
			dz := q.Z - strip.A.Z
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
				return p.Y - (q.Y - field.Height), Paved, Vec3{}, true
			}
		}
		if len(field.Coast) > 2 && inside(q, field.Coast) {
			return p.Y - (q.Y - field.Height), Soft, Vec3{}, true
		}
	}
	return 0, 0, Vec3{}, false
}

// Footing is the velocity of the surface under a point at time t, as the gear
// meets it: the deck's own on the carrier, still on land, and none at sea or
// in the air. A jet spotted on the deck of a ship under way starts with it,
// or the deck runs out from under the wheels and the catapult snatches the
// jet up to the ship's speed. place carries the caller's frame to Home.
func (w *World) Footing(p Vec3, t float64, place Rigid) Vec3 {
	_, _, velocity, _ := w.surface(p, t, place)
	return velocity
}

// struck reports whether a point of the caller's frame is inside a prism or a
// post - the carrier's island among them, met where the ship is at time t.
// place carries the point to Home, where the map is charted; a top is a
// height above the sea, which the carrier's deck frame measures from its own
// deck.
func (w *World) struck(p Vec3, t float64, place Rigid) bool {
	if c := w.Carrier; c != nil && (len(c.Prisms) > 0 || len(c.Posts) > 0) {
		local := c.local(p, t, place)
		height := local.Y + c.Position.Y
		for pi := range c.Prisms {
			prism := &c.Prisms[pi]
			if height <= prism.Top && len(prism.Outline) >= 3 && inside(local, prism.Outline) {
				return true
			}
		}
		for pi := range c.Posts {
			post := &c.Posts[pi]
			dx, dz := local.X-post.Position.X, local.Z-post.Position.Z
			if height <= post.Top && dx*dx+dz*dz <= post.Radius*post.Radius {
				return true
			}
		}
	}
	q := place.Point(p)
	for pi := range w.Prisms {
		prism := &w.Prisms[pi]
		if q.Y > prism.Top || len(prism.Outline) < 3 {
			continue
		}
		if inside(q, prism.Outline) {
			return true
		}
	}
	for pi := range w.Posts {
		post := &w.Posts[pi]
		if q.Y > post.Top {
			continue
		}
		dx, dz := q.X-post.Position.X, q.Z-post.Position.Z
		if dx*dx+dz*dz <= post.Radius*post.Radius {
			return true
		}
	}
	return false
}

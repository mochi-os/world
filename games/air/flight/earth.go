// Mochi world: The earth
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The WGS84 ellipsoid, and the frames the simulation works in. The earth does
// not turn (a fighter's Coriolis acceleration is about 0.4% of g, and the pilot
// flies it out without noticing), and sea level is the ellipsoid itself: the
// geoid's departure from it, up to about 100 m, changes only over thousands of
// kilometres, where no pilot can see it.
//
// A frame is a tangent frame of the ellipsoid: its origin on the ellipsoid at
// an anchor, x east, y up along the ellipsoid's normal, z south - the axes the
// game has always used. Every frame is Cartesian, so a straight line, a
// distance and an angle between two points are exact in any of them, however
// far apart the points are; only "up" and "level" belong to one place. The
// world frame (Home) is the frame at Midway, the map's own position, extended
// over the whole earth: near Midway it is the flat world the game once flew in.

package flight

import (
	"math"
)

// The WGS84 ellipsoid.
const (
	Equator    = 6378137.0         // semi-major axis, m
	Flattening = 1 / 298.257223563 // (equator - pole) / equator
)

var (
	pole       = Equator * (1 - Flattening)    // semi-minor axis, m
	eccentric  = Flattening * (2 - Flattening) // first eccentricity squared
	second     = eccentric / (1 - eccentric)   // second eccentricity squared
	oblateness = Equator / pole                // the stretch along the axis that makes the ellipsoid a sphere of radius Equator
	midway     = Tangent(28.2334*degree, -177.3668*degree)
	degree     = math.Pi / 180
)

// Home is the world frame, the one the server and the cores simulate in: the
// tangent frame at the centre of Midway Atoll (28.2334° N, 177.3668° W), the
// point the map is projected about (apps/air/midway-prep/prep.py), so the
// map's fields lie at their true places. The map, the carrier, the wakes and
// the weather are fixed in it.
var Home = midway

// Place is a geodetic position: latitude and longitude in radians, altitude
// in metres above the ellipsoid.
type Place struct {
	Latitude  float64
	Longitude float64
	Altitude  float64
}

// Centred is the earth-centred, earth-fixed position of a place: x through the
// equator at Greenwich, z through the north pole.
func Centred(p Place) Vec3 {
	sin, cos := math.Sincos(p.Latitude)
	normal := Equator / math.Sqrt(1-eccentric*sin*sin) // prime vertical radius of curvature
	return Vec3{
		X: (normal + p.Altitude) * cos * math.Cos(p.Longitude),
		Y: (normal + p.Altitude) * cos * math.Sin(p.Longitude),
		Z: (normal*(1-eccentric) + p.Altitude) * sin,
	}
}

// Geodetic is the place of an earth-centred point, in closed form (Heikkinen's
// solution of the quartic): exact to well under a millimetre from the deepest
// trench to far above any aircraft, at the poles as everywhere else.
func Geodetic(e Vec3) Place {
	a, b := Equator, pole
	p := math.Hypot(e.X, e.Y)
	z := e.Z
	f := 54 * b * b * z * z
	g := p*p + (1-eccentric)*z*z - eccentric*(a*a-b*b)
	c := eccentric * eccentric * f * p * p / (g * g * g)
	s := math.Cbrt(1 + c + math.Sqrt(c*c+2*c))
	k := s + 1 + 1/s
	q := f / (3 * k * k * g * g)
	r := math.Sqrt(1 + 2*eccentric*eccentric*q)
	root := -q*eccentric*p/(1+r) + math.Sqrt(math.Max(0, a*a/2*(1+1/r)-q*(1-eccentric)*z*z/(r*(1+r))-q*p*p/2))
	u := math.Hypot(p-eccentric*root, z)
	v := math.Sqrt((p-eccentric*root)*(p-eccentric*root) + (1-eccentric)*z*z)
	lift := b * b * z / (a * v)
	return Place{
		Latitude:  math.Atan2(z+second*lift, p),
		Longitude: math.Atan2(e.Y, e.X),
		Altitude:  u * (1 - b*b/(a*v)),
	}
}

// Frame is the tangent frame of the ellipsoid at an anchor: origin on the
// ellipsoid, x east, y up along its normal, z south.
type Frame struct {
	Latitude  float64 // the anchor, radians
	Longitude float64
	origin    Vec3 // the anchor, earth-centred
	east      Vec3 // the axes, earth-centred
	up        Vec3
	south     Vec3
	turn      Quat // frame to earth-centred
}

// Tangent is the frame anchored at a latitude and longitude, radians.
func Tangent(latitude, longitude float64) Frame {
	sinφ, cosφ := math.Sincos(latitude)
	sinλ, cosλ := math.Sincos(longitude)
	east := Vec3{X: -sinλ, Y: cosλ}
	up := Vec3{X: cosφ * cosλ, Y: cosφ * sinλ, Z: sinφ}
	south := east.Cross(up) // east × up points south
	return Frame{
		Latitude:  latitude,
		Longitude: longitude,
		origin:    Centred(Place{Latitude: latitude, Longitude: longitude}),
		east:      east,
		up:        up,
		south:     south,
		turn:      columns(east, up, south),
	}
}

// columns is the rotation taking the unit axes x, y, z to a, b and c.
func columns(a, b, c Vec3) Quat {
	m00, m01, m02 := a.X, b.X, c.X
	m10, m11, m12 := a.Y, b.Y, c.Y
	m20, m21, m22 := a.Z, b.Z, c.Z
	var q Quat
	if t := m00 + m11 + m22; t > 0 {
		r := math.Sqrt(1 + t)
		h := 0.5 / r
		q = Quat{0.5 * r, (m21 - m12) * h, (m02 - m20) * h, (m10 - m01) * h}
	} else if m00 >= m11 && m00 >= m22 {
		r := math.Sqrt(1 + m00 - m11 - m22)
		h := 0.5 / r
		q = Quat{(m21 - m12) * h, 0.5 * r, (m01 + m10) * h, (m02 + m20) * h}
	} else if m11 >= m22 {
		r := math.Sqrt(1 - m00 + m11 - m22)
		h := 0.5 / r
		q = Quat{(m02 - m20) * h, (m01 + m10) * h, 0.5 * r, (m12 + m21) * h}
	} else {
		r := math.Sqrt(1 - m00 - m11 + m22)
		h := 0.5 / r
		q = Quat{(m10 - m01) * h, (m02 + m20) * h, (m12 + m21) * h, 0.5 * r}
	}
	return q.Normalize()
}

// Set reports whether the frame was built by Tangent: the zero Frame is none.
func (f Frame) Set() bool { return f.up != (Vec3{}) }

// Same reports whether two frames are anchored at the same place.
func (f Frame) Same(o Frame) bool {
	return f.Latitude == o.Latitude && f.Longitude == o.Longitude
}

// Centred is the earth-centred position of a point given in the frame.
func (f Frame) Centred(p Vec3) Vec3 {
	return f.origin.Add(f.east.Scale(p.X)).Add(f.up.Scale(p.Y)).Add(f.south.Scale(p.Z))
}

// Local is the frame's coordinates of an earth-centred point.
func (f Frame) Local(e Vec3) Vec3 {
	d := e.Subtract(f.origin)
	return Vec3{X: d.Dot(f.east), Y: d.Dot(f.up), Z: d.Dot(f.south)}
}

// Place is the geodetic position of a point given in the frame.
func (f Frame) Place(p Vec3) Place { return Geodetic(f.Centred(p)) }

// Position is the frame's coordinates of a place.
func (f Frame) Position(p Place) Vec3 { return f.Local(Centred(p)) }

// Altitude is a point's height above the ellipsoid, metres.
func (f Frame) Altitude(p Vec3) float64 { return f.Place(p).Altitude }

// Level is the tangent frame under a point given in this frame: its own up,
// east and south.
func (f Frame) Level(p Vec3) Frame {
	place := f.Place(p)
	return Tangent(place.Latitude, place.Longitude)
}

// Survey is a point's place and the tangent frame under it, from one
// conversion.
func (f Frame) Survey(p Vec3) (Place, Frame) {
	place := f.Place(p)
	return place, Tangent(place.Latitude, place.Longitude)
}

// Sea is the point of the sea (the ellipsoid) straight down the frame's own up
// from (x, 0, z), in the frame, and the rotation from the tangent frame there
// to this frame's axes: how a ship placed by its chart position sits on the
// curved water. Closed form, no trigonometry: stretched along the axis the
// ellipsoid is a sphere, and the line meets it at a root of a quadratic.
func (f Frame) Sea(x, z float64) (Vec3, Quat) {
	start := f.origin.Add(f.east.Scale(x)).Add(f.south.Scale(z))
	c := Vec3{X: start.X, Y: start.Y, Z: start.Z * oblateness}
	u := Vec3{X: f.up.X, Y: f.up.Y, Z: f.up.Z * oblateness}
	a, b, k := u.Dot(u), c.Dot(u), c.Dot(c)-Equator*Equator
	y := -k / (b + math.Sqrt(math.Max(b*b-a*k, 0))) // the root by the surface, written to keep its precision when it is small
	e := start.Add(f.up.Scale(y))
	normal := Vec3{X: e.X, Y: e.Y, Z: e.Z * oblateness * oblateness}.Normalize()
	east := Vec3{X: -normal.Y, Y: normal.X}
	if east.Length() < 1e-12 {
		east = f.east // at a pole east is anything level: the frame's own serves
	}
	east = east.Normalize()
	south := east.Cross(normal)
	axes := columns(Vec3{X: east.Dot(f.east), Y: east.Dot(f.up), Z: east.Dot(f.south)},
		Vec3{X: normal.Dot(f.east), Y: normal.Dot(f.up), Z: normal.Dot(f.south)},
		Vec3{X: south.Dot(f.east), Y: south.Dot(f.up), Z: south.Dot(f.south)})
	return Vec3{X: x, Y: y, Z: z}, axes
}

// Up is the ellipsoid's normal at a point, in the frame's axes.
func (f Frame) Up(p Vec3) Vec3 { return f.Normal(f.Place(p)) }

// Normal is the ellipsoid's normal at a place, in the frame's axes.
func (f Frame) Normal(place Place) Vec3 {
	sinφ, cosφ := math.Sincos(place.Latitude)
	sinλ, cosλ := math.Sincos(place.Longitude)
	normal := Vec3{X: cosφ * cosλ, Y: cosφ * sinλ, Z: sinφ}
	return Vec3{X: normal.Dot(f.east), Y: normal.Dot(f.up), Z: normal.Dot(f.south)}
}

// Rigid is a rigid motion from one frame to another: a point p in the first
// is Turn.Rotate(p) + Move in the second. The zero Rigid is not the identity;
// Identity is.
type Rigid struct {
	Turn Quat
	Move Vec3
}

// Identity is the rigid motion that changes nothing.
var Identity = Rigid{Turn: Quat{W: 1}}

// Between is the rigid motion taking coordinates in one frame to another.
func Between(from, to Frame) Rigid {
	if from.Same(to) {
		return Identity
	}
	turn := to.turn.Conjugate().Multiply(from.turn).Normalize()
	return Rigid{Turn: turn, Move: to.Local(from.origin)}
}

// Plain reports whether the motion is the identity, exactly: the fast path
// every host takes in the world frame.
func (r Rigid) Plain() bool { return r == Identity }

// Point carries a point through the motion.
func (r Rigid) Point(p Vec3) Vec3 {
	if r.Plain() {
		return p
	}
	return r.Turn.Rotate(p).Add(r.Move)
}

// Vector carries a direction or a velocity through the motion.
func (r Rigid) Vector(v Vec3) Vec3 {
	if r.Plain() {
		return v
	}
	return r.Turn.Rotate(v)
}

// Attitude carries a body-to-frame attitude through the motion.
func (r Rigid) Attitude(q Quat) Quat {
	if r.Plain() {
		return q
	}
	return r.Turn.Multiply(q).Normalize()
}

// Inverse is the motion back.
func (r Rigid) Inverse() Rigid {
	if r.Plain() {
		return r
	}
	back := r.Turn.Conjugate()
	return Rigid{Turn: back, Move: back.Rotate(r.Move).Scale(-1)}
}

// Then is this motion followed by another.
func (r Rigid) Then(next Rigid) Rigid {
	if r.Plain() {
		return next
	}
	if next.Plain() {
		return r
	}
	return Rigid{Turn: next.Turn.Multiply(r.Turn).Normalize(), Move: next.Turn.Rotate(r.Move).Add(next.Move)}
}

// Clear reports whether the straight line between two points of the frame
// passes above the sea everywhere: the earth's bulge hides a low jet from a
// radar, a seeker or an eye a long way off. Exact for the ellipsoid: stretched
// along its axis by Equator/pole it is a sphere of radius Equator, and a
// straight line stays straight.
func (f Frame) Clear(a, b Vec3) bool {
	p, q := f.Centred(a), f.Centred(b)
	p.Z *= oblateness
	q.Z *= oblateness
	d := q.Subtract(p)
	length := d.Dot(d)
	if length == 0 {
		return p.Dot(p) >= Equator*Equator
	}
	t := clamp(-p.Dot(d)/length, 0, 1) // the nearest the line comes to the centre
	near := p.Add(d.Scale(t))
	return near.Dot(near) >= Equator*Equator
}

// Theatre is the frame an environment's coordinates are in: the world frame
// unless a host has set another.
func (e Environment) Theatre() Frame {
	if e.Frame.Set() {
		return e.Frame
	}
	return Home
}

// Placement is the motion from the environment's frame to the world frame,
// where the map, the carrier, the wakes and the weather are fixed.
func (e Environment) Placement() Rigid {
	if !e.Frame.Set() || e.Frame.Same(Home) {
		return Identity
	}
	return Between(e.Frame, Home)
}

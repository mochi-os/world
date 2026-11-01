// Mochi world: The earth
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The WGS84 ellipsoid, the geoid, and the frames the simulation works in. The
// earth does not turn (a fighter's Coriolis acceleration is about 0.4% of g,
// and the pilot flies it out without noticing). Sea level is the geoid, mean
// sea level (geoid.go, EGM2008), which stands up to about 107 m off the
// ellipsoid: every altitude is a height above it, as an altimeter's is and as
// the world's elevation data are.
//
// The world is Earth-centred (Earth): state is kept and exchanged as
// Earth-centred, Earth-fixed coordinates, so no place is built into them. A
// tangent frame is laid at an anchor: its origin at sea level there, x east,
// y up along the ellipsoid's normal, z south - the axes the game has always
// used. Every frame is Cartesian, so a straight line, a distance and an angle
// between two points are exact in any of them, however far apart the points
// are; only "up" and "level" belong to one place. So nothing reads altitude or
// up from a frame's axes: whatever needs a horizon works in the tangent frame
// under itself, as a jet's step and a missile's do. Midway is data: the
// anchor its map is charted about (Midway), not the origin of the world.

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
	degree     = math.Pi / 180
)

// Earth is the world frame: Earth-centred, Earth-fixed coordinates, x through
// the equator at Greenwich, y through the equator at 90 E, z through the north
// pole. The server keeps its state in it, the wire carries it, and an
// environment that names no frame is in it.
var Earth = Frame{origin: Vec3{}, east: Vec3{X: 1}, up: Vec3{Y: 1}, south: Vec3{Z: 1}, turn: Quat{W: 1}, centre: true}

// Midway is the tangent frame at the centre of Midway Atoll (28.2334° N,
// 177.3668° W), the point its map is projected about
// (apps/air/midway-prep/prep.py): the frame the map, the carrier's track, the
// wakes and the weather's pattern are charted in unless an environment names
// another (Environment.Chart).
var Midway = Tangent(28.2334*degree, -177.3668*degree)

// Place is a geodetic position: latitude and longitude in radians, altitude
// in metres above sea level (the geoid).
type Place struct {
	Latitude  float64
	Longitude float64
	Altitude  float64
}

// Geoid is the height of mean sea level above the ellipsoid at a latitude and
// longitude, radians: EGM2008 at whole degrees (geoid.go), bilinear between.
func Geoid(latitude, longitude float64) float64 {
	if math.IsNaN(latitude) || math.IsNaN(longitude) || math.IsInf(latitude, 0) || math.IsInf(longitude, 0) {
		return 0 // a place that is not one: a diverged state reads the ellipsoid, and the host's checks catch it
	}
	row := (math.Pi/2 - latitude) / degree // 0 at 90 N
	column := math.Mod(longitude/degree+180, 360)
	if column < 0 {
		column += 360
	}
	row = clamp(row, 0, 180)
	r, c := math.Floor(row), math.Floor(column)
	if r >= 180 {
		r = 179
	}
	if c >= 360 {
		c = 359
	}
	u, v := row-r, column-c
	i := int(r)*361 + int(c)
	a, b := float64(geoid[i]), float64(geoid[i+1])
	d, e := float64(geoid[i+361]), float64(geoid[i+362])
	return ((a*(1-v)+b*v)*(1-u) + (d*(1-v)+e*v)*u) / 10
}

// Centred is the earth-centred, earth-fixed position of a place: x through the
// equator at Greenwich, z through the north pole.
func Centred(p Place) Vec3 {
	sin, cos := math.Sincos(p.Latitude)
	normal := Equator / math.Sqrt(1-eccentric*sin*sin) // prime vertical radius of curvature
	h := p.Altitude + Geoid(p.Latitude, p.Longitude)   // above the ellipsoid
	return Vec3{
		X: (normal + h) * cos * math.Cos(p.Longitude),
		Y: (normal + h) * cos * math.Sin(p.Longitude),
		Z: (normal*(1-eccentric) + h) * sin,
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
	latitude, longitude := math.Atan2(z+second*lift, p), math.Atan2(e.Y, e.X)
	return Place{
		Latitude:  latitude,
		Longitude: longitude,
		Altitude:  u*(1-b*b/(a*v)) - Geoid(latitude, longitude),
	}
}

// Frame is a Cartesian frame of the earth: Earth itself, or the tangent frame
// at an anchor, origin at sea level there, x east, y up along the ellipsoid's
// normal, z south.
type Frame struct {
	Latitude  float64 // the anchor, radians (a tangent frame's)
	Longitude float64
	origin    Vec3 // the anchor, earth-centred
	east      Vec3 // the axes, earth-centred
	up        Vec3
	south     Vec3
	turn      Quat // frame to earth-centred
	centre    bool // Earth: its origin the earth's centre, its axes the earth's own
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

// Set reports whether the frame is one: the zero Frame is none.
func (f Frame) Set() bool { return f.up != (Vec3{}) }

// Same reports whether two frames are one: the same origin and axes.
func (f Frame) Same(o Frame) bool {
	return f.centre == o.centre && f.origin == o.origin && f.turn == o.turn
}

// Tangent reports whether the frame is a tangent frame, laid at an anchor.
func (f Frame) Tangent() bool { return f.Set() && !f.centre }

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

// Altitude is a point's height above sea level, metres.
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

// Sea is the point of the sea straight down the frame's own up from (x, 0, z),
// in the frame, and the rotation from the tangent frame there to this frame's
// axes: how a ship placed by her chart position sits on the curved water.
// Newton steps along the frame's up find sea level, until a step moves under
// a tenth of a millimetre: one or two near the anchor, four at 5,000 km out,
// where three left it 3 m off.
func (f Frame) Sea(x, z float64) (Vec3, Quat) {
	at := Vec3{X: x, Z: z}
	place := f.Place(at)
	for step := 0; step < 8; step++ {
		fall := place.Altitude / math.Max(f.Normal(place).Y, 0.1) // the height falls along the normal, the step runs along the frame's up
		at.Y -= fall
		place = f.Place(at)
		if math.Abs(fall) < 1e-4 {
			break
		}
	}
	return at, Between(Tangent(place.Latitude, place.Longitude), f).Turn
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

// Theatre is the frame an environment's coordinates are in: Earth unless a
// host has set another.
func (e Environment) Theatre() Frame {
	if e.Frame.Set() {
		return e.Frame
	}
	return Earth
}

// Charted is the frame the environment's map is charted in, where the
// carrier's track, the wakes and the weather's pattern are fixed too: Midway
// unless a host has set another.
func (e Environment) Charted() Frame {
	if e.Chart.Set() {
		return e.Chart
	}
	return Midway
}

// Placement is the motion from the environment's frame to its chart.
func (e Environment) Placement() Rigid {
	return Between(e.Theatre(), e.Charted())
}

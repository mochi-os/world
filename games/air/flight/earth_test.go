// Mochi world: The earth's tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// wind is the air at a point of the environment's frame, the carrier's burble
// included when a carrier is given: Wind as a jet's own step meets it.
func wind(position Vec3, time float64, env Environment, carrier *Carrier) Vec3 {
	theatre := env.Theatre()
	place, level := theatre.Survey(position)
	back := Between(level, theatre)
	return back.Vector(field(env.Placement().Point(position), place.Altitude, time, env, carrier, back.Then(env.Placement())))
}

// heading is the carrier's heading at a session time.
func heading(c *Carrier, t float64) float64 {
	_, h := c.At(t)
	return h
}

// places spans the earth: Midway, the equator at Greenwich, both sides of
// the antimeridian, the poles and their neighbourhood, the Mariana Trench's
// depth and a missile's ceiling.
var places = []Place{
	{Latitude: 28.2072 * degree, Longitude: -177.3735 * degree, Altitude: 0},
	{Latitude: 0, Longitude: 0, Altitude: 0},
	{Latitude: 0, Longitude: math.Pi, Altitude: 12000},
	{Latitude: -0.3, Longitude: -math.Pi + 1e-9, Altitude: 300},
	{Latitude: math.Pi / 2, Longitude: 0, Altitude: 9000},
	{Latitude: -math.Pi / 2, Longitude: 2, Altitude: 50},
	{Latitude: math.Pi/2 - 1e-7, Longitude: 1, Altitude: 15000},
	{Latitude: 0.7, Longitude: 2.5, Altitude: -10900},
	{Latitude: -0.9, Longitude: -1.2, Altitude: 100000},
}

// TestGeodetic: a place survives the trip to earth-centred coordinates and
// back to well under a millimetre, the WGS84 axes come out where they are
// published, and a point straight above a place reads that place, higher.
func TestGeodetic(t *testing.T) {
	for _, p := range places {
		back := Geodetic(Centred(p))
		if math.Abs(back.Latitude-p.Latitude) > 1e-11 || math.Abs(back.Altitude-p.Altitude) > 1e-6 ||
			(math.Abs(p.Latitude) < math.Pi/2-1e-6 && math.Abs(math.Remainder(back.Longitude-p.Longitude, 2*math.Pi)) > 1e-11) {
			t.Errorf("%+v came back %+v", p, back)
		}
	}
	if e := Centred(Place{}); math.Abs(e.X-6378137) > 1e-6 || math.Abs(e.Y) > 1e-6 || math.Abs(e.Z) > 1e-6 {
		t.Errorf("the equator at Greenwich is %v, want (6378137, 0, 0)", e)
	}
	if e := Centred(Place{Latitude: math.Pi / 2}); math.Abs(e.Z-6356752.314245) > 1e-5 || math.Abs(e.X)+math.Abs(e.Y) > 1e-6 {
		t.Errorf("the north pole is %v, want (0, 0, 6356752.3142)", e)
	}
}

// TestTangent: a frame's axes are east, the ellipsoid's normal and south,
// at right angles; its origin lies on the sea; a point 5 km above the anchor
// reads (0, 5000, 0), one a little north lies toward -z and one a little
// east toward +x; and coming back through Between is exact to the last bits.
func TestTangent(t *testing.T) {
	for _, p := range places[:7] {
		f := Tangent(p.Latitude, p.Longitude)
		if math.Abs(f.east.Dot(f.up))+math.Abs(f.up.Dot(f.south))+math.Abs(f.south.Dot(f.east)) > 1e-12 ||
			math.Abs(f.east.Length()-1)+math.Abs(f.up.Length()-1)+math.Abs(f.south.Length()-1) > 1e-12 {
			t.Fatalf("at %+v the axes are not orthonormal", p)
		}
		if h := Geodetic(f.origin).Altitude; math.Abs(h) > 1e-6 {
			t.Errorf("at %+v the origin is %.3g m off the sea", p, h)
		}
		above := f.Position(Place{Latitude: p.Latitude, Longitude: p.Longitude, Altitude: 5000})
		if above.Subtract(Vec3{Y: 5000}).Length() > 1e-6 {
			t.Errorf("at %+v 5 km up reads %v", p, above)
		}
		if math.Abs(p.Latitude) < 1.5 {
			north := f.Position(Place{Latitude: p.Latitude + 1e-4, Longitude: p.Longitude})
			east := f.Position(Place{Latitude: p.Latitude, Longitude: p.Longitude + 1e-4})
			if north.Z > -600 || math.Abs(north.X) > 1 || east.X < 100 || math.Abs(east.Z) > 1 {
				t.Errorf("at %+v a step north reads %v and east %v", p, north, east)
			}
		}
		there := Tangent(-p.Latitude/2+0.1, p.Longitude+2)
		r := Between(f, there)
		q := Vec3{X: 1234.5, Y: 8000, Z: -4321}
		if back := r.Inverse().Point(r.Point(q)); back.Subtract(q).Length() > 1e-6 {
			t.Errorf("at %+v the round trip moved a point to %v", p, back)
		}
		if got, want := r.Point(q), there.Local(f.Centred(q)); got.Subtract(want).Length() > 1e-6 {
			t.Errorf("at %+v Between puts a point at %v, the earth at %v", p, got, want)
		}
	}
	if !Between(Home, Home).Plain() || !(Environment{}).Placement().Plain() || !(Environment{Frame: Home}).Placement().Plain() {
		t.Error("the world frame is not the identity on itself")
	}
}

// TestSea: the point Sea finds lies on the ellipsoid, straight down the
// frame's own up, and the frame it returns there is level on the water;
// near the anchor it is the flat world, dropping as the square of the
// distance - 785 m a hundred kilometres out.
func TestSea(t *testing.T) {
	for _, c := range []struct{ x, z float64 }{{0, 0}, {40000, -25000}, {-100000, 0}, {700000, 900000}} {
		point, level := Home.Sea(c.x, c.z)
		if point.X != c.x || point.Z != c.z || math.Abs(Home.Altitude(point)) > 1e-6 {
			t.Errorf("(%g, %g): the sea is at %v, altitude %.3g m", c.x, c.z, point, Home.Altitude(point))
		}
		if up := level.Rotate(Vec3{Y: 1}); up.Subtract(Home.Up(point)).Length() > 1e-9 {
			t.Errorf("(%g, %g): level up %v, the ellipsoid's normal %v", c.x, c.z, up, Home.Up(point))
		}
	}
	if point, _ := Home.Sea(100000, 0); point.Y > -780 || point.Y < -790 {
		t.Errorf("the sea 100 km east of Midway is %.1f m below the level line, want about 785", -point.Y)
	}
}

// TestClear: the earth's bulge hides a low jet a long way off and nothing
// close; two jets high enough see each other across hundreds of kilometres.
func TestClear(t *testing.T) {
	at := func(d, h float64) Vec3 { // h above the sea d metres east along the surface
		point, level := Home.Sea(d, 0)
		return point.Add(level.Rotate(Vec3{Y: h}))
	}
	for _, c := range []struct {
		d, low, high float64
		clear        bool
	}{
		{20000, 100, 100, true},     // the horizon from 100 m is 35.7 km
		{70000, 100, 100, true},     // just inside twice that
		{75000, 100, 100, false},    // just past it
		{380000, 10000, 100, true},  // a high radar sees 357 km, a jet on the waves 36 km more
		{420000, 10000, 100, false}, // and no further
		{700000, 10000, 10000, true},
	} {
		if got := Home.Clear(at(0, c.low), at(c.d, c.high)); got != c.clear {
			t.Errorf("%.0f m and %.0f m, %.0f km apart: clear %v, want %v", c.low, c.high, c.d/1000, got, c.clear)
		}
	}
}

// handsoff is a trimmed jet's height above the sea and its pitch against the
// level where it is, a second at a time, flown hands off for two minutes at
// spawn in the frame given: the same flight should read the same anywhere.
func handsoff(frame Frame, spawn Vec3, east Vec3) (heights []float64, pitches []float64) {
	m := New(Fighter, Environment{Frame: frame}, World{})
	m.State = Level(m, spawn, east, 220, 3000)
	for second := 0; second < 120; second++ {
		for i := 0; i < 240; i++ {
			m.Step(Inputs{Throttle: lever(m.State.Engine[0].Spool)})
		}
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		heights = append(heights, m.Altitude())
		pitches = append(pitches, math.Asin(clamp(forward.Dot(m.Up()), -1, 1)))
	}
	return heights, pitches
}

// TestFlightAnywhere: two minutes of hands-off flight read the same at
// Midway, over the north pole, across the antimeridian and in the world
// frame two thousand kilometres from Midway, where its y axis leans 18
// degrees off the jet's up: gravity is the sea's normal wherever the jet is.
func TestFlightAnywhere(t *testing.T) {
	base, pitch := handsoff(Home, Vec3{Y: 5000}, Vec3{X: 1})
	far, _ := Home.Sea(2000000, -500000)
	_, level := Home.Sea(2000000, -500000)
	for name, c := range map[string]struct {
		frame       Frame
		spawn, east Vec3
	}{
		"the pole":         {Tangent(math.Pi/2-1e-5, 0.3), Vec3{Y: 5000}, Vec3{X: 1}},
		"the antimeridian": {Tangent(-0.2, math.Pi-1e-6), Vec3{Y: 5000}, Vec3{X: 1}},
		"2,000 km out":     {Home, far.Add(level.Rotate(Vec3{Y: 5000})), level.Rotate(Vec3{X: 1})},
	} {
		heights, pitches := handsoff(c.frame, c.spawn, c.east)
		for k := range heights {
			if math.Abs(heights[k]-base[k]) > 0.5 || math.Abs(pitches[k]-pitch[k]) > 2e-4 {
				t.Fatalf("%s, %d s: %.2f m and %.4f rad against %.2f m and %.4f rad at Midway", name, k+1, heights[k], pitches[k], base[k], pitch[k])
			}
		}
	}
	if span := base[len(base)-1] - base[0]; math.Abs(span) > 150 {
		t.Errorf("the trimmed jet wandered %.0f m in two minutes: the case is not level flight", span)
	}
}

// TestNoWrap: a jet flown 300 km east of Midway is 300 km east of it, still
// near its height above the sea, which has fallen 7 km below the level line
// it started on: a jet that flew that line would be 13 km up.
func TestNoWrap(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.Interval = 1.0 / 60
	m.State = Level(m, Vec3{Y: 6000}, Vec3{X: 1}, 250, 6000)
	m.Environment.Cheat.Fuel = true
	for m.State.Position.X < 300000 && m.State.Time < 2000 {
		m.Step(Inputs{Throttle: lever(m.State.Engine[0].Spool)})
	}
	if m.State.Position.X < 300000 || math.Abs(m.State.Position.Z) > 20000 {
		t.Fatalf("after %.0f s the jet is at %v", m.State.Time, m.State.Position)
	}
	if h := m.Altitude(); math.Abs(h-6000) > 600 {
		t.Errorf("300 km out the jet is %.0f m above the sea, having started at 6,000", h)
	}
	if sea, _ := Home.Sea(m.State.Position.X, m.State.Position.Z); sea.Y > -7000 {
		t.Errorf("300 km out the sea is %.0f m below Midway's level line, want about 7,000", -sea.Y)
	}
}

// TestReframedFlight: a jet whose frame moves under it every 4 km, as the
// client's does, flies the path of one left in the world frame: ten minutes
// west, 34 moves, ending at the same place, speed and attitude, in still air
// and in a gusty trade wind laid in the world's axes. The frame is
// bookkeeping; a move that turned, rolled or slowed the jet would show here.
func TestReframedFlight(t *testing.T) {
	for _, weather := range []Environment{{}, {Seed: 7, Wind: Vec3{X: -12.1, Z: 4.4}, Turbulence: 1.5}} {
		reframed(t, weather)
	}
}

func reframed(t *testing.T, weather Environment) {
	fly := func(follow bool) (State, int) {
		m := New(Fighter, weather, World{})
		m.State = Level(m, Vec3{Y: 6000}, Vec3{X: -1}, 230, 4000)
		throttle := lever(m.State.Engine[0].Spool) // the power the trim solved for
		moves := 0
		for i := 0; i < 600*240; i++ {
			m.Step(Inputs{Throttle: throttle})
			if p := m.State.Position; follow && p.X*p.X+p.Z*p.Z > 4000*4000 {
				m.Reframe(m.Environment.Theatre().Level(p))
				moves++
			}
		}
		r := Between(m.Environment.Theatre(), Home)
		s := m.State
		s.Position, s.Velocity, s.Attitude = r.Point(s.Position), r.Vector(s.Velocity), r.Attitude(s.Attitude)
		return s, moves
	}
	still, _ := fly(false)
	moved, moves := fly(true)
	if moves < 30 {
		t.Fatalf("in %+v the frame moved %d times in ten minutes at 230 m/s", weather, moves)
	}
	if d := moved.Position.Subtract(still.Position).Length(); d > 0.01 {
		t.Errorf("in %+v, after %d moves of its frame the jet is %.4f m from the one left in the world frame (%v against %v)", weather, moves, d, moved.Position, still.Position)
	}
	if d := moved.Velocity.Subtract(still.Velocity).Length(); d > 1e-4 {
		t.Errorf("in %+v, after %d moves of its frame the jet's velocity differs by %.6f m/s", weather, moves, d)
	}
	for _, axis := range []Vec3{{X: 1}, {Y: 1}, {Z: 1}} {
		if d := moved.Attitude.Rotate(axis).Subtract(still.Attitude.Rotate(axis)).Length(); d > 1e-7 {
			t.Errorf("in %+v, after %d moves of its frame the jet's %v axis is %.3e rad from the other's", weather, moves, axis, d)
		}
	}
}

// TestReframe: a jet moved to another frame and back is where it was, and
// what it reads - its altitude, Mach, airspeed, alpha and sideslip in a gusty
// wind - does not depend on the frame it is read in.
func TestReframe(t *testing.T) {
	m := New(Fighter, Environment{Seed: 7, Wind: Vec3{X: -12.1, Z: 4.4}, Turbulence: 1.5}, World{})
	m.State = Level(m, Vec3{X: 30000, Y: 7000, Z: -12000}, Vec3{X: 1, Z: 1}, 230, 4000)
	for i := 0; i < 240; i++ {
		m.Step(Inputs{Throttle: 0.7, Pitch: 0.2})
	}
	before, altitude, mach, cas, alpha, beta := m.State, m.Altitude(), m.Mach(), m.Cas(), m.Alpha(), m.Beta()
	m.Reframe(Tangent(0.4, 2))
	if math.Abs(m.Altitude()-altitude) > 1e-6 || math.Abs(m.Mach()-mach) > 1e-12 || math.Abs(m.Cas()-cas) > 1e-9 {
		t.Errorf("in another frame the jet reads %.6f m, M%.6f, %.6f m/s against %.6f, M%.6f, %.6f", m.Altitude(), m.Mach(), m.Cas(), altitude, mach, cas)
	}
	if math.Abs(m.Alpha()-alpha) > 1e-12 || math.Abs(m.Beta()-beta) > 1e-12 {
		t.Errorf("in another frame the air meets the jet at alpha %.9f, sideslip %.9f against %.9f, %.9f", m.Alpha(), m.Beta(), alpha, beta)
	}
	m.Reframe(Home)
	if m.State.Position.Subtract(before.Position).Length() > 1e-6 || m.State.Velocity.Subtract(before.Velocity).Length() > 1e-9 {
		t.Errorf("back in the world frame the jet is at %v moving %v, was %v moving %v", m.State.Position, m.State.Velocity, before.Position, before.Velocity)
	}
}

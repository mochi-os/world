// Mochi world: Air flight core tests - the carrier's track
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// voyage is a track as the ship's steering writes them: a straight leg from
// the origin on heading 0, a half turn to port starting at `turn`, and a
// straight leg after it - each leg starting where the last one leaves the ship.
func voyage(c *Carrier, speed float64, turn float64) {
	rate := speed / 700
	c.Track = []Leg{{Start: 0, Position: c.Position, Heading: 0, Speed: speed}}
	at, heading := c.At(turn)
	c.Track = append(c.Track, Leg{Start: turn, Position: at, Heading: heading, Speed: speed, Turn: rate})
	end := turn + math.Pi/rate
	at, heading = c.At(end)
	c.Track = append(c.Track, Leg{Start: end, Position: at, Heading: heading, Speed: speed})
}

// TestTrackPose: the pose is continuous across every leg boundary, a turn
// sweeps a circle of radius speed/rate, the reported deck velocity is the
// pose's own rate of change at any deck point, and local and world are each
// other's inverse at every time.
func TestTrackPose(t *testing.T) {
	c := &Carrier{Position: Vec3{X: 120, Y: 19, Z: -40}}
	voyage(c, 15, 30)
	rate := 15.0 / 700
	end := 30 + math.Pi/rate
	for _, boundary := range []float64{30, end} {
		a, ha := c.At(boundary - 1e-7)
		b, hb := c.At(boundary + 1e-7)
		if a.Subtract(b).Length() > 1e-4 || math.Abs(ha-hb) > 1e-6 {
			t.Fatalf("the pose jumps at %.1f s: %.4f m, %.6f rad", boundary, a.Subtract(b).Length(), ha-hb)
		}
	}
	// The turn's centre lies a radius to port of where it starts.
	start, _ := c.At(30)
	centre := start.Add(Vec3{Z: -700})
	for _, t0 := range []float64{40, 80, 120, end - 1} {
		at, heading := c.At(t0)
		if r := math.Hypot(at.X-centre.X, at.Z-centre.Z); math.Abs(r-700) > 1e-6 {
			t.Fatalf("at %.0f s the ship is %.3f m from the turn's centre, want 700", t0, r)
		}
		if want := rate * (t0 - 30); math.Abs(heading-want) > 1e-9 {
			t.Fatalf("at %.0f s heading %.4f, want %.4f", t0, heading, want)
		}
	}
	if _, heading := c.At(end + 50); math.Abs(heading-math.Pi) > 1e-9 {
		t.Fatalf("the turn ends heading %.4f, want π", heading)
	}
	// Velocity: finite differences of a deck point carried by the ship.
	const h = 1e-4
	for _, t0 := range []float64{10, 31, 90, end + 20} {
		for _, deck := range []Vec3{{}, {X: 140, Z: 20}, {X: -150, Z: -25}} {
			p := c.world(deck, t0, Identity)
			moved := c.world(deck, t0+h, Identity).Subtract(c.world(deck, t0-h, Identity)).Scale(1 / (2 * h))
			if v := c.velocity(p, t0, Identity); v.Subtract(moved).Length() > 1e-3 {
				t.Fatalf("at %.0f s deck point %v moves %v, velocity reports %v", t0, deck, moved, v)
			}
			if back := c.local(p, t0, Identity); back.Subtract(deck).Length() > 1e-9 {
				t.Fatalf("local(world(p)) = %v, want %v", back, deck)
			}
		}
	}
	// Gathering way on a straight: the speed climbs at Change and holds once the
	// next leg takes over, the pose and the velocity continuous throughout.
	surge := &Carrier{Position: Vec3{Y: 19}}
	surge.Track = []Leg{{Start: 10, Position: surge.Position, Heading: 0.7, Speed: 2.6, Change: 0.1}}
	end = 10 + (15.4-2.6)/0.1
	at, heading := surge.At(end)
	surge.Track = append(surge.Track, Leg{Start: end, Position: at, Heading: heading, Speed: 15.4})
	if before, _ := surge.At(end - 1e-7); before.Subtract(at).Length() > 1e-4 {
		t.Fatalf("the pose jumps %.4f m where the ship stops gathering way", before.Subtract(at).Length())
	}
	for _, t0 := range []float64{5, 10, 60, end, end + 30} {
		want := 2.6 + 0.1*math.Max(0, math.Min(t0, end)-10)
		if t0 < 10 {
			want = 2.6 + 0.1*(t0-10) // before its first leg the track runs that leg back
		}
		if way := surge.Way(t0); math.Abs(way-want) > 1e-9 {
			t.Fatalf("at %.0f s the ship makes %.3f m/s, want %.3f", t0, way, want)
		}
		p := surge.world(Vec3{X: 50, Z: 10}, t0, Identity)
		moved := surge.world(Vec3{X: 50, Z: 10}, t0+h, Identity).Subtract(surge.world(Vec3{X: 50, Z: 10}, t0-h, Identity)).Scale(1 / (2 * h))
		if v := surge.velocity(p, t0, Identity); v.Subtract(moved).Length() > 1e-3 && t0 != end {
			t.Fatalf("gathering way at %.0f s: the deck moves %v, velocity reports %v", t0, moved, v)
		}
	}
	// Without a track the ship holds its Position, Heading and Speed, as before.
	plain := &Carrier{Position: Vec3{X: 5, Y: 19, Z: 7}, Heading: 0.4, Speed: 3}
	if at, heading := plain.At(10); at.Subtract(Vec3{X: 5 + 30*math.Cos(0.4), Y: 19, Z: 7 - 30*math.Sin(0.4)}).Length() > 1e-9 || heading != 0.4 {
		t.Fatalf("a carrier without a track at %v %.3f", at, heading)
	}
}

// TestRideTheTurn: a jet parked on the deck with the brakes on rides a turning
// ship - it stays on its spot and turns with the deck. Without the turn's
// sweep in the deck velocity the wheels are dragged against the deck by the
// rotation and the jet creeps off its spot.
func TestRideTheTurn(t *testing.T) {
	m := aboard()
	c := m.World.Carrier
	rate := 15.4 / 700
	c.Track = []Leg{{Position: c.Position, Speed: 15.4, Turn: rate}}
	park(m, -30, 5)
	m.State.Velocity = c.velocity(m.State.Position, 0, Identity)
	m.State.Omega = Vec3{Y: rate}
	spot := c.local(m.State.Position, 0, Identity)
	for i := 0; i < 240*60; i++ {
		m.Step(Inputs{Gear: true, Brake: true})
	}
	if !m.State.Gear.Wow {
		t.Fatal("no weight on wheels")
	}
	now := c.local(m.State.Position, m.State.Time, Identity)
	if moved := math.Hypot(now.X-spot.X, now.Z-spot.Z); moved > 0.3 {
		t.Fatalf("the jet crept %.2f m on the deck in a minute's turn (at %.1f, %.1f)", moved, now.X, now.Z)
	}
	nose := m.State.Attitude.Rotate(Vec3{X: 1})
	if off := math.Abs(math.Remainder(math.Atan2(-nose.Z, nose.X)-heading(c, m.State.Time), 2*math.Pi)); off > 1*math.Pi/180 {
		t.Fatalf("the jet's nose is %.2f° off the ship's heading after the turn", off*180/math.Pi)
	}
}

// TestRideTheSurge: a jet parked on the deck with the brakes on rides the
// ship gathering way, from steerage way toward its top speed, without
// creeping off its spot.
func TestRideTheSurge(t *testing.T) {
	m := aboard()
	c := m.World.Carrier
	c.Track = []Leg{{Position: c.Position, Speed: 2.6, Change: 0.1}}
	park(m, -30, 5)
	m.State.Velocity = c.velocity(m.State.Position, 0, Identity)
	spot := c.local(m.State.Position, 0, Identity)
	for i := 0; i < 240*60; i++ {
		m.Step(Inputs{Gear: true, Brake: true})
	}
	now := c.local(m.State.Position, m.State.Time, Identity)
	if moved := math.Hypot(now.X-spot.X, now.Z-spot.Z); moved > 0.3 || !m.State.Gear.Wow {
		t.Fatalf("the jet crept %.2f m on a deck gathering way for a minute (weight on wheels %v)", moved, m.State.Gear.Wow)
	}
	if relative := m.State.Velocity.Subtract(c.velocity(m.State.Position, m.State.Time, Identity)).Length(); relative > 0.05 {
		t.Fatalf("the jet moves %.2f m/s over the deck", relative)
	}
}

// TestIslandRidesTheShip: the island's solids are the ship's, met where the
// ship is at the time asked - not where it was charted.
func TestIslandRidesTheShip(t *testing.T) {
	w := harbor()
	c := w.Carrier
	c.Prisms = []Prism{{Outline: []Vec3{{X: -10, Z: 15}, {X: 10, Z: 15}, {X: 10, Z: 25}, {X: -10, Z: 25}}, Top: 40}}
	c.Posts = []Post{{Position: Vec3{X: 0, Z: 20}, Radius: 1.5, Top: 60}}
	c.Track = []Leg{{Position: c.Position, Speed: 15}}
	for _, probe := range []Vec3{{X: 0, Y: 30, Z: 20}, {X: 0, Y: 50, Z: 20}} {
		if !w.struck(probe, 0, Identity) {
			t.Fatalf("%v misses the island at time zero", probe)
		}
		if w.struck(probe, 10, Identity) {
			t.Fatalf("%v strikes the island ten seconds after the ship has steamed 150 m on", probe)
		}
		if moved := c.world(Vec3{X: probe.X, Z: probe.Z}, 10, Identity); !w.struck(Vec3{X: moved.X, Y: probe.Y, Z: moved.Z}, 10, Identity) {
			t.Fatalf("%v misses the island where the ship has taken it", probe)
		}
	}
	if w.struck(Vec3{X: 5, Y: 45, Z: 20}, 0, Identity) {
		t.Fatal("above the island's top but below the mast's, beside the mast: a strike")
	}
}

// TestKnownIsTheTracksFirstStart: the earliest time the track covers is its
// first leg's start, moving on as the steering drops old legs; a ship with no
// track has held her course from any time.
func TestKnownIsTheTracksFirstStart(t *testing.T) {
	c := &Carrier{Position: Vec3{Y: 19}, Heading: 0.4, Speed: 2.6}
	if known := c.Known(); !math.IsInf(known, -1) {
		t.Fatalf("a ship without a track is known from %v, want -Inf", known)
	}
	voyage(c, 15, 30)
	c.Track[0].Start = -12
	if known := c.Known(); known != -12 {
		t.Fatalf("known from %v, want the first leg's start -12", known)
	}
	c.Track = c.Track[1:]
	if known := c.Known(); known != 30 {
		t.Fatalf("with the first leg dropped, known from %v, want the turn's start 30", known)
	}
}

// TestTrapUnderWay: the trap with the ship steaming at 15 m/s - the same pass
// flown relative to the deck catches a wire and stops the jet on the deck,
// moving with it.
func TestTrapUnderWay(t *testing.T) {
	m := aboard()
	c := m.World.Carrier
	c.Track = []Leg{{Position: c.Position, Speed: 15}}
	approach(m, 0)
	m.State.Position.X = -260 * (70 - 15) / 70.0 // the same time to the wires, closing at 55 m/s
	caught := false
	for i := 0; i < 240*12; i++ {
		throttle := 0.5
		if caught {
			throttle = 0
		}
		m.Step(Inputs{Gear: true, Hook: true, Flap: 2, Throttle: throttle})
		if m.State.Gear.Contact >= 0 {
			t.Fatalf("probe %d fired at %.2f s", m.State.Gear.Contact, float64(i)/240)
		}
		if m.State.Gear.Wire >= 0 {
			caught = true
		}
	}
	if !caught {
		t.Fatal("hook never found a wire")
	}
	relative := m.State.Velocity.Subtract(c.velocity(m.State.Position, m.State.Time, Identity))
	if relative.Length() > 3 {
		t.Fatalf("trap did not stop the jet on the deck: %.1f m/s relative", relative.Length())
	}
	if local := c.local(m.State.Position, m.State.Time, Identity); local.X > 60 || !m.State.Gear.Wow {
		t.Fatalf("rollout past the deck: x=%.0f, weight on wheels %v", local.X, m.State.Gear.Wow)
	}
}

// TestCatapultUnderWay: a shot from a ship steaming at 15 m/s and turning
// leaves the deck at the stroke's end speed relative to it, down the
// catapult's heading as the ship points it at the moment of release.
func TestCatapultUnderWay(t *testing.T) {
	m := aboard()
	c := m.World.Carrier
	rate := 15.0 / 700
	c.Track = []Leg{{Position: c.Position, Speed: 15, Turn: rate}}
	park(m, 42.7, -0.6)
	m.State.Velocity = c.velocity(m.State.Position, 0, Identity)
	m.State.Omega = Vec3{Y: rate}
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true})
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("did not attach under way: %d", m.State.Gear.Catapult)
	}
	m.State.Engine[0] = EngineState{Spool: 1}
	m.State.Engine[1] = EngineState{Spool: 1}
	held := c.local(m.State.Position, m.State.Time, Identity)
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true, Throttle: 1, Reheat: 1})
	}
	if crept := c.local(m.State.Position, m.State.Time, Identity).Subtract(held).Length(); crept > 1 {
		t.Fatalf("crept %.2f m along the deck against the holdback", crept)
	}
	for i := 0; i < 240*6; i++ {
		m.Step(Inputs{Gear: true, Throttle: 1, Reheat: 1, Launch: i > 240})
		if m.State.Gear.Catapult < 0 && i > 240 {
			relative := m.State.Velocity.Subtract(c.velocity(m.State.Position, m.State.Time, Identity))
			track := heading(c, m.State.Time) + c.Catapults[0].Heading
			along := relative.Dot(Vec3{X: math.Cos(track), Z: -math.Sin(track)})
			if along < 60 {
				t.Fatalf("limp shot under way: %.1f m/s along the track relative to the deck", along)
			}
			if side := math.Sqrt(math.Max(relative.Dot(relative)-along*along-relative.Y*relative.Y, 0)); side > 3 {
				t.Fatalf("the shot leaves %.1f m/s across its own track", side)
			}
			return
		}
	}
	t.Fatal("the catapult never released")
}

// TestHeldThroughTheTurn: a jet held on a catapult, unfired, turns with a ship
// in her 30 kt turn and stays on the catapult's line, at the bow and at the
// waist alike: only the turn's own sideways pull, on a tail that scrubs on
// the mains, sets it a little off. The squaring couple damped the jet's own
// yaw rate, which fought the deck's rotation, and held it a degree and more
// behind its line (2.6 degrees in the browser, its tail 0.28 m across).
func TestHeldThroughTheTurn(t *testing.T) {
	for _, cat := range []Catapult{
		{Position: Vec3{X: 48, Z: -0.6}, Heading: 0.028, Stroke: 85, Speed: 88},                      // the bow
		{Position: Vec3{X: -46.61, Z: -17.08}, Heading: 4.03 * math.Pi / 180, Stroke: 85, Speed: 88}, // the waist, aft and off her centre line
	} {
		m := aboard()
		c := m.World.Carrier
		c.Catapults = []Catapult{cat}
		rate := 15.4 / 700
		c.Track = []Leg{{Position: c.Position, Speed: 15.4, Turn: rate}}
		back := cat.Position.Subtract(Vec3{X: 5.3 * math.Cos(cat.Heading), Z: -5.3 * math.Sin(cat.Heading)})
		park(m, back.X, back.Z)
		m.State.Attitude = Quat{W: math.Cos(cat.Heading / 2), Y: math.Sin(cat.Heading / 2)}
		m.State.Velocity = c.velocity(m.State.Position, 0, Identity)
		m.State.Omega = Vec3{Y: rate}
		for i := 0; i < 240*60; i++ {
			m.Step(Inputs{Gear: true})
		}
		if m.State.Gear.Catapult != 0 {
			t.Fatalf("did not stay attached under way at %.0f m: %d", cat.Position.X, m.State.Gear.Catapult)
		}
		nose := m.State.Attitude.Rotate(Vec3{X: 1})
		off := math.Remainder(math.Atan2(-nose.Z, nose.X)-heading(c, m.State.Time)-cat.Heading, 2*math.Pi) * 180 / math.Pi
		if math.Abs(off) > 0.8 {
			t.Fatalf("a minute into the turn the jet held at %.0f m is %.2f° off its catapult's line", cat.Position.X, off)
		}
	}
}

// TestTrimsKeepTheClock: Level and Approach lay a jet down on the clock it
// already has, so a respawn meets the carrier where the match has it.
func TestTrimsKeepTheClock(t *testing.T) {
	m := New(Fighter, Environment{}, World{})
	m.State.Time = 1234.5
	if s := Level(m, Vec3{Y: 1000}, Vec3{X: 1}, 150, 2500); s.Time != 1234.5 {
		t.Fatalf("Level reset the clock to %.1f", s.Time)
	}
	m.State.Time = 987.25
	if s, _ := Approach(m, Vec3{Y: 300}, Vec3{X: 1}, -3.5*math.Pi/180, 2500); s.Time != 987.25 {
		t.Fatalf("Approach reset the clock to %.2f", s.Time)
	}
}

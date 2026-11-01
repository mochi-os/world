// Mochi world: Air carrier steering
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Package ship steers the carrier as a real one is worked: into the wind for
// launches and recoveries, at the speed that makes up the wind over the deck
// the crews want, and back across its operating area when nobody needs the
// deck - its turns and its area always clear of the islands. It writes the
// ship's track (flight.Carrier.Track), which the flight core flies as a pure
// function of time; the world server and single player's wasm core run this
// same code, so both steer the ship alike.
package ship

import (
	"math"

	"world/games/air/flight"
)

const (
	Over     = 14.4          // m/s: the wind over the deck the ship steams for (28 kt)
	Least    = 2.6           // m/s: steerage way (5 kt), however strong the wind
	Most     = 15.4          // m/s: 30 kt, its top speed, into the wind or back across the area
	Radius   = 700.0         // m: its turning circle's radius
	Surge    = 0.1           // m/s²: how fast it gathers or sheds way, about two minutes from steerage way to its top speed
	Reach    = 15000.0       // m: its operating area runs this far up and down the wind from its station
	Overrun  = 6000.0        // m: this far past the area's upwind end the deck closes and the ship turns, recovery or not
	Clear    = 10000.0       // m: the closest its track comes to any coast
	Approach = 25000.0       // m: an aircraft this close with its gear down is coming in to land
	Window   = 300.0         // s: the least open deck a turn back into the wind must leave before the area's end
	Lead     = 1.0           // s: a turn is ordered this far ahead, so every client has it before it starts
	Swing    = 24.0          // s: how long she takes to come to her full rate of turn, and to come off it
	Notches  = 6             // the steps her rate of turn is laid in over Swing (see turn)
	Kept     = 3*Notches + 6 // legs of track kept: a manoeuvre's (way gathered, its turn eased on and off, way shed, the straight out of it) and, behind them, the last one's easing off and full rate, which a client may still be replaying when a jet calls her straight back
)

// Ease is how much further along her area her turn carries her for being eased
// on and off rather than put on at once.
const Ease = Swing * Most / 2

// Ship is a carrier's steering: the station its area is centred on, the
// heading into the wind it works along, the speed that gives the wind over
// the deck, and the side it turns to, away from the islands.
type Ship struct {
	Station flight.Vec3 // the area's centre (Y = deck height)
	Into    float64     // the heading into the wind, rad
	Speed   float64     // its speed into the wind, m/s
	Side    float64     // +1 turns the bow to port (as Heading counts), -1 to starboard
}

// direction is a heading's forward unit vector, as the flight core counts headings.
func direction(heading float64) flight.Vec3 {
	return flight.Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
}

// Plan sets up a match's ship and starts its track at time start: its
// station is the carrier's charted position, moved across the wind until the
// whole racetrack - the area's length up and down the wind, and the turning
// circle's width to one side - stays Clear of every coast; it heads into the
// wind (the charted heading in a calm) at the speed that makes up Over.
func Plan(c *flight.Carrier, wind flight.Vec3, coasts [][]flight.Vec3, start float64) Ship {
	into := c.Heading
	if math.Hypot(wind.X, wind.Z) > 0.5 {
		into = math.Atan2(wind.Z, -wind.X) // bow into the air's motion
	}
	along := direction(into)
	headwind := -(wind.X*along.X + wind.Z*along.Z)
	s := Ship{Into: into, Speed: clamp(Over-headwind, Least, Most), Side: 1}
	port := direction(into + math.Pi/2)
	land := survey(coasts)
	// nearest is how close the racetrack from a station comes to land: the
	// into-wind line, the downwind one the turns offset it to, and between,
	// each a radius past either end, where a turn carries the ship. It stops
	// at the first point closer than floor, the racetrack already failed.
	nearest := func(station flight.Vec3, side float64, floor float64) float64 {
		least := math.Inf(1)
		for k := 0.0; k <= 2; k++ {
			offset := port.Scale(side * Radius * k)
			for f := -Reach - Radius - Ease; f <= Reach+Overrun+Gather+Radius+Ease; f += 250 {
				least = land.distance(station.Add(along.Scale(f)).Add(offset), math.Min(least, math.Max(floor, Clear)))
				if least < floor {
					return least
				}
			}
		}
		return least
	}
	// The least shift across the wind that clears, nearest first; failing
	// every one, the clearest.
	side, shift, found := 1.0, 0.0, false
	shifts := func(each func(k float64, turn float64) bool) {
		for k := 0.0; k <= 60000; k += 1000 {
			for _, sign := range []float64{1, -1} {
				for _, turn := range []float64{1, -1} {
					if each(sign*k, turn) {
						return
					}
				}
			}
		}
	}
	shifts(func(k float64, turn float64) bool {
		if nearest(c.Position.Add(port.Scale(k)), turn, Clear) >= Clear {
			side, shift, found = turn, k, true
		}
		return found
	})
	if !found {
		best := math.Inf(-1)
		shifts(func(k float64, turn float64) bool {
			if clear := nearest(c.Position.Add(port.Scale(k)), turn, math.Inf(-1)); clear > best {
				best, side, shift = clear, turn, k
			}
			return false
		})
	}
	s.Station = c.Position.Add(port.Scale(shift))
	s.Side = side
	c.Track = []flight.Leg{{Start: start, Position: s.Station, Heading: into, Speed: s.Speed}}
	return s
}

// Step steers the ship at time t, given whether anyone needs the deck, and
// reports whether it changed the track. Into the wind the ship holds on to
// the area's upwind end, and turns away there only when nobody needs the
// deck - or, Overrun past it, regardless. Away from the wind it turns back at
// the area's downwind end, or as soon as someone needs the deck and the run
// back to the upwind limit leaves them Window of open deck. Each turn is a
// manoeuvre ordered Lead ahead (turn).
func (s *Ship) Step(c *flight.Carrier, t float64, needed bool) bool {
	if len(c.Track) == 0 {
		return false
	}
	last := c.Track[len(c.Track)-1]
	if last.Start > t || last.Turn != 0 {
		return false // a manoeuvre is ordered or under way
	}
	at, _ := c.At(t)
	along := direction(s.Into)
	position := (at.X-s.Station.X)*along.X + (at.Z-s.Station.Z)*along.Z
	if math.Abs(angle(last.Heading-s.Into)) < 0.01 {
		if (position >= Reach && !needed) || position >= Reach+Overrun {
			s.turn(c, t+Lead, Most)
			return true
		}
		return false
	}
	shed := (Most*Most - s.Speed*s.Speed) / (2 * Surge) // run on upwind, deck closed, while the ship sheds way after the turn back
	if position <= -Reach || (needed && position <= Reach+Overrun-Window*s.Speed-shed) {
		s.turn(c, t+Lead, s.Speed)
		return true
	}
	return false
}

// Closed reports whether the deck is closed at time t: a manoeuvre ordered
// or under way - way gathered, the turn, way shed - or the ship steaming
// away from the wind.
func (s *Ship) Closed(c *flight.Carrier, t float64) bool {
	if len(c.Track) == 0 {
		return false
	}
	last := c.Track[len(c.Track)-1]
	if last.Start > t || last.Turn != 0 {
		return true
	}
	return math.Abs(angle(last.Heading-s.Into)) >= 0.01
}

// Gather is the furthest the ship runs on while gathering way for a turn.
const Gather = (Most*Most - Least*Least) / (2 * Surge)

// turn orders a manoeuvre from time start: way gathered to the ship's top
// speed on the straight it is on - it is clear of the deck's work - a half
// turn on Side, and way shed to the speed given on the straight out of it.
// The ship's speed never jumps, and nor does her rate of turn: it comes on
// over Swing, as her rudder and her swing take hold, and goes off the same
// way, in Notches steps. Put on at once, the deck under a jet a ship's length
// from her pivot jumped a metre a second sideways and threw it across its
// catapult; each step is a sixth of that. Her full rate is held for as much
// less as turns her through the half circle exactly, so she comes out on the
// reciprocal of the heading she went in on, about two of her turning
// circle's radii across the wind.
func (s *Ship) turn(c *flight.Carrier, start float64, after float64) {
	t := start
	at, heading := c.At(t)
	if way := c.Way(t); way < Most {
		c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: way, Change: Surge})
		t += (Most - way) / Surge
		at, heading = c.At(t)
	}
	rate := s.Side * Most / Radius
	ease := func(from, to float64) {
		for k := 0; k < Notches; k++ {
			c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: Most, Turn: from + (to-from)*(float64(k)+0.5)/Notches})
			t += Swing / Notches
			at, heading = c.At(t)
		}
	}
	ease(0, rate)
	c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: Most, Turn: rate})
	t += math.Pi/Most*Radius - Swing
	at, heading = c.At(t)
	ease(rate, 0)
	heading = angle(heading)
	if after < Most {
		c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: Most, Change: -Surge})
		t += (Most - after) / Surge
		at, _ = c.At(t)
	}
	c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: after})
	if len(c.Track) > Kept {
		c.Track = append([]flight.Leg(nil), c.Track[len(c.Track)-Kept:]...)
	}
}

// Hold keeps the ship at a speed on her straight runs, from time t: her speed
// into the wind becomes it, and on the straight she steams at t she takes it
// at once if instant, as single player's speed select is resumed from, and
// otherwise gathers or sheds way to it at Surge from Lead ahead. It reports
// whether it changed the track. A manoeuvre ordered or under way runs its
// course first, turning at her top speed, so a caller holding a speed calls
// it again after each Step. Single player's temporary speed select drives it,
// so her bow wave and wake can be seen at every speed; the steering itself
// never does.
func (s *Ship) Hold(c *flight.Carrier, t float64, speed float64, instant bool) bool {
	s.Speed = speed
	if len(c.Track) == 0 {
		return false
	}
	last := c.Track[len(c.Track)-1]
	if last.Start > t || math.Abs(last.Speed-speed) < 0.01 { // a manoeuvre always ends on a straight
		return false
	}
	if instant {
		at, heading := c.At(t)
		c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: speed})
	} else {
		t += Lead
		at, heading := c.At(t)
		way := c.Way(t)
		c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: way, Change: math.Copysign(Surge, speed-way)})
		t += math.Abs(speed-way) / Surge
		at, _ = c.At(t)
		c.Track = append(c.Track, flight.Leg{Start: t, Position: at, Heading: heading, Speed: speed})
	}
	if len(c.Track) > Kept {
		c.Track = append([]flight.Leg(nil), c.Track[len(c.Track)-Kept:]...)
	}
	return true
}

// Needed reports whether anyone needs the deck at time t: an aircraft on it,
// or one coming in to land - gear down within Approach of the ship. The
// aircraft are in the world frame, where the ship sails.
func Needed(c *flight.Carrier, t float64, aircraft []flight.State) bool {
	at, _ := c.Where(t, flight.Identity)
	for i := range aircraft {
		a := &aircraft[i]
		if c.Aboard(a.Position, t, flight.Identity) {
			return true
		}
		if a.Gear.Extension > 0.5 && a.Position.Subtract(at).Length() < Approach {
			return true
		}
	}
	return false
}

// shore is the coasts the ship keeps clear of, each with its bounding box, so
// that a point far from all of them costs a few comparisons and not a walk of
// every coast's thousands of vertices.
type shore struct {
	coasts [][]flight.Vec3
	boxes  [][4]float64 // least x, least z, most x, most z
}

// survey measures the coasts' boxes.
func survey(coasts [][]flight.Vec3) shore {
	l := shore{coasts: coasts, boxes: make([][4]float64, len(coasts))}
	for i, coast := range coasts {
		box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for _, q := range coast {
			box = [4]float64{math.Min(box[0], q.X), math.Min(box[1], q.Z), math.Max(box[2], q.X), math.Max(box[3], q.Z)}
		}
		l.boxes[i] = box
	}
	return l
}

// distance is how far a point lies from the nearest coast (0 inside one),
// exact while it is less than below; otherwise below itself, which the
// distance is no less than.
func (l *shore) distance(p flight.Vec3, below float64) float64 {
	least := below
	for i, coast := range l.coasts {
		box := l.boxes[i]
		if math.Hypot(math.Max(math.Max(box[0]-p.X, p.X-box[2]), 0), math.Max(math.Max(box[1]-p.Z, p.Z-box[3]), 0)) >= least {
			continue
		}
		n := len(coast)
		if n < 2 {
			continue
		}
		in := false
		for i, j := 0, n-1; i < n; j, i = i, i+1 {
			a, b := coast[i], coast[j]
			if (a.Z > p.Z) != (b.Z > p.Z) && p.X < (b.X-a.X)*(p.Z-a.Z)/(b.Z-a.Z)+a.X {
				in = !in
			}
			ex, ez := b.X-a.X, b.Z-a.Z
			f := 0.0
			if length := ex*ex + ez*ez; length > 0 {
				f = clamp(((p.X-a.X)*ex+(p.Z-a.Z)*ez)/length, 0, 1)
			}
			least = math.Min(least, math.Hypot(p.X-a.X-f*ex, p.Z-a.Z-f*ez))
		}
		if in {
			return 0
		}
	}
	return least
}

// angle wraps an angle into (-π, π].
func angle(a float64) float64 {
	return math.Remainder(a, 2*math.Pi)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

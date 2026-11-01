// Mochi world: Air carrier steering tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package ship

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"world/games/air/flight"
)

// midway is the map the ship sails: its charted carrier and its coasts.
func midway(t *testing.T) (*flight.Carrier, [][]flight.Vec3) {
	t.Helper()
	bytes, err := os.ReadFile("../maps/midway.json")
	if err != nil {
		t.Fatal(err)
	}
	var w flight.World
	if err := json.Unmarshal(bytes, &w); err != nil {
		t.Fatal(err)
	}
	var coasts [][]flight.Vec3
	for _, f := range w.Fields {
		if len(f.Coast) >= 3 {
			coasts = append(coasts, f.Coast)
		}
	}
	if w.Carrier == nil || len(coasts) == 0 {
		t.Fatal("the map carries no carrier or no coast")
	}
	return w.Carrier, coasts
}

// trade is single player's wind: 25 kt from 070, the way the air moves.
var trade = flight.Vec3{X: -12.9 * math.Sin(70*math.Pi/180), Z: 12.9 * math.Cos(70*math.Pi/180)}

// TestPlanIntoTheWind: the ship heads into the wind - its bow into the air's
// motion - at the speed that makes up the wind over the deck, held between
// steerage way and its top speed; in a calm it keeps its charted heading.
func TestPlanIntoTheWind(t *testing.T) {
	for _, test := range []struct {
		name  string
		wind  flight.Vec3
		into  float64
		speed float64
	}{
		{"calm", flight.Vec3{}, 0.349, Over},
		{"trade", trade, 0.349, Least}, // 25 kt over the bow already: steerage way
		{"light westerly", flight.Vec3{X: 5}, math.Pi, Over - 5},
		{"gale from the north", flight.Vec3{Z: 20}, math.Pi / 2, Least},
		{"zephyr from the south", flight.Vec3{Z: -1}, -math.Pi / 2, Over - 1},
	} {
		c, coasts := midway(t)
		s := Plan(c, test.wind, coasts, 0)
		if math.Abs(angle(s.Into-test.into)) > 1e-3 || math.Abs(s.Speed-test.speed) > 1e-6 {
			t.Errorf("%s: heading %.3f at %.2f m/s, want %.3f at %.2f", test.name, s.Into, s.Speed, test.into, test.speed)
		}
		if over := math.Hypot(trade.X, trade.Z); test.name == "trade" && over+s.Speed < Over {
			t.Errorf("trade: %.1f m/s over the deck, under the %.1f wanted", over+s.Speed, Over)
		}
		if len(c.Track) != 1 || c.Track[0].Heading != s.Into || c.Track[0].Speed != s.Speed || c.Track[0].Position != s.Station {
			t.Errorf("%s: the track does not start from the plan: %+v", test.name, c.Track)
		}
	}
}

// sail flies the steering for a span of session time, one Step a second, as
// the world server does, and calls see at every second with the ship's pose
// and whether the deck is closed.
func sail(t *testing.T, s *Ship, c *flight.Carrier, from, span float64, needed func(float64) bool, see func(float64, flight.Vec3, float64, bool)) (turns int) {
	t.Helper()
	for tick := from; tick < from+span; tick++ {
		before := make([]flight.Vec3, 0, 8)
		for back := -30.0; back <= Lead; back += 0.25 {
			at, _ := c.At(tick + back)
			before = append(before, at)
		}
		if s.Step(c, tick, needed(tick)) {
			turns++
			// A track change never rewrites a time a client may already have
			// flown, or be flying a second ahead of the server.
			n := 0
			for back := -30.0; back <= Lead; back += 0.25 {
				if at, _ := c.At(tick + back); at.Subtract(before[n]).Length() > 1e-9 {
					t.Fatalf("the turn ordered at %.0f s moved the ship at %.2f s by %.3f m", tick, tick+back, at.Subtract(before[n]).Length())
				}
				n++
			}
			if len(c.Track) > Kept {
				t.Fatalf("the track holds %d legs", len(c.Track))
			}
		}
		at, heading := c.At(tick)
		see(tick, at, heading, s.Closed(c, tick))
	}
	return turns
}

// TestVoyage: four hours of steering, nobody needing the deck, then four with
// someone needing it all the time, from the charted carrier in the calm and
// in single player's trade wind. The ship never comes within Clear of land,
// keeps to its area, moves no faster than its top speed, gathers and sheds
// way no faster than Surge - a jet parked on its deck is never yanked - and,
// needed all the time, still gives the deck a fair share of the clock rather
// than turning on the spot.
func TestVoyage(t *testing.T) {
	for _, wind := range []flight.Vec3{{}, trade} {
		c, coasts := midway(t)
		s := Plan(c, wind, coasts, 0)
		along := direction(s.Into)
		position := func(at flight.Vec3) float64 {
			return (at.X-s.Station.X)*along.X + (at.Z-s.Station.Z)*along.Z
		}
		last, _ := c.At(0)
		way := c.Way(0)
		land := survey(coasts)
		open, least, lowest, highest := 0, math.Inf(1), math.Inf(1), math.Inf(-1)
		check := func(tick float64, at flight.Vec3, heading float64, closed bool) {
			if step := at.Subtract(last).Length(); step > Most+1e-6 {
				t.Fatalf("the ship jumped %.1f m in the second to %.0f s", step, tick)
			}
			last = at
			if now := c.Way(tick); math.Abs(now-way) > Surge+1e-9 {
				t.Fatalf("the ship's speed jumped from %.2f to %.2f m/s in the second to %.0f s", way, now, tick)
			}
			way = c.Way(tick)
			p := position(at)
			if p < -Reach-Radius-Ease-2*Most || p > Reach+Overrun+Gather+Radius+Ease+2*Most {
				t.Fatalf("at %.0f s the ship is %.0f m along its area, past its ends", tick, p)
			}
			lowest, highest = math.Min(lowest, p), math.Max(highest, p)
			least = land.distance(at, math.Min(least, Clear))
			if !closed {
				open++
			}
		}
		idle := sail(t, &s, c, 0, 4*3600, func(float64) bool { return false }, check)
		if idle < 2 || lowest > -Reach || highest < Reach {
			t.Fatalf("wind %v: %d turns in four idle hours, from %.0f to %.0f m along the area: the ship is not working it", wind, idle, lowest, highest)
		}
		open = 0
		busy := sail(t, &s, c, 4*3600, 4*3600, func(float64) bool { return true }, check)
		if share := float64(open) / (4 * 3600); share < 0.3 {
			t.Fatalf("wind %v: needed all the time, the deck was open %.0f%% of four hours (%d turns)", wind, share*100, busy)
		}
		if least < Clear-50 {
			t.Fatalf("wind %v: the ship came within %.0f m of land", wind, least)
		}
	}
}

// TestHold: a speed held, as single player's temporary select holds it, is
// taken at once when asked for, never moving the ship at a time already
// flown, and is then the speed of every straight run, gathered or shed after
// her turns at her own rate; the turns still run at her top speed, and she
// still works her area.
func TestHold(t *testing.T) {
	c, coasts := midway(t)
	s := Plan(c, trade, coasts, 0)
	const speed = 10.3 // 20 kt
	before, _ := c.At(5)
	if !s.Hold(c, 5, speed, true) || s.Speed != speed {
		t.Fatalf("held from steerage way, the ship planned %.2f m/s", s.Speed)
	}
	if after, _ := c.At(5); after.Subtract(before).Length() > 1e-9 {
		t.Fatalf("taking the speed at once moved her by %.3f m", after.Subtract(before).Length())
	}
	if way := c.Way(5); way != speed {
		t.Fatalf("asked for at once, she made %.2f m/s, not the %.2f held", way, speed)
	}
	if s.Hold(c, 5, speed, true) {
		t.Fatal("held again at the speed she has, the track changed")
	}
	last, _ := c.At(5)
	way, turns := c.Way(5), 0
	for tick := 6.0; tick < 4*3600; tick++ {
		earlier := make([]flight.Vec3, 0, 8)
		for back := -30.0; back <= Lead; back += 0.25 {
			at, _ := c.At(tick + back)
			earlier = append(earlier, at)
		}
		stepped := s.Step(c, tick, false)
		held := s.Hold(c, tick, speed, false)
		if stepped {
			turns++
			if held {
				t.Fatalf("the speed was held at %.0f s over a manoeuvre just ordered", tick)
			}
		}
		n := 0
		for back := -30.0; back <= Lead; back += 0.25 {
			if at, _ := c.At(tick + back); at.Subtract(earlier[n]).Length() > 1e-9 {
				t.Fatalf("the change at %.0f s moved the ship at %.2f s by %.3f m", tick, tick+back, at.Subtract(earlier[n]).Length())
			}
			n++
		}
		if len(c.Track) > Kept {
			t.Fatalf("the track holds %d legs", len(c.Track))
		}
		at, _ := c.At(tick)
		if step := at.Subtract(last).Length(); step > Most+1e-6 {
			t.Fatalf("the ship jumped %.1f m in the second to %.0f s", step, tick)
		}
		last = at
		now := c.Way(tick)
		if math.Abs(now-way) > Surge+1e-9 {
			t.Fatalf("after her turns the ship's speed jumped from %.2f to %.2f m/s in the second to %.0f s", way, now, tick)
		}
		way = now
		if now < speed-1e-9 {
			t.Fatalf("at %.0f s the ship made %.2f m/s, below the %.2f held", tick, now, speed)
		}
		l := c.Track[0]
		for _, leg := range c.Track {
			if leg.Start <= tick {
				l = leg
			}
		}
		if l.Turn != 0 && l.Speed != Most {
			t.Fatalf("at %.0f s the ship turned at %.2f m/s, not her top speed", tick, l.Speed)
		}
		if l.Turn == 0 && l.Change == 0 && tick >= l.Start+Lead+1 && l.Speed != speed {
			t.Fatalf("at %.0f s the ship has steamed %.0f s at %.2f m/s on a straight, not the %.2f held", tick, tick-l.Start, l.Speed, speed)
		}
	}
	if turns < 2 {
		t.Fatalf("%d turns in four idle hours: holding a speed stopped the ship working her area", turns)
	}
}

// TestPlanShiftsClear: the charted carrier's own racetrack runs onto Midway,
// so the plan moves the station across the wind - no further than it must.
func TestPlanShiftsClear(t *testing.T) {
	c, coasts := midway(t)
	charted := c.Position
	s := Plan(c, flight.Vec3{}, coasts, 0)
	shift := s.Station.Subtract(charted).Length()
	if shift == 0 {
		t.Fatal("the station was not moved, though the charted racetrack runs onto Midway")
	}
	port := direction(s.Into + math.Pi/2)
	land := survey(coasts)
	nearest := func(station flight.Vec3, side float64) float64 {
		least := math.Inf(1)
		for k := 0.0; k <= 2; k++ {
			for f := -Reach - Radius - Ease; f <= Reach+Overrun+Gather+Radius+Ease; f += 250 {
				least = land.distance(station.Add(direction(s.Into).Scale(f)).Add(port.Scale(side*Radius*k)), least)
			}
		}
		return least
	}
	if nearest(charted, 1) >= Clear && nearest(charted, -1) >= Clear {
		t.Fatal("the charted racetrack is already clear: this test no longer tests the shift")
	}
	if nearest(s.Station, s.Side) < Clear {
		t.Fatalf("the planned racetrack comes within %.0f m of land", nearest(s.Station, s.Side))
	}
	for k := shift - 1000; k > 0; k -= 1000 {
		for _, sign := range []float64{1, -1} {
			for _, side := range []float64{1, -1} {
				if nearest(charted.Add(port.Scale(sign*k)), side) >= Clear {
					t.Fatalf("a shift of %.0f m already clears, but the plan moved %.0f m", k, shift)
				}
			}
		}
	}
}

// TestCalledBack: steaming away with nobody needing the deck, a jet coming in
// to land turns the ship back into the wind at once - the deck open again a
// half turn later.
func TestCalledBack(t *testing.T) {
	c, coasts := midway(t)
	s := Plan(c, flight.Vec3{}, coasts, 0)
	tick := 0.0
	away := func() bool {
		last := c.Track[len(c.Track)-1]
		return last.Start < tick && math.Abs(angle(last.Heading-s.Into-math.Pi)) < 0.01
	}
	for ; tick < 10000 && !away(); tick++ {
		s.Step(c, tick, false)
	}
	if tick >= 10000 {
		t.Fatal("the idle ship never turned away from the wind")
	}
	tick += 120 // two minutes down the area
	if !s.Closed(c, tick) {
		t.Fatal("the deck is open while the ship steams away from the wind")
	}
	if !s.Step(c, tick, true) {
		t.Fatal("a jet needing the deck did not turn the ship back")
	}
	back := Lead + math.Pi/Most*Radius + Swing + (Most-s.Speed)/Surge // the half turn at speed, eased on and off, then way shed to the recovery's
	if !s.Closed(c, tick+back-1) {
		t.Fatal("the deck opened before the turn back was done")
	}
	if s.Closed(c, tick+back+1) {
		t.Fatal("the deck is still closed once the ship is back into the wind")
	}
	if math.Abs(c.Way(tick+back+1)-s.Speed) > 1e-9 {
		t.Fatalf("back into the wind at %.2f m/s, want the recovery's %.2f", c.Way(tick+back+1), s.Speed)
	}
	if _, heading := c.At(tick + back + 1); math.Abs(angle(heading-s.Into)) > 1e-6 {
		t.Fatalf("after the turn back the ship heads %.3f, not into the wind at %.3f", heading, s.Into)
	}
}

// TestTurnsEase: her rate of turn comes on and goes off in steps, never all at
// once: a step in it jolts the deck under a jet sideways by the step times its
// distance from her pivot, a metre a second at a catapult when the whole turn
// came on at once. She still turns through exactly the half circle, out on the
// reciprocal about two radii across, in Swing more than the circle itself.
func TestTurnsEase(t *testing.T) {
	c, coasts := midway(t)
	s := Plan(c, trade, coasts, 0)
	tick := 0.0
	for ; tick < 10000 && !s.Step(c, tick, false); tick++ {
	}
	if tick >= 10000 {
		t.Fatal("the idle ship never turned")
	}
	rate := Most / Radius
	before := 0.0
	var first, last flight.Leg
	turning := 0
	for _, leg := range c.Track {
		if leg.Start < tick {
			continue
		}
		if step := math.Abs(leg.Turn - before); step > rate/Notches+1e-12 {
			t.Fatalf("at %.0f s her rate of turn steps by %.4f rad/s, more than a %d-th of her full %.4f: %.2f m/s across the deck 160 m from her pivot", leg.Start, step, Notches, rate, step*160)
		}
		before = leg.Turn
		if leg.Turn != 0 {
			if turning == 0 {
				first = leg
			}
			last = leg
			turning++
		}
	}
	if turning != 2*Notches+1 {
		t.Fatalf("%d legs of turn, want %d eased on, her full rate and %d eased off", turning, Notches, Notches)
	}
	if math.Abs(first.Turn) >= rate/Notches || math.Abs(last.Turn) >= rate/Notches {
		t.Fatalf("her turn starts at %.4f and ends at %.4f rad/s, not from and to a fraction of a step", first.Turn, last.Turn)
	}
	end := c.Track[len(c.Track)-1]
	for _, leg := range c.Track {
		if leg.Start > last.Start && leg.Turn == 0 {
			end = leg
			break
		}
	}
	if took := end.Start - first.Start; math.Abs(took-(math.Pi/Most*Radius+Swing)) > 1e-6 {
		t.Fatalf("the turn took %.1f s, want the half circle's %.1f and Swing", took, math.Pi/Most*Radius)
	}
	if off := math.Abs(angle(end.Heading - first.Heading - s.Side*math.Pi)); off > 1e-9 {
		t.Fatalf("she came out %.2e rad off the reciprocal of her heading in", off)
	}
	across := direction(first.Heading + s.Side*math.Pi/2)
	if gone := end.Position.Subtract(first.Position); math.Abs(gone.X*across.X+gone.Z*across.Z-2*Radius) > 0.02*2*Radius {
		t.Fatalf("she came out %.0f m across the wind, not about two radii", gone.X*across.X+gone.Z*across.Z)
	}
}

// TestNeeded: the deck is needed by a jet on it, or one with its gear down
// within Approach of the ship - and by no one else.
func TestNeeded(t *testing.T) {
	c, _ := midway(t)
	at, attitude := c.Where(0, flight.Identity) // the deck where she floats, on the curved sea
	on := flight.State{Position: at.Add(attitude.Rotate(flight.Vec3{X: -30, Y: 2.5}))}
	down := flight.State{Position: at.Add(flight.Vec3{X: 8000, Y: 300})}
	down.Gear.Extension = 1
	up := down
	up.Gear.Extension = 0
	far := down
	far.Position = at.Add(flight.Vec3{X: Approach + 1000, Y: 300})
	for _, test := range []struct {
		name  string
		state flight.State
		want  bool
	}{{"aboard", on, true}, {"gear down inbound", down, true}, {"gear up", up, false}, {"gear down far off", far, false}} {
		if got := Needed(c, 0, []flight.State{test.state}); got != test.want {
			t.Errorf("%s: needed %v, want %v", test.name, got, test.want)
		}
	}
	if Needed(c, 0, nil) {
		t.Error("an empty sky needs the deck")
	}
}

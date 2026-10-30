// Mochi world: AIM-120 tests - the round in the wind
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package round

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// gale is a strong wind along +X, about 30 kt at the surface and 80 kt at
// 6,000 m, where these shots are taken.
var gale = flight.Environment{Seed: 1, Wind: flight.Vec3{X: 15}}

// duel is a shot at 6,000 m: the shooter flying along line at 0.9 Mach through
// the air, the target `apart` metres along line flying `way` (a unit vector)
// at 0.9 Mach through the air, and the environment's wind carrying both.
func duel(environment flight.Environment, line, way flight.Vec3, apart float64) (Target, Target) {
	_, sound := atmosphere(6000)
	shooter := Target{Position: flight.Vec3{Y: 6000}}
	target := Target{Position: shooter.Position.Add(line.Scale(apart))}
	shooter.Velocity = line.Scale(0.9 * sound).Add(flight.Wind(shooter.Position, 0, environment))
	target.Velocity = way.Scale(0.9 * sound).Add(flight.Wind(target.Position, 0, environment))
	return shooter, target
}

// shoot flies one round of the duel at its target flying on through the air,
// and returns its time of flight and Mach at the fuse, or -1 if it never fuses.
func shoot(environment flight.Environment, line, way flight.Vec3, apart float64) (float64, float64) {
	shooter, target := duel(environment, line, way, apart)
	air := target.Velocity.Subtract(flight.Wind(target.Position, 0, environment))
	m := New(shooter.Position, shooter.Velocity, &Target{Position: target.Position, Velocity: target.Velocity}, environment, 0)
	for m.Life > 0 {
		alive, fired, _ := m.Advance(dt, &target, &target)
		if fired {
			return m.Time, m.Mach()
		}
		if !alive {
			break
		}
		target.Velocity = air.Add(flight.Wind(target.Position, m.Time, environment))
		target.Position = target.Position.Add(target.Velocity.Scale(dt))
	}
	return -1, -1
}

var (
	east  = flight.Vec3{X: 1}
	west  = flight.Vec3{X: -1}
	south = flight.Vec3{Z: 1}
)

// shot is one geometry the wind tests fly: the line of sight and the way the
// target flies, named for the reader.
type shot struct {
	name      string
	line, way flight.Vec3
	apart     float64
}

// TestShotFliesTheAir: a round flies through the air, so a shot taken
// downwind, upwind or across the wind - the same jets at the same speeds
// through the air - flies as it does in still air: the wind carries the round
// as it carries the target, and the round leads the target through the air
// they share. Over the ground they did not: the drag braked the wind out of
// the round, a target drifting with the air outran a round fired downwind,
// one fired upwind gained on its target, and a crossing shot led a target
// drifting the way the round was not. Not to the hundredth: the wind grows
// with height, and a round lofting above its target meets more of it, as a
// real one does.
func TestShotFliesTheAir(t *testing.T) {
	for _, s := range []shot{
		{"head-on downwind", east, west, 40000},
		{"head-on upwind", west, east, 40000},
		{"crossing downwind", south, east, 25000},
		{"crossing upwind", south, west, 25000},
	} {
		still, _ := shoot(flight.Environment{Seed: 1}, s.line, s.way, s.apart)
		seconds, mach := shoot(gale, s.line, s.way, s.apart)
		t.Logf("%s: %.2f s to the fuse at Mach %.2f (still air %.2f s)", s.name, seconds, mach, still)
		if still < 0 || seconds < 0 || math.Abs(seconds-still) > 0.5 {
			t.Errorf("the shot %s flew %.2f s to the fuse, in still air %.2f s", s.name, seconds, still)
		}
	}
}

// TestLadderFliesTheAir: the zone flies the round and the target through the
// air the same way, so its ranges are the same downwind and upwind, head-on
// and in a tail chase, and the same as in still air.
func TestLadderFliesTheAir(t *testing.T) {
	for _, s := range []shot{
		{"head-on downwind", east, west, 40000},
		{"head-on upwind", west, east, 40000},
		{"tail chase downwind", east, east, 20000},
		{"tail chase upwind", west, west, 20000},
	} {
		shooter, target := duel(flight.Environment{Seed: 1}, s.line, s.way, s.apart)
		still := Ladder(shooter, target, flight.Environment{Seed: 1}, 0)
		shooter, target = duel(gale, s.line, s.way, s.apart)
		zone := Ladder(shooter, target, gale, 0)
		t.Logf("%s: aero %.0f max %.0f escape %.0f (still %.0f %.0f %.0f)", s.name, zone.Aero, zone.Max, zone.Escape, still.Aero, still.Max, still.Escape)
		for _, rung := range [][3]any{{"aero", zone.Aero, still.Aero}, {"max", zone.Max, still.Max}, {"escape", zone.Escape, still.Escape}} {
			if got, want := rung[1].(float64), rung[2].(float64); math.Abs(got-want) > 0.02*want {
				t.Errorf("the %s rung %s reads %.0f m, in still air %.0f m", rung[0], s.name, got, want)
			}
		}
	}
}

// TestChaffDrifts: a bloom hangs in the air, so the cloud a seduced seeker
// stares at goes where the wind takes it, sinking.
func TestChaffDrifts(t *testing.T) {
	_, sound := atmosphere(6000)
	truth := Target{Position: flight.Vec3{X: 14000, Y: 2000}, Velocity: flight.Vec3{Z: 0.9 * sound}} // beaming, the sea behind him
	m := New(flight.Vec3{Y: 6000}, flight.Vec3{X: 0.9 * sound}, &Target{Position: truth.Position, Velocity: truth.Velocity}, gale, 30)
	m.Phase = Pitbull
	bloom := truth.Position.Add(flight.Vec3{X: -20})
	if !m.Distract(bloom, truth) {
		t.Fatal("the beaming target's chaff did not seduce the seeker")
	}
	want := flight.Wind(bloom, 30, gale).Subtract(gale.Theatre().Up(bloom)) // sinking down the level up there
	if m.Drift.Subtract(want).Length() > 1e-9 || want.X < 10 {
		t.Fatalf("the seduced estimate drifts %v, where the wind carries the sinking cloud %v", m.Drift, want)
	}
}

// TestMachThroughTheAir: the round's Mach is its speed through the air.
func TestMachThroughTheAir(t *testing.T) {
	_, sound := atmosphere(6000)
	at := flight.Vec3{Y: 6000}
	m := New(at, flight.Vec3{X: 2 * sound}.Add(flight.Wind(at, 0, gale)), nil, gale, 0)
	if mach := m.Mach(); math.Abs(mach-2) > 1e-9 {
		t.Fatalf("a round at Mach 2 through a moving air reads Mach %.4f", mach)
	}
}

// TestLethalFliesTheAir: a defender's judgement of a round already in flight
// reads its arrival Mach through the air, so in a tail chase, where the
// arrival speed decides it, the farthest range it calls a round credible is
// the same downwind, upwind and in still air.
func TestLethalFliesTheAir(t *testing.T) {
	edge := func(environment flight.Environment, line flight.Vec3) float64 {
		credible := func(apart float64) bool {
			shooter, target := duel(environment, line, line, apart)
			m := New(shooter.Position, shooter.Velocity, &Target{Position: target.Position, Velocity: target.Velocity}, environment, 0)
			return Lethal(*m, target, 0.2)
		}
		low, high := 2000.0, 80000.0
		if !credible(low) || credible(high) {
			t.Fatalf("no edge between %.0f and %.0f m", low, high)
		}
		for k := 0; k < 14; k++ {
			if mid := (low + high) / 2; credible(mid) {
				low = mid
			} else {
				high = mid
			}
		}
		return low
	}
	still := edge(flight.Environment{Seed: 1}, east)
	for _, line := range []flight.Vec3{east, west} {
		got := edge(gale, line)
		t.Logf("%v: credible out to %.0f m (still air %.0f m)", line, got, still)
		if math.Abs(got-still) > 0.02*still {
			t.Errorf("a round fired along %v is credible out to %.0f m, in still air %.0f m", line, got, still)
		}
	}
}

// TestLeadThroughTheAir: the midcourse leads the target through the air the
// round flies in. Crossing the wind, the round's first turn is the turn it
// makes in still air at the same speeds through the air - not one that leads
// the target's drift with the wind, which carries the round just as far.
func TestLeadThroughTheAir(t *testing.T) {
	_, sound := atmosphere(6000)
	turn := func(environment flight.Environment) flight.Vec3 {
		at, there := flight.Vec3{Y: 6000}, flight.Vec3{Y: 6000, Z: 30000}
		target := Target{Position: there, Velocity: flight.Vec3{X: 50}.Add(flight.Wind(there, 0, environment))}
		m := New(at, flight.Vec3{Z: 1.5 * sound}.Add(flight.Wind(at, 0, environment)), &target, environment, 0)
		m.Loft = false
		m.Fuel = 0 // coasting: the turn and the drag, no thrust
		before := m.Velocity
		m.Step(dt, &target, nil)
		return m.Velocity.Subtract(before).Scale(1 / dt)
	}
	still, blown := turn(flight.Environment{Seed: 1}), turn(gale)
	t.Logf("the first turn across the line: %.1f m/s² in a crosswind, %.1f in still air", blown.X, still.X)
	if still.X < 5 || math.Abs(blown.X-still.X) > 0.1*still.X {
		t.Fatalf("crossing a %.0f m/s wind the round turns at %.1f m/s² toward the target's way, in still air %.1f", flight.Wind(flight.Vec3{Y: 6000}, 0, gale).X, blown.X, still.X)
	}
}

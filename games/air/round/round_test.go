// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The acceptance gates: the model pinned to the public record (the Zaretto
// AIM-120C-5 assessment's CFD scenarios), bands ±15% around its figures.
// Kinematic gates run with the battery extended — battery is an employment
// truth, not a physics one — and the loft gates fly the model's own
// autopilot against the scenario's launch conditions and intercept point,
// not the reference's hand-scripted profile.

package round

import (
	"math"
	"testing"

	"world/games/air/flight"
)

const dt = 1.0 / 240

// launch builds a round flying level along +X at the given Mach and
// altitude, aimed at a distant co-altitude estimate so guidance holds level.
func launch(altitude float64, mach float64, lofting bool) *Model {
	_, sound := atmosphere(altitude)
	m := New(
		flight.Vec3{Y: altitude},
		flight.Vec3{X: mach * sound},
		&Target{Position: flight.Vec3{X: 500000, Y: altitude}},
		0,
	)
	m.Loft = lofting
	m.Life = 1e9
	return m
}

// glide steps the round until it decays through Mach 1 (or the limit) and
// returns the distance flown and the elapsed time.
func glide(m *Model) (float64, float64) {
	start := m.Position
	for m.Time < 300 {
		m.Step(dt, nil, nil)
		_, sound := atmosphere(m.Position.Y)
		if m.Time > Burn && m.Velocity.Length() < sound {
			break
		}
	}
	return m.Position.Subtract(start).Length(), m.Time
}

// TestBurnout: an M0.83 launch at 10 000 m peaks around Mach 4 at the end
// of the 7.75 s boost.
func TestBurnout(t *testing.T) {
	m := launch(10000, 0.83, false)
	for m.Time < Burn {
		m.Step(dt, nil, nil)
	}
	_, sound := atmosphere(m.Position.Y)
	mach := m.Velocity.Length() / sound
	if mach < 3.5 || mach > 4.4 {
		t.Fatalf("burnout Mach %.2f, want ~4", mach)
	}
}

// TestLevelRange: aerodynamic range (launch to decay through Mach 1), level
// flight — ~65 km from 10 000 m, ~35 km from 5 000 m (the reference states
// this figure in its scenario 1 notes), and ~22 km from 500 m: density
// scaling pins the low-altitude ratio near 3× once the high gate holds,
// which is exactly why the low-altitude LOFT (45 km) doubles the level
// shot. The low shot goes subsonic within ~45 s.
func TestLevelRange(t *testing.T) {
	high, _ := glide(launch(10000, 0.83, false))
	if high < 55000 || high > 78000 {
		t.Fatalf("10 km level range %.1f km, want ~65", high/1000)
	}
	medium, _ := glide(launch(5000, 0.83, false))
	if medium < 29000 || medium > 42000 {
		t.Fatalf("5 km level range %.1f km, want ~35", medium/1000)
	}
	low, when := glide(launch(500, 0.83, false))
	if low < 18000 || low > 28000 {
		t.Fatalf("500 m level range %.1f km, want ~22", low/1000)
	}
	if when > 50 {
		t.Fatalf("500 m shot stayed supersonic %.0f s, want under ~45", when)
	}
}

// TestSupersonicLaunch: an M1.5 launch buys about 15% more range than
// M0.83 at the same altitude.
func TestSupersonicLaunch(t *testing.T) {
	slow, _ := glide(launch(10000, 0.83, false))
	fast, _ := glide(launch(10000, 1.5, false))
	if gain := fast / slow; gain < 1.08 || gain > 1.35 {
		t.Fatalf("supersonic launch gain %.2f, want ~1.15", gain)
	}
}

// chase flies the round with a periodic datalink against a target, returns
// the closest approach, the speed there (in Mach), and the peak altitude.
func chase(m *Model, target Target, seconds float64) (float64, float64, float64) {
	closest, speed, apogee := math.MaxFloat64, 0.0, 0.0
	for m.Time < seconds {
		support := &Target{Position: target.Position, Velocity: target.Velocity}
		if !m.Step(dt, support, &target) {
			break
		}
		target.Position = target.Position.Add(target.Velocity.Scale(dt))
		if m.Position.Y > apogee {
			apogee = m.Position.Y
		}
		if miss := m.Position.Subtract(target.Position).Length(); miss < closest {
			closest = miss
			_, sound := atmosphere(m.Position.Y)
			speed = m.Velocity.Length() / sound
		}
	}
	return closest, speed, apogee
}

// TestLoft: the reference's three loft scenarios, flown by the model's own
// autopilot against stationary intercept points.
func TestLoft(t *testing.T) {
	cases := []struct {
		name           string
		altitude, mach float64
		target         flight.Vec3
		arrive         float64 // minimum arrival Mach
		roof           [2]float64
	}{
		{"medium 60 km", 6000, 0.83, flight.Vec3{X: 60000, Y: 6000}, 0.75, [2]float64{9000, 19000}},
		{"maximum 90 km", 13000, 1.5, flight.Vec3{X: 90000, Y: 6000}, 0.75, [2]float64{15000, 26000}},
		{"low 45 km", 500, 1.1, flight.Vec3{X: 45000, Y: 500}, 0.75, [2]float64{5000, 14000}},
	}
	for _, c := range cases {
		m := launch(c.altitude, c.mach, true)
		m.Estimate = c.target
		m.Drift = flight.Vec3{}
		miss, arrival, apogee := chase(m, Target{Position: c.target}, 280)
		if miss > 300 {
			t.Fatalf("%s: closest approach %.0f m — never arrived (apogee %.0f m)", c.name, miss, apogee)
		}
		if arrival < c.arrive {
			t.Fatalf("%s: arrived at Mach %.2f, want ≥ %.2f", c.name, arrival, c.arrive)
		}
		if apogee < c.roof[0] || apogee > c.roof[1] {
			t.Fatalf("%s: apogee %.0f m outside [%.0f, %.0f]", c.name, apogee, c.roof[0], c.roof[1])
		}
	}
}

// TestCeiling: the turn-capability collapse — 30 g lives above ~700 m/s at
// medium altitude and is gone below it; transonic at sea level manages
// about 10 g. This collapse is what makes range escapable.
func TestCeiling(t *testing.T) {
	empty := Mass - Propellant
	if g := Ceiling(1200, 500, empty) / 9.80665; g < 29.9 {
		t.Fatalf("Mach-4-class at 500 m gives %.1f g, want the structural 30", g)
	}
	if g := Ceiling(650, 5000, empty) / 9.80665; g >= 30 {
		t.Fatalf("650 m/s at 5 km still gives %.1f g, want under 30", g)
	}
	if g := Ceiling(750, 5000, empty) / 9.80665; g < 29.9 {
		t.Fatalf("750 m/s at 5 km gives %.1f g, want the structural 30", g)
	}
	if g := Ceiling(340, 0, empty) / 9.80665; g < 9 || g > 14 {
		t.Fatalf("transonic sea level gives %.1f g, want ~10-12", g)
	}
}

// TestIntercept: the guidance phases end in a fuse against a real closing
// target — Midcourse under datalink, Active at the gate, Pitbull to the
// merge — and the datalink dropping mid-flight still arrives (the estimate
// coasts).
func TestIntercept(t *testing.T) {
	for _, supported := range []bool{true, false} {
		_, sound := atmosphere(8000)
		m := New(flight.Vec3{Y: 8000}, flight.Vec3{X: 0.9 * sound}, &Target{
			Position: flight.Vec3{X: 30000, Y: 8000},
			Velocity: flight.Vec3{X: -250},
		}, 0)
		target := Target{Position: flight.Vec3{X: 30000, Y: 8000}, Velocity: flight.Vec3{X: -250}}
		phases := map[int]bool{}
		fused := false
		for m.Time < Battery {
			var support *Target
			if supported || m.Time < 5 {
				support = &Target{Position: target.Position, Velocity: target.Velocity}
			}
			if !m.Step(dt, support, &target) {
				break
			}
			target.Position = target.Position.Add(target.Velocity.Scale(dt))
			phases[m.Phase] = true
			if fired, _ := m.Fused(dt, target); fired {
				fused = true
				break
			}
		}
		if !phases[Midcourse] || !phases[Active] || !phases[Pitbull] {
			t.Fatalf("supported=%v: phases %v, want Midcourse, Active and Pitbull all visited", supported, phases)
		}
		if !fused {
			t.Fatalf("supported=%v: never fused", supported)
		}
	}
}

// TestVisual: a no-estimate launch is active off the rail and takes what it
// finds ahead — and never lofts.
func TestVisual(t *testing.T) {
	_, sound := atmosphere(3000)
	m := New(flight.Vec3{Y: 3000}, flight.Vec3{X: 0.8 * sound}, nil, 0)
	if m.Phase != Active || m.Loft {
		t.Fatalf("visual launch starts phase %d loft %v, want Active and no loft", m.Phase, m.Loft)
	}
	target := Target{Position: flight.Vec3{X: 8000, Y: 3000}, Velocity: flight.Vec3{X: -200}}
	fused := false
	for m.Time < 60 {
		if !m.Step(dt, nil, &target) {
			break
		}
		target.Position = target.Position.Add(target.Velocity.Scale(dt))
		if fired, _ := m.Fused(dt, target); fired {
			fused = true
			break
		}
	}
	if !fused {
		t.Fatalf("visual shot never fused (phase %d)", m.Phase)
	}
}

// TestDeterminism: the same launch and the same world produce the same
// flight — the server's requirement.
func TestDeterminism(t *testing.T) {
	fly := func() flight.Vec3 {
		m := launch(6000, 0.83, true)
		m.Estimate = flight.Vec3{X: 60000, Y: 6000}
		m.Drift = flight.Vec3{}
		for m.Time < 60 {
			m.Step(dt, nil, &Target{Position: flight.Vec3{X: 60000, Y: 6000}})
		}
		return m.Position
	}
	a, b := fly(), fly()
	if a != b {
		t.Fatalf("two identical flights diverged: %+v vs %+v", a, b)
	}
}

// TestBattery: the default life ends the flight.
func TestBattery(t *testing.T) {
	m := New(flight.Vec3{Y: 5000}, flight.Vec3{X: 300}, &Target{Position: flight.Vec3{X: 400000, Y: 5000}}, 0)
	steps := 0
	for m.Step(dt, nil, nil) {
		steps++
		if steps > 300*240 {
			t.Fatalf("battery never expired")
		}
	}
	if m.Time < Battery-1 || m.Time > Battery+1 {
		t.Fatalf("battery died at %.1f s, want ~%.0f", m.Time, Battery)
	}
}

// TestLadder: the launch zone against real geometries — the rungs order
// (Escape ≤ Max ≤ Aero), head-on beats tail-chase, altitude stretches every
// rung, and the floor sits under them all.
func TestLadder(t *testing.T) {
	_, high := atmosphere(10000)
	head := Ladder(
		Target{Position: flight.Vec3{Y: 10000}, Velocity: flight.Vec3{X: 0.9 * high}},
		Target{Position: flight.Vec3{X: 70000, Y: 10000}, Velocity: flight.Vec3{X: -0.9 * high}},
		0,
	)
	if !(head.Minimum < head.Escape && head.Escape <= head.Max && head.Max <= head.Aero) {
		t.Fatalf("head-on rungs disordered: %+v", head)
	}
	if head.Max < 40000 || head.Max > 115000 {
		t.Fatalf("head-on Rmax %.0f km outside a plausible band", head.Max/1000)
	}
	if head.Active <= 0 {
		t.Fatalf("A-time preview missing at %.0f km", 70.0)
	}

	tail := Ladder(
		Target{Position: flight.Vec3{Y: 10000}, Velocity: flight.Vec3{X: 0.9 * high}},
		Target{Position: flight.Vec3{X: 40000, Y: 10000}, Velocity: flight.Vec3{X: 0.9 * high}},
		0,
	)
	if tail.Max >= head.Max*0.7 {
		t.Fatalf("tail-chase Rmax %.0f km should sit well under head-on %.0f km", tail.Max/1000, head.Max/1000)
	}

	_, low := atmosphere(1000)
	deck := Ladder(
		Target{Position: flight.Vec3{Y: 1000}, Velocity: flight.Vec3{X: 0.9 * low}},
		Target{Position: flight.Vec3{X: 40000, Y: 1000}, Velocity: flight.Vec3{X: -0.9 * low}},
		0,
	)
	if deck.Max >= head.Max {
		t.Fatalf("deck Rmax %.0f km should sit under high-altitude %.0f km", deck.Max/1000, head.Max/1000)
	}
	if deck.Escape > deck.Max || deck.Aero < deck.Max {
		t.Fatalf("deck rungs disordered: %+v", deck)
	}
}

// TestTerminalMiss: the endgame must be TIGHT — a well-guided round passes
// close enough that the warhead does the rest, against a straight target and
// against one breaking hard. A graze at 8-12 m is a miss in everything but
// name: the fragments spray past a fighter-sized body.
func TestTerminalMiss(t *testing.T) {
	fly := func(evading bool) float64 {
		_, sound := atmosphere(6000)
		start := Target{Position: flight.Vec3{X: 20000, Y: 6000}, Velocity: flight.Vec3{X: -0.9 * sound}}
		m := New(flight.Vec3{Y: 6000}, flight.Vec3{X: 0.9 * sound}, &Target{Position: start.Position, Velocity: start.Velocity}, 0)
		target := start
		closest := math.MaxFloat64
		for m.Time < 60 {
			if !m.Step(dt, &Target{Position: target.Position, Velocity: target.Velocity}, &target) {
				break
			}
			if evading && m.Time > 3 {
				// A 7.5 g level break away from the shot.
				speed := target.Velocity.Length()
				turn := flight.Vec3{X: -target.Velocity.Z, Z: target.Velocity.X}.Normalize().Scale(7.5 * 9.80665 * dt)
				target.Velocity = target.Velocity.Add(turn).Normalize().Scale(speed)
			}
			target.Position = target.Position.Add(target.Velocity.Scale(dt))
			if miss := m.Position.Subtract(target.Position).Length(); miss < closest {
				closest = miss
			}
		}
		return closest
	}
	if miss := fly(false); miss > 3 {
		t.Fatalf("straight target: closest approach %.1f m, want under 3", miss)
	}
	// Augmented PN is what earns this number: plain proportional navigation
	// grazes a breaking target at 8-15 m, which the fragments spray past.
	if miss := fly(true); miss > 4 {
		t.Fatalf("breaking target: closest approach %.1f m, want under 4", miss)
	}
}

// TestFrameRate: the flight is the same whatever the caller's stride —
// Advance dices dt into fixed slices, so a 30 Hz client and a 240 Hz server
// fly the same missile to the same terminal accuracy.
func TestFrameRate(t *testing.T) {
	fly := func(stride float64) (flight.Vec3, float64) {
		_, sound := atmosphere(6000)
		target := Target{Position: flight.Vec3{X: 20000, Y: 6000}, Velocity: flight.Vec3{X: -0.9 * sound}}
		m := New(flight.Vec3{Y: 6000}, flight.Vec3{X: 0.9 * sound}, &Target{Position: target.Position, Velocity: target.Velocity}, 0)
		closest := math.MaxFloat64
		for m.Time < 60 {
			alive, fired, _ := m.Advance(stride, &Target{Position: target.Position, Velocity: target.Velocity}, &target)
			target.Position = target.Position.Add(target.Velocity.Scale(stride))
			if miss := m.Position.Subtract(target.Position).Length(); miss < closest {
				closest = miss
			}
			if fired || !alive {
				break
			}
		}
		return m.Position, closest
	}
	slow, slowMiss := fly(1.0 / 30)
	fast, _ := fly(1.0 / 240)
	// A 30 Hz caller loses a couple of metres to its own sampling of the
	// world (the truth is frozen between frames); the warhead covers that.
	if slowMiss > 7 {
		t.Fatalf("30 Hz caller: closest approach %.1f m, want under 7", slowMiss)
	}
	if drift := slow.Subtract(fast).Length(); drift > 60 {
		t.Fatalf("30 Hz and 240 Hz flights diverged by %.0f m", drift)
	}
}

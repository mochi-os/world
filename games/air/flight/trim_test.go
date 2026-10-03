// Mochi world: Trim and spawn helper tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// TestLevel: the spawn helper produces flight the FCS holds without a
// transient — a second later the aircraft is still level, near 1g, on speed.
// TestEvaluateAtRest: no flow, no coefficients. The static evaluation at zero
// airspeed once returned 0/0, and the landing law's secant fit carried that
// NaN into the stabilators of every calm-air ground start.
func TestEvaluateAtRest(t *testing.T) {
	m := calm()
	cl, cd := m.Evaluate(0, 0.1, 100)
	if cl != 0 || cd != 0 {
		t.Fatalf("coefficients at zero airspeed must be finite zeros, got %v %v", cl, cd)
	}
	if cl, _ := m.Evaluate(60, 0.1, 100); !(cl > 0) {
		t.Fatalf("a real flow must still give a lift coefficient, got %v", cl)
	}
}

// TestLevelInWind: Level spawns at the true airspeed it is asked for,
// whichever way the wind blows, and the jet holds it. It spawned at that speed
// over the ground, so in the single-player trade wind (62 kt at 15,000 ft) the
// joust's 428 kt start flew 366 kt through the air downwind and 490 kt into it.
func TestLevelInWind(t *testing.T) {
	air := Environment{Seed: 1, Wind: Vec3{X: -12.1, Z: 4.4}, Wrap: 250000} // the single-player trades: 12.9 m/s from 070
	for _, direction := range []Vec3{{X: 1}, {X: -1}, {Z: 1}} {
		m := New(Fighter, air, World{})
		s := Level(m, Vec3{Y: 4572}, direction, 220, 3000)
		if through := s.Velocity.Subtract(wind(s.Position, 0, air, nil)).Length(); math.Abs(through-220) > 0.01 {
			t.Errorf("heading %+.0f,%+.0f: spawned at %.1f m/s through the air, asked for 220", direction.X, direction.Z, through)
		}
		m.State = s
		in := Inputs{Throttle: s.Engine[0].Spool}
		for i := 0; i < 240; i++ {
			m.Step(in)
		}
		if through := m.State.Velocity.Subtract(m.Gust()).Length(); math.Abs(through-220) > 5 {
			t.Errorf("heading %+.0f,%+.0f: %.1f m/s through the air a second after spawn", direction.X, direction.Z, through)
		}
	}
}

func TestLevel(t *testing.T) {
	m := New(Fighter, Environment{Wrap: 250000}, World{})
	s := Level(m, Vec3{Y: 4572}, Vec3{X: 1}, 220, 3000)
	m.State = s
	in := Inputs{Throttle: s.Engine[0].Spool}
	for i := 0; i < 240*3; i++ {
		m.Step(in)
	}
	if math.Abs(m.State.Position.Y-4572) > 60 {
		t.Fatalf("altitude drifted to %.1f", m.State.Position.Y)
	}
	if speed := m.State.Velocity.Length(); math.Abs(speed-220) > 25 {
		t.Fatalf("speed drifted to %.1f", speed)
	}
	if nz := m.State.Fcs.Normal; math.Abs(nz-1) > 0.3 {
		t.Fatalf("load factor %.2f three seconds after spawn", nz)
	}
	// The composed attitude must carry the solved alpha; the tolerances above pass
	// even with the spawn at minus the trimmed alpha.
	spawn := Level(New(Fighter, Environment{Wrap: 250000}, World{}), Vec3{Y: 4572}, Vec3{X: 1}, 220, 3000)
	if held := alpha(spawn.Attitude.Unrotate(spawn.Velocity)); held < 0 {
		t.Fatalf("spawn alpha %.3f° is negative — the trim attitude is inverted", held*180/math.Pi)
	}
	for _, direction := range []Vec3{{X: 1}, {Z: 1}, {X: 0.6, Z: 0.8}} {
		s := Level(New(Fighter, Environment{Wrap: 250000}, World{}), Vec3{Y: 4572}, direction, 220, 3000)
		forward := s.Attitude.Rotate(Vec3{X: 1})
		pitch := math.Asin(clamp(forward.Y, -1, 1))
		if math.Abs(pitch-alpha(s.Attitude.Unrotate(s.Velocity))) > 1e-6 {
			t.Errorf("heading %.1f,%.1f: pitch %.3f° does not equal alpha in level flight", direction.X, direction.Z, pitch*180/math.Pi)
		}
	}
}

// TestLevelLoaded: Level trims the jet as it is loaded and keeps the external
// fuel it weighed, so a pattern entry flown on the solved power holds its speed
// and height. The client mounted the loadout after the trim, and a Case I
// entry lost 14 kt and 31 m in 30 s.
func TestLevelLoaded(t *testing.T) {
	load := Fighter.Default | mask(t, "rail2", "9m2", "rail8", "9m8", "rail4", "120c4", "rail6", "120c6", "pylon5", "tank5")
	m := New(Fighter, Environment{Seed: 1, Wrap: 250000}, World{})
	m.Stores(load)
	s := Level(m, Vec3{Y: 244}, Vec3{X: 1}, 180, 4877)
	if s.External.Centre != 1010 {
		t.Fatalf("the trimmed state carries %.0f kg in the centreline tank the trim weighed full (1,010)", s.External.Centre)
	}
	m.State = s
	in := Inputs{Throttle: (s.Engine[0].Spool - idle) / (1 - idle)}
	for i := 0; i < 240*30; i++ {
		m.Step(in)
	}
	speed, height := m.State.Velocity.Length(), m.State.Position.Y
	t.Logf("thirty seconds on the solved power: %.1f m/s of 180, %.0f m of 244", speed, height)
	if math.Abs(speed-180) > 2.6 || math.Abs(height-244) > 15 {
		t.Fatalf("the loaded jet flew to %.1f m/s and %.0f m from 180 and 244 on the power its trim solved", speed, height)
	}
}

// TestApproachInWind: Approach spawns on-speed through the air whichever way
// the wind blows, as Level does, and the jet stays there. It spawned at its
// on-speed figure over the ground, so the Case II start, level at 1,200 ft into
// the trades the deck faces, began some 35 kt fast and ballooned 500 ft.
func TestApproachInWind(t *testing.T) {
	air := Environment{Seed: 1, Wind: Vec3{X: -12.1, Z: 4.4}, Wrap: 250000} // the single-player trades: 12.9 m/s from 070
	calm := New(Fighter, Environment{Seed: 1, Wrap: 250000}, World{})
	still, _ := Approach(calm, Vec3{Y: 366}, Vec3{X: 1}, 0, 4900)
	want := still.Velocity.Length()
	for _, direction := range []Vec3{{X: 1}, {X: -1}, {Z: 1}} {
		m := New(Fighter, air, World{})
		s, throttle := Approach(m, Vec3{Y: 366}, direction, 0, 4900)
		if through := s.Velocity.Subtract(wind(s.Position, 0, air, nil)).Length(); math.Abs(through-want) > 0.01 {
			t.Errorf("heading %+.0f,%+.0f: spawned at %.1f m/s through the air, on-speed is %.1f", direction.X, direction.Z, through, want)
		}
		m.State = s
		high := s.Position.Y
		for i := 0; i < 240*20; i++ {
			m.Step(Inputs{Throttle: throttle, Gear: true, Flap: 2})
			high = math.Max(high, m.State.Position.Y)
		}
		if through := m.State.Velocity.Subtract(m.Gust()).Length(); math.Abs(through-want) > 3 {
			t.Errorf("heading %+.0f,%+.0f: %.1f m/s through the air twenty seconds after spawn, on-speed is %.1f", direction.X, direction.Z, through, want)
		}
		if climbed := high - s.Position.Y; climbed > 15 {
			t.Errorf("heading %+.0f,%+.0f: ballooned %.0f m above the altitude it was given", direction.X, direction.Z, climbed)
		}
	}
}

// TestApproachLoaded: the trim is of the jet as it is loaded. Its stores'
// weight and drag are in the solution, and the state keeps the external fuel
// the trim weighed, so a loaded jet holds the altitude it was given hands-off.
func TestApproachLoaded(t *testing.T) {
	load := Fighter.Default | mask(t, "rail2", "9m2", "rail8", "9m8", "rail4", "120c4", "rail6", "120c6", "pylon5", "tank5")
	m := New(Fighter, Environment{Seed: 1, Wrap: 250000}, World{})
	m.Stores(load)
	s, throttle := Approach(m, Vec3{Y: 366}, Vec3{X: 1}, 0, 4877)
	if s.External.Centre != 1010 {
		t.Fatalf("the trimmed state carries %.0f kg in the centreline tank the trim weighed full (1,010)", s.External.Centre)
	}
	m.State = s
	low, high := s.Position.Y, s.Position.Y
	for i := 0; i < 240*25; i++ {
		m.Step(Inputs{Throttle: throttle, Gear: true, Flap: 2, Hook: true})
		low, high = math.Min(low, m.State.Position.Y), math.Max(high, m.State.Position.Y)
	}
	if low < s.Position.Y-10 || high > s.Position.Y+10 {
		t.Fatalf("hands-off for 25 s the loaded jet ranged %.0f to %.0f m from the %.0f it was trimmed at", low, high, s.Position.Y)
	}
}

// TestApproach: the approach spawn helper produces a trimmed on-speed descent
// the PA law holds hands-off, so no caller carries hand-measured trim.
func TestApproach(t *testing.T) {
	for _, slope := range []float64{-3.5, -4.0} {
		path := slope * math.Pi / 180
		m := New(Fighter, Environment{Wrap: 250000}, World{})
		s, spool := Approach(m, Vec3{Y: 400}, Vec3{X: 1}, path, 3000)
		m.State = s
		onspeed := m.Airframe.Control.Onspeed * 180 / math.Pi

		entry := s.Velocity.Length()
		sink := -s.Velocity.Y
		low, high := entry, entry
		for i := 0; i < 240*20; i++ {
			m.Step(Inputs{Throttle: spool, Gear: true, Flap: 2})
			speed := m.State.Velocity.Length()
			low, high = math.Min(low, speed), math.Max(high, speed)
		}

		// On-speed: the wing condition is what the helper solves for, so alpha
		// is the assertion that matters — the speed is whatever the weight makes it.
		v := m.State.Attitude.Unrotate(m.State.Velocity)
		if held := alpha(v) * 180 / math.Pi; math.Abs(held-onspeed) > 0.6 {
			t.Errorf("%.1f°: alpha %.2f° after 20 s, want on-speed %.2f°", slope, held, onspeed)
		}
		// Hands-off it must still be descending at about the requested slope —
		// the ballooning spawn climbed instead.
		descent := -m.State.Velocity.Y
		if math.Abs(descent-sink) > 1.5 {
			t.Errorf("%.1f°: sink %.2f m/s after 20 s, spawned at %.2f", slope, descent, sink)
		}
		// No phugoid excursion worth the name: a mistrimmed spawn shows up here
		// as a speed swing long before it shows up in alpha.
		if high-low > 6 {
			t.Errorf("%.1f°: speed ranged %.1f..%.1f (spawn %.1f) — spawn is not trimmed", slope, low, high, entry)
		}
		if spool <= 0.05 || spool >= 0.9 {
			t.Errorf("%.1f°: implausible approach power %.2f", slope, spool)
		}
	}
}

// Mochi world: Air game module tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestRecoveryOutranksTheJink pins the ordering inside steer(): the deck
// recovery is checked BEFORE the commanded-evasion block and RETURNS, so a jink
// can never fly the jet into the water.
//
// #224 was filed on the opposite reading - that polish() calls guard() before
// steer() runs, leaving the jink unbounded - and it is wrong, because steer()
// carries a deck recovery of its own. Measured across 80 fights before this
// test was written: the jink ran 14,368 ticks, its lowest at 814 m, and ZERO of
// them below 900 m descending, while the recovery fired 33,595 ticks and took
// the stick in all 486 where a jink also wanted it. A sweep is a poor guard for
// a one-line invariant, so this drives the two states directly instead.
//
// What it protects: the evasion block sits after an early return, and an edit
// that reorders them, or turns that return into a fallthrough, reads as
// harmless and is not. The negative control for it is exactly that - delete the
// `return` at the end of the recovery branch and this fails.
func TestRecoveryOutranksTheJink(t *testing.T) {
	seat := func(height, climb float64, dodging bool) flight.Inputs {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = flight.Vec3{Y: height}
		m.State.Velocity = flight.Vec3{X: 200, Y: climb}
		m.State.Attitude = flight.Look(m.State.Velocity.Normalize())
		m.State.Fuel = 2450
		b := &brain{skill: skills["expert"], aim: flight.Vec3{X: 1}, g: 4, throttle: 1}
		if dodging {
			// The window stands well past the tick we steer at, and the
			// quadrant matters: (dodge/15)%4 picks it, and 0 and 2 push along
			// the LIFT vector, which at this synthetic state lies in the
			// velocity plane - so their whole effect lands on pitch, which the
			// recovery has already saturated at full aft, and the difference is
			// invisible. 135 selects the SIDE quadrant, out of plane, which
			// must move roll. Measured on a build with the return deleted:
			// pitch 1.000/roll 0.000 becomes 0.819/0.120.
			b.dodge = 135
		}
		return b.steer(m, 60)
	}
	// Low and descending, the recovery owns the stick and a live jink must
	// change NOTHING. Asserting merely that the demand is nose-up is too weak
	// to bite: with the return deleted the jink only PERTURBS the recovery's
	// aim by half a lift-vector, which leaves the pitch positive, and
	// want = max(limit, pull*0.9) is the limit either way. Identity is the
	// invariant the return actually provides.
	calmLow, jinkingLow := seat(600, -40, false), seat(600, -40, true)
	if calmLow != jinkingLow {
		t.Errorf("low and descending, a live jink moved the stick: pitch %.3f->%.3f roll %.3f->%.3f yaw %.3f->%.3f - the deck recovery no longer returns before the evasion block (#224)",
			calmLow.Pitch, jinkingLow.Pitch, calmLow.Roll, jinkingLow.Roll, calmLow.Yaw, jinkingLow.Yaw)
	}
	if calmLow.Pitch <= 0 {
		t.Errorf("low and descending: pitch %.2f, expected a nose-up demand from the deck recovery", calmLow.Pitch)
	}
	// The control that keeps the assertion honest: high and level, the recovery
	// is not armed, so the two cases must DIFFER - otherwise the test above
	// would pass on a build where the jink never runs at all.
	calm, jinking := seat(6000, 0, false), seat(6000, 0, true)
	if calm.Pitch == jinking.Pitch && calm.Roll == jinking.Roll {
		t.Error("high and level, a live jink changed neither pitch nor roll: the evasion block is not running, so the ordering above is untested")
	}
}

// diving lays a jet in a dive at height metres: knots along the flight path,
// the dive and the bank in degrees, the bank rolled about the path from wings
// level. A negative dive climbs.
func diving(height, knots, dive, bank float64) *flight.Model {
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1}, flight.World{Sea: sea})
	speed := knots / 1.944
	d := dive * math.Pi / 180
	path := flight.Vec3{X: math.Cos(d), Y: -math.Sin(d)}
	m.State = flight.Level(m, flight.Vec3{Y: height}, flight.Vec3{X: 1}, speed, 2904)
	m.State.Velocity = path.Scale(speed)
	m.State.Attitude = flight.Axis(path, bank*math.Pi/180).Multiply(flight.Axis(flight.Vec3{Z: 1}, -d))
	return m
}

// TestDeckBudgetsTheRecovery: from the highest point at which deck() calls the
// recovery, steer()'s own pull-out clears the 400 m floor, and the call never
// comes more than 2.5 times the height the pull-out actually loses above it.
// The budget this replaced let a 250 kt pull-out bottom out under 200 m and
// called a 550 kt inverted dive at 3.5 times its loss.
func TestDeckBudgetsTheRecovery(t *testing.T) {
	for _, knots := range []float64{250, 450, 550} {
		for _, dive := range []float64{45, 75, 85} {
			for _, bank := range []float64{0, 180} {
				low, high := 0.0, 12000.0
				for k := 0; k < 40; k++ {
					if middle := (low + high) / 2; deck(&diving(middle, knots, dive, bank).State) {
						low = middle
					} else {
						high = middle
					}
				}
				m := diving(low, knots, dive, bank)
				b := &brain{skill: skills["expert"], aim: flight.Vec3{X: 1}, g: 4, throttle: 1}
				bottom := low
				for tick := uint64(0); tick < 60*30; tick++ {
					in := b.steer(m, tick)
					for k := 0; k < 4; k++ {
						m.Step(in)
					}
					bottom = math.Min(bottom, m.State.Position.Y)
					if m.State.Velocity.Y > 0 && tick > 30 {
						break
					}
				}
				lost := low - bottom
				if bottom < 400 {
					t.Errorf("%.0f kt, %.0f degree dive, bank %.0f: called at %.0f ft, the pull-out bottomed at %.0f ft, under the 1,300 ft floor",
						knots, dive, bank, low*3.281, bottom*3.281)
				}
				if low-400 > 2.5*lost {
					t.Errorf("%.0f kt, %.0f degree dive, bank %.0f: called %.0f ft above the floor for a pull-out that loses %.0f ft",
						knots, dive, bank, (low-400)*3.281, lost*3.281)
				}
			}
		}
	}
}

// TestRecoveryHoldsUntilClimbing: once deck() calls the recovery it keeps the
// stick while the jet descends, even after rolling upright clears the call, and
// lets go once the jet climbs.
func TestRecoveryHoldsUntilClimbing(t *testing.T) {
	inverted, upright := diving(2400, 500, 75, 180), diving(2400, 500, 75, 0)
	if !deck(&inverted.State) || deck(&upright.State) {
		t.Fatalf("the fixture is wrong: at 7,900 ft deck() reads %v inverted and %v upright, want true and false",
			deck(&inverted.State), deck(&upright.State))
	}
	b := &brain{}
	if !b.recovers(&inverted.State) {
		t.Fatal("inverted in a 75 degree dive at 7,900 ft, the recovery was not called")
	}
	if !b.recovers(&upright.State) {
		t.Error("rolled upright and still diving, the recovery let go of the stick")
	}
	if b.recovers(&diving(2400, 500, -10, 0).State) {
		t.Error("climbing, the recovery kept the stick")
	}
}

// TestScriptDiveHoldsTheFight: the scripted expert in a dive, rolled toward a
// pilot below. At 10,000 ft in a 75 degree dive at 500 kt that is a fight, and
// the doctrine flies it without the deck recovery. At 6,200 ft in a 60 degree
// dive at 450 kt the recovery takes the stick once and keeps it: judged afresh
// each tick it took the stick 9 times before the jet climbed, as each roll
// toward him called it and each roll upright cleared it.
func TestScriptDiveHoldsTheFight(t *testing.T) {
	fly := func(height, knots, dive float64) (deck, entries int) {
		b := NewBandit("expert", 1, 250000, "", false, false, "guns", 0, false)
		b.Spawn(flight.Vec3{Y: height}, flight.Vec3{X: 257})
		b.craft.brain.routine.passed = true
		dove := diving(height, knots, dive, 150)
		b.craft.model.State.Velocity, b.craft.model.State.Attitude = dove.State.Velocity, dove.State.Attitude
		pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		pm.State = flight.Level(pm, flight.Vec3{X: 900, Y: height - 1500, Z: 300}, flight.Vec3{X: 1}, 200, fuel)
		words := make([]float64, flight.Size)
		previous := ""
		for tick := 0; tick < 60*8; tick++ {
			pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
			pm.State.Encode(words)
			b.Mirror(words, false, true)
			b.Step()
			if b.Mode() == "deck" {
				deck++
				if previous != "deck" {
					entries++
				}
			}
			previous = b.Mode()
			if b.craft.model.State.Velocity.Y > 0 {
				break
			}
		}
		return deck, entries
	}
	if ticks, _ := fly(3048, 500, 75); ticks > 0 {
		t.Errorf("diving at 10,000 ft the deck recovery took the stick for %.1f s: a 75 degree dive at 500 kt loses about 4,000 ft", float64(ticks)/60)
	}
	if ticks, entries := fly(1900, 450, 60); ticks == 0 || entries > 1 {
		t.Errorf("diving at 6,200 ft the deck recovery took the stick %d times for %.1f s in all, want once", entries, float64(ticks)/60)
	}
}

// Mochi world: Battle tests - rounds and wreckage in the wind
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// breeze is a strong level wind, the way the air moves.
var breeze = flight.Vec3{X: -30, Z: 12}

// TestRoundsRideTheWind: a volley fired into a wind flies, through that air,
// exactly the volley fired into still air. Drag works on the speed through
// the air, so the wind carries every round whole: each is displaced from its
// still-air twin by the wind's own travel and by nothing else, and moves
// through the air at its twin's velocity.
func TestRoundsRideTheWind(t *testing.T) {
	shooter := Pose{Position: flight.Vec3{Y: 4500}, Forward: flight.Vec3{X: 1}, Up: flight.Vec3{Y: 1}, Right: flight.Vec3{Z: 1}, Velocity: flight.Vec3{X: 250, Z: 20}}
	blown := shooter
	blown.Velocity = shooter.Velocity.Add(breeze) // the same jet, flying the same way through air that moves
	blown.Wind = breeze
	still, carried := Volley(shooter, 6, 1, 0, 1), Volley(blown, 6, 1, 0, 1)
	for k := range still {
		if carried[k].Wind != breeze {
			t.Fatalf("round %d leaves the muzzle in %v, not the wind there %v", k, carried[k].Wind, breeze)
		}
		for step := 0; step < 180; step++ {
			Fly(&still[k], 1.0/60)
			Fly(&carried[k], 1.0/60)
		}
		if drift := carried[k].Position.Subtract(still[k].Position).Subtract(breeze.Scale(still[k].Age)); drift.Length() > 1e-6 {
			t.Errorf("after %.1f s round %d is %.3f m from where the wind carried its still-air twin", still[k].Age, k, drift.Length())
		}
		if through := carried[k].Velocity.Subtract(breeze).Subtract(still[k].Velocity); through.Length() > 1e-9 {
			t.Errorf("round %d flies through the air %.4f m/s off its still-air twin", k, through.Length())
		}
	}
}

// TestDebrisRidesTheWind: a wreck's pieces slow into the air they fall
// through, so the cloud a kill leaves in a wind is the still-air cloud carried
// by the wind: displaced by its travel, its pieces moving with it.
func TestDebrisRidesTheWind(t *testing.T) {
	blown := wreck
	blown.Velocity = wreck.Velocity.Add(breeze)
	blown.Wind = breeze
	for _, age := range []float64{0, 0.5, 2, 4.5} {
		c0, r0, v0, _ := wreck.Cloud(age)
		c1, r1, v1, alive := blown.Cloud(age)
		if !alive || r1 != r0 {
			t.Fatalf("at %.1f s the blown cloud is %.1f m across and alive %v, the still one %.1f m", age, r1, alive, r0)
		}
		if drift := c1.Subtract(c0).Subtract(breeze.Scale(age)); drift.Length() > 1e-9 {
			t.Errorf("at %.1f s the cloud is %.3f m from where the wind carried it", age, drift.Length())
		}
		if through := v1.Subtract(breeze).Subtract(v0); through.Length() > 1e-9 {
			t.Errorf("at %.1f s the pieces move through the air %.4f m/s off the still cloud's", age, through.Length())
		}
	}
}

// TestRoundFeelsTheWind: a round takes the wind where it is every Sampled
// seconds of its flight, and in between flies the air it last took.
func TestRoundFeelsTheWind(t *testing.T) {
	trades := flight.Environment{Seed: 7, Wind: flight.Vec3{X: -12.1, Z: 4.4}}
	r := Round{Position: flight.Vec3{Y: 3000}, Velocity: flight.Vec3{X: 1000}, Wind: breeze}
	r.Age = Sampled / 2
	r.Feel(trades, 10)
	if r.Wind != breeze || r.Felt != 0 {
		t.Fatalf("a round %.3f s out took the wind %v, its muzzle's was %v", r.Age, r.Wind, breeze)
	}
	r.Age = Sampled
	r.Feel(trades, 10)
	if want := flight.Wind(r.Position, 10, trades); r.Wind != want || r.Felt != Sampled {
		t.Fatalf("a round %.3f s out flies %v, the wind where it is %v", r.Age, r.Wind, want)
	}
}

// TestSteepShotFeelsTheWind: a round fired steeply down through the trades,
// where the wind grows and veers with height, flies within half a metre of one
// that takes the wind it meets every step, over the two kilometres a gun
// reaches. Kept on the muzzle's air it strays metres.
func TestSteepShotFeelsTheWind(t *testing.T) {
	trades := flight.Environment{Seed: 7, Wind: flight.Vec3{X: -12.9 * math.Sin(70*math.Pi/180), Z: 12.9 * math.Cos(70*math.Pi/180)}}
	at := flight.Vec3{Y: 1200}
	bore := flight.Vec3{X: math.Cos(-0.6), Y: math.Sin(-0.6)} // 34 degrees down into the lowest kilometre, where the wind veers and slows the most
	w := flight.Wind(at, 0, trades)
	felt := Round{Position: at, Velocity: bore.Scale(Muzzle + 250).Add(w), Wind: w}
	exact, kept := felt, felt
	worst, held := 0.0, 0.0
	for step := 0; step < 240*3 && felt.Position.Subtract(at).Length() < 2000; step++ {
		clock := float64(step) / 240
		felt.Feel(trades, clock)
		exact.Wind = flight.Wind(exact.Position, clock, trades)
		Fly(&felt, 1.0/240)
		Fly(&exact, 1.0/240)
		Fly(&kept, 1.0/240)
		worst = math.Max(worst, felt.Position.Subtract(exact.Position).Length())
		held = math.Max(held, kept.Position.Subtract(exact.Position).Length())
	}
	t.Logf("within 2 km: %.3f m off the round that takes the wind every step; kept on the muzzle's air, %.3f m", worst, held)
	if worst > 0.5 || held < 4*worst {
		t.Fatalf("the round strays %.3f m from one taking the wind every step (the muzzle's air alone %.3f m)", worst, held)
	}
}

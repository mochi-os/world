// Mochi world: Battle debris tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"math"
	"reflect"
	"testing"

	"world/games/air/flight"
)

// wreck is a kill at 2,000 m, the wreck going +X at 150 m/s.
var wreck = Debris{Origin: flight.Vec3{Y: 2000}, Velocity: flight.Vec3{X: 150}}

// pass flies a jet straight along +X through the cloud's centre, meeting it
// `late` seconds after the kill at `closing` m/s over the wreck's speed, lifted
// `aside` metres off the centre, and returns the mean strikes a pass over the
// seeds and the mean damage a strike did.
func pass(late float64, closing float64, aside float64, seeds uint64) (float64, float64) {
	strikes, damage := 0, 0.0
	for seed := uint64(0); seed < seeds; seed++ {
		body, _ := target()
		centre, _, _, _ := wreck.Cloud(late)
		speed := 150 + closing
		for step := 0; step < 240; step++ {
			time := late - 2 + float64(step)/60
			position := centre.Add(flight.Vec3{X: speed * (time - late), Y: aside})
			if hit, _, _ := wreck.Meet(time, 1.0/60, position, flight.Vec3{X: speed}, flight.Quat{W: 1}, body, 0, seed, 3, uint64(step)); hit {
				strikes++
			}
		}
		damage += body.Damage.Drag
		for _, e := range body.Damage.Element {
			damage += e
		}
	}
	mean := float64(strikes) / float64(seeds)
	if strikes == 0 {
		return mean, 0
	}
	return mean, damage / float64(strikes)
}

func TestDebrisCloud(t *testing.T) {
	centre, radius, velocity, alive := wreck.Cloud(0)
	if !alive || centre != wreck.Origin || radius != debris_core || velocity != wreck.Velocity {
		t.Fatalf("at the break-up the cloud is the wreck: %v %v %v %v", centre, radius, velocity, alive)
	}
	centre, radius, velocity, _ = wreck.Cloud(2)
	carried := 150 * (1 - math.Exp(-debris_drag*2)) / debris_drag
	if math.Abs(centre.X-carried) > 1e-9 || math.Abs(centre.Y-(2000-0.5*9.8*4)) > 1e-9 {
		t.Fatalf("two seconds on the cloud is carried %.1f m and fallen 19.6 m, got %v", carried, centre)
	}
	if radius != debris_core+2*debris_spread || velocity.X >= 150 || velocity.Y >= 0 {
		t.Fatalf("two seconds on the pieces have spread, slowed and begun to fall: radius %.1f, velocity %v", radius, velocity)
	}
	if _, _, _, alive := wreck.Cloud(debris_life + 0.1); alive {
		t.Fatal("the cloud strikes nothing past its life")
	}
	if _, _, _, alive := wreck.Cloud(-0.1); alive {
		t.Fatal("the cloud is nowhere before the kill")
	}
}

func TestDebrisPass(t *testing.T) {
	if fresh, _ := pass(0.2, 100, 0, 200); fresh < 1.5 {
		t.Errorf("straight through a kill's fresh wreckage the jet is struck: %.2f strikes a pass", fresh)
	}
	if second, _ := pass(1, 100, 0, 200); second < 0.15 || second > 0.6 {
		t.Errorf("a second on the jet has a fair chance of a strike: %.2f a pass", second)
	}
	if late, _ := pass(4, 100, 0, 200); late > 0.1 {
		t.Errorf("four seconds on the pieces have spread too thin to matter: %.2f a pass", late)
	}
	if clear, _ := pass(0.5, 100, 60, 200); clear != 0 {
		t.Errorf("60 m above a half-second cloud is outside it: %.2f strikes a pass", clear)
	}
	if gone, _ := pass(debris_life+0.5, 100, 0, 200); gone != 0 {
		t.Errorf("past its life the cloud strikes nothing: %.2f a pass", gone)
	}
}

func TestDebrisFormation(t *testing.T) {
	// A jet flying with the pieces meets none of them.
	body, _ := target()
	for step := 0; step < 60; step++ {
		time := float64(step) / 60
		centre, _, velocity, _ := wreck.Cloud(time)
		if hit, _, _ := wreck.Meet(time, 1.0/60, centre, velocity, flight.Quat{W: 1}, body, 0, 7, 3, uint64(step)); hit {
			t.Fatalf("struck at %.2f s flying with the pieces", time)
		}
	}
}

func TestDebrisSeverity(t *testing.T) {
	_, slow := pass(0.3, 20, 0, 300)
	_, fast := pass(0.3, 300, 0, 300)
	if fast <= slow {
		t.Errorf("a piece met fast wounds more than one met slow: %.4f against %.4f a strike", fast, slow)
	}
}

func TestDebrisDeterministic(t *testing.T) {
	// The server and the single-player core decide a pass alike: the same
	// kill, jet, seed, slot and tick strike the same way every time.
	for tick := uint64(0); tick < 400; tick++ {
		a, _ := target()
		b, _ := target()
		centre, _, _, _ := wreck.Cloud(0.3)
		hitA, eventsA, pointsA := wreck.Meet(0.3, 1.0/60, centre, flight.Vec3{X: 260}, flight.Quat{W: 1}, a, 0, 11, 2, tick)
		hitB, eventsB, pointsB := wreck.Meet(0.3, 1.0/60, centre, flight.Vec3{X: 260}, flight.Quat{W: 1}, b, 0, 11, 2, tick)
		if hitA != hitB || !reflect.DeepEqual(eventsA, eventsB) || !reflect.DeepEqual(pointsA, pointsB) || !reflect.DeepEqual(a.Damage, b.Damage) {
			t.Fatalf("tick %d met differently twice", tick)
		}
	}
}

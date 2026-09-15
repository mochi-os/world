// Mochi world: Trigger gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"testing"

	"world/games/air/flight"
)

// TestTriggerFollowsTheGun: the core sees the trigger only while rounds
// leave - the button held, a loaded drum, and the weapons free. Everything
// else in the sample passes through untouched.
func TestTriggerFollowsTheGun(t *testing.T) {
	_, a, _ := pair(t, "furball")
	a.latest = flight.Inputs{Throttle: 0.7, Fire: true}
	cases := []struct {
		name       string
		ammunition int
		free       bool
		want       bool
	}{
		{"loaded and free", rounds, true, true},
		{"empty drum", 0, true, false},
		{"weapons held", rounds, false, false},
	}
	for _, c := range cases {
		a.ammunition = c.ammunition
		if fed := a.trigger(c.free); fed.Fire != c.want || fed.Throttle != 0.7 {
			t.Errorf("%s: the core sees fire %v throttle %.1f, want %v and 0.7", c.name, fed.Fire, fed.Throttle, c.want)
		}
	}
	a.ammunition = rounds
	a.latest.Fire = false
	if a.trigger(true).Fire {
		t.Errorf("trigger up: the core sees fire")
	}
}

// TestRecoilServed: the server steps a jet through its trigger - a second on
// the button with rounds costs it the recoil (about 1.2 m/s at 13-14 t); an
// empty drum flies on as if the button were up.
func TestRecoilServed(t *testing.T) {
	speed := func(ammunition int) float64 {
		i, a, b := pair(t, "furball")
		posed(a, flight.Vec3{Y: 3000}, 0, 200)
		posed(b, flight.Vec3{X: -20000, Y: 3000}, 0, 200)
		a.ammunition = ammunition
		a.latest = flight.Inputs{Throttle: a.model.State.Engine[0].Spool, Fire: true}
		for tick := uint64(1); tick <= 60; tick++ {
			i.Step(tick, nil)
		}
		if !a.alive {
			t.Fatalf("the jet died firing into empty sky")
		}
		return a.model.State.Velocity.Length()
	}
	loaded, empty, quiet := speed(rounds), speed(0), 0.0
	{
		i, a, b := pair(t, "furball")
		posed(a, flight.Vec3{Y: 3000}, 0, 200)
		posed(b, flight.Vec3{X: -20000, Y: 3000}, 0, 200)
		a.latest = flight.Inputs{Throttle: a.model.State.Engine[0].Spool}
		for tick := uint64(1); tick <= 60; tick++ {
			i.Step(tick, nil)
		}
		quiet = a.model.State.Velocity.Length()
	}
	if lost := quiet - loaded; lost < 0.9 || lost > 1.6 {
		t.Errorf("a second of fire costs %.2f m/s on the server, want about 1.2 (17 kN over 13-14 t)", lost)
	}
	if empty != quiet {
		t.Errorf("an empty drum with the button held: %.4f m/s, want the quiet flight's %.4f", empty, quiet)
	}
}

// TestBanditLoad: the single-player client mirrors its belt into the bandit
// before each frame. A guns bandit with a player 300 m dead ahead holds the
// trigger for most of four seconds and the recoil costs it the impulse over
// its mass; told its belt is empty, the brain stops pressing and the jet
// flies on unkicked.
func TestBanditLoad(t *testing.T) {
	fresh := NewBandit("ace", 1, 250000, "", false, false, "", 0)
	fresh.Spawn(flight.Vec3{Y: 2000}, flight.Vec3{X: 200})
	if fresh.craft.ammunition != rounds {
		t.Fatalf("a fresh bandit carries %d rounds, want the full %d", fresh.craft.ammunition, rounds)
	}
	fresh.Load(37)
	if fresh.craft.ammunition != 37 {
		t.Fatalf("belt %d after Load(37)", fresh.craft.ammunition)
	}
	hunt := func(belt int) (firing int, speed float64) {
		b := NewBandit("ace", 1, 250000, "", false, false, "guns", 0)
		b.Spawn(flight.Vec3{Y: 2000}, flight.Vec3{X: 200})
		player := flight.Level(b.craft.model, flight.Vec3{X: 300, Y: 2000}, flight.Vec3{X: 1}, 200, 2500)
		words := make([]float64, flight.Size)
		for frame := 0; frame < 240; frame++ {
			player.Position = flight.Vec3{X: 300 + 200*float64(frame)/60, Y: 2000}
			player.Encode(words)
			b.Mirror(words, false, true)
			b.Load(belt)
			if fire, _, _, _, _ := b.Step(); fire {
				firing++
			}
		}
		return firing, b.craft.model.State.Velocity.Length()
	}
	loaded, slow := hunt(rounds)
	empty, fast := hunt(0)
	if loaded < 150 || empty != 0 {
		t.Fatalf("the brain held the trigger for %d frames loaded and %d empty, want most of 240 and none", loaded, empty)
	}
	want := fresh.craft.model.Airframe.Gun.Recoil / 13400 * float64(loaded) / 60
	if lost := fast - slow; lost < 0.8*want || lost > 1.25*want {
		t.Errorf("%d frames on the trigger cost the loaded bandit %.2f m/s against the empty one, want about %.2f", loaded, lost, want)
	}
}

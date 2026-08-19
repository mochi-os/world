// Mochi world: dead aircraft fly the real model
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// bank reports how far the jet is rolled from wings-level, in degrees.
func bank(s *flight.State) float64 {
	right := s.Attitude.Rotate(flight.Vec3{Z: 1})
	return math.Abs(math.Asin(clamp(right.Y, -1, 1)) * 57.3)
}

// coast flies a dead bandit for the given seconds and reports where it got to.
func coast(t *testing.T, throttle, reheat, lean float64, seconds int) (speed, rolled, fell float64) {
	t.Helper()
	b := NewBandit("ace", 1, 250000, "", false, false, "guns", 0)
	b.Spawn(flight.Vec3{Y: 5000}, flight.Vec3{X: 220})
	b.craft.latest = flight.Inputs{Throttle: throttle, Reheat: reheat}
	high := b.State().Position.Y
	for tick := 0; tick < 60*seconds; tick++ {
		b.Coast(lean)
		if r := bank(b.State()); r > rolled {
			rolled = r
		}
	}
	return b.State().Velocity.Length(), rolled, high - b.State().Position.Y
}

// TestCoastHoldsTheLevers: the stick is spring-centred so it returns to neutral
// and the FCS holds attitude, but the THROTTLE is friction-held and stays
// exactly where the pilot left it. Zeroing both spooled every wreck to idle,
// which is the one thing an unattended jet does not do.
func TestCoastHoldsTheLevers(t *testing.T) {
	hot, _, _ := coast(t, 1, 1, 0, 12)
	cold, _, _ := coast(t, 0, 0, 0, 12)
	if hot <= cold {
		t.Errorf("a jet left in full burner coasted to %.0f m/s, no faster than one left at idle (%.0f): the levers are not being held", hot, cold)
	}
	if hot < 220 {
		t.Errorf("a jet left in full burner slowed to %.0f m/s: the throttle is being zeroed somewhere", hot)
	}
}

// TestCoastSpirals: no airframe is rigged perfectly and nobody is holding the
// wings level, so a dead jet rolls off and spirals. The scripted descent it
// replaces steered to one fixed heading and then flew it — measured at a
// constant 233 kt down 26.6 degrees, wings level, for 79 s.
func TestCoastSpirals(t *testing.T) {
	_, rolled, fell := coast(t, 0.7, 0, 0.10, 20)
	if rolled < 10 {
		t.Errorf("a dead jet reached only %.1f degrees of bank in twenty seconds: it is gliding flat, not spiralling", rolled)
	}
	if fell <= 0 {
		t.Errorf("a dead jet gained %.0f m: it should be coming down", -fell)
	}
	// And the roll must come from the standing lean, not from the model being
	// unstable on its own — otherwise the test passes for the wrong reason.
	_, level, _ := coast(t, 0.7, 0, 0, 20)
	if level >= rolled {
		t.Errorf("a jet with no standing roll banked %.1f degrees, as much as one with (%.1f): the lean is not reaching the model", level, rolled)
	}
}

// TestCoastVaries: the defect this replaces was a jet pinned at ONE speed and
// ONE descent angle for as long as it took to reach the water. Whatever the
// model does, it must not do that.
func TestCoastVaries(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, false, "guns", 0)
	b.Spawn(flight.Vec3{Y: 5000}, flight.Vec3{X: 220})
	b.craft.latest = flight.Inputs{Throttle: 0.7}
	var speeds []float64
	for tick := 0; tick < 60*20; tick++ {
		b.Coast(0.10)
		if tick%120 == 0 {
			speeds = append(speeds, b.State().Velocity.Length())
		}
	}
	low, high := speeds[0], speeds[0]
	for _, v := range speeds {
		low, high = math.Min(low, v), math.Max(high, v)
	}
	if high-low < 5 {
		t.Errorf("speed held between %.0f and %.0f m/s across the fall: that is the scripted rail, not a flying aeroplane", low, high)
	}
}

// TestDriftHoldsTheLevers is the server's half of the same rule: a wreck
// carries the throttle its pilot left, so it flies on at cruise power rather
// than spooling to idle the instant he dies.
func TestDriftHoldsTheLevers(t *testing.T) {
	made, err := (&Air{}).Create(game.Session{Identifier: "wreck", Game: "air", Mode: "furball", Capacity: 8, Seed: 1,
		Parameters: map[string]any{"bots": map[string]any{"ace": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	slot := -1
	for _, s := range i.slots() {
		if i.aircraft[s] != nil && i.aircraft[s].model != nil {
			slot = s
			break
		}
	}
	if slot < 0 {
		t.Fatal("no aircraft in the session")
	}
	a := i.aircraft[slot]
	a.latest = flight.Inputs{Throttle: 1, Reheat: 1}
	i.down(slot, a, "pilot")
	if len(i.wrecks) != 1 {
		t.Fatalf("down() left %d wrecks, want 1", len(i.wrecks))
	}
	if w := i.wrecks[0]; w.throttle != 1 || w.reheat != 1 {
		t.Errorf("the wreck took throttle %.2f reheat %.2f off a jet flying at full burner", w.throttle, w.reheat)
	}

	// And the levers must reach the MODEL, not merely the struct: fly one
	// wreck left hot and one left cold, and the hot one must end faster.
	fly := func(throttle, reheat float64) float64 {
		made, err := (&Air{}).Create(game.Session{Identifier: "drift", Game: "air", Mode: "furball", Capacity: 8, Seed: 2,
			Parameters: map[string]any{"bots": map[string]any{"ace": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		j := made.(*instance)
		defer j.Close()
		for _, s := range j.slots() {
			if c := j.aircraft[s]; c != nil && c.model != nil {
				c.latest = flight.Inputs{Throttle: throttle, Reheat: reheat}
				j.down(s, c, "pilot")
				break
			}
		}
		if len(j.wrecks) == 0 {
			t.Fatal("no wreck to fly")
		}
		w := j.wrecks[0]
		for tick := 0; tick < 60*10; tick++ {
			j.drift(1.0 / 60)
		}
		return w.model.State.Velocity.Length()
	}
	if hot, cold := fly(1, 1), fly(0, 0); hot <= cold {
		t.Errorf("a wreck left in full burner drifted to %.0f m/s, no faster than one left at idle (%.0f): the levers are not reaching the model", hot, cold)
	}
}

// TestBurnerRation: past its burner bingo a bot goes dry — unless the shot it
// was saving the fuel for is actually in front of it. The doctrine batteries
// cannot reach this (a bandit burns ~4.2 kg/s, so the threshold is 327 s away
// and their fights run 98-178 s), which is why it is tested here directly.
func TestBurnerRation(t *testing.T) {
	slow := &track{velocity: flight.Vec3{X: 100}} // ~195 kt: collapsed
	fast := &track{velocity: flight.Vec3{X: 250}} // ~486 kt: still flying
	for _, c := range []struct {
		name     string
		prey     *track
		distance float64
		dry      bool
	}{
		{"a slow opponent close aboard is the shot the fuel was for", slow, 1400, false},
		{"the same opponent too far to reach", slow, 3000, true},
		{"close, but he still has his energy", fast, 1400, true},
		{"nothing in front of me at all", nil, 1400, true},
		{"exactly at the range bound", slow, 2200, true},
		{"exactly at the speed bound", &track{velocity: flight.Vec3{X: 170}}, 1400, true},
	} {
		if got := rationed(c.prey, c.distance); got != c.dry {
			verb := map[bool]string{true: "went dry", false: "kept its burner"}
			t.Errorf("%s: %s, want %s", c.name, verb[got], verb[c.dry])
		}
	}
}

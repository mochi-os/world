package air

import (
	"testing"

	"world/game"
	"world/games/air/flight"
)

// spam builds an input batch of count samples, each with the given flags set.
func spam(count int, missile bool, flare bool, alternate bool) []game.Input {
	list := make([]game.Input, count)
	for k := range list {
		on := true
		if alternate {
			on = k%2 == 0
		}
		list[k] = game.Input{Data: map[string]any{
			"missile": missile && on,
			"flare":   flare && on,
		}}
	}
	return list
}

// hostile session: one human slot against a drone, missiles enabled.
func hostileSession(t *testing.T) (*instance, int) {
	t.Helper()
	g := New()
	made, err := g.Create(game.Session{Identifier: "abuse", Game: "air", Mode: "furball", Capacity: 100, Seed: 7,
		Parameters: map[string]any{"missiles": true, "bots": map[string]any{"drone": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	slot := 1
	i.Join(game.Player{Name: "abuser", Slot: slot})
	// Park the drone dead ahead so the seeker always has an acquisition.
	s := &i.aircraft[slot].model.State
	ahead := s.Attitude.Rotate(flight.Vec3{X: 1})
	drone := &i.aircraft[99].model.State
	drone.Position = s.Position.Add(ahead.Scale(1500))
	drone.Velocity = s.Velocity
	return i, slot
}

// TestMissileDiscipline: a client streaming missile:true (the level attack)
// or alternating it per sample (the edge attack) gets the magazine and the
// launch spacing, not a missile per input sample.
func TestMissileDiscipline(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		i, slot := hostileSession(t)
		launched := 0
		for tick := uint64(0); tick < 60*30; tick++ {
			i.Step(tick, map[int][]game.Input{slot: spam(64, true, false, alternate)})
			for _, e := range i.events {
				if e["kind"] == "missile" && e["slot"] == slot {
					launched++
				}
			}
			i.events = i.events[:0]
		}
		if launched > 2 {
			t.Fatalf("alternate=%v: %d missiles from one life's magazine of 2", alternate, launched)
		}
		if len(i.flying) > 256 {
			t.Fatalf("flying set unbounded: %d", len(i.flying))
		}
	}
}

// TestFlareDiscipline: the flare event is edge-triggered with a server-side
// cooldown — a spamming client cannot storm the reliable broadcast.
func TestFlareDiscipline(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		i, slot := hostileSession(t)
		dropped := 0
		const seconds = 10
		for tick := uint64(0); tick < 60*seconds; tick++ {
			i.Step(tick, map[int][]game.Input{slot: spam(64, false, true, alternate)})
			for _, e := range i.events {
				if e["kind"] == "flare" && e["slot"] == slot {
					dropped++
				}
			}
			i.events = i.events[:0]
		}
		if dropped > seconds*2+1 {
			t.Fatalf("alternate=%v: %d flare events in %d s against the 0.5 s cooldown", alternate, dropped, seconds)
		}
	}
}

// TestWrapFloor: a sub-arena wrap is rejected at creation — the parameter
// that once hung the session goroutine keeps the default instead.
func TestWrapFloor(t *testing.T) {
	g := New()
	made, err := g.Create(game.Session{Identifier: "wrap", Game: "air", Mode: "furball", Capacity: 4, Seed: 1,
		Parameters: map[string]any{"wrap": 1e-9}})
	if err != nil {
		t.Fatal(err)
	}
	if w := made.(*instance).environment.Wrap; w != 250000 {
		t.Fatalf("hostile wrap accepted: %v", w)
	}
}

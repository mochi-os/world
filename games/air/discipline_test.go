package air

import (
	"fmt"
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
	made, err := g.Create(game.Session{Identifier: "abuse", Game: "air", Mode: "furball", Capacity: 16, Seed: 7,
		Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{"drone": 1.0}}})
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
		if launched > 4 { // one life's magazine (4 since the LAU-127 loadout, 2026-07-30) — the property is that spam gets the magazine and nothing more
			t.Fatalf("alternate=%v: %d missiles from one life's magazine of 4", alternate, launched)
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

// TestPairDiscipline holds the BOT's shoot-shoot-look rule: a pair may go
// inside a second, and the third round then waits three seconds for the look.
func TestPairDiscipline(t *testing.T) {
	heavy(t)
	worst, fights := 0, 0
	var quick int // third-or-later rounds that followed inside the look
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		for seed := uint64(1); seed <= 6; seed++ {
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("pair%s%d", level, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": true, "weapons": "fox2",
					"bots": map[string]any{level: 1.0, "pilot": 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			fights++
			shots := map[int][]uint64{} // launch ticks per shooter
			for tick := uint64(0); tick < 60*180; tick++ {
				i.events = i.events[:0]
				i.Step(tick, nil)
				for _, e := range i.events {
					if e["kind"] != "missile" {
						continue
					}
					slot, _ := e["slot"].(int)
					shots[slot] = append(shots[slot], tick)
				}
			}
			for _, ticks := range shots {
				for n := range ticks {
					// How many rounds fell inside the second before this one?
					window := 1
					for k := n - 1; k >= 0 && ticks[n]-ticks[k] < 60; k-- {
						window++
					}
					if window > worst {
						worst = window
					}
					// A third round must wait the look out, not ride the pair.
					if n >= 2 && ticks[n]-ticks[n-2] < 180 {
						quick++
					}
				}
			}
			i.Close()
		}
	}
	fmt.Printf("pair discipline: %d fights | most rounds inside one second %d | third-or-later rounds inside the look %d\n",
		fights, worst, quick)
	if worst > 2 {
		t.Errorf("%d rounds left the rails inside one second: the pair is not counted, and a stream this dense saturates any flare defence", worst)
	}
	if quick > 0 {
		t.Errorf("%d rounds followed their pair without waiting the three-second look", quick)
	}
}

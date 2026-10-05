package air

import (
	"fmt"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestScriptLadder: the scripted doctrine (stage 16) against mimic, the scripted
// pilot of the user's own fights, from the joust's real start - the difficulty
// target in numbers. The user should win most fights against the novice, lose
// most against the pilot, and rarely or never beat the ace or the superhuman;
// mimic is a little weaker than the user, so the ace and the superhuman lose at
// most two in sixteen to it, the pilot wins more than it loses and less than
// the ace, and the novice loses more than it wins. Measured over 32 fights each
// when this was written, won and lost: 8/21, 23/9, 30/1, 29/1; stage 0 read
// 4/23, 7/21, 14/13, 12/14.
func TestScriptLadder(t *testing.T) {
	heavy(t)
	net := map[string]int{}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		n := 0
		b := sweepFrom(t, level, "joust", func() flyer {
			n++
			return mimic([]float64{1, -1}[n%2])
		}, true, 0, 16, 150, func(i *instance, bot int) {
			jousted(i, bot)
			i.aircraft[bot].brain.tactics.evaluate(16, 1<<16-2) // the script alone, whatever AIR_STAGE says
		})
		fmt.Printf("%-10s against mimic: won %2d lost %2d of 16\n", level, b.downed, b.lost)
		net[level] = b.downed - b.lost
		switch level {
		case "novice":
			if b.downed >= b.lost {
				t.Errorf("the novice won %d and lost %d: the user should win most fights against it", b.downed, b.lost)
			}
		case "pilot":
			if b.downed <= b.lost {
				t.Errorf("the pilot won %d and lost %d: the user should lose most fights against it", b.downed, b.lost)
			}
		default:
			if b.lost > 2 {
				t.Errorf("the %s lost %d of 16: the user should rarely or never beat it", level, b.lost)
			}
		}
	}
	if net["pilot"] >= net["ace"] {
		t.Errorf("the pilot's wins less its losses are %d and the ace's %d: the ladder does not climb", net["pilot"], net["ace"])
	}
}

// TestScriptPass: the scripted doctrine (stage 16) passes the pilot at 150 m or
// more, a training merge's bubble, whether he flies straight, points at the
// bandit all the way in, or lead-turns first at 1.6 km toward the bandit's side
// or away from it (a pilot who turns away that hard at 1.6 km opens the range
// and never goes by at all); and a second and a half after the pass it is
// fighting, not still flying the run-in.
func TestScriptPass(t *testing.T) {
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		for seed := uint64(1); seed <= 8; seed++ {
			for _, offset := range []float64{0, 300, -300} {
				for _, pilot := range []string{"straight", "pursuit", "toward"} {
					if pilot == "pursuit" && (offset != 0 || seed > 3) || pilot == "toward" && seed > 3 {
						continue
					}
					got := approached(t, level, 16, seed, 220, offset, false, pilot)
					name := fmt.Sprintf("%s seed %d offset %+.0f pilot %s", level, seed, offset, pilot)
					fmt.Printf("%-44s closest %4.0f m  roll %4.0f  reversed %-5v plays %v after %s\n", name, got.closest, got.roll, got.reversed, got.plays, got.after)
					if got.closest < bubble {
						t.Errorf("%s: passed %.0f m from him, inside the bubble", name, got.closest)
					}
					if got.after == "line" || got.after == "lead" || got.after == "hold" {
						t.Errorf("%s: still flying the run-in (%s) a second and a half after the pass", name, got.after)
					}
				}
			}
		}
	}
}

// TestScriptRecordsItsBranch: a developer recording's Doctrine channel names the
// scripted doctrine's branch (the run-in's line, lead turn and hold, then the
// fight, the gun, the break, the regain), not the one word "script" for the
// whole fight, which would tell a debrief nothing.
func TestScriptRecordsItsBranch(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, true, "fox2", 0, true)
	b.Stage(16, 1<<16-2)
	b.Spawn(flight.Vec3{X: -2750, Y: 4572}, flight.Vec3{X: 220})
	pm := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	pm.State = flight.Level(pm, flight.Vec3{X: 2750, Y: 4572}, flight.Vec3{X: -1}, 220, fuel)
	words := make([]float64, flight.Size)
	seen := map[string]bool{}
	for tick := 0; tick < 60*20; tick++ {
		pm.State.Position = pm.State.Position.Add(pm.State.Velocity.Scale(1.0 / 60))
		pm.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		seen[b.Mode()] = true
	}
	for _, branch := range []string{"line", "fight"} {
		if !seen[branch] {
			t.Errorf("the Doctrine channel never read %q; it read %v", branch, seen)
		}
	}
	if seen["script"] {
		t.Errorf("the Doctrine channel read \"script\": %v", seen)
	}
}

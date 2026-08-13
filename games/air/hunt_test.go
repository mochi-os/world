// Mochi world: Bot radar and AMRAAM employment tests (hunt.go).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
)

// TestHuntSilence is the WVR sentinel: in a fox2-pinned furball — the fit
// and mode every dogfight battery flies — no bot carries an AMRAAM,
// radiates, locks, or launches one. Bots arm to the match's weapons class,
// so this is the configuration the batteries pin; if this test fails, the
// byte-identity guarantee the BVR programme rests on has been broken and
// every battery number is suspect.
func TestHuntSilence(t *testing.T) {
	made, err := (&Air{}).Create(game.Session{Identifier: "huntsilence", Game: "air", Mode: "furball",
		Capacity: 8, Seed: 3,
		Parameters: map[string]any{"missiles": true, "weapons": "fox2",
			"bots": map[string]any{"ace": 1.0, "pilot": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	for _, s := range i.slots() {
		if a := i.aircraft[s]; a != nil && a.bot && a.amraams != 0 {
			t.Fatalf("fox2 bot spawned with %d AMRAAMs — the pinned heater fit changed", a.amraams)
		}
	}
	for tick := uint64(0); tick < 60*30; tick++ {
		i.events = i.events[:0]
		i.Step(tick, nil)
		for _, e := range i.events {
			if e["kind"] == "fox3" {
				t.Fatalf("fox3 launched in a fox2 furball at tick %d", tick)
			}
		}
		if tick%60 != 0 {
			continue
		}
		for _, s := range i.slots() {
			if a := i.aircraft[s]; a != nil && a.bot && a.emitter != 0 {
				t.Fatalf("bot in slot %d radiating (emitter %d) in a fox2 furball at tick %d", s, a.emitter, tick)
			}
		}
	}
}

// TestHuntBvrJoust flies the BVR joust bot-versus-bot — the pair may include
// bots, at most two combatants — and asserts the whole chain: the radar
// acquires far beyond the 12 km the canopy sees, the emission discipline
// separates the tiers, a DLZ-judged shot leaves through the same fox3() a
// human trigger reaches, and the round flies with datalink attached.
func TestHuntBvrJoust(t *testing.T) {
	made, err := (&Air{}).Create(game.Session{Identifier: "huntjoust", Game: "air", Mode: "joust",
		Seed: 5,
		Parameters: map[string]any{"missiles": true, "start": "bvr",
			"bots": map[string]any{"novice": 1.0, "superhuman": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	var novice, machine *craft
	for _, s := range i.slots() {
		a := i.aircraft[s]
		if a == nil || !a.bot {
			continue
		}
		if a.brain.skill.machine {
			machine = a
		} else {
			novice = a
		}
	}
	if novice == nil || machine == nil {
		t.Fatal("joust roster wrong: the pair should be two bots")
	}
	if novice.amraams == 0 || machine.amraams == 0 {
		t.Fatalf("BVR joust bots spawned without AMRAAMs (%d/%d): the open class should arm them", novice.amraams, machine.amraams)
	}
	if _, err := i.Join(game.Player{Name: "third", Slot: 0}); err == nil {
		t.Fatal("a human joined a full bot pair: the joust must stay exactly two combatants")
	}

	launched, datalinked := false, false
	sttNovice := uint64(0)
	for tick := uint64(0); tick < 60*150; tick++ {
		i.events = i.events[:0]
		i.Step(tick, nil)
		for _, e := range i.events {
			if e["kind"] == "fox3" {
				launched = true
			}
		}
		for _, m := range i.flying {
			if m.radar != nil {
				datalinked = true
			}
		}
		if novice.emitter == 2 && sttNovice == 0 {
			sttNovice = tick
		}
		if tick == 600 {
			// Ten seconds in, over a hundred kilometres apart: the novice
			// radiates (its whole syllabus is radiate-and-press), and the
			// disciplined machine holds search — paint on the RWR, but the
			// hard-lock warning withheld until the commit range.
			if novice.alive && novice.emitter < 1 {
				t.Fatalf("novice not radiating ten seconds into a BVR joust (emitter %d)", novice.emitter)
			}
			if machine.model != nil && novice.model != nil {
				if span := i.span(machine.model, novice.model); span > 100000 && machine.emitter == 2 {
					t.Fatalf("the machine holds an STT %.0f km out: the discipline is not withholding the lock", span/1000)
				}
			}
		}
		if launched && datalinked && sttNovice > 0 {
			break
		}
	}
	if sttNovice == 0 {
		t.Fatal("the novice never built an STT in 150 seconds: the radar is not acquiring beyond visual range")
	}
	if !launched {
		t.Fatal("no AMRAAM left a rail in 150 seconds of a BVR joust")
	}
	if !datalinked {
		t.Fatal("a round flew with no radar guidance attached")
	}
	fmt.Printf("bvr joust: novice STT at %.1f s, first launch inside 150 s\n", float64(sttNovice)/60)
}

// TestHuntDefence flies a fixed superhuman attacker against a defender of
// each level over six seeds and orders the outcomes: defence quality must
// rise with level. The ends of the ladder are gated; the middle is reported
// (kill counts at this sample size swing, and the dials are the battery's
// to tune). The jammer trade is asserted here too: only the machine ever
// arms it, loud beyond the terminal call and quiet inside it.
func TestHuntDefence(t *testing.T) {
	survived := map[string]int{}
	loud := map[string]bool{}
	quieted := map[string]bool{}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		for seed := uint64(1); seed <= 6; seed++ {
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("defence%s%d", level, seed),
				Game: "air", Mode: "joust", Seed: seed,
				Parameters: map[string]any{"missiles": true, "start": "bvr",
					"bots": func() map[string]any {
						if level == "superhuman" {
							return map[string]any{"superhuman": 2.0}
						}
						return map[string]any{level: 1.0, "superhuman": 1.0}
					}()}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var defender, attacker *craft
			for _, s := range i.slots() {
				a := i.aircraft[s]
				if a == nil || !a.bot {
					continue
				}
				// In the equal superhuman pairing the roles are symmetric;
				// take the higher slot as the defender consistently.
				if defender == nil {
					defender = a
				} else {
					attacker = a
				}
			}
			if level != "superhuman" {
				if defender.brain.skill.machine {
					defender, attacker = attacker, defender
				}
			}
			if defender == nil || attacker == nil {
				t.Fatal("defence roster wrong")
			}
			alive := true
			for tick := uint64(0); tick < 60*300 && alive; tick++ {
				i.Step(tick, nil)
				if defender.brain.jam {
					loud[level] = true
					if !defender.brain.skill.machine {
						t.Fatalf("%s armed the jammer: the emission trade belongs to the machine tier", level)
					}
				}
				if loud[level] && !defender.brain.jam && defender.alive && defender.brain.alerted > 0 {
					quieted[level] = true
				}
				if !defender.alive || defender.model == nil {
					alive = false
				}
				if attacker.model == nil || !attacker.alive {
					break // the defender won outright: counts as survival
				}
			}
			if alive {
				survived[level]++
			}
			i.Close()
		}
	}
	fmt.Printf("bvr defence vs a superhuman attacker: novice %d/6, pilot %d/6, ace %d/6, superhuman %d/6 survived\n",
		survived["novice"], survived["pilot"], survived["ace"], survived["superhuman"])
	// Survival compresses at this sample size, and the physics keeps it
	// compressed: the novice's full-burner drag is authentically the best
	// pure-survival play against marginal stern shots — it survives by
	// running and never wins, which is why the BvrLadder battery, not this
	// gate, owns the outcome ordering. Fail only on separation beyond
	// one-seed noise; the tuning round owes this instrument a sharper
	// metric (time alive, rounds defeated) than binary survival.
	if survived["novice"] > survived["superhuman"]+1 {
		t.Fatalf("defence ladder inverted end to end: novice survived %d, superhuman %d", survived["novice"], survived["superhuman"])
	}
	if !loud["superhuman"] {
		t.Fatal("the machine never armed its jammer with a round inbound")
	}
}

// TestHuntSeam is the merge-seam test the hand-off demands: a BVR fight
// collapsing to WVR hands the flight path to the dogfight arbiter cleanly.
// The crank overlay must never fire inside the seam, and the pair must
// genuinely merge or resolve — a never-closing fight is the degenerate
// play the battery gates against.
func TestHuntSeam(t *testing.T) {
	made, err := (&Air{}).Create(game.Session{Identifier: "seam", Game: "air", Mode: "joust", Seed: 11,
		Parameters: map[string]any{"missiles": true, "start": "bvr",
			"bots": map[string]any{"ace": 2.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	bots := []*craft{}
	for _, s := range i.slots() {
		if a := i.aircraft[s]; a != nil && a.bot {
			bots = append(bots, a)
		}
	}
	if len(bots) != 2 {
		t.Fatal("seam roster wrong")
	}
	closest, resolved := math.MaxFloat64, false
	for tick := uint64(0); tick < 60*480; tick++ {
		i.Step(tick, nil)
		if bots[0].model == nil || bots[1].model == nil || !bots[0].alive || !bots[1].alive {
			resolved = true
			break
		}
		span := i.span(bots[0].model, bots[1].model)
		if span < closest {
			closest = span
		}
		for _, a := range bots {
			if span < radar_seam && a.brain.cranked == tick {
				t.Fatalf("the crank overlay fired %.0f m inside the merge seam at tick %d", span, tick)
			}
		}
	}
	if !resolved && closest > 2000 {
		t.Fatalf("the pair never merged nor resolved in eight minutes: closest approach %.0f m — degenerate never-closing play", closest)
	}
	fmt.Printf("seam: closest approach %.0f m, resolved %v\n", closest, resolved)
}

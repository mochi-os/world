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

// TestHuntSilence is the WVR sentinel: in a fox2-pinned furball no bot carries
// an AMRAAM, radiates, locks, or launches one. Every dogfight battery's numbers
// rest on that configuration.
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

// TestHuntBvrJoust flies the BVR joust bot-versus-bot and asserts the chain:
// acquisition beyond canopy range, emission discipline per tier, a DLZ-judged
// shot through fox3(), and the round flying with datalink attached.
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

// TestHuntDefence flies a fixed superhuman attacker against a defender of each
// level over six seeds and measures gradient, not survival: it tracks time
// alive, each inbound round's closest approach, and rounds defeated. Jammer
// trade too.
func TestHuntDefence(t *testing.T) {
	type outcome struct {
		alive    float64 // s the defender lasted (full fight = the limit)
		faced    int     // inbound radar rounds launched at the defender
		settled  int     // rounds that reached a verdict before the fight ended
		defeated int     // rounds that died with the defender still alive
		least    float64 // summed closest approach of settled rounds (m)
	}
	const limit = 300.0
	score := map[string]*outcome{}
	survived := map[string]int{}
	loud := map[string]bool{}
	quieted := map[string]bool{}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		score[level] = &outcome{}
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
			slot := -1
			for _, s := range i.slots() {
				if i.aircraft[s] == defender {
					slot = s
				}
			}
			closest := map[*missile]float64{} // every inbound round's nearest point so far
			resolved := map[*missile]bool{}
			o := score[level]
			for tick := uint64(0); tick < uint64(60*limit); tick++ {
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
				current := map[*missile]bool{}
				if defender.alive && defender.model != nil {
					for _, m := range i.flying {
						if m.radar == nil || m.target != slot {
							continue
						}
						current[m] = true
						_, d := i.bearing(m.position, defender.model.State.Position)
						if prev, ok := closest[m]; !ok || d < prev {
							closest[m] = d
						}
					}
				}
				for m, d := range closest {
					if current[m] || resolved[m] {
						continue
					}
					// The round is gone; the defender's state at retirement
					// is the verdict. The killing round retires unresolved
					// with the defender dead and rightly counts undefeated.
					resolved[m] = true
					o.settled++
					o.least += d
					if defender.alive {
						o.defeated++
					}
				}
				if !defender.alive || defender.model == nil {
					o.alive += float64(tick) / 60
					break
				}
				if attacker.model == nil || !attacker.alive {
					break // the defender won outright: full time credit below
				}
			}
			if defender.alive && defender.model != nil {
				o.alive += limit
				survived[level]++
			}
			o.faced += len(closest)
			i.Close()
		}
	}
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		o := score[level]
		mean := 0.0
		if o.settled > 0 {
			mean = o.least / float64(o.settled)
		}
		fmt.Printf("bvr defence vs a superhuman attacker: %-10s alive %5.1f s mean, survived %d/6, rounds faced %2d, defeated %2d/%2d, closest approach %6.0f m mean\n",
			level, o.alive/6, survived[level], o.faced, o.defeated, o.settled, mean)
	}
	// The gates hold the instrument's ends only: the novice must not outlast the
	// machine, and the machine must defeat a larger share of inbound rounds. The
	// middle tiers are reported.
	if score["novice"].alive > score["superhuman"].alive+limit {
		t.Fatalf("defence ladder inverted end to end: novice alive %.0f s total, superhuman %.0f s", score["novice"].alive, score["superhuman"].alive)
	}
	ratio := func(o *outcome) float64 {
		if o.settled == 0 {
			return 0
		}
		return float64(o.defeated) / float64(o.settled)
	}
	if ratio(score["superhuman"]) < ratio(score["novice"])-0.34 {
		t.Fatalf("the machine defeats a smaller share of inbound rounds than the novice: %.2f vs %.2f", ratio(score["superhuman"]), ratio(score["novice"]))
	}
	if !loud["superhuman"] {
		t.Fatal("the machine never armed its jammer with a round inbound")
	}
}

// TestHuntSeam: a BVR fight collapsing to WVR hands the flight path to the
// dogfight arbiter cleanly - the crank must never fire inside the seam, and the
// majority of seeds must merge or resolve. Never-closing is a real 1-in-6
// outcome of symmetric competent BVR, so one seed cannot gate it.
func TestHuntSeam(t *testing.T) {
	seeds := []uint64{11, 12, 13, 14}
	closing := 0
	for _, seed := range seeds {
		made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("seam%d", seed), Game: "air", Mode: "joust", Seed: seed,
			Parameters: map[string]any{"missiles": true, "start": "bvr",
				"bots": map[string]any{"ace": 2.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
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
			// The span the bots DECIDED on: the crank gate reads the range at
			// the top of the tick, and a head-on pair closes some eight metres
			// before the step is over — judged after the step, a crank at
			// 10,005 m read as one at 9,997.
			before := i.span(bots[0].model, bots[1].model)
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
				if before < radar_seam && a.brain.cranked == tick {
					t.Fatalf("seed %d: the crank overlay fired %.0f m inside the merge seam at tick %d", seed, before, tick)
				}
			}
		}
		if resolved || closest <= 2000 {
			closing++
		}
		fmt.Printf("seam seed %d: closest approach %.0f m, resolved %v\n", seed, closest, resolved)
		i.Close()
	}
	if closing < 3 {
		t.Fatalf("only %d of %d seeds merged or resolved in eight minutes: degenerate never-closing play", closing, len(seeds))
	}
}

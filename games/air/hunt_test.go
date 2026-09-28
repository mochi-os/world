// Mochi world: Bot radar and AMRAAM employment tests (hunt.go).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"

	"world/game"
	"world/games/air/flight"
	"world/games/air/round"
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
	// A doctrine sweep, and gated like the other ten (#222): 4 tiers x 6 seeds
	// x a 300 s limit is 24 full BVR fights, measured at 693 s - more than the
	// 10 minutes Go allows the WHOLE package, so leaving it ungated made
	// `make test` unpassable and presented as a timeout panic mid-fight rather
	// than a named failure.
	//
	// What it uniquely holds is statistical - the tier ladder's two ends, over
	// 24 fights - which is exactly what a sweep is for. The behavioural rule it
	// also happens to check, that only the machine tier arms a jammer, is
	// covered in full and in milliseconds by TestJammerStaysWithTheMachine
	// below, so nothing fast is lost by moving this behind the gate.
	heavy(t)
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

// TestJammerStaysWithTheMachine pins a tier boundary that measurement chose
// over argument. Every craft carries the same jammer and the same loud rules,
// and arming it is one range comparison -- by every structural test it is
// doctrine, which would put it at library >= 3 alongside the ace. Measured
// 2026-09-08, that collapses the symmetric BVR seam: ace v ace fell from 3 of
// 4 seeds merging or resolving to 1 of 4, while machine v machine, where both
// sides already jam, held at 3 of 4. Two jamming pilots hold no lock on each
// other and only machine reflexes recover the fight. TestHuntSeam catches the
// regression indirectly, after seventy seconds of simulation; this names it.
func TestJammerStaysWithTheMachine(t *testing.T) {
	// Driven through the real defensive path, not by restating the condition.
	arm := func(level string, span float64) bool {
		b := NewBandit(level, 1, 250000, "", false, true, "open", 0, false)
		b.Spawn(flight.Vec3{X: span, Y: 8000}, flight.Vec3{X: -240})
		player := flight.New(b.craft.model.Airframe, b.arena.environment, flight.World{Sea: sea})
		player.State = flight.Level(player, flight.Vec3{Y: 8000}, flight.Vec3{X: 1}, 260, fuel)
		words := make([]float64, flight.Size)
		player.State.Encode(words)
		for tick := 0; tick < 90; tick++ {
			b.Mirror(words, false, true)
			b.Menace(menace(flight.Vec3{Y: 8000}, flight.Vec3{X: 900}, 0, float64(round.Pitbull)))
			b.Step()
		}
		return b.craft.brain.jam && b.craft.latest.Jammer
	}

	if !arm("superhuman", guard_quiet+6000) {
		t.Error("the machine did not arm its jammer against a spoofable inbound")
	}
	if arm("superhuman", guard_quiet-3000) {
		t.Error("the machine kept radiating inside the terminal call, where home-on-jam overrides every other defence")
	}
	for _, level := range []string{"novice", "pilot", "ace"} {
		if arm(level, guard_quiet+6000) {
			t.Errorf("%s armed a jammer: below the machine the trade does not pay (see the comment above)", level)
		}
	}
}

// radar_pair parks a BVR joust's two bots for the radar tests: the novice,
// which never withholds its lock, at the origin with its nose east at 250 m/s
// and 3,000 m, searching; the machine where each test puts it.
func radar_pair(t *testing.T) (*instance, *craft, *craft, int) {
	t.Helper()
	made, err := (&Air{}).Create(game.Session{Identifier: "huntradar", Game: "air", Mode: "joust", Seed: 5,
		Parameters: map[string]any{"missiles": true, "start": "bvr",
			"bots": map[string]any{"novice": 1.0, "superhuman": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	t.Cleanup(i.Close)
	i.environment.Wrap = 0 // an unwrapped sky, as radar.test.ts has it: the cases reach past half the arena
	var a, c *craft
	slot := -1
	for _, s := range i.slots() {
		if k := i.aircraft[s]; k != nil && k.bot {
			if k.brain.skill.machine {
				c, slot = k, s
			} else {
				a = k
			}
		}
	}
	if a == nil || c == nil {
		t.Fatal("joust roster wrong: the pair should be two bots")
	}
	a.model.State.Position = flight.Vec3{Y: 3000}
	a.model.State.Velocity = flight.Vec3{X: 250}
	a.model.State.Attitude = flight.Quat{W: 1}
	a.emitter, a.lock = 1, -1
	return i, a, c, slot
}

func park(c *craft, position flight.Vec3, velocity flight.Vec3) {
	c.model.State.Position, c.model.State.Velocity = position, velocity
}

// TestRadarDetection holds the bot's radar to the pilot's: the cases are
// radar.test.ts's, on the same numbers.
func TestRadarDetection(t *testing.T) {
	i, a, c, _ := radar_pair(t)
	const nm = 1852.0
	at := func(y, x float64) float64 {
		park(c, flight.Vec3{X: x, Y: y}, flight.Vec3{Z: 250}) // on the beam
		return i.detection(a, c)
	}
	park(c, flight.Vec3{X: 38 * nm, Y: 3000}, flight.Vec3{X: -272})
	if d := i.detection(a, c); math.Abs(d-44*nm) > 1 {
		t.Errorf("nose-on detection %.1f nm, want 44", d/nm)
	}
	if d := at(3000, 15*nm); math.Abs(d-55*nm) > 1 {
		t.Errorf("beam detection %.1f nm, want 55", d/nm)
	}
	clear := at(3000, 15*nm)
	if d := at(2999, 15*nm); math.Abs(d-clear) > 1e-6 {
		t.Errorf("a metre below at 15 nm paid %.3f of its range: the sky is behind it", 1-d/clear)
	}
	if d, level := at(2900, 40*nm), at(3000, 40*nm); math.Abs(d-level) > 1e-6 {
		t.Errorf("a hundred metres below at 40 nm paid %.3f of its range: the sky is still behind it", 1-d/level)
	}
	if d := at(500, 15*nm); math.Abs(d-clear*0.65) > 1 {
		t.Errorf("far below against the sea: %.1f nm, want %.1f", d/nm, clear*0.65/nm)
	}
	if d := at(3000, 60*nm); math.Abs(d-clear) > 1e-6 {
		t.Errorf("level at 60 nm paid %.3f of its range: the earth's curve is still above the horizon's dip", 1-d/clear)
	}
	low := flight.Vec3{Y: 300} // at 1,000 ft the horizon is 62 km off
	if k := i.clutter(low, flight.Vec3{X: 130000, Y: 300}); k <= 0.5 {
		t.Errorf("a jet level with us at 130 km from 300 m: clutter %.2f, want the sea behind it", k)
	}
	if k := i.clutter(low, flight.Vec3{X: 20000, Y: 300}); k != 0 {
		t.Errorf("a jet level with us at 20 km from 300 m: clutter %.2f, want the sky", k)
	}
	last := clear
	for y := 3000.0; y >= 0; y -= 10 {
		now := at(y, 15*nm)
		if now > last+1e-6 || last-now > 0.05*clear {
			t.Fatalf("descending through the horizon at %.0f m the range stepped from %.1f to %.1f nm", y, last/nm, now/nm)
		}
		last = now
	}
	if p := probability(10*nm, 40*nm); math.Abs(p-0.97) > 1e-9 {
		t.Errorf("paint odds well inside: %.2f, want 0.97", p)
	}
	if p := probability(39*nm, 40*nm); p >= 0.25 {
		t.Errorf("paint odds at the edge: %.2f, want under a quarter", p)
	}
	if p := probability(41*nm, 40*nm); p != 0 {
		t.Errorf("paint odds beyond the detection range: %.2f, want none", p)
	}
}

// TestRadarNotch: the notch is the target's own speed along the line of sight,
// as the pilot's radar and the seeker have it - not the two jets' closure.
func TestRadarNotch(t *testing.T) {
	i, a, c, _ := radar_pair(t)
	look := func() bool { // any look in ten frames
		for tick := uint64(0); tick < 10*radar_frame; tick += radar_frame {
			if i.painted(a, c, tick) {
				return true
			}
		}
		return false
	}
	park(c, flight.Vec3{X: 30000, Y: 3000}, flight.Vec3{Z: 250}) // beaming a radar that flies straight at it
	if look() {
		t.Error("a beaming target painted: its own speed along the line of sight is nil, the notch")
	}
	park(c, flight.Vec3{X: 30000, Y: 3000}, flight.Vec3{X: 250}) // running away at the radar's own speed
	if !look() {
		t.Error("a dragging target at 30 km never painted: the jets' closure is nil, but its own speed along the line of sight is not")
	}
	park(c, flight.Vec3{X: 30000, Y: 3000}, flight.Vec3{X: -250})
	if !look() {
		t.Error("a hot target at 30 km never painted")
	}
	park(c, flight.Vec3{X: 90 * 1852, Y: 3000}, flight.Vec3{X: -250})
	if look() {
		t.Error("a hot target at 90 nm painted, twice past its 44 nm detection range")
	}
}

// TestRadarMemory: a lock starved by the notch coasts four seconds on memory,
// then drops, as the pilot's STT does; a lock that tracks holds.
func TestRadarMemory(t *testing.T) {
	i, a, c, slot := radar_pair(t)
	b := a.brain
	b.target = slot
	park(c, flight.Vec3{X: 30000, Y: 3000}, flight.Vec3{X: -250})
	b.known[slot] = &track{when: 0, position: c.model.State.Position, velocity: c.model.State.Velocity}
	i.hunt(0, a, 1)
	if a.emitter != 2 || a.lock != slot {
		t.Fatalf("no STT on a hot target at 30 km (emitter %d, lock %d)", a.emitter, a.lock)
	}
	park(c, flight.Vec3{X: 30000, Y: 3000}, flight.Vec3{Z: 250}) // into the notch
	for tick := uint64(2); tick <= 2+radar_memory; tick++ {
		i.hunt(0, a, tick)
		if a.emitter != 2 || a.lock != slot {
			t.Fatalf("the starved lock dropped %.2f s in, inside the %d s memory", float64(tick-2)/60, radar_memory/60)
		}
	}
	i.hunt(0, a, 3+radar_memory)
	if a.emitter == 2 {
		t.Fatalf("the starved lock outlasted its %d s memory", radar_memory/60)
	}
}

// TestRadarHold: a lock acquires inside the detection range and, once held,
// holds out to radar_hold of it; the cone and the notch gate both.
func TestRadarHold(t *testing.T) {
	i, a, c, _ := radar_pair(t)
	park(c, flight.Vec3{X: 48 * 1852, Y: 3000}, flight.Vec3{X: -250}) // past the 44 nm nose-on detection, inside 1.15 of it
	if i.trackable(a, c, false, 1) {
		t.Error("a lock acquired at 48 nm, past the 44 nm detection range")
	}
	if !i.trackable(a, c, true, 1) {
		t.Error("a held lock dropped at 48 nm, inside 1.15 of the detection range")
	}
	park(c, flight.Vec3{X: 52 * 1852, Y: 3000}, flight.Vec3{X: -250})
	if i.trackable(a, c, true, 1) {
		t.Error("a held lock survived at 52 nm, past 1.15 of the detection range")
	}
	park(c, flight.Vec3{X: -20000, Y: 3000}, flight.Vec3{X: 250}) // behind the radar
	if i.trackable(a, c, false, 1) {
		t.Error("a lock acquired on a target behind the gimbal")
	}
	park(c, flight.Vec3{X: 20000, Y: 3000}, flight.Vec3{Z: 250})
	if i.trackable(a, c, false, 1) {
		t.Error("a fresh lock acquired on a target in the notch")
	}
}

// TestRadarCounterfire: the counter-shot a withholding tier takes under an
// inbound round needs a lock the same radar can hold - none on a target in the
// notch, which a beam puts it in, and one on a target running straight away.
func TestRadarCounterfire(t *testing.T) {
	shot := func(velocity flight.Vec3, x float64) bool {
		i, c, a, self := radar_pair(t) // the machine withholds: it is the one that counterfires, at the novice
		slot := -1
		for _, s := range i.slots() {
			if i.aircraft[s] == c {
				slot = s
			}
		}
		i.merged = true
		a.model.State.Position, a.model.State.Velocity, a.model.State.Attitude = flight.Vec3{Y: 3000}, flight.Vec3{X: 250}, flight.Quat{W: 1}
		park(c, flight.Vec3{X: x, Y: 3000}, velocity)
		a.brain.target, a.brain.alerted = slot, 1
		inbound := round.New(flight.Vec3{X: -4000, Y: 3000}, flight.Vec3{X: 900}, nil, 0) // an active round closing from behind
		i.flying = append(i.flying, &missile{radar: inbound, shooter: slot, target: self, position: inbound.Position, velocity: inbound.Velocity, life: 60})
		before := a.amraams
		i.counterfire(self, a, 100)
		return a.brain.countered == 100 && (a.amraams < before || i.cheat.ammunition)
	}
	if shot(flight.Vec3{Z: 250}, 10000) {
		t.Error("counterfired on a beaming target: it sits in the notch")
	}
	if !shot(flight.Vec3{X: 250}, 6000) {
		t.Error("no counter-shot at a target running straight away at 6 km: out of the notch, inside the zone")
	}
}

// TestRadarPicture: the bot fights from what it senses. While its STT holds it
// has the jet as it is; once the lock is lost it has only its last look,
// carried on by that look's velocity - a notch costs it the picture too.
func TestRadarPicture(t *testing.T) {
	i, a, c, slot := radar_pair(t)
	b := a.brain
	b.target = slot
	park(c, flight.Vec3{X: 30000, Y: 3000}, flight.Vec3{X: -250})
	b.known[slot] = &track{when: 0, position: c.model.State.Position, velocity: c.model.State.Velocity}
	i.hunt(0, a, 1) // acquires the lock
	i.hunt(0, a, 2)
	if b.contact != c.model.State.Position {
		t.Fatalf("locked, the picture is %+v, not the jet at %+v", b.contact, c.model.State.Position)
	}
	a.emitter, a.lock = 1, -1                                             // the lock lost
	park(c, flight.Vec3{X: 30000, Y: 3000, Z: 4000}, flight.Vec3{Z: 250}) // and he has beamed away since the last look
	i.hunt(0, a, 120)
	want := flight.Vec3{X: 30000 - 250*2, Y: 3000}
	if b.contact.Subtract(want).Length() > 1e-6 {
		t.Fatalf("unlocked, the picture is %+v: want the last look carried on two seconds, %+v", b.contact, want)
	}
}

// TestRadarParity reads the pilot's radar (radar.ts) where the monorepo has it
// and holds the bot's constants to it.
func TestRadarParity(t *testing.T) {
	source, err := os.ReadFile("../../../apps/air/web/src/game/radar.ts")
	if err != nil {
		t.Skip("apps/air is not beside world: the parity check needs the monorepo")
	}
	read := func(pattern string) float64 {
		m := regexp.MustCompile(pattern).FindSubmatch(source)
		if m == nil {
			t.Fatalf("radar.ts: %s not found", pattern)
		}
		v, err := strconv.ParseFloat(string(m[1]), 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		name      string
		ts, world float64
	}{
		{"BASE", read(`const BASE = ([0-9.]+) \* NM`) * 1852, radar_base},
		{"CLUTTER", read(`const CLUTTER = ([0-9.]+)`), radar_clutter},
		{"EDGE", read(`const EDGE = ([0-9.]+)`), radar_edge},
		{"EARTH", read(`const EARTH = ([0-9.]+)`), earth},
		{"HOLD", read(`const HOLD = ([0-9.]+)`), radar_hold},
		{"MEMORY", read(`const MEMORY = ([0-9.]+)`), radar_memory / 60},
		{"NOTCH", read(`const NOTCH = ([0-9.]+)`), round.Notch},
		{"aspect", read(`return 1 - ([0-9.]+) \* along`), 1 - aspect(flight.Vec3{X: 1}, flight.Vec3{X: 100})},
		{"stationary", read(`if \(speed < 20\) return ([0-9.]+)`), aspect(flight.Vec3{X: 1}, flight.Vec3{})},
	} {
		if math.Abs(c.ts-c.world) > 1e-9 {
			t.Errorf("%s: the pilot's radar has %g, the bot's %g", c.name, c.ts, c.world)
		}
	}
}

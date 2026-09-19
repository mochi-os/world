// Mochi world: the structural stages behind tactics.stage — what each one is, pinned
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/flight"
)

// line sums a rehearsed line's samples the way rehearse() does.
func line(samples []float64) (weighed, last, before float64) {
	for n, one := range samples {
		weighed += confidence(float64(n+1)/2) * one
		before, last = last, one
	}
	return
}

// TestEndsShareOneDivisor pins the three properties the stage 8 scorer exists
// for, each of which one of the declined repairs lacked.
func TestEndsShareOneDivisor(t *testing.T) {
	window := 12 * 60
	flat := func(value float64, seconds int) []float64 {
		out := make([]float64, seconds*2)
		for n := range out {
			out[n] = value
		}
		return out
	}
	value := func(samples []float64) float64 {
		weighed, last, before := line(samples)
		return ends(weighed, last, before, float64(len(samples)), len(samples)*30, window)
	}
	// A line worth the same at every instant is worth that, whatever its span.
	for _, seconds := range []int{4, 6, 12} {
		if got := value(flat(0.4, seconds)); math.Abs(got-0.4) > 1e-9 {
			t.Errorf("a constant 0.4 over %d s scored %.4f", seconds, got)
		}
	}
	// A short play that stops where a long one carries on level scores the same
	// as the long one: nothing is gained or lost by being judged briefly.
	short := append(flat(0.5, 2), flat(-0.2, 2)...)
	long := append(append(flat(0.5, 2), flat(-0.2, 2)...), flat(-0.2, 8)...)
	if a, b := value(short), value(long); math.Abs(a-b) > 1e-9 {
		t.Errorf("the same line judged over 4 s and over 12 s scored %.4f and %.4f", a, b)
	}
	// A short play is charged for where it leaves the jet. Under the per-play
	// mean this line scored +0.15; its end state is a fight being lost.
	if got := value(short); got >= 0 {
		t.Errorf("a line ending at -0.2 scored %.4f over the common window: the end state is not being charged", got)
	}
	// And the far end of a long play counts for less than its near end.
	early := append(flat(1, 4), flat(0, 8)...)
	late := append(flat(0, 8), flat(1, 4)...)
	if a, b := value(early), value(late); a <= b {
		t.Errorf("four good seconds now (%.4f) must outscore four good seconds at the far end of the window (%.4f)", a, b)
	}
	if confidence(0) != 1 || confidence(12) != 0.1 || confidence(4) <= confidence(8) {
		t.Errorf("the confidence curve is not the measured one: %.2f %.2f %.2f %.2f", confidence(0), confidence(4), confidence(8), confidence(12))
	}
}

// TestStagedPlaysStayOut: the plays the structural stages add are not
// candidates below their stage, and are at it.
func TestStagedPlaysStayOut(t *testing.T) {
	for _, stage := range []int{0, 8, 9, 10} {
		i, ace, prey := duellist(t, "drone")
		ace.brain.tactics.stage = stage
		aloft(ace, flight.Vec3{Y: 4000}, flight.Vec3{X: 180})
		aloft(prey, flight.Vec3{X: 900, Y: 4000, Z: 300}, flight.Vec3{Z: 160})
		for tick := uint64(0); tick < 90; tick++ {
			i.Step(tick, nil)
		}
		b := ace.brain
		if b.prey == nil {
			t.Fatalf("stage %d: the ace never took the drone as its target", stage)
		}
		scores := map[string]float64{}
		sim := flight.New(ace.model.Airframe, ace.model.Environment, ace.model.World)
		i.choose(1, ace, b, sim, b.prey, 90, b.distance, scores)
		_, bleed := scores["bleed"]
		_, regain := scores["regain"]
		if bleed != (stage >= 9) || regain != (stage >= 10) {
			t.Errorf("stage %d: bleed rehearsed %v, regain rehearsed %v", stage, bleed, regain)
		}
		if stage == 10 {
			// A stage omitted from the stack is out, whatever sits above it.
			b.tactics.omit = 1 << 9
			alone := map[string]float64{}
			i.choose(1, ace, b, sim, b.prey, 90, b.distance, alone)
			if _, found := alone["bleed"]; found {
				t.Errorf("stage 10 with stage 9 omitted still rehearsed bleed")
			}
			if _, found := alone["regain"]; !found {
				t.Errorf("stage 10 with stage 9 omitted lost regain with it")
			}
		}
	}
}

// TestBleedKeepsItsDemand: the licence reaches the stick. polish() holds every
// other play under corner discipline and the aero cap; the bleed is the one
// play that must arrive whole, or it is a lag turn at idle.
func TestBleedKeepsItsDemand(t *testing.T) {
	for _, play := range []string{"press", "bleed"} {
		i, ace, prey := duellist(t, "drone")
		ace.brain.tactics.stage = 9
		slow := flight.Vec3{X: 120}
		for tick := uint64(0); tick < 120; tick++ {
			aloft(ace, flight.Vec3{Y: 4000}, slow)
			aloft(prey, flight.Vec3{X: 700, Y: 4000, Z: 500}, flight.Vec3{Z: 120})
			ace.brain.play, ace.brain.until = play, tick+60 // hold the line under test: the arbiter's own pick is not what is being measured
			i.Step(tick, nil)
		}
		d := ace.brain.demand
		if ace.brain.mode != play {
			t.Fatalf("%s: the ace flew %q instead", play, ace.brain.mode)
		}
		if play == "bleed" && d.Capped < d.Law-1e-9 {
			t.Errorf("bleed: the law asked %.2f g and %.2f reached the stick: the licence is not reaching polish()", d.Law, d.Capped)
		}
		if play == "press" && d.Capped >= d.Law {
			t.Errorf("press: %.2f g asked and %.2f sent at 120 m/s: the cap has stopped working, so this test proves nothing about the bleed", d.Law, d.Capped)
		}
	}
}

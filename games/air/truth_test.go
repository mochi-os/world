// Mochi world: truth - the rehearsal flies the jet the bot flies
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"sort"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// truthfully puts a bandit at a stage with every one of stage 6's corrections
// on, whatever AIR_TRUTH_PARTS the doctrine battery is flying: these tests pin
// each correction's own effect, not the stack under evaluation.
func truthfully(b *Bandit, stage int) {
	b.Stage(stage, 0)
	b.craft.brain.tactics.truth.parts = 0
}

// turned flies a six-second level turn from one entry speed, the ace's own
// capped and disciplined pull on an aim held 90 degrees off the path on the
// horizon, either through the real flight model and the brain's executor or
// through the rehearsal's point-mass surrogate. It reports the height gained,
// the heading turned and the speed kept.
func turned(t *testing.T, entry float64, stage int, rehearsed bool) (climb, heading, speed float64) {
	t.Helper()
	b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, false)
	truthfully(b, stage)
	b.Spawn(flight.Vec3{Y: 4000}, flight.Vec3{X: entry})
	m, brain := b.craft.model, b.craft.brain
	shadow := *brain
	pace := corner(m)
	for tick := 1; tick <= 60*6; tick++ {
		v := m.State.Velocity
		flat := flight.Vec3{X: v.X, Z: v.Z}.Normalize()
		flown := v.Length()
		g := disciplined(brain.skill.pull, flown, pace)
		g = brain.skill.capped(g, flown, pace/math.Sqrt(m.Airframe.Limit.Positive))
		o := order{aim: flight.Vec3{X: -flat.Z, Y: 0.02, Z: flat.X}.Normalize(), g: g, throttle: 1, reheat: 1,
			gravity: brain.tactics.on(6), weighed: brain.tactics.on(6)}
		if rehearsed {
			glide(m, o, 4*flight.Dt)
		} else {
			shadow.aim, shadow.g, shadow.throttle, shadow.reheat, shadow.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
			in := shadow.steer(m, uint64(tick))
			for sub := 0; sub < 4; sub++ {
				m.Step(in)
			}
		}
		after := flight.Vec3{X: m.State.Velocity.X, Z: m.State.Velocity.Z}.Normalize()
		heading += math.Asin(clamp(flat.X*after.Z-flat.Z*after.X, -1, 1))
	}
	return m.State.Position.Y - 4000, heading * 180 / math.Pi, m.State.Velocity.Length()
}

// TestTruthHoldsHeight: at stage 6 the rehearsed level turn stays within 75 m
// of the height the jet actually flies; the rehearsal as it stands climbs
// about one g's worth, 177-250 m in six seconds, and the control arm pins that.
func TestTruthHoldsHeight(t *testing.T) {
	for _, entry := range []float64{230, 170, 130} {
		flown, _, _ := turned(t, entry, 6, false)
		rehearsed, _, _ := turned(t, entry, 6, true)
		if gap := math.Abs(rehearsed - flown); gap > 75 {
			t.Errorf("stage 6, entry %.0f m/s: rehearsed %+.0f m against %+.0f m flown, %.0f m apart", entry, rehearsed, flown, gap)
		}
		before, _, _ := turned(t, entry, 0, false)
		optimistic, _, _ := turned(t, entry, 0, true)
		if optimistic-before < 150 {
			t.Errorf("stage 0, entry %.0f m/s: the control no longer shows the climb (%+.0f m rehearsed, %+.0f m flown): this test has lost its reference", entry, optimistic, before)
		}
	}
}

// TestTruthDeliversTheAsk: at stage 6 the g the brain commands below corner is
// the g that arrives, within 5%; as it stands a sixth of it goes missing in the
// stick mapping (the control arm).
func TestTruthDeliversTheAsk(t *testing.T) {
	for _, stage := range []int{0, 6} {
		b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, false)
		truthfully(b, stage)
		b.Spawn(flight.Vec3{X: 1500, Y: 3000}, flight.Vec3{X: -150})
		environment := flight.Environment{Seed: 7, Wrap: 250000}
		player := flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea})
		player.State = flight.Level(player, flight.Vec3{Y: 3000}, flight.Vec3{X: 1}, 140, fuel)
		words := make([]float64, flight.Size)
		var ratios []float64
		for tick := uint64(1); tick <= 60*60; tick++ {
			// A slow, tight, level turn that keeps the bandit below corner and pulling.
			heading := 9.81 * math.Sqrt(3.2*3.2-1) / 140 * float64(tick) / 60
			player.State.Velocity = flight.Vec3{X: 140 * math.Cos(heading), Z: 140 * math.Sin(heading)}
			player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
			player.State.Encode(words)
			b.Mirror(words, false, true)
			b.Menace(nil)
			b.Step()
			m := b.craft.model
			if tick%6 != 0 || m.State.Velocity.Length() >= corner(m) || b.craft.brain.demand.Capped < 2 {
				continue
			}
			ratios = append(ratios, m.Nz()/b.craft.brain.demand.Capped)
		}
		if len(ratios) < 100 {
			t.Fatalf("stage %d: only %d sub-corner turning samples", stage, len(ratios))
		}
		sort.Float64s(ratios)
		median := ratios[len(ratios)/2]
		t.Logf("stage %d: delivered / commanded, median %.3f over %d samples", stage, median, len(ratios))
		switch {
		case stage == 6 && math.Abs(median-1) > 0.05:
			t.Errorf("stage 6 delivers %.3f of the g it commands below corner", median)
		case stage == 0 && median > 0.9:
			t.Errorf("stage 0 delivers %.3f: the control no longer shows the shortfall, so this test has lost its reference", median)
		}
	}
}

// TestTruthCeiling: at stage 6 the rehearsal holds a full pull to the ceiling
// the flight control system enforces at this weight, not the bare placard.
func TestTruthCeiling(t *testing.T) {
	for _, weighed := range []bool{false, true} {
		b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, false)
		b.Spawn(flight.Vec3{Y: 1000}, flight.Vec3{X: 300}) // fast and low: the wing can give more than any limiter allows
		m := b.craft.model
		v := m.State.Velocity.Normalize()
		o := order{aim: flight.Vec3{X: -v.Z, Z: v.X}, g: 9, throttle: 1, reheat: 1, weighed: weighed}
		glide(m, o, 4*flight.Dt)
		want := m.Airframe.Limit.Positive
		if weighed {
			want *= math.Min(1, m.Airframe.Limit.Reference/m.Mass())
		}
		if got := m.State.Fcs.Normal; math.Abs(got-want) > 1e-9 {
			t.Errorf("weighed %v: the rehearsal pulled %.3f g where the limiter allows %.3f", weighed, got, want)
		}
		if weighed && want >= m.Airframe.Limit.Positive {
			t.Errorf("the jet at %.0f kg is not above the reference weight: the case proves nothing", m.Mass())
		}
	}
}

// TestTruthIsWired: the arbiter's own rehearsal (rehearse, not glide called by
// hand) flies the true geometry, the live jet's weight and the weighed ceiling
// at stage 6 and none of them at stage 0. A hard left turn rehearsed from level
// flight at 170 m/s must not climb; a full pull rehearsed at 300 m/s by a jet
// carrying heaters must stop at the ceiling scheduled for ITS weight.
func TestTruthIsWired(t *testing.T) {
	var left play
	for _, p := range plays {
		if p.name == "left" {
			left = p
		}
	}
	rehearsed := func(stage int, position, velocity flight.Vec3, horizon int) (*flight.Model, float64) {
		// Armed with heaters, so the jet carries stores: the scratch model's own
		// fallback (the empty airframe and internal fuel) is a clean jet over a
		// tonne lighter, and only the live mass gives the scheduled ceiling below.
		i := build(t, "furball", map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{"ace": 1.0, "drone": 1.0}}, 0)
		var ace, prey *craft
		for _, slot := range i.slots() {
			if c := i.aircraft[slot]; c != nil && c.brain != nil {
				ace = c
			} else if c != nil && c.bot {
				prey = c
			}
		}
		if ace == nil || prey == nil {
			t.Fatal("the furball did not seat an ace and a drone")
		}
		ace.brain.tactics.stage, ace.brain.tactics.truth.parts = stage, 0
		for tick := uint64(0); tick < 90; tick++ {
			aloft(ace, position, velocity)
			aloft(prey, position.Add(flight.Vec3{X: 3000, Z: 3000}), flight.Vec3{Z: 150})
			i.Step(tick, nil)
		}
		if ace.brain.prey == nil {
			t.Fatalf("stage %d: the ace never took the drone as its target", stage)
		}
		aloft(ace, position, velocity)
		sim := flight.New(ace.model.Airframe, ace.model.Environment, ace.model.World)
		i.rehearse(ace, ace.brain, sim, left, ace.brain.prey, 90, horizon, 0)
		return sim, ace.model.Mass()
	}
	for _, stage := range []int{0, 6} {
		slow, _ := rehearsed(stage, flight.Vec3{Y: 4000}, flight.Vec3{X: 170}, 360)
		climbed := slow.State.Position.Y - 4000
		fast, mass := rehearsed(stage, flight.Vec3{Y: 1000}, flight.Vec3{X: 300}, 60)
		// The same rollout through the FULL model, which weighs itself every step:
		// at stage 6 it must weigh the stores the jet is carrying.
		live := rehearsal
		rehearsal = full
		whole, _ := rehearsed(stage, flight.Vec3{Y: 1000}, flight.Vec3{X: 300}, 60)
		rehearsal = live
		if carried := math.Abs(whole.Mass() - mass); stage == 6 && carried > 50 {
			t.Errorf("stage 6: the full-model rollout weighs %.0f kg against the live jet's %.0f: it is not carrying the jet's stores", whole.Mass(), mass)
		} else if stage == 0 && carried < 500 {
			t.Errorf("stage 0: the full-model rollout weighs %.0f kg, the live jet %.0f: the control no longer shows the clean-jet rollout", whole.Mass(), mass)
		}
		pulled, placard := fast.State.Fcs.Normal, fast.Airframe.Limit.Positive
		scheduled := placard * math.Min(1, fast.Airframe.Limit.Reference/mass)
		if fallback := fast.Airframe.Mass.Empty + fast.State.Fuel; mass-fallback < 500 {
			t.Fatalf("the ace flies at %.0f kg against a fallback of %.0f kg: it carries no stores, so the weight case proves nothing", mass, fallback)
		}
		t.Logf("stage %d: six seconds of `left` climbed %+.0f m; a full pull at 300 m/s held %.2f g (placard %.1f, scheduled %.2f)", stage, climbed, pulled, placard, scheduled)
		if stage == 6 {
			if climbed > 120 {
				t.Errorf("stage 6 rehearsed a level hard turn climbing %.0f m: the true geometry is not reaching glide", climbed)
			}
			if math.Abs(pulled-scheduled) > 1e-6 {
				t.Errorf("stage 6 rehearsed a full pull at %.3f g where the limiter allows %.3f g at the live jet's weight", pulled, scheduled)
			}
		} else {
			if climbed < 150 || pulled < placard-1e-6 {
				t.Errorf("stage 0 no longer shows the optimistic rehearsal (climbed %.0f m, pulled %.2f g): this test has lost its reference", climbed, pulled)
			}
		}
	}
}

// rolled flies four seconds from wings-level flight at 180 m/s toward an aim
// held fixed in the world, the ace's capped and disciplined pull, either
// through the real flight model and the brain's executor or through the
// rehearsal's surrogate with or without stage 14's roll law. It reports the
// bank every half second and the mean load over the first second.
func rolled(t *testing.T, aim flight.Vec3, rehearsed, rolling bool) (bank []float64, load float64) {
	t.Helper()
	b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, false)
	truthfully(b, 14)
	b.Spawn(flight.Vec3{Y: 4000}, flight.Vec3{X: 180})
	m, brain := b.craft.model, b.craft.brain
	shadow := *brain
	pace := corner(m)
	aim = aim.Normalize()
	for tick := 1; tick <= 60*4; tick++ {
		flown := m.State.Velocity.Length()
		g := disciplined(brain.skill.pull, flown, pace)
		g = brain.skill.capped(g, flown, pace/math.Sqrt(m.Airframe.Limit.Positive))
		o := order{aim: aim, g: g, throttle: 1, reheat: 1, gravity: true, weighed: true, rolling: rolling}
		if rehearsed {
			glide(m, o, 4*flight.Dt)
		} else {
			shadow.aim, shadow.g, shadow.throttle, shadow.reheat, shadow.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
			in := shadow.steer(m, uint64(tick))
			for sub := 0; sub < 4; sub++ {
				m.Step(in)
			}
		}
		if tick <= 60 {
			load += m.State.Fcs.Normal / 60
		}
		if tick%30 == 0 {
			v := m.State.Velocity.Normalize()
			up := m.State.Attitude.Rotate(flight.Vec3{Y: 1})
			up = up.Subtract(v.Scale(up.Dot(v))).Normalize()
			sky := flight.Vec3{Y: 1}.Subtract(v.Scale(v.Y)).Normalize()
			bank = append(bank, math.Acos(clamp(up.Dot(sky), -1, 1))*180/math.Pi)
		}
	}
	return bank, load
}

// TestTruthRollsBeforeItPulls: at stage 14 the rehearsal rolls onto a turn as
// steer()'s roll law does, easing in rather than slewing at the full rate, and
// holds back the pull while the wings are off the plane they want. Turning in
// toward an aim 90 degrees off, the bank it rehearses at 0.5 and 1 s is within
// 10 degrees of the bank the jet flies (the surrogate as it stands is there in
// 0.4 s, over 30 degrees early); reversing toward an aim 160 degrees behind,
// its load over the first second is within 0.4 g of the jet's (as it stands it
// pulls the whole load through the roll, over 3 g too much).
func TestTruthRollsBeforeItPulls(t *testing.T) {
	flown, _ := rolled(t, flight.Vec3{Z: -1}, false, false)
	rehearsed, _ := rolled(t, flight.Vec3{Z: -1}, true, true)
	optimistic, _ := rolled(t, flight.Vec3{Z: -1}, true, false)
	t.Logf("turn-in bank at 0.5 and 1 s: flown %.0f %.0f, stage 14 %.0f %.0f, as it stands %.0f %.0f", flown[0], flown[1], rehearsed[0], rehearsed[1], optimistic[0], optimistic[1])
	for k := range 2 {
		if gap := math.Abs(rehearsed[k] - flown[k]); gap > 10 {
			t.Errorf("stage 14 rehearsed %.0f degrees of bank at %.1f s where the jet flies %.0f", rehearsed[k], 0.5*float64(k+1), flown[k])
		}
	}
	if optimistic[0]-flown[0] < 25 {
		t.Errorf("as it stands the rehearsal banks %.0f degrees at 0.5 s against %.0f flown: the control no longer shows the full-rate roll, so this test has lost its reference", optimistic[0], flown[0])
	}
	behind := flight.Vec3{X: -0.94, Y: -0.34}
	_, pulled := rolled(t, behind, false, false)
	_, held := rolled(t, behind, true, true)
	_, whole := rolled(t, behind, true, false)
	t.Logf("reversal, load over the first second: flown %.2f g, stage 14 %.2f, as it stands %.2f", pulled, held, whole)
	if gap := math.Abs(held - pulled); gap > 0.4 {
		t.Errorf("stage 14 rehearsed %.2f g through the reversal's roll where the jet pulls %.2f", held, pulled)
	}
	if whole-pulled < 3 {
		t.Errorf("as it stands the rehearsal pulls %.2f g through the roll against %.2f flown: the control no longer shows it, so this test has lost its reference", whole, pulled)
	}
}

// TestRollingIsWired: the arbiter's own rehearsal (rehearse, not glide called
// by hand) flies stage 14's roll law at stage 14 and not below it. The same
// half second of `left` from wings-level flight at 170 m/s turns less when the
// rehearsal rolls onto the turn before it pulls.
func TestRollingIsWired(t *testing.T) {
	var left play
	for _, p := range plays {
		if p.name == "left" {
			left = p
		}
	}
	turned := map[int]float64{}
	for _, stage := range []int{13, 14} {
		i := build(t, "furball", map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{"ace": 1.0, "drone": 1.0}}, 0)
		var ace, prey *craft
		for _, slot := range i.slots() {
			if c := i.aircraft[slot]; c != nil && c.brain != nil {
				ace = c
			} else if c != nil && c.bot {
				prey = c
			}
		}
		if ace == nil || prey == nil {
			t.Fatal("the furball did not seat an ace and a drone")
		}
		ace.brain.tactics.stage, ace.brain.tactics.truth.parts = stage, 0
		position, velocity := flight.Vec3{Y: 4000}, flight.Vec3{X: 170}
		for tick := uint64(0); tick < 90; tick++ {
			aloft(ace, position, velocity)
			aloft(prey, position.Add(flight.Vec3{X: 3000, Z: 3000}), flight.Vec3{Z: 150})
			i.Step(tick, nil)
		}
		if ace.brain.prey == nil {
			t.Fatalf("stage %d: the ace never took the drone as its target", stage)
		}
		aloft(ace, position, velocity)
		sim := flight.New(ace.model.Airframe, ace.model.Environment, ace.model.World)
		i.rehearse(ace, ace.brain, sim, left, ace.brain.prey, 90, 30, 0)
		v := sim.State.Velocity
		turned[stage] = math.Abs(math.Atan2(v.Z, v.X)) * 180 / math.Pi
	}
	t.Logf("half a second of `left`: turned %.1f degrees at stage 13, %.1f at stage 14", turned[13], turned[14])
	if turned[14] > 0.8*turned[13] {
		t.Errorf("stage 14 rehearsed %.1f degrees of turn in half a second against %.1f at stage 13: the roll law is not reaching glide", turned[14], turned[13])
	}
}

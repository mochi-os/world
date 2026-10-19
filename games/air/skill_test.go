// Mochi world: the skill table's own rules
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

// TestAeroCapByTier: the aero cap is a skill, not a law of the airframe. One
// coefficient capped every tier at 85% of what the wing gives, so a novice -
// whose whole authenticity is getting slow without noticing - flew the same
// energy discipline as the ace and never mushed. The wing's g at a speed is
// (speed/stall)^2. The coefficients pinned here are the ones the doctrine
// battery is green on: 1.5 on the flying tiers bought the slow fight and
// lost seven gates, 1.2 three, and a cap lifted only inside the close fight
// four - the energy doctrine and the slow fight are in tension at this
// number (#153), and the gates decide it.
func TestAeroCapByTier(t *testing.T) {
	const demand, stall = 7.5, 60.0 // a full pull, and a 117 kt stall
	wing := func(speed float64) float64 { return (speed / stall) * (speed / stall) }
	slow := 90.0 // 175 kt: the wing gives 2.25 g here

	novice, pilot, ace, machine := skills["novice"], skills["pilot"], skills["ace"], skills["superhuman"]

	if got := novice.capped(demand, slow, stall); got != demand {
		t.Errorf("novice commanded %.2f g of a %.1f g demand at %.0f m/s: the rookie has no cap, it pulls what it asks for", got, demand, slow)
	}
	if want := wing(slow); math.Abs(pilot.capped(demand, slow, stall)-want) > 1e-9 {
		t.Errorf("pilot commanded %.2f g at %.0f m/s, want exactly the wing's %.2f: no margin, no excess", pilot.capped(demand, slow, stall), slow, want)
	}
	if want := 0.85 * wing(slow); math.Abs(ace.capped(demand, slow, stall)-want) > 1e-9 {
		t.Errorf("ace commanded %.2f g at %.0f m/s, want %.2f: 85%% of the wing, the margin that keeps it out of the mush", ace.capped(demand, slow, stall), slow, want)
	}
	if ace.capped(demand, slow, stall) != machine.capped(demand, slow, stall) {
		t.Errorf("superhuman %.2f g v ace %.2f g: the superhuman is the ace with the human dials at zero, and the cap is not a human dial",
			machine.capped(demand, slow, stall), ace.capped(demand, slow, stall))
	}

	// The ladder is monotone at a slow speed: the less disciplined the tier,
	// the more it will pull past the wing.
	if !(novice.capped(demand, slow, stall) > pilot.capped(demand, slow, stall) && pilot.capped(demand, slow, stall) > ace.capped(demand, slow, stall)) {
		t.Errorf("cap ladder not monotone at %.0f m/s: novice %.2f, pilot %.2f, ace %.2f", slow,
			novice.capped(demand, slow, stall), pilot.capped(demand, slow, stall), ace.capped(demand, slow, stall))
	}

	// A capped tier is never taken below 1.1 g, however slow: level flight and
	// a little more is always available. Nor is a demand ever RAISED.
	if got := ace.capped(demand, 30, stall); got != 1.1 {
		t.Errorf("ace at 30 m/s commanded %.2f g, want the 1.1 g floor", got)
	}
	if got := ace.capped(1.0, slow, stall); got != 1.0 {
		t.Errorf("a 1.0 g demand became %.2f g: the cap only ever lowers", got)
	}
	// And at corner speed and above, the wing gives the placard and the cap
	// costs nothing anyone would command.
	if fast := stall * math.Sqrt(7.5) * 1.2; ace.capped(demand, fast, stall) != demand {
		t.Errorf("ace above corner commanded %.2f g of %.1f: the cap should be inert where the wing has the g", ace.capped(demand, fast, stall), demand)
	}
}

// TestTheAceOutTurnsThePilotOnlyAboveCorner pins the crossover the tier table
// implies and nothing tested: the ace and the pilot do not command the same
// demand, so comparing them at a shared one (as the sibling above does, by
// design, to test the cap itself) hides where the ladder actually stands.
//
// Below corner the cap binds both and the ace, at 0.85 of the wing against the
// pilot's 1.0, is the WORSE turning fighter. Above it both saturate at their
// own pull and the ace's 7.5 beats the pilot's 7.2. The ordering reverses, and
// it reverses just above corner speed.
//
// That crossover is the mechanism behind the guns stalemate (#42) and the slow
// ladder inversion (#107), measured on the real airframe at 4,000 ft with
// corner at 394 kt: in the twelve undecided fights the ace averages 388 kt and
// commands 6.19 g against the pilot's 7.20 - 86% - and holds its nose inside
// 20 degrees for 1% of its time in the gun window against the pilot's 43%; in
// the four it wins it averages 462 kt, where the cap is inert, and holds 49%.
// It is the priced consequence of the energy doctrine, not a defect: 1.5, 1.2
// and a lift confined to the close fight were each measured and each lost
// gates (see capped()'s own comment). Read this test before proposing an
// arbitration remedy for the ace's conversion - no scorer can outvote a g
// limit.
func TestTheAceOutTurnsThePilotOnlyAboveCorner(t *testing.T) {
	const stall = 60.0 // 117 kt, the sibling test's airframe
	corner := stall * math.Sqrt(7.5)
	pilot, ace := skills["pilot"], skills["ace"]
	commanded := func(s skill, speed float64) float64 { return s.capped(s.pull, speed, stall) }

	slow := 0.8 * corner
	if commanded(ace, slow) >= commanded(pilot, slow) {
		t.Errorf("at %.0f kt the ace commands %.2f g and the pilot %.2f: below corner the cap is meant to make the ace the worse turning fighter",
			slow*1.944, commanded(ace, slow), commanded(pilot, slow))
	}
	if share := commanded(ace, slow) / commanded(pilot, slow); math.Abs(share-0.85) > 1e-9 {
		t.Errorf("the ace holds %.0f%% of the pilot's g at %.0f kt, want 85%%: both are capped there, so the ratio IS the coefficient ratio",
			100*share, slow*1.944)
	}

	fast := 1.2 * corner
	if commanded(ace, fast) <= commanded(pilot, fast) {
		t.Errorf("at %.0f kt the ace commands %.2f g and the pilot %.2f: above corner the cap is inert and the ace's higher pull must tell",
			fast*1.944, commanded(ace, fast), commanded(pilot, fast))
	}

	// The crossover itself: the ladder is not restored at corner speed, only
	// past it, so a fight held AT corner still belongs to the pilot.
	over := 0.0
	for v := 0.5 * corner; v <= 2*corner; v += 0.001 * corner {
		if commanded(ace, v) > commanded(pilot, v) {
			over = v
			break
		}
	}
	if over == 0 {
		t.Fatal("the ace never out-commands the pilot at any speed: the ladder has no crossover at all")
	}
	if over <= corner {
		t.Errorf("the crossover sits at %.0f kt, at or below corner (%.0f kt): the slow fight is supposed to be where the cap costs the ace, and this says it costs it nothing",
			over*1.944, corner*1.944)
	}
}

// TestSunGlareByTier: up-sun, a pilot cannot see into the glare about the
// disc. The pilot and the ace lose a jet within about 5 degrees, where the
// eye's own veil leaves it about 5% of its contrast (the client draws the same
// veil); the novice will not look near the sun at all and loses one within 20;
// the superhuman, a machine, sees straight through. Only by day.
func TestSunGlareByTier(t *testing.T) {
	// An observer at 3,000 m flying toward the sun, and a target 3 km away at
	// an angle from the sun's direction, turned toward the east.
	look := func(tier string, night bool, angle float64, mind bool) bool {
		i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, night: night}
		m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
		m.State = flight.Level(m, flight.Vec3{Y: 3000}, flight.Vec3{Z: 1}, 200, 2500)
		me := &craft{model: m, alive: true}
		if mind {
			me.brain = &brain{skill: skills[tier]}
		}
		r := angle * math.Pi / 180
		toward := glare.Scale(math.Cos(r)).Add(flight.Vec3{X: 1}.Scale(math.Sin(r))) // glare is a unit vector and X is square to it
		n := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
		n.State = flight.Level(n, m.State.Position.Add(toward.Scale(3000)), flight.Vec3{X: 1}, 200, 2500)
		other := &craft{model: n, alive: true}
		return i.visible(me, other, 0)
	}
	cases := []struct {
		tier    string
		angle   float64
		visible bool
	}{
		{"novice", 3, false}, {"novice", 10, false}, {"novice", 19, false}, {"novice", 21, true}, {"novice", 40, true},
		{"pilot", 3, false}, {"pilot", 5, false}, {"pilot", 6, true}, {"pilot", 19, true},
		{"ace", 3, false}, {"ace", 5, false}, {"ace", 6, true}, {"ace", 19, true},
		{"superhuman", 0.5, true}, {"superhuman", 3, true},
	}
	for _, c := range cases {
		if got := look(c.tier, false, c.angle, true); got != c.visible {
			t.Errorf("%s by day, a jet %.1f degrees from the sun: visible %v, want %v", c.tier, c.angle, got, c.visible)
		}
	}
	for _, tier := range []string{"novice", "pilot", "ace"} {
		if !look(tier, true, 3, true) {
			t.Errorf("%s at night lost a jet 3 degrees from the moon: the glare blinds only by day", tier)
		}
	}
	// A pilot with no mind of its own keeps the cone every pilot had (cos 0.996).
	if look("", false, 4, false) || !look("", false, 6, false) {
		t.Errorf("an observer with no brain: the 5 degree cone moved")
	}
	if math.Abs(math.Cos(skills["pilot"].glare*math.Pi/180)-0.996) > 1e-6 || skills["ace"].glare != skills["pilot"].glare {
		t.Errorf("the pilot and the ace keep the cone they always had, cos 0.996: pilot %v, ace %v", skills["pilot"].glare, skills["ace"].glare)
	}
	if skills["novice"].glare <= 3*skills["pilot"].glare {
		t.Errorf("the novice will not look near the sun: its cone %v is not wide against the pilot's %v", skills["novice"].glare, skills["pilot"].glare)
	}
}

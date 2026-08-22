// Mochi world: Battle tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"fmt"
	"math"
	"testing"

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

func target() (*Body, *flight.Model) {
	m := flight.New(fa18c.Airframe, flight.Environment{Seed: 7}, flight.World{Sea: 0})
	m.State = flight.Level(m, flight.Vec3{Y: 2000}, flight.Vec3{X: 1}, 180, fa18c.Airframe.Mass.Fuel*0.7)
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe), Damage: &m.State.Damage, Condition: &Condition{Damager: -1}}
	return body, m
}

// astern is a perfect tracking solution from 300 m behind the target.
func astern(m *flight.Model) Pose {
	behind := m.State.Position.Subtract(flight.Vec3{X: 300})
	return Pose{Position: behind, Forward: flight.Vec3{X: 1}, Up: flight.Vec3{Y: 1}, Right: flight.Vec3{Z: 1}}
}

// TestTraceElement: a ray aimed at a known wing element hits that element.
func TestTraceElement(t *testing.T) {
	body, _ := target()
	// Find the left wing's outboard element.
	var aim flight.Vec3
	want := -1
	for pi := range body.Parts {
		p := &body.Parts[pi]
		if p.Kind == Structure && body.Airframe.Surfaces[p.Surface].Kind == flight.Wing && body.Airframe.Surfaces[p.Surface].Side < 0 {
			want = pi // last one wins: outboard
			aim = p.A.Add(p.B).Scale(0.5)
		}
	}
	origin := aim.Subtract(flight.Vec3{X: 200}) // dead ahead of the element
	part, distance := trace(body.Parts, origin, flight.Vec3{X: 1}, reach)
	if part < 0 {
		t.Fatal("ray at a wing element missed everything")
	}
	if part != want && body.Parts[part].Kind != Structure {
		t.Fatalf("ray hit part %d (kind %d), want the wing element %d", part, body.Parts[part].Kind, want)
	}
	if distance < 150 || distance > 250 {
		t.Fatalf("hit distance %.0f m, want ~200", distance)
	}
}

// salvo fires one volley and flies it out tick by tick against the target,
// advancing the target on its own velocity each step (drive turns by mutating
// the model inside manoeuvre). Returns the hits landed and the impact points.
func salvo(pose Pose, body *Body, m *flight.Model, rounds int, tick uint64, manoeuvre func(step int)) (int, []flight.Vec3) {
	flying := Volley(pose, rounds, 7, 3, tick)
	const dt = 1.0 / 60
	hits := 0
	var impacts []flight.Vec3
	for step := 0; step < 180 && len(flying) > 0; step++ {
		if manoeuvre != nil {
			manoeuvre(step)
		}
		m.State.Position = m.State.Position.Add(m.State.Velocity.Scale(dt))
		alive := flying[:0]
		for i := range flying {
			r := &flying[i]
			hit, _, impact := Strike(r, m.State.Position, m.State.Attitude, m.State.Velocity, body, dt, 0, 7)
			if hit {
				hits++
				if len(impacts) < ImpactPoints {
					impacts = append(impacts, impact)
				}
				continue // a landed round is spent
			}
			if !Fly(r, dt) {
				alive = append(alive, *r)
			}
		}
		flying = alive
	}
	return hits, impacts
}

// TestVolleyDeterminism: identical inputs produce identical outcomes.
func TestVolleyDeterminism(t *testing.T) {
	first, m1 := target()
	second, m2 := target()
	h1, _ := salvo(astern(m1), first, m1, 50, 999, nil)
	h2, _ := salvo(astern(m2), second, m2, 50, 999, nil)
	if h1 != h2 {
		t.Fatalf("determinism broken: %d vs %d hits", h1, h2)
	}
	for i := range first.Damage.Element {
		if first.Damage.Element[i] != second.Damage.Element[i] {
			t.Fatal("determinism broken: element damage differs")
		}
	}
}

// TestVolleyLethality: two seconds of perfect tracking from dead astern must
// cripple the target — the time-to-kill tuning gate (1.5–3 s class). Dead
// astern is the no-lead geometry: the rounds chase him straight up the wake.
func TestVolleyLethality(t *testing.T) {
	body, m := target()
	total := 0
	for tick := uint64(0); tick < 120; tick++ { // 2 s at 60 Hz, ~1.7 rounds/tick
		rounds := 2
		if tick%3 == 0 {
			rounds = 1
		}
		hits, _ := salvo(astern(m), body, m, rounds, tick, nil)
		total += hits
	}
	if total < 10 {
		t.Fatalf("a 2 s tracking burst landed only %d hits — the gun cannot kill", total)
	}
}

// TestFireDrill: an engine fire grows under throttle, ramps thrust loss, and
// the idle drill extinguishes it.
func TestFireDrill(t *testing.T) {
	body, m := target()
	body.Condition.Fire[0] = 0.1
	for tick := uint64(0); tick < 300; tick++ { // 5 s at throttle
		Advance(body, m, 0.8, [2]bool{}, 60, 7, 3, tick)
	}
	if body.Condition.Fire[0] <= 0.1 {
		t.Fatal("fire did not grow under throttle")
	}
	if body.Damage.Engine[0] <= 0 {
		t.Fatal("a burning engine must lose thrust")
	}
	for tick := uint64(300); tick < 2000 && body.Condition.Fire[0] > 0; tick++ {
		Advance(body, m, 0.0, [2]bool{}, 60, 7, 3, tick)
	}
	if body.Condition.Fire[0] > 0 {
		t.Fatal("the idle drill failed to extinguish the fire")
	}
}

// TestSecureDrill: the per-engine cutoff (NATOPS 15.1) starves a fire with
// the throttle still at power — the good engine keeps fighting while the
// secured one burns out.
func TestSecureDrill(t *testing.T) {
	body, m := target()
	body.Condition.Fire[0] = 0.1
	for tick := uint64(0); tick < 2000 && body.Condition.Fire[0] > 0; tick++ {
		Advance(body, m, 0.8, [2]bool{true, false}, 60, 7, 3, tick)
	}
	if body.Condition.Fire[0] > 0 {
		t.Fatal("securing the burning engine at full throttle must starve its fire")
	}
	body.Condition.Fire[1] = 0.1
	for tick := uint64(0); tick < 300; tick++ {
		Advance(body, m, 0.8, [2]bool{true, false}, 60, 7, 3, tick)
	}
	if body.Condition.Fire[1] <= 0.1 {
		t.Fatal("the OTHER engine's fire must still feed on the open throttle")
	}
}

// TestFuse: a fuel fire always explodes within its 10–30 s window.
func TestFuse(t *testing.T) {
	body, m := target()
	ignite(body, 7, 3, 50)
	if !body.Condition.Burning || body.Condition.Fuse < 10 || body.Condition.Fuse > 30 {
		t.Fatalf("fuse %.1f s outside the 10–30 window", body.Condition.Fuse)
	}
	exploded := false
	for tick := uint64(0); tick < 60*31; tick++ {
		for _, e := range Advance(body, m, 0.5, [2]bool{}, 60, 7, 3, tick) {
			if e.Kind == "explode" {
				exploded = true
			}
		}
		if exploded {
			break
		}
	}
	if !exploded {
		t.Fatal("the fuel fire never exploded")
	}
}

// TestShed: accumulated overstress weakens the wing until a hard pull sheds
// it; a pristine jet at the same load keeps its wings.
func TestShed(t *testing.T) {
	pull := func(stress float64, normal float64) bool {
		body, m := target()
		body.Damage.Stress = stress
		m.State.Fcs.Normal = normal
		for _, e := range Advance(body, m, 0.5, [2]bool{}, 60, 7, 3, 1) {
			if e.Kind == "shed" {
				return true
			}
		}
		return false
	}
	if pull(0, 9) {
		t.Fatal("a pristine jet shed its wing below ultimate load")
	}
	if !pull(0, 12) {
		t.Fatal("beyond ultimate load the wing must shed")
	}
	if !pull(6, 9) {
		t.Fatal("6 g·s of overstress must weaken the wing enough to shed at 9 g")
	}
}

// TestBlast: a direct hit is a structural kill; a fringe burst fragments.
func TestBlast(t *testing.T) {
	body, m := target()
	kill, _, _ := Blast(m.State.Position.Add(flight.Vec3{Y: 2}), m.State.Position, m.State.Attitude, body, 0, 7, 3, 1)
	if !kill {
		t.Fatal("a 2 m miss must be a structural kill")
	}
	body, m = target()
	kill, events, _ := Blast(m.State.Position.Add(flight.Vec3{Y: 9}), m.State.Position, m.State.Attitude, body, 0, 7, 3, 2)
	if kill {
		t.Fatal("a 9 m miss must not be an outright kill")
	}
	if len(events) == 0 {
		t.Fatal("a 9 m burst threw no effective fragments")
	}
}

// TestFringeFlies: the non-binary promise — a fringe blast (the same 9 m
// miss TestBlast proves is not a kill) leaves a jet that still FLIES. Eight
// seconds later, under a plain altitude-holding stick with the cascade
// running, it is neither exploded nor falling out of the sky.
func TestFringeFlies(t *testing.T) {
	body, m := target()
	kill, _, _ := Blast(m.State.Position.Add(flight.Vec3{Y: 9}), m.State.Position, m.State.Attitude, body, 0, 7, 3, 2)
	if kill {
		t.Fatal("the fringe blast killed outright — the premise of this test needs re-basing")
	}
	altitude := m.State.Position.Y
	stick := 0.0
	for tick := uint64(0); tick < 60*8; tick++ {
		for step := 0; step < 4; step++ {
			s := &m.State
			stick = clamp(stick+clamp(((altitude-s.Position.Y)*0.002-s.Velocity.Y*0.02-stick*4)*0.002, -0.004, 0.004), -0.5, 1)
			m.Step(flight.Inputs{Throttle: 0.8, Pitch: stick})
		}
		for _, e := range Advance(body, m, 0.8, [2]bool{}, 60, 7, 3, tick) {
			if e.Kind == "explode" {
				t.Fatal("the fringe wound cascaded to an explosion within 8 s")
			}
		}
		if body.Condition.Killed {
			t.Fatal("the fringe wound killed the pilot")
		}
	}
	if m.State.Position.Y < altitude-150 {
		t.Fatalf("the wounded jet is falling, not flying: lost %.0f m in 8 s", altitude-m.State.Position.Y)
	}
	if m.State.Velocity.Length() < 100 {
		t.Fatalf("the wounded jet cannot hold flying speed: %.0f m/s", m.State.Velocity.Length())
	}
}

// TestGearShot: a round up into a landing-gear leg wounds THAT leg (#78) —
// the gear capsules are live hit geometry like any other part.
func TestGearShot(t *testing.T) {
	body, _ := target()
	leg := fa18c.Airframe.Gear.Left.Attach
	origin := leg.Add(flight.Vec3{Y: -8})
	hit, _ := trace(body.Parts, origin, flight.Vec3{Y: 1}, 20)
	if hit < 0 || body.Parts[hit].Kind != Gear {
		t.Fatalf("the ray up into the left leg missed the gear capsule: part %d", hit)
	}
	events := strike(body, &body.Parts[hit], 1, true, 7, 3, 1, 1)
	if body.Damage.Gear[1] <= 0 {
		t.Fatalf("the gear hit dealt nothing: %v", body.Damage.Gear)
	}
	if body.Damage.Gear[0] != 0 || body.Damage.Gear[2] != 0 {
		t.Fatalf("the wound leaked to other legs: %v", body.Damage.Gear)
	}
	found := false
	for _, e := range events {
		if e.Kind == "gear" {
			found = true
		}
	}
	if !found {
		t.Fatal("no gear event raised for the wound")
	}
}

// TestVolleyDeflection: a burst aimed at a beam-crossing target ITSELF misses
// wholesale (his velocity carries him out of the stream during the flight),
// and the same trigger squeeze from the led bore hits. This is the contract
// the bot's lead point and the HUD director pipper both build on.
func TestVolleyDeflection(t *testing.T) {
	shoot := func(led bool) int {
		body, m := target()
		muzzle := m.State.Position.Add(flight.Vec3{Z: 600})
		aim := m.State.Position
		if led {
			time := 600.0 / Average(600, m.State.Position.Y) // the drag-aware flight, as every fire-control consumer computes it
			aim = aim.Add(m.State.Velocity.Scale(time)).Add(flight.Vec3{Y: 4.9 * time * time})
		}
		bore := aim.Subtract(muzzle).Normalize()
		pose := Pose{Position: muzzle, Forward: bore, Up: flight.Vec3{Y: 1}, Right: bore.Cross(flight.Vec3{Y: 1})}
		hits, _ := salvo(pose, body, m, 100, 999, nil)
		return hits
	}
	if direct := shoot(false); direct > 2 {
		t.Fatalf("a no-lead burst against a beam crosser landed %d/100 — the time of flight is not being flown", direct)
	}
	if led := shoot(true); led < 20 {
		t.Fatalf("the led burst landed only %d/100 — the lead solution does not match the gunnery", led)
	}
}

// TestJinkDefeatsTheBullet: a perfectly led volley from the beam arrives where
// the target would have been and finds him gone when he breaks at the trigger.
// The same volley hits a non-jinking control.
func TestJinkDefeatsTheBullet(t *testing.T) {
	shoot := func(jink bool) int {
		body, m := target()
		muzzle := m.State.Position.Add(flight.Vec3{Z: 600})
		// Time of flight is DRAG-AWARE: a lead computed at muzzle speed lands short
		// and even the control volley misses.
		time := 600.0 / Average(600, m.State.Position.Y)
		aim := m.State.Position.Add(m.State.Velocity.Scale(time)).Add(flight.Vec3{Y: 4.9 * time * time})
		bore := aim.Subtract(muzzle).Normalize()
		pose := Pose{Position: muzzle, Forward: bore, Up: flight.Vec3{Y: 1}, Right: bore.Cross(flight.Vec3{Y: 1})}
		hits, _ := salvo(pose, body, m, 100, 999, func(step int) {
			if jink && step == 1 {
				m.State.Velocity = m.State.Velocity.Add(flight.Vec3{Y: -60, Z: 40}) // the break: down and away, hard
			}
		})
		return hits
	}
	if control := shoot(false); control < 20 {
		t.Fatalf("the control led volley landed only %d/100", control)
	}
	if jinked := shoot(true); jinked > 4 {
		t.Fatalf("the jink was led-through: %d/100 rounds still arrived — time of flight is not real", jinked)
	}
}

// TestVolleyImpactsLandOnTheAirframe: the strike points a salvo reports must sit
// on the target's structure, not at its centre — they are what puts a gun flash
// where the round actually hit (#217). Body-frame points, so "on the airframe"
// means within a part's capsule radius of that part's axis.
func TestVolleyImpactsLandOnTheAirframe(t *testing.T) {
	m := flight.New(fa18c.Airframe, flight.Environment{Seed: 7}, flight.World{Sea: 0})
	m.State.Position = flight.Vec3{Y: 3000}
	m.State.Velocity = flight.Vec3{X: 200}
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe), Damage: &m.State.Damage, Condition: &Condition{Damager: -1}}
	hits, impacts := salvo(astern(m), body, m, 100, 999, nil)
	if hits == 0 {
		t.Fatal("no hits to place")
	}
	if len(impacts) == 0 {
		t.Fatal("hits reported no impact points")
	}
	if len(impacts) > ImpactPoints {
		t.Fatalf("burst reported %d impacts, over the %d cap", len(impacts), ImpactPoints)
	}
	for n, point := range impacts {
		best := math.MaxFloat64
		for pi := range body.Parts {
			part := &body.Parts[pi]
			axis := part.B.Subtract(part.A)
			length := axis.Length()
			along := 0.0
			if length > 1e-9 {
				along = math.Max(0, math.Min(1, point.Subtract(part.A).Dot(axis)/(length*length)))
			}
			if gap := point.Subtract(part.A.Add(axis.Scale(along))).Length() - part.Radius; gap < best {
				best = gap
			}
		}
		// A tolerance, not zero: the capsule test is analytic but the point is
		// reconstructed from the ray parameter in float64.
		if best > 0.05 {
			t.Errorf("impact %d sits %.3f m off the nearest structure — a flash there would hang in mid air", n, best)
		}
	}
	t.Logf("%d hits reported %d impact points, all on structure", hits, len(impacts))
}

// TestFringeRate: a probabilistic model cannot promise a seed, so this measures
// the SHARE of fringe bursts that end in a catastrophic kill and bands it. Too
// low and a near miss never reads; too high and the certainty radius is a lie.
func TestFringeRate(t *testing.T) {
	blown, total := 0, 0
	for seed := uint64(1); seed <= 240; seed++ {
		body, m := target()
		body.Stores = ^uint64(0) // a full rack: the most explosive case the model allows
		// A burst somewhere in the fragment band, walked around the jet so
		// no single geometry dominates the answer.
		angle := float64(seed%12) * math.Pi / 6
		reach := lethal + 0.5 + float64(seed%5)
		point := m.State.Position.Add(flight.Vec3{
			X: reach * math.Cos(angle) * 0.4,
			Y: reach * math.Sin(angle),
			Z: reach * math.Cos(angle),
		})
		if point.Subtract(m.State.Position).Length() <= lethal {
			continue // inside the certainty radius: not a fringe burst
		}
		kill, _, _ := Blast(point, m.State.Position, m.State.Attitude, body, 0, seed, 3, 2)
		if kill {
			continue // an outright structural kill is the certainty radius, not this measurement
		}
		total++
		if body.Condition.Burning && body.Condition.Fuse <= 0.5 {
			blown++ // a catastrophic path took: the jet is coming apart now, not burning down over half a minute
		}
	}
	if total < 100 {
		t.Fatalf("only %d fringe bursts landed: the geometry walk is not exercising the envelope", total)
	}
	share := 100 * float64(blown) / float64(total)
	fmt.Printf("fringe bursts on a fully loaded jet: %d of %d ended catastrophically (%.0f%%)\n", blown, total, share)
	if share < 4 {
		t.Errorf("a fringe burst never blows a jet up (%.0f%% of %d): the stochastic paths are dead and only the certainty radius kills", share, total)
	}
	if share > 35 {
		t.Errorf("a fringe burst blows a jet up %.0f%% of the time (%d bursts): the fragment band has become a second certainty radius", share, total)
	}
}

// TestShedElements pins the element ranges engine.ts hides wing panels by (4-7
// port, 20-23 starboard): the client carries them as constants, so a change to
// the airframe's construction order must fail here.
func TestShedElements(t *testing.T) {
	base, ranges := 0, map[float64][2]int{}
	for si := range fa18c.Airframe.Surfaces {
		s := &fa18c.Airframe.Surfaces[si]
		if s.Kind == flight.Wing {
			ranges[s.Side] = [2]int{base + len(s.Elements)/2, base + len(s.Elements)}
		}
		base += len(s.Elements)
	}
	if got, want := ranges[-1], [2]int{4, 8}; got != want {
		t.Errorf("the PORT wing's shed elements are %v, not %v — engine.ts PANELS[0].first must change with it", got, want)
	}
	if got, want := ranges[1], [2]int{20, 24}; got != want {
		t.Errorf("the STARBOARD wing's shed elements are %v, not %v — engine.ts PANELS[1].first must change with it", got, want)
	}
	// And the shed itself must write exactly that range.
	body, _ := target()
	for si := range fa18c.Airframe.Surfaces {
		if fa18c.Airframe.Surfaces[si].Kind == flight.Wing && fa18c.Airframe.Surfaces[si].Side == -1 {
			if !shed(body, si) {
				t.Fatal("the port wing would not shed")
			}
			break
		}
	}
	for e := 4; e < 8; e++ {
		if body.Damage.Element[e] < 1 {
			t.Errorf("element %d survived the port shed (%.2f): the client hides the panel on 4-7 all being gone", e, body.Damage.Element[e])
		}
	}
	if body.Damage.Element[3] >= 1 {
		t.Error("the shed reached element 3 — that is the INBOARD half, and the panel the client hides is only the outboard one")
	}
}

// TestShellDepth: a 20 mm round is an HE shell, not a slug - severity must NOT
// scale with impact energy, but the momentum left decides how far in it works.
func TestShellDepth(t *testing.T) {
	if got := Depth(Muzzle); got != through {
		t.Errorf("a shell striking at muzzle speed reached %d parts, want %d", got, through)
	}
	if got := Depth(striking); got != through {
		t.Errorf("a shell at the full-depth threshold reached %d parts, want %d", got, through)
	}
	if got := Depth(graze); got != 1 {
		t.Errorf("a shell down to grazing speed reached %d parts, want 1: it still bursts, but on the skin", got)
	}
	if got := Depth(50); got != 1 {
		t.Errorf("a nearly spent shell reached %d parts, want 1 — a fuze that functions always functions on something", got)
	}
	// Monotone in between, and never zero: an HE round that arrives always hurts.
	last := 0
	for speed := 0.0; speed <= 1200; speed += 25 {
		got := Depth(speed)
		if got < 1 {
			t.Fatalf("a shell at %.0f m/s reached nothing at all", speed)
		}
		if got > through {
			t.Fatalf("a shell at %.0f m/s reached %d parts, past the %d-part limit", speed, got, through)
		}
		if got < last {
			t.Fatalf("depth fell from %d to %d as speed rose to %.0f m/s", last, got, speed)
		}
		last = got
	}
	if Depth(500) <= Depth(300) {
		t.Error("a faster shell must reach further in than a slower one")
	}
}

// TestShellLife: damage does not decay with speed, so Life is a cost cap, not a
// lethality one - it must not cut a shell off inside gunnery ranges.
func TestShellLife(t *testing.T) {
	environment := flight.Environment{Seed: 1, Wrap: 250000}
	_ = environment
	// How far a round gets before Life expires, from a jet at a fighting speed.
	round := Round{Position: flight.Vec3{Y: 4000}, Velocity: flight.Vec3{X: Muzzle + 240}}
	flown := 0.0
	for step := 0; step < 10000; step++ {
		before := round.Position
		if Fly(&round, 1.0/60) {
			break
		}
		flown += round.Position.Subtract(before).Length()
	}
	if flown < 2500 {
		t.Errorf("a shell fired at a fighting speed reached only %.0f m before expiring: the pipper offers solutions further than that", flown)
	}
	if round.Velocity.Length() > Muzzle {
		t.Errorf("the round ended faster (%.0f m/s) than it started: drag is not being applied", round.Velocity.Length())
	}
}

// TestAmmunitionCookoff: the gun's belt is 578 rounds of high explosive sitting
// in the nose ahead of the cockpit, which is where a front-quarter burst lands.
// A hit on it can set it off — and a jet that has fired everything presents
// nothing to set off, the same way an empty rail does.
func TestAmmunitionCookoff(t *testing.T) {
	drum := -1
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe)}
	for i, part := range body.Parts {
		if part.Kind == Ammunition {
			if drum >= 0 {
				t.Fatal("more than one ammunition part: the drum is one volume")
			}
			drum = i
		}
	}
	if drum < 0 {
		t.Fatal("no ammunition part in the hit geometry: the belt cannot be hit at all")
	}
	// Ahead of the cockpit, where the M61 lives on this airframe.
	if body.Parts[drum].A.X <= fa18c.Airframe.Cockpit.X {
		t.Errorf("the drum sits at x=%.1f, not forward of the cockpit at x=%.1f",
			body.Parts[drum].A.X, fa18c.Airframe.Cockpit.X)
	}

	// A full drum cooks off sometimes; an empty one never does. Run the roll
	// across many seeds, since it is a chance and not a certainty.
	blown := func(belt float64) int {
		count := 0
		for seed := uint64(0); seed < 400; seed++ {
			damage := flight.DamageState{}
			condition := Condition{Damager: -1}
			victim := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe),
				Damage: &damage, Condition: &condition, Belt: belt}
			strike(victim, &victim.Parts[drum], 1.0, true, seed, 1, 0, 0)
			if condition.Killed || condition.Burning {
				count++
			}
		}
		return count
	}
	full, empty, half := blown(1.0), blown(0.0), blown(0.5)
	if empty != 0 {
		t.Errorf("an empty drum cooked off %d times in 400: there is nothing in it to go off", empty)
	}
	if full == 0 {
		t.Error("a full drum never cooked off in 400 hits: 578 high-explosive rounds should be a hazard")
	}
	if full <= half {
		t.Errorf("a full drum (%d/400) was no more dangerous than a half-empty one (%d/400): the belt must scale it", full, half)
	}
	// And it must be a chance, not a certainty — a hit on the nose is not a kill.
	if full > 200 {
		t.Errorf("a full drum cooked off %d times in 400: that is a death sentence, not a hazard", full)
	}
}

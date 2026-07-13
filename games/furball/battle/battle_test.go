// Mochi world: Battle tests
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package battle

import (
	"testing"

	"world/games/furball/aircraft/fa18c"
	"world/games/furball/flight"
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

// TestBurstDeterminism: identical inputs produce identical outcomes.
func TestBurstDeterminism(t *testing.T) {
	first, m1 := target()
	second, m2 := target()
	h1, _ := Burst(astern(m1), m1.State.Position, m1.State.Attitude, m1.State.Velocity, first, 50, 0, 7, 3, 999)
	h2, _ := Burst(astern(m2), m2.State.Position, m2.State.Attitude, m2.State.Velocity, second, 50, 0, 7, 3, 999)
	if h1 != h2 {
		t.Fatalf("determinism broken: %d vs %d hits", h1, h2)
	}
	for i := range first.Damage.Element {
		if first.Damage.Element[i] != second.Damage.Element[i] {
			t.Fatal("determinism broken: element damage differs")
		}
	}
}

// TestBurstLethality: two seconds of perfect tracking from dead astern must
// cripple the target — the time-to-kill tuning gate (1.5–3 s class).
func TestBurstLethality(t *testing.T) {
	body, m := target()
	total := 0
	for tick := uint64(0); tick < 120; tick++ { // 2 s at 60 Hz, ~1.7 rounds/tick
		rounds := 2
		if tick%3 == 0 {
			rounds = 1
		}
		hits, _ := Burst(astern(m), m.State.Position, m.State.Attitude, m.State.Velocity, body, rounds, 0, 7, 3, tick)
		total += hits
	}
	if total < 10 {
		t.Fatalf("a 2 s tracking burst landed only %d hits — the gun cannot kill", total)
	}
	loss := 0.0
	for _, v := range body.Damage.Element {
		loss += v
	}
	crippled := loss > 1.5 || body.Damage.Engine[0]+body.Damage.Engine[1] > 0.6 || body.Condition.Killed || body.Condition.Burning || body.Damage.Leak > 0.4
	if !crippled {
		t.Fatalf("2 s of tracking fire did not cripple: %d hits, element loss %.2f, engines %.2f/%.2f, leak %.2f",
			total, loss, body.Damage.Engine[0], body.Damage.Engine[1], body.Damage.Leak)
	}
}

// TestFireDrill: an engine fire grows under throttle, ramps thrust loss, and
// the idle drill extinguishes it.
func TestFireDrill(t *testing.T) {
	body, m := target()
	body.Condition.Fire[0] = 0.1
	for tick := uint64(0); tick < 300; tick++ { // 5 s at throttle
		Advance(body, m, 0.8, 60, 7, 3, tick)
	}
	if body.Condition.Fire[0] <= 0.1 {
		t.Fatal("fire did not grow under throttle")
	}
	if body.Damage.Engine[0] <= 0 {
		t.Fatal("a burning engine must lose thrust")
	}
	for tick := uint64(300); tick < 2000 && body.Condition.Fire[0] > 0; tick++ {
		Advance(body, m, 0.0, 60, 7, 3, tick)
	}
	if body.Condition.Fire[0] > 0 {
		t.Fatal("the idle drill failed to extinguish the fire")
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
		for _, e := range Advance(body, m, 0.5, 60, 7, 3, tick) {
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
		for _, e := range Advance(body, m, 0.5, 60, 7, 3, 1) {
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
	kill, _ := Blast(m.State.Position.Add(flight.Vec3{Y: 2}), m.State.Position, m.State.Attitude, body, 0, 7, 3, 1)
	if !kill {
		t.Fatal("a 2 m miss must be a structural kill")
	}
	body, m = target()
	kill, events := Blast(m.State.Position.Add(flight.Vec3{Y: 9}), m.State.Position, m.State.Attitude, body, 0, 7, 3, 2)
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
	kill, _ := Blast(m.State.Position.Add(flight.Vec3{Y: 9}), m.State.Position, m.State.Attitude, body, 0, 7, 3, 2)
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
		for _, e := range Advance(body, m, 0.8, 60, 7, 3, tick) {
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
	events := strike(body, &body.Parts[hit], 1, 7, 3, 1, 1)
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

// TestBurstDeflection: rounds fly real time of flight now — a burst aimed at
// a beam-crossing target ITSELF misses wholesale (his velocity carries him
// out of the stream during the flight), and the same trigger squeeze from
// the led bore hits. This is the contract the bot's lead point and the HUD
// director pipper both build on.
func TestBurstDeflection(t *testing.T) {
	shoot := func(led bool) int {
		body, m := target()
		muzzle := m.State.Position.Add(flight.Vec3{Z: 600})
		aim := m.State.Position
		if led {
			time := 600.0 / Muzzle
			aim = aim.Add(m.State.Velocity.Scale(time)).Add(flight.Vec3{Y: 4.9 * time * time})
		}
		bore := aim.Subtract(muzzle).Normalize()
		pose := Pose{Position: muzzle, Forward: bore, Up: flight.Vec3{Y: 1}, Right: bore.Cross(flight.Vec3{Y: 1})}
		hits, _ := Burst(pose, m.State.Position, m.State.Attitude, m.State.Velocity, body, 100, 0, 7, 3, 999)
		return hits
	}
	if direct := shoot(false); direct > 2 {
		t.Fatalf("a no-lead burst against a beam crosser landed %d/100 — the time of flight is not being flown", direct)
	}
	if led := shoot(true); led < 20 {
		t.Fatalf("the led burst landed only %d/100 — the lead solution does not match the gunnery", led)
	}
}

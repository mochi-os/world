// Mochi world: Contact gates
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// harbor is the test world: a carrier at the origin and an island runway
// well clear of it.
func harbor() World {
	deck := []Vec3{{X: -165, Z: -22}, {X: -165, Z: 22}, {X: 165, Z: 22}, {X: 165, Z: -22}}
	return World{
		Carrier: &Carrier{
			Position: Vec3{Y: 19},
			Deck:     deck,
			Catapults: []Catapult{
				{Position: Vec3{X: 48, Z: -0.6}, Heading: 0.028, Stroke: 85, Speed: 88},
			},
			Wires: []Wire{
				{A: Vec3{X: -97, Z: -14}, B: Vec3{X: -97, Z: 14}},
				{A: Vec3{X: -87, Z: -14}, B: Vec3{X: -87, Z: 14}},
				{A: Vec3{X: -72, Z: -14}, B: Vec3{X: -72, Z: 14}},
			},
		},
		Fields: []Field{{
			Height: 3.5,
			Strips: []Strip{{A: Vec3{X: -1500, Z: 8000}, B: Vec3{X: 1500, Z: 8000}, Width: 46}},
			Coast:  []Vec3{{X: -2500, Z: 7000}, {X: 2500, Z: 7000}, {X: 2500, Z: 9000}, {X: -2500, Z: 9000}},
		}},
	}
}

// park places the model resting on the deck at a carrier-local spot.
func park(m *Model, x float64, z float64) {
	m.State.Position = Vec3{X: x, Y: 21.52, Z: z}
	m.State.Velocity = Vec3{}
	m.State.Attitude = Quat{W: 1}
	m.State.Omega = Vec3{}
	m.State.Fcs = FcsState{}
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
}

func aboard() *Model { return New(Fighter, Environment{}, harbor()) }

// TestRest: sixty seconds parked on deck with the brakes held — no drift,
// no bounce, no NaN. (Brakes are required: flight idle makes real thrust.)
func TestRest(t *testing.T) {
	m := aboard()
	park(m, -30, 5)
	for i := 0; i < 240*60; i++ {
		m.Step(Inputs{Gear: true, Brake: true})
	}
	if !m.State.Gear.Wow {
		t.Fatal("no weight on wheels at rest")
	}
	dx := m.State.Position.X + 30
	dz := m.State.Position.Z - 5
	if math.Abs(dx) > 0.05 || math.Abs(dz) > 0.05 {
		t.Fatalf("drifted %.3f, %.3f m in a minute", dx, dz)
	}
	if math.IsNaN(m.State.Position.Y) {
		t.Fatal("diverged")
	}
}

// TestTaxi: throttle rolls the jet, brakes stop it, the nosewheel steers.
func TestTaxi(t *testing.T) {
	m := aboard()
	park(m, -150, 5)
	m.State.Engine[0] = EngineState{Spool: 0.18}
	m.State.Engine[1] = EngineState{Spool: 0.18}
	for i := 0; i < 240*6; i++ {
		m.Step(Inputs{Gear: true, Throttle: 0.18})
	}
	rolling := m.State.Velocity.Length()
	if rolling < 2 {
		t.Fatalf("throttle does not taxi: %.2f m/s", rolling)
	}
	heading := math.Atan2(-m.State.Velocity.Z, m.State.Velocity.X)
	// 1.5 s of pedal: with realistic LOW-mode taxi authority (22.5°, ~13 m radius)
	// a longer full-pedal turn arcs the jet off the deck edge before the brake
	// phase — the old 75° throw turned in a 1.4 m pirouette and never moved.
	for i := 0; i < 240*3/2; i++ {
		m.Step(Inputs{Gear: true, Throttle: 0.18, Yaw: 1})
	}
	turned := math.Atan2(-m.State.Velocity.Z, m.State.Velocity.X)
	if math.Abs(turned-heading) < 0.05 {
		t.Fatal("nosewheel does not steer")
	}
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{Gear: true, Brake: true})
	}
	if m.State.Velocity.Length() > 0.5 {
		t.Fatalf("brakes do not stop the jet: %.2f m/s", m.State.Velocity.Length())
	}
}

// TestCatapult: taxi onto the shuttle, attach, run up, launch — a clean end
// speed and a flying jet.
func TestCatapult(t *testing.T) {
	m := aboard()
	park(m, 42.7, -0.6) // the NOSE GEAR (5.3 m ahead of the CG) lands on the shuttle
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true})
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("did not attach: %d", m.State.Gear.Catapult)
	}
	m.State.Engine[0] = EngineState{Spool: 1}
	m.State.Engine[1] = EngineState{Spool: 1}
	// Full reheat against the holdback for two seconds: the bar restrains
	// max thrust, so the jet must stay put until the shot is fired.
	held := m.State.Position
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true, Throttle: 1, Reheat: 1})
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("detached under tension: %d", m.State.Gear.Catapult)
	}
	if crept := m.State.Position.Subtract(held).Length(); crept > 1 {
		t.Fatalf("crept %.2f m against the holdback at full reheat", crept)
	}
	if speed := m.State.Velocity.Length(); speed > 0.5 {
		t.Fatalf("moving %.2f m/s against the holdback", speed)
	}
	launched := false
	for i := 0; i < 240*6; i++ {
		m.Step(Inputs{Gear: true, Throttle: 1, Reheat: 1, Launch: i > 240})
		if m.State.Gear.Catapult < 0 && !launched && i > 240 {
			launched = true
			speed := m.State.Velocity.Length()
			if speed < 60 {
				t.Fatalf("limp cat shot: %.1f m/s at release", speed)
			}
		}
	}
	if !launched {
		t.Fatal("the catapult never released")
	}
	if m.State.Position.Y < 15 || math.IsNaN(m.State.Position.Y) {
		t.Fatalf("did not fly away: y=%.1f", m.State.Position.Y)
	}
}

// TestUnhook: steering away at low power releases the catapult so the
// aircraft can taxi off instead of launching.
func TestUnhook(t *testing.T) {
	m := aboard()
	park(m, 42.7, -0.6)
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true})
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("did not attach: %d", m.State.Gear.Catapult)
	}
	for i := 0; i < 240; i++ {
		m.Step(Inputs{Gear: true, Yaw: 1, Throttle: 0.2})
	}
	if m.State.Gear.Catapult >= 0 {
		t.Fatal("still attached after steering away at low power")
	}
	if m.State.Gear.Stroke >= 0 {
		t.Fatal("unhooking must not fire the stroke")
	}
}

// TestTrap: an on-speed pass over the wires with the hook down stops the
// jet within the deck run.
func TestTrap(t *testing.T) {
	m := aboard()
	m.State.Position = Vec3{X: -300, Y: 25.5, Z: 0} // low enough to touch down BEFORE the wires and scrape in: the deck-height wire catch no longer snags mid-air crossings (the old 4 m band did, and this pass leaned on it)
	m.State.Velocity = Vec3{X: 59, Y: -3.4}         // 65 -> 59 with deck ground effect (#132): the cushion floated the faster pass into a bounce clean over all the wires; 59 catches the middle wire across the whole 0.30-0.42 throttle range
	m.State.Attitude = Axis(Vec3{Z: 1}, 0.10)
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Engine[0] = EngineState{Spool: 0.7}
	m.State.Engine[1] = EngineState{Spool: 0.7}
	caught := false
	for i := 0; i < 240*10; i++ {
		throttle := 0.42 // 0.45 -> 0.42 with the #131 polar calibration; the 2026-07-21 drag recalibration (K 0.19 -> 0.14 + the cl>1.1 polar break) nets out at the approach CL, so the scripted pass trims where #131 left it
		if caught {
			throttle = 0 // throttle to idle in the wire, as the real procedure has it — the gentler low-energy arrest otherwise lets approach power creep the trapped jet
		}
		m.Step(Inputs{Gear: true, Hook: true, Throttle: throttle})
		if m.State.Gear.Wire >= 0 {
			caught = true
		}
	}
	if !caught {
		t.Fatal("hook never found a wire")
	}
	if speed := m.State.Velocity.Length(); speed > 3 {
		t.Fatalf("trap did not stop the jet: %.1f m/s", speed)
	}
	if m.State.Position.X > 60 {
		t.Fatalf("rollout past the deck: x=%.0f", m.State.Position.X)
	}
}

// TestBolter: the same pass with the hook up crosses every wire and flies on.
func TestBolter(t *testing.T) {
	m := aboard()
	m.State.Position = Vec3{X: -300, Y: 29.5, Z: 0}
	m.State.Velocity = Vec3{X: 65, Y: -3.4}
	m.State.Attitude = Axis(Vec3{Z: 1}, 0.10)
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Engine[0] = EngineState{Spool: 1}
	m.State.Engine[1] = EngineState{Spool: 1}
	for i := 0; i < 240*8; i++ {
		m.Step(Inputs{Gear: true, Throttle: 1, Reheat: 1})
		if m.State.Gear.Wire >= 0 {
			t.Fatal("caught a wire with the hook up")
		}
	}
	if m.State.Velocity.Length() < 40 {
		t.Fatalf("bolter died on deck: %.1f m/s", m.State.Velocity.Length())
	}
}

// TestBelly: a gentle gear-up arrival on the runway skids to a stop —
// permitted contact, touchdown recorded, no crash probe.
func TestBelly(t *testing.T) {
	m := aboard()
	m.State.Position = Vec3{X: -400, Y: 8, Z: 8000}
	m.State.Velocity = Vec3{X: 62, Y: -1.2}
	m.State.Attitude = Axis(Vec3{Z: 1}, 0.05)
	m.State.Gear = GearState{Extension: 0, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	touched := false
	for i := 0; i < 240*30; i++ { // idle thrust stretches the float before the skid

		m.Step(Inputs{})
		if m.State.Gear.Touch.Occurred {
			touched = true
		}
		if m.State.Gear.Contact >= 0 {
			t.Fatalf("crash probe fired on a belly slide: %d", m.State.Gear.Contact)
		}
	}
	if !touched {
		t.Fatal("no touchdown recorded")
	}
	if m.State.Velocity.Length() > 2 {
		t.Fatalf("belly slide never stopped: %.1f m/s", m.State.Velocity.Length())
	}
}

// TestTopple: parked with the right wheels past the deck edge, the jet
// rolls over and goes into the sea — emergent, no scripting.
func TestTopple(t *testing.T) {
	m := aboard()
	park(m, -30, 21.2) // right main beyond the deck edge at z=22
	toppled := false
	for i := 0; i < 240*10; i++ {
		m.Step(Inputs{Gear: true})
		right := m.State.Attitude.Rotate(Vec3{Z: 1})
		bank := math.Asin(clamp(-right.Y, -1, 1))
		if m.State.Position.Y < 15 || math.Abs(bank) > 0.6 {
			toppled = true
			break
		}
	}
	if !toppled {
		right := m.State.Attitude.Rotate(Vec3{Z: 1})
		t.Fatalf("did not topple: y=%.1f z=%.1f bank=%.2f", m.State.Position.Y, m.State.Position.Z, math.Asin(clamp(-right.Y, -1, 1)))
	}
}

// TestProbe: flying the nose into the runway fires a crash probe.
func TestProbe(t *testing.T) {
	m := aboard()
	m.State.Position = Vec3{X: -200, Y: 12, Z: 8000}
	m.State.Velocity = Vec3{X: 80, Y: -12}
	m.State.Attitude = Axis(Vec3{Z: 1}, -0.25) // nose down
	m.State.Gear = GearState{Extension: 0, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.Direct = true
	fired := false
	for i := 0; i < 240*3; i++ {
		m.Step(Inputs{})
		if m.State.Gear.Contact >= 0 {
			fired = true
			break
		}
	}
	if !fired {
		t.Fatal("nose-first arrival fired no crash probe")
	}
}

// TestTaxiUpSlow: rolling onto the shuttle at taxi speed must gather, not
// slingshot — the parked-capture holdback tuning once pitched a 3-4 m/s
// arrival onto its tail probe (grip is now speed-scheduled).
func TestTaxiUpSlow(t *testing.T) {
	for _, offset := range []float64{0, 1.5, -1.5} {
		m := aboard()
		park(m, 42.7-12, -0.6+offset)
		for i := 0; i < 240*2; i++ {
			m.Step(Inputs{Gear: true})
		}
		attached := false
		for i := 0; i < 240*30; i++ {
			th := 0.12
			if m.State.Velocity.Length() > 2.5 {
				th = 0.0
			}
			m.Step(Inputs{Gear: true, Throttle: th})
			if m.State.Gear.Contact >= 0 {
				t.Fatalf("offset %.1f: CRASH t=%.1fs probe %d", offset, float64(i)/240, m.State.Gear.Contact)
			}
			if m.State.Gear.Catapult >= 0 && m.State.Velocity.Length() < 0.2 && i > 240*4 {
				t.Logf("offset %.1f: attached cleanly at t=%.1fs", offset, float64(i)/240)
				attached = true
				break
			}
		}
		if !attached {
			t.Fatalf("offset %.1f: never attached", offset)
		}
		// settle, then the jet must sit ON the spot, aligned down the track
		for i := 0; i < 240*4; i++ {
			m.Step(Inputs{Gear: true})
		}
		cat := m.World.Carrier.Catapults[m.State.Gear.Catapult]
		shuttle := m.World.Carrier.world(cat.Position, m.State.Time)
		nose := m.State.Position.Add(m.State.Attitude.Rotate(m.Airframe.Gear.Nose.Attach.Subtract(m.center)))
		off := Vec3{X: nose.X - shuttle.X, Z: nose.Z - shuttle.Z}.Length()
		heading := m.World.Carrier.Heading + cat.Heading
		track := Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		align := forward.X*track.X + forward.Z*track.Z
		if off > 0.5 || align < 0.97 { // within 0.5 m and ~14°: a sloppy offset approach parks visibly crabbed (alignment decays with distance ROLLED in the slot; the caster/damping balance favours a calm capture over a square park) — cosmetic, because the TENSION phase squares the jet on Launch (asserted below)
			t.Fatalf("offset %.1f: poor spotting: %.2f m off the shuttle, alignment %.5f", offset, off, align)
		}
		t.Logf("offset %.1f: spotted %.2f m off, alignment %.5f", offset, off, align)
		// The launch must run straight regardless of the parked crab: measure
		// LATE IN THE STROKE while still captive — the release frame itself
		// carries a legitimate weathervane transient in the test crosswind,
		// and later samples just read wind drift.
		for i := 0; i < 240*8 && (m.State.Gear.Stroke < 0 || m.State.Gear.Stroke < 60); i++ {
			m.Step(Inputs{Gear: true, Throttle: 1, Reheat: 1, Launch: true})
		}
		heading = m.World.Carrier.Heading + cat.Heading
		track = Vec3{X: math.Cos(heading), Z: -math.Sin(heading)}
		v := m.State.Velocity
		lateral := v.X*track.Z - v.Z*track.X
		forward = m.State.Attitude.Rotate(Vec3{X: 1})
		align = forward.X*track.X + forward.Z*track.Z
		// Yaw RATE is deliberately not asserted: a crabbed start actively
		// swings into line during the run (that rotation is the fix working);
		// what must hold is the PATH (lateral) and the late-stroke HEADING.
		if m.State.Gear.Contact >= 0 || math.Abs(lateral) > 2.5 || align < 0.99 { // the captive jet crabs a few degrees INTO the test crosswind while the slot holds its path — nose pinned, tail blown downwind
			t.Fatalf("offset %.1f: crooked launch: lateral %.2f m/s heading %.5f contact %d", offset, lateral, align, m.State.Gear.Contact)
		}
		t.Logf("offset %.1f: stroke straight: lateral %.2f m/s heading %.5f", offset, lateral, align)
	}
}

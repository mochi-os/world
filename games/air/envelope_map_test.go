package air

import (
	"fmt"
	"math"
	"testing"
	"world/games/air/aircraft"
	"world/games/air/flight"
)

// sustained hunts the Ps=0 load factor at one speed/altitude: fly a bank-held
// level turn at candidate n (bank from cos φ = 1/n, stick trimmed onto n),
// measure specific excess power over a settled window, bisect n.
func sustained(speed, altitude float64) (float64, float64) {
	measure := func(n float64) float64 {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = flight.Vec3{Y: altitude}
		m.State.Velocity = flight.Vec3{X: speed}
		m.State.Attitude = flight.Look(flight.Vec3{X: 1})
		m.State.Fuel = 2450 // ~half internal: the EM reference weight
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
		stick := clamp((n-1)/6.5, 0.1, 1)
		target := -math.Acos(clamp(1/n, 0, 1)) // level-turn bank for n
		var joules0, joules1 float64
		for tick := 0; tick < 240*8; tick++ {
			s := &m.State
			up := s.Attitude.Rotate(flight.Vec3{Y: 1})
			right := s.Attitude.Rotate(flight.Vec3{Z: 1})
			bank := math.Atan2(right.Y, up.Y)
			roll := clamp((bank-target)*2.5, -1, 1)
			stick = clamp(stick+clamp((n-s.Fcs.Normal)*0.01, -0.01, 0.01), 0.05, 1)
			m.Step(flight.Inputs{Pitch: stick, Roll: roll, Throttle: 1, Reheat: 1})
			v := s.Velocity.Length()
			if tick == 240*5 {
				joules0 = s.Position.Y + v*v/19.62
			}
			if tick == 240*8-1 {
				joules1 = s.Position.Y + v*v/19.62
			}
		}
		return (joules1 - joules0) / 3 // Ps, m/s, over the settled 3 s window
	}
	low, high := 1.5, 7.5
	for i := 0; i < 9; i++ {
		mid := (low + high) / 2
		if measure(mid) > 0 {
			low = mid
		} else {
			high = mid
		}
	}
	n := (low + high) / 2
	omega := 9.81 * math.Sqrt(n*n-1) / speed * 180 / math.Pi
	return n, omega
}

// TestEnvelopeMap is the EM regression battery (#131): the sustained-turn
// envelope calibrated against the published F/A-18C chart shape (~34,000 lb,
// sea level, max afterburner). The gate flies the model at its own clean
// half-fuel weight (~29,000 lb) against those 34,000 lb bands DELIBERATELY
// (decided 2026-07-21): the model carries no pylons or stores, so its clean
// combat weight stands in for the real jet's combat-loaded configuration.
// Consequence: per pound the model is slightly conservative, and the V-speed
// survey's minimum-fuel rows post limiter-bound rates (~22 deg/s SL light)
// that are correct under this mapping. Bands are deliberately generous — the
// gate catches envelope DRIFT, not chart-transcription pedantry.
func TestEnvelopeMap(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes of trim hunting")
	}
	type point struct{ kt, ft, gLow, gHigh, rateLow float64 }
	for _, at := range []point{
		{250, 1500, 3.5, 4.3, 15.0},  // low speed: the radius fight regime
		{350, 1500, 5.3, 6.1, 16.5},  // the corner-speed sustained benchmark (~18 deg/s real)
		{450, 1500, 7.0, 7.5, 17.0},  // past the knee: the limiter IS the sustained bound
		{550, 1500, 7.0, 7.5, 14.0},  // high speed: limiter-bound, rate falling geometrically
		{350, 15000, 3.3, 4.2, 10.0}, // altitude: thrust lapse bites
	} {
		speed := at.kt / 1.944
		altitude := at.ft / 3.281
		n, omega := sustained(speed, altitude)
		fmt.Printf("%4.0f kt @ %5.0f ft: sustained %.2f g, %.1f deg/s\n", at.kt, at.ft, n, omega)
		if n < at.gLow || n > at.gHigh {
			t.Fatalf("%.0f kt/%.0f ft: sustained %.2f g outside [%.1f, %.1f]", at.kt, at.ft, n, at.gLow, at.gHigh)
		}
		if omega < at.rateLow {
			t.Fatalf("%.0f kt/%.0f ft: sustained rate %.1f deg/s below %.1f", at.kt, at.ft, omega, at.rateLow)
		}
	}
}

// TestAcceleration: the straight-line envelope — a clean jet at sea level in
// full afterburner takes roughly 20-30 s from 300 to 600 kt. This gate would
// have caught the lean-afterburner SFC bug from the other direction.
func TestAcceleration(t *testing.T) {
	if testing.Short() {
		t.Skip("a long acceleration run")
	}
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	m.State.Position = flight.Vec3{Y: 1500}
	m.State.Velocity = flight.Vec3{X: 300 / 1.944}
	m.State.Attitude = flight.Look(flight.Vec3{X: 1})
	m.State.Fuel = 2450
	m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
	m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
	stick, elapsed := 0.0, -1.0
	for tick := 0; tick < 240*60; tick++ {
		s := &m.State
		stick = clamp(stick+clamp(((1500-s.Position.Y)*0.001-s.Velocity.Y*0.01-stick*4)*0.001, -0.002, 0.002), -0.3, 0.5)
		m.Step(flight.Inputs{Pitch: stick, Throttle: 1, Reheat: 1})
		if s.Velocity.Length() >= 600/1.944 {
			elapsed = float64(tick) / 240
			break
		}
	}
	fmt.Printf("300->600 kt at sea level: %.1f s\n", elapsed)
	if elapsed < 0 {
		t.Fatal("never reached 600 kt in a minute of full afterburner")
	}
	if elapsed < 15 || elapsed > 40 {
		t.Fatalf("300->600 kt took %.1f s: expected ~20-30", elapsed)
	}
}

// TestClimb: the 1-g specific excess power at sea level — the real jet climbs
// ~44,000 ft/min (~220 m/s of Ps) at combat weight in full afterburner.
func TestClimb(t *testing.T) {
	if testing.Short() {
		t.Skip("an energy-rate measurement")
	}
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	m.State.Position = flight.Vec3{Y: 1000}
	m.State.Velocity = flight.Vec3{X: 180} // ~350 kt: near best climb speed
	m.State.Attitude = flight.Look(flight.Vec3{X: 1})
	m.State.Fuel = 2450
	m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
	m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
	// Level full-burner run: Ps at 1 g is the energy-height rate, measured
	// over a settled window regardless of how it splits into climb vs accel.
	var joules0, joules1 float64
	stick := 0.0
	for tick := 0; tick < 240*8; tick++ {
		s := &m.State
		stick = clamp(stick+clamp(((1000-s.Position.Y)*0.001-s.Velocity.Y*0.01-stick*4)*0.001, -0.002, 0.002), -0.3, 0.5)
		m.Step(flight.Inputs{Pitch: stick, Throttle: 1, Reheat: 1})
		v := s.Velocity.Length()
		if tick == 240*3 {
			joules0 = s.Position.Y + v*v/19.62
		}
		if tick == 240*8-1 {
			joules1 = s.Position.Y + v*v/19.62
		}
	}
	rate := (joules1 - joules0) / 5
	fmt.Printf("sea-level 1 g Ps at ~350 kt: %.0f m/s (%.0f ft/min equivalent)\n", rate, rate*197)
	if rate < 150 || rate > 300 {
		t.Fatalf("1 g Ps %.0f m/s: expected ~180-260 (the ~44,000 ft/min class)", rate)
	}
}

// TestRoll: full lateral stick at combat speed — the Hornet rolls
// ~180-220 deg/s; the FCS commands 3.8 rad/s tempered by speed and alpha.
func TestRoll(t *testing.T) {
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	m.State.Position = flight.Vec3{Y: 1500}
	m.State.Velocity = flight.Vec3{X: 180}
	m.State.Attitude = flight.Look(flight.Vec3{X: 1})
	m.State.Fuel = 2450
	m.State.Engine[0] = flight.EngineState{Spool: 1}
	m.State.Engine[1] = flight.EngineState{Spool: 1}
	peak := 0.0
	for tick := 0; tick < 240*3; tick++ {
		m.Step(flight.Inputs{Roll: 1, Throttle: 0.8})
		if r := math.Abs(m.State.Omega.X) * 180 / math.Pi; r > peak {
			peak = r
		}
	}
	fmt.Printf("full-stick roll at 350 kt: %.0f deg/s peak\n", peak)
	if peak < 150 || peak > 260 {
		t.Fatalf("roll rate %.0f deg/s: expected the 180-220 class", peak)
	}
}

// TestTopSpeed locks the just-validated transonic anchor: a clean jet at low
// altitude in full afterburner tops out around Mach 1.0 — the drag work above
// must never quietly turn the Hornet into something faster.
func TestTopSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("a long acceleration run")
	}
	m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
	m.State.Position = flight.Vec3{Y: 1500}
	m.State.Velocity = flight.Vec3{X: 250}
	m.State.Attitude = flight.Look(flight.Vec3{X: 1})
	m.State.Fuel = 2450
	m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
	m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
	stick := 0.0
	for tick := 0; tick < 240*120; tick++ {
		s := &m.State
		// hold roughly level with a gentle altitude loop
		stick = clamp(stick+clamp(((1500-s.Position.Y)*0.001-s.Velocity.Y*0.01-stick*4)*0.001, -0.002, 0.002), -0.3, 0.5)
		m.Step(flight.Inputs{Pitch: stick, Throttle: 1, Reheat: 1})
	}
	local := 340.3 * math.Sqrt(1-2.2558e-5*1500*6.5/288) // rough local sound speed
	mach := m.State.Velocity.Length() / local
	if mach < 0.93 || mach > 1.12 {
		t.Fatalf("clean sea-level top speed M%.2f: the transonic anchor moved", mach)
	}
}

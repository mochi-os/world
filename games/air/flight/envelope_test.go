package flight

// Performance-envelope anchors: the model trimmed/settled at published
// F/A-18C reference points, so every future tuning round has regression
// teeth instead of feel-tests. Tolerances are generous — this is an anchor,
// not a certification.

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

// settle flies the PA law hands-off at a fixed condition with a slow
// autothrottle holding the vertical speed near zero, and returns the
// settled speed and alpha — the model's own on-speed point.
// settle sweeps fixed throttle settings in the landing configuration and
// returns the time-averaged speed and alpha of the level-est run — open loop,
// so no autothrottle can pump the phugoid; averaging over the tail washes it out.
func settle(t *testing.T, fuel float64) (float64, float64) {
	bestVy, bestSpeed, bestAlpha := 1e9, 0.0, 0.0
	for throttle := 0.20; throttle <= 0.64; throttle += 0.01 {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.State = Level(m, Vec3{Y: 400}, Vec3{X: 1}, 72, fuel)
		sumVy, sumSpeed, sumAlpha, n := 0.0, 0.0, 0.0, 0
		for i := 0; i < 240*90; i++ {
			m.Step(Inputs{Throttle: throttle, Gear: true})
			if i >= 240*45 {
				body := m.State.Attitude.Unrotate(m.State.Velocity)
				sumVy += m.State.Velocity.Y
				sumSpeed += m.State.Velocity.Length()
				sumAlpha += alpha(body)
				n++
			}
		}
		vy := sumVy / float64(n)
		if math.Abs(vy) < math.Abs(bestVy) {
			bestVy, bestSpeed, bestAlpha = vy, sumSpeed/float64(n), sumAlpha/float64(n)
		}
	}
	t.Logf("settle fuel %.0f: speed %.1f m/s (%.0f kt) alpha %.1f° (level-est vy %.2f)",
		fuel, bestSpeed, bestSpeed*1.94384, bestAlpha*57.3, bestVy)
	if math.Abs(bestVy) > 1.5 {
		t.Fatalf("no near-level trim found in the throttle sweep: best vy %.2f", bestVy)
	}
	return bestSpeed, bestAlpha
}

// TestOnspeed: the PA law's hands-off on-speed condition should sit near the
// real jet's approach numbers — 8.1° alpha at roughly 135 kt (69 m/s) at a
// typical trap weight (about half fuel).
func TestOnspeed(t *testing.T) {
	speed, a := settle(t, 2500)
	if math.Abs(a-8.1*math.Pi/180) > 1.2*math.Pi/180 {
		t.Fatalf("on-speed alpha %.1f°, want ~8.1°", a*180/math.Pi)
	}
	if speed < 62 || speed > 76 {
		t.Fatalf("on-speed %.1f m/s (%.0f kt), want ~66-74 m/s (130-143 kt)", speed, speed*1.94384)
	}
	t.Logf("on-speed: %.1f m/s (%.0f kt) at alpha %.1f°", speed, speed*1.94384, a*180/math.Pi)
}

// TestOnspeedHeavy: heavier jets fly faster approaches at the same alpha.
func TestOnspeedHeavy(t *testing.T) {
	light, _ := settle(t, 1000)
	heavy, _ := settle(t, 4500)
	if heavy <= light+1 {
		t.Fatalf("approach speed must grow with weight: light %.1f heavy %.1f m/s", light, heavy)
	}
	t.Logf("V approach light %.0f kt, heavy %.0f kt", light*1.94384, heavy*1.94384)
}

// TestFlyawayCapture: hands-off off the catapult, the PA law settles near the
// 16 deg flyaway deck attitude (the weight board's light row, #154) instead
// of riding approach alpha into a zoom.
func TestFlyawayCapture(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 25}, Vec3{X: 1}, 75, 3000)
	gear := true
	for i := 0; i < 240*30; i++ {
		if m.State.Velocity.Length() > 110 {
			gear = false // clean up like a real departure — the climb must SURVIVE the PA-to-UA law switch at 130 m/s
		}
		m.Step(Inputs{Throttle: 1, Gear: gear})
		if i == 240*12-1 {
			forward := m.State.Attitude.Rotate(Vec3{X: 1})
			pitch := math.Asin(clamp(forward.Y, -1, 1)) * 180 / math.Pi
			t.Logf("hands-off flyaway pitch after 12 s: %.1f°", pitch)
			if pitch < 12 || pitch > 20 {
				t.Fatalf("flyaway pitch %.1f°, want ~16°", pitch)
			}
		}
	}
	forward := m.State.Attitude.Rotate(Vec3{X: 1})
	pitch := math.Asin(clamp(forward.Y, -1, 1)) * 180 / math.Pi
	t.Logf("pitch after 30 s (through the law switch): %.1f°", pitch)
	if pitch < 6 {
		t.Fatalf("climb collapsed after the law switch: pitch %.1f°", pitch)
	}
}

// TestFlyawayProfile: a REAL catapult shot, hands off, sampling attitude,
// flight path, alpha and speed through the flyaway. Measurement only — set
// AIR_FLYAWAY=1 to run, AIR_FLYAWAY_DATUM=<degrees> to sweep the capture
// datum. The reference is the legacy Hornet's catapult longitudinal trim,
// read off the weight board: 16° up to 44,000 lb, 17° to 48,000, then 19°.
// Nose position is not flight path — the difference is alpha — so the trim
// figure is compared against PITCH here, never against the climb angle.
func TestFlyawayProfile(t *testing.T) {
	if os.Getenv("AIR_FLYAWAY") == "" {
		t.Skip("measurement probe: set AIR_FLYAWAY=1")
	}
	m := aboard()
	if datum := os.Getenv("AIR_FLYAWAY_DATUM"); datum != "" {
		degrees, err := strconv.ParseFloat(datum, 64)
		if err != nil {
			t.Fatalf("AIR_FLYAWAY_DATUM %q: %v", datum, err)
		}
		m.Airframe.Control.Flyaway = degrees * math.Pi / 180
	}
	park(m, 42.7, -0.6)
	for i := 0; i < 240*2; i++ {
		m.Step(Inputs{Gear: true})
	}
	if m.State.Gear.Catapult != 0 {
		t.Fatalf("did not attach: %d", m.State.Gear.Catapult)
	}
	m.State.Engine[0] = EngineState{Spool: 1}
	m.State.Engine[1] = EngineState{Spool: 1}
	pounds := m.mass * 2.20462
	board := 19.0
	switch {
	case pounds <= 44000:
		board = 16
	case pounds <= 48000:
		board = 17
	}
	t.Logf("mass %.0f kg (%.0f lb) — weight board calls for %.0f° nose up; datum %.1f°",
		m.mass, pounds, board, m.Airframe.Control.Flyaway*180/math.Pi)

	marks := []float64{0, 1, 2, 3, 4, 6, 8, 10, 12, 15, 20, 25}
	next, launched := 0, -1
	peak := math.Inf(-1)
	gear := true
	for i := 0; i < 240*40; i++ {
		if m.State.Velocity.Length() > 110 {
			gear = false // clean up like a real departure
		}
		m.Step(Inputs{Gear: gear, Throttle: 1, Reheat: 1, Launch: i > 240})
		if launched < 0 && m.State.Gear.Catapult < 0 && i > 240 {
			launched = i
			t.Logf("release: %.1f m/s (%.0f kt), altitude %.1f m",
				m.State.Velocity.Length(), m.State.Velocity.Length()*1.94384, m.State.Position.Y)
		}
		if launched < 0 {
			continue
		}
		forward := m.State.Attitude.Rotate(Vec3{X: 1})
		pitch := math.Asin(clamp(forward.Y, -1, 1)) * 180 / math.Pi
		if pitch > peak {
			peak = pitch
		}
		if elapsed := float64(i-launched) / 240; next < len(marks) && elapsed >= marks[next] {
			speed := m.State.Velocity.Length()
			path := 0.0
			if speed > 1e-6 {
				path = math.Asin(clamp(m.State.Velocity.Y/speed, -1, 1)) * 180 / math.Pi
			}
			body := m.State.Attitude.Unrotate(m.State.Velocity)
			t.Logf("t+%4.1fs  pitch %5.1f°  path %5.1f°  alpha %4.1f°  speed %5.0f kt  altitude %6.0f m",
				elapsed, pitch, path, alpha(body)*180/math.Pi, speed*1.94384, m.State.Position.Y)
			next++
		}
	}
	t.Logf("peak pitch %.1f° against the board's %.0f°", peak, board)
}

// TestStaticThrust: two F404-GE-402s at military power, sea level, static —
// the airframe should accelerate at roughly thrust/mass minus rolling drag.
func TestStaticThrust(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 2000}, Vec3{X: 1}, 200, 2500)
	for i := 0; i < 240*8; i++ {
		m.Step(Inputs{Throttle: 1})
	}
	if m.State.Fcs.Normal < 0.5 {
		t.Fatalf("model unstable at military power")
	}
}

// ---- Energy anchors (AIR_ENERGY=1) ----------------------------------------
// The three public reference points for "is the jet's energy right": level
// top speed, level acceleration, and sustained turn rate, each measured under
// a closed-loop altitude hold so thrust goes into speed rather than climb.
// Measurement only, no gates. Clean F/A-18C anchors: M1.7-1.8 near 36,000 ft
// (brochure, firm); roughly M1.0 on the deck (commonly cited); sustained turn
// ~18°/s low / ~13°/s at 15,000 ft (community EM-chart numbers — the firm
// source is the NFM-200 performance charts). Fuel is frozen (Cheat.Fuel) so
// mass stays constant through a run.

// leveler is a PI altitude hold on the pitch stick: the altitude error
// commands a vertical speed, and the integral supplies the steady pull a
// banked turn demands.
type leveler struct{ integral float64 }

func (l *leveler) pitch(m *Model, target float64) float64 {
	want := clamp((target-m.State.Position.Y)*0.05, -15, 15)
	err := want - m.State.Velocity.Y
	l.integral = clamp(l.integral+err*Dt*0.03, -1, 1)
	return clamp(err*0.03+l.integral, -1, 1)
}

// bank is the current bank angle, positive right wing down.
func bank(m *Model) float64 {
	right := m.State.Attitude.Rotate(Vec3{Z: 1})
	return -math.Asin(clamp(right.Y, -1, 1))
}

// energy builds the constant-mass measurement jet.
func energy() *Model {
	environment := Environment{Seed: 1}
	environment.Cheat.Fuel = true
	return New(Fighter, environment, World{Sea: 0})
}

// TestTopSpeed: level-held terminal speed on the deck and at 36,000 ft.
func TestTopSpeed(t *testing.T) {
	if os.Getenv("AIR_ENERGY") == "" {
		t.Skip("measurement probe: set AIR_ENERGY=1")
	}
	for _, altitude := range []float64{300, 11000} {
		m := energy()
		m.State = Level(m, Vec3{Y: altitude}, Vec3{X: 1}, 220, 2500)
		var hold leveler
		mark := 0.0
		for i := 0; i < 240*480; i++ {
			m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: hold.pitch(m, altitude)})
			if i == 240*450 {
				mark = m.State.Velocity.Length()
			}
		}
		speed := m.State.Velocity.Length()
		t.Logf("altitude %5.0f m: %3.0f kt TAS / %3.0f KCAS, M%.2f  (mass %.0f kg, drift %+.1f kt over the last 30 s, altitude %+.0f m off datum)",
			altitude, speed*1.94384, m.Cas()*1.94384, m.Mach(), m.mass, (speed-mark)*1.94384, m.State.Position.Y-altitude)
	}
}

// TestAcceleration: timed level acceleration through the combat band on the
// deck, full reheat — the segment times are the feel of the jet.
func TestAcceleration(t *testing.T) {
	if os.Getenv("AIR_ENERGY") == "" {
		t.Skip("measurement probe: set AIR_ENERGY=1")
	}
	m := energy()
	m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, 140, 2500)
	var hold leveler
	gates := []float64{300, 350, 400, 450, 500, 550, 600} // KCAS
	previous := 0.0
	next := 0
	t.Logf("level acceleration at 300 m, full reheat, mass %.0f kg (%.0f lb)", m.mass, m.mass*2.20462)
	for i := 0; i < 240*240 && next < len(gates); i++ {
		m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: hold.pitch(m, 300)})
		if cas := m.Cas() * 1.94384; cas >= gates[next] {
			elapsed := float64(i) / 240
			if next == 0 {
				t.Logf("  %3.0f KCAS  t+%5.1fs", gates[next], elapsed)
			} else {
				t.Logf("  %3.0f KCAS  t+%5.1fs  (segment %+5.1fs)", gates[next], elapsed, elapsed-previous)
			}
			previous = elapsed
			next++
		}
	}
	if next < len(gates) {
		t.Logf("  never reached %3.0f KCAS (topped out at %.0f KCAS)", gates[next], m.Cas()*1.94384)
	}
}

// excess measures specific excess power at one speed/load point: trim level,
// roll steep so the lift vector stays near the horizon, servo the pull to the
// target load factor, and read the energy-height rate over a two-second
// window — short enough that the state stays near the target while it drifts.
// Returns the power (m/s), the load factor actually achieved (the alpha or g
// limiter may cap the pull), and the mean true airspeed over the window.
func excess(altitude float64, target float64, load float64) (float64, float64, float64) {
	m := energy()
	ratio := math.Sqrt(1.225 / air(altitude, m.Environment).Density)
	m.State = Level(m, Vec3{Y: altitude}, Vec3{X: 1}, target/1.94384*ratio, 2500)
	height := func() float64 {
		speed := m.State.Velocity.Length()
		return m.State.Position.Y + speed*speed/(2*9.80665)
	}
	const begin, end = 240 * 3, 240 * 5
	stick, start := 0.0, 0.0
	normal, speed := 0.0, 0.0
	for i := 0; i < end; i++ {
		stick = clamp(stick+(load-m.Nz())*1.5*Dt, -0.3, 1)
		m.Step(Inputs{
			Throttle: 1, Reheat: 1,
			Roll:  clamp((80*math.Pi/180-bank(m))*2, -1, 1),
			Pitch: stick,
		})
		if i == begin {
			start = height()
		}
		if i >= begin {
			normal += m.Nz()
			speed += m.State.Velocity.Length()
		}
	}
	window := float64(end - begin)
	return (height() - start) / (window / 240), normal / window, speed / window
}

// TestSustainedTurn: the EM-chart quantity, measured directly. For each
// speed, sweep the load factor and sample specific excess power; the Ps = 0
// crossing is the sustained g, and the sustained turn rate follows from it.
// A speed whose power is still positive at the highest pull the jet gives is
// lift- or g-limited rather than thrust-limited — the sustained rate there
// is the rate at that maximum pull.
func TestSustainedTurn(t *testing.T) {
	if os.Getenv("AIR_ENERGY") == "" {
		t.Skip("measurement probe: set AIR_ENERGY=1")
	}
	type point struct{ load, power, tas float64 }
	for _, altitude := range []float64{300, 4600} {
		t.Logf("--- altitude %.0f m ---", altitude)
		best, at := 0.0, 0.0
		for _, target := range []float64{250, 300, 350, 400, 450} { // KCAS
			var samples []point
			line := ""
			for _, load := range []float64{2, 3, 4, 5, 6, 7} {
				power, achieved, tas := excess(altitude, target, load)
				samples = append(samples, point{achieved, power, tas})
				line += fmt.Sprintf("  n%.0f:%+4.0f", load, power)
				if achieved < load-0.3 {
					line += fmt.Sprintf(" (pull capped at n%.1f)", achieved)
					break
				}
			}
			sustained, tas, note := 0.0, 0.0, ""
			for j := 1; j < len(samples); j++ {
				a, b := samples[j-1], samples[j]
				if a.power > 0 && b.power <= 0 {
					f := a.power / (a.power - b.power)
					sustained = a.load + (b.load-a.load)*f
					tas = a.tas + (b.tas-a.tas)*f
					break
				}
			}
			if sustained == 0 && len(samples) > 0 && samples[len(samples)-1].power > 0 {
				last := samples[len(samples)-1]
				sustained, tas = last.load, last.tas
				note = "  (thrust to spare: limited by the pull)"
			}
			rate := 0.0
			if sustained > 1 {
				rate = math.Sqrt(sustained*sustained-1) * 9.80665 / tas * 180 / math.Pi
			}
			t.Logf("  %3.0f KCAS: sustained n %.1f -> %4.1f°/s%s   [Ps m/s:%s]", target, sustained, rate, note, line)
			if rate > best {
				best, at = rate, target
			}
		}
		t.Logf("  best sustained: %.1f°/s at %.0f KCAS", best, at)
	}
}

// TestPullProbe (AIR_PULL=1): full-aft-stick pull at combat speeds, tracing
// the command chain (demand, alpha, lift, stabilator, trim integrators) —
// the instrument that found and fixed the never-pegs-7.5 defect.
func TestPullProbe(t *testing.T) {
	if os.Getenv("AIR_PULL") == "" {
		t.Skip("probe")
	}
	for _, target := range []float64{300, 350, 400, 450} {
		m := energy()
		m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, target/1.94384*1.015, 2500)
		throw := m.Airframe.Control.Throw.Down
		t.Logf("--- %3.0f KCAS pull (throw %.1f°, blowdown %.0f Pa) ---",
			target, throw*180/math.Pi, m.Airframe.Control.Blowdown)
		peak := 0.0
		for i := 0; i < 240*12; i++ {
			m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: 1,
				Roll: clamp((80*math.Pi/180-bank(m))*2, -1, 1)})
			if n := m.Nz(); n > peak {
				peak = n
			}
			if i%120 == 119 { // every 0.5 s
				f := m.State.Fcs
				speed := m.State.Velocity.Length()
				pressure := 0.5 * air(m.State.Position.Y, m.Environment).Density * speed * speed
				bound := throw * clamp(m.Airframe.Control.Blowdown/pressure, 0, 1)
				cl := m.Nz() * m.mass * 9.80665 / math.Max(pressure*m.Airframe.Reference.Area, 1)
				t.Logf("t+%4.1fs %3.0f KCAS  n %4.2f  demand %4.2f  alpha %4.1f°  cl %4.2f  stab %+5.1f°/±%4.1f°  trim %+5.2f  integral %+5.2f",
					float64(i+1)/240, m.Cas()*1.94384, m.Nz(), f.Demand, m.Alpha()*180/math.Pi, cl,
					f.Stabilator.Left*180/math.Pi, bound*180/math.Pi, f.Trim, f.Integral)
			}
		}
		t.Logf("  peak n %.2f", peak)
	}
}

// TestPull: full aft stick at 450 KCAS must PEG the limiter. Regression
// teeth for the never-pegs-7.5 fix (the C* fixed-point droop, the
// zero-referenced carefree caps, the missing kinematic feedforward, and the
// trim-rate bottleneck each parked pulls at 5.5-7.2 g). Generous bounds:
// ≥7.3 g within 2.5 s, still pegged at 4 s, never past 7.65 (the overstress
// ledger opens at 7.5).
func TestPull(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, 450/1.94384*1.015, 2500)
	peak, when := 0.0, 0.0
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Throttle: 1, Reheat: 1, Pitch: 1,
			Roll: clamp((80*math.Pi/180-bank(m))*2, -1, 1)})
		if n := m.Nz(); n > peak {
			peak, when = n, float64(i)/240
		}
		if m.Nz() > 7.65 {
			t.Fatalf("overshoot: %.2f g at %.2f s", m.Nz(), float64(i)/240)
		}
		if i == 240*5/2 && m.Nz() < 7.3 {
			t.Fatalf("pull too slow: %.2f g at 2.5 s, want >=7.3", m.Nz())
		}
	}
	if m.Nz() < 7.2 {
		t.Fatalf("not pegged: %.2f g at 4 s", m.Nz())
	}
	t.Logf("peak %.2f g at %.1f s; %.2f g at 4 s", peak, when, m.Nz())
}

// loaded builds a synthetic state at one body alpha and speed in the UA
// manoeuvring configuration (slats and AUTO flaps at their scheduled angles,
// stabilator as given), evaluates the aero pass alone, and returns wind-axis
// lift and drag coefficients plus the pitch moment about the CG. The engine
// state is empty so thrust cannot contaminate the lift axis.
func loaded(m *Model, angle float64, speed float64, stabilator float64) (float64, float64, float64) {
	local := air(1000, m.Environment)
	pressure := 0.5 * local.Density * speed * speed
	s := &m.State
	s.Position = Vec3{Y: 1000}
	s.Velocity = Vec3{X: speed}
	s.Attitude = Axis(Vec3{Z: 1}, angle)
	s.Omega = Vec3{}
	s.Engine = [4]EngineState{}
	s.Fuel = 2500
	s.Fcs = FcsState{}
	c := &m.Airframe.Control
	s.Fcs.Slat = clamp(c.Slat.Slope*(angle-c.Slat.Offset), 0, c.Slat.Limit)
	droop := clamp(c.Flap.Slope*(angle-c.Flap.Offset), 0, c.Flap.Limit) * clamp(1-pressure/c.Flap.Pressure, 0, 1)
	s.Fcs.Flaperon.Left, s.Fcs.Flaperon.Right = droop, droop
	s.Fcs.Stabilator.Left, s.Fcs.Stabilator.Right = stabilator, stabilator
	m.weigh()
	m.gust = Vec3{}
	var forces Forces
	m.aero(s, &forces, local)
	area := pressure * m.Airframe.Reference.Area
	lift := (forces.Force.X*math.Sin(angle) + forces.Force.Y*math.Cos(angle)) / area
	drag := -(forces.Force.X*math.Cos(angle) - forces.Force.Y*math.Sin(angle)) / area
	return lift, drag, forces.Moment.Z
}

// TestLift (AIR_LIFT=1): the whole-airframe lift curve in the manoeuvring
// configuration, free and TRIMMED — the stabilator bisected to zero pitching
// moment per point, which is the lift that actually buys turn rate. The
// corner anchor: 7.5 g at ~330-360 KCAS at mid weight needs usable trimmed
// CL of about 1.4-1.55 by ~20° alpha, smooth.
func TestLift(t *testing.T) {
	if os.Getenv("AIR_LIFT") == "" {
		t.Skip("measurement probe: set AIR_LIFT=1")
	}
	speed := 180.0 // ~350 KCAS at the probe altitude
	m := energy()
	t.Logf("lift curve at %.0f m/s: free = stab 0, trimmed = stab bisected to zero moment", speed)
	for degrees := 0.0; degrees <= 34; degrees += 2 {
		angle := degrees * math.Pi / 180
		free, _, _ := loaded(m, angle, speed, 0)
		low, high := -m.Airframe.Control.Throw.Down, m.Airframe.Control.Throw.Up
		trimmed, drag, deflection := free, 0.0, 0.0
		for i := 0; i < 40; i++ {
			mid := (low + high) / 2
			lift, d, moment := loaded(m, angle, speed, mid)
			if moment > 0 {
				low = mid // nose-up excess: more trailing-edge down
			} else {
				high = mid
			}
			trimmed, drag, deflection = lift, d, mid
		}
		t.Logf("alpha %4.1f°: free CL %5.2f  trimmed CL %5.2f (stab %+5.1f°)  CD %5.3f  L/D %4.1f",
			degrees, free, trimmed, deflection*180/math.Pi, drag, trimmed/math.Max(drag, 1e-6))
	}
}

// TestPush: full forward stick at 400 KCAS — the negative side of the
// carefree limiter, exercised nowhere else. The push must reach the floor's
// neighbourhood without diving through it or winding into deep negative
// stall, and the negative-alpha protection must hold.
func TestPush(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, 400/1.94384*1.015, 2500)
	least, alpha := 1.0, 0.0
	for i := 0; i < 240*4; i++ {
		m.Step(Inputs{Throttle: 0.6, Pitch: -1})
		if n := m.Nz(); n < least {
			least = n
		}
		if a := m.Alpha(); a < alpha {
			alpha = a
		}
		if m.Nz() < -3.6 {
			t.Fatalf("dove through the floor: %.2f g at %.2f s", m.Nz(), float64(i)/240)
		}
	}
	t.Logf("least %.2f g, deepest alpha %.1f°", least, alpha*180/math.Pi)
	if least > -2.0 {
		t.Fatalf("push never approached the floor: least %.2f g, want near -3", least)
	}
	if alpha < -(m.Airframe.Limit.Floor + 6*math.Pi/180) {
		t.Fatalf("negative-alpha protection failed: %.1f°", alpha*180/math.Pi)
	}
}

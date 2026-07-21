// V-speed survey (#89): every speed is MEASURED by flying the model — decel
// stalls, climb sweeps, engine-out trims — not computed from formulas, so the
// numbers track whatever the flight model actually does.
//
// Deliberately NOT part of the normal suite (several minutes of simulation):
// run it with tools/vspeeds.sh, or
//   VSPEEDS=1 go test ./games/air/flight -run TestVSpeeds -v -timeout 30m
//
// Conventions: standard atmosphere, calm air. Climbs at MIL (dry) power, the
// charted convention; sustained turns at full afterburner (fighter EM
// convention); Vmc at full afterburner on the live engine (worst case).
// Speeds print as KCAS (the HUD box, via the compressible pitot) and KTAS.

package flight

import (
	"fmt"
	"math"
	"os"
	"testing"
)

const (
	lightFuel = 500  // kg — minimum usable (the server tank clamp floor)
	heavyFuel = 4900 // kg — full internal: maximum gross for the clean-plus-AIM-9 jet
)

var vspeedAlts = []struct {
	label string
	m     float64
}{
	{"sea level", 30},
	{"15,000 ft", 4572},
	{"30,000 ft", 9144},
}

func vsKnots(tas float64) float64 { return tas * 1.943844 }
func vsBoth(tas, alt float64) string {
	return fmt.Sprintf("%3.0f KCAS/%3.0f KTAS", vsKnots(calibrated(tas, alt, Environment{})), vsKnots(tas))
}

func vsJet(fuel, alt, speed float64, gear bool) *Model {
	m := New(Fighter, Environment{Seed: 1, Wrap: 250000}, World{Sea: 0})
	m.State.Position = Vec3{Y: alt}
	m.State.Velocity = Vec3{X: speed}
	m.State.Attitude = Look(Vec3{X: 1})
	m.State.Fuel = fuel
	ext := 0.0
	if gear {
		ext = 1
	}
	m.State.Gear = GearState{Extension: ext, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	m.State.Engine[0] = EngineState{Spool: 0.8}
	m.State.Engine[1] = EngineState{Spool: 0.8}
	return m
}

// vsStall decelerates in level flight at idle until the jet can no longer
// hold altitude — the operational stall under the carefree FCS (the alpha
// limiter IS this jet's stall boundary). The entry is trimmed analytically
// (attitude at the level-flight alpha) so no spawn transient trips the sink
// detector; idle thrust then bleeds ~1-2 kt/s with the stick holding level.
// Returns TAS at the sink onset.
func vsStall(fuel, alt float64, gear bool) float64 {
	rho := air(alt, Environment{}).Density
	v0 := 1.35 * math.Sqrt(2*(10700+fuel)*9.81/(rho*1.3*37.16))
	m := vsJet(fuel, alt, v0, gear)
	trim := clamp(2*(10700+fuel)*9.81/(rho*v0*v0*37.16)/4.7+0.01, 0, 0.3) // CL/(finite-wing slope) plus a hair
	m.State.Attitude = Axis(Vec3{Z: 1}, trim)
	m.State.Engine[0] = EngineState{Spool: 0.3}
	m.State.Engine[1] = EngineState{Spool: 0.3}
	stick := 0.0
	candidate := 0.0
	for i := 0; i < 240*180; i++ {
		s := &m.State
		v := s.Velocity.Length()
		stick = clamp(stick+clamp(((alt-s.Position.Y)*0.002-s.Velocity.Y*0.02-stick*4)*0.002, -0.004, 0.004), -0.5, 1)
		in := Inputs{Throttle: 0, Pitch: stick}
		in.Gear = gear
		m.Step(in)
		if i < 240*3 {
			continue // arm the detector once the entry settles
		}
		low := alt - s.Position.Y
		if candidate == 0 && low > 8 && s.Velocity.Y < -1.5 {
			candidate = v // sink onset: the mush has begun
		}
		if low > 20 && s.Velocity.Y < -4 {
			if candidate != 0 {
				return candidate
			}
			return v
		}
		if v < 40 {
			return v
		}
	}
	return candidate
}

// vsRotate measures the minimum rotation speed: full MIL takeoff roll with
// full aft stick held from 30 m/s — Vr where the nose lifts, Vlof at liftoff.
func vsRotate(fuel float64) (float64, float64) {
	world := World{Sea: 0, Fields: []Field{{Height: 0, Strips: []Strip{{A: Vec3{X: -200}, B: Vec3{X: 4000}, Width: 60}}}}}
	m := New(Fighter, Environment{Seed: 1, Wrap: 250000}, world)
	m.State.Position = Vec3{Y: 2.6}
	m.State.Velocity = Vec3{X: 0.5}
	m.State.Attitude = Look(Vec3{X: 1})
	m.State.Fuel = fuel
	m.State.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
	base := math.NaN()
	vr := 0.0
	for i := 0; i < 240*90; i++ {
		v := m.State.Velocity.Length()
		stick := 0.0
		if v > 30 {
			stick = 1
		}
		m.Step(Inputs{Gear: true, Throttle: 1, Pitch: stick})
		pitch := math.Asin(clamp(m.State.Attitude.Rotate(Vec3{X: 1}).Y, -1, 1))
		if v > 30 && math.IsNaN(base) {
			base = pitch
		}
		if vr == 0 && !math.IsNaN(base) && pitch > base+2*math.Pi/180 {
			vr = v // nosewheel lifting
		}
		if vr != 0 && !m.State.Gear.Wow && m.State.Position.Y > 3.5 {
			return vr, v
		}
	}
	return vr, math.NaN()
}

// vsClimbTrim bisects the level-trim alpha at a speed and returns it with the
// STATIC steady-climb estimate (thrust minus trimmed drag): the entry state
// for the dynamic measurement below.
func vsClimbTrim(m *Model, fuel, alt, v float64, engines float64, reheat bool) (float64, float64) {
	w := (10700 + fuel) * 9.81
	local := air(alt, Environment{})
	lift := func(alpha float64) float64 {
		trial := m.State
		trial.Velocity = Vec3{X: v}
		trial.Attitude = Axis(Vec3{Z: 1}, alpha)
		trial.Engine[0] = EngineState{}
		trial.Engine[1] = EngineState{}
		return trial.Attitude.Rotate(m.forces(&trial, Inputs{}, local).Force).Y
	}
	lo, hi := 0.0, 0.35
	for i := 0; i < 20; i++ {
		mid := (lo + hi) / 2
		if lift(mid) < w {
			lo = mid
		} else {
			hi = mid
		}
	}
	alpha := (lo + hi) / 2
	trial := m.State
	trial.Velocity = Vec3{X: v}
	trial.Attitude = Axis(Vec3{Z: 1}, alpha)
	trial.Engine[0] = EngineState{}
	trial.Engine[1] = EngineState{}
	drag := -trial.Attitude.Rotate(m.forces(&trial, Inputs{}, local).Force).X
	mach := v / local.Sound
	dry, boost := output(EngineState{Spool: 1, Reheat: 1}, &m.Airframe.Engines[0], local.Density, mach)
	thrust := engines * dry
	if reheat {
		thrust = engines * (dry + boost)
	}
	gamma := math.Asin(clamp((thrust-drag)/w, -0.95, 0.95))
	return alpha, gamma
}

// vsClimbPoint measures the climb capability at one speed as the SPECIFIC
// ENERGY rate (Ps) over a windowed MIL run. The entry is PRE-ESTABLISHED in
// the statically-estimated steady climb (attitude and velocity vector both on
// the climb angle): entering level, the jet spent the whole window pulling
// into a 45-degree climb — sustained n>1 burning the excess in induced drag —
// and read HALF the true low-speed Ps, smearing Vx into Vy. engines counts
// the live engines: 2, or 1 for the single-engine rows. (A zero-engine glide
// through this machinery porpoises — vsGlide measures that statically.)
func vsClimbPoint(fuel, alt, target float64, engines float64, reheat bool) float64 {
	m := vsJet(fuel, alt, target, false)
	alpha, gamma := vsClimbTrim(m, fuel, alt, target, engines, reheat)
	m.State.Velocity = Vec3{X: target * math.Cos(gamma), Y: target * math.Sin(gamma)}
	m.State.Attitude = Axis(Vec3{Z: 1}, gamma+alpha)
	wet := 0.0
	if reheat {
		wet = 1
	}
	m.State.Engine[0] = EngineState{Spool: 1, Reheat: wet}
	m.State.Engine[1] = EngineState{Spool: 1, Reheat: wet}
	if engines < 2 {
		m.State.Damage.Engine[1] = 1
	}
	stick := 0.05
	var e0, e1 float64
	const settle, window = 240 * 4, 240 * 8
	for i := 0; i < settle+window; i++ {
		s := &m.State
		v := s.Velocity.Length()
		stick = clamp(stick+clamp((v-target)*0.001, -0.004, 0.004), -0.4, 1)
		up := s.Attitude.Rotate(Vec3{Y: 1})
		right := s.Attitude.Rotate(Vec3{Z: 1})
		bank := math.Atan2(right.Y, up.Y)
		roll := clamp(bank*2.5, -1, 1)
		body := s.Attitude.Unrotate(s.Velocity)
		beta := math.Asin(clamp(body.Z/math.Max(v, 1), -1, 1))
		pedal := clamp(-beta*3-s.Omega.Y*4, -1, 1) // sideslip-nulling + rate damping: a rate-only pedal leaves the dead-engine moment balanced by STEADY sideslip, whose drag turned a +10 deg single-engine climb into -5
		m.Step(Inputs{Throttle: 1, Reheat: wet, Pitch: stick, Roll: roll, Yaw: pedal})
		if i == settle {
			e0 = s.Position.Y + v*v/19.62
		}
		if i == settle+window-1 {
			e1 = s.Position.Y + v*v/19.62
			if math.Abs(v-target) > 20 {
				return math.NaN() // the hold lost the point entirely
			}
		}
	}
	return (e1 - e0) / (float64(window) / 240)
}

// vsClimbSweep finds Vy (best rate) and Vx (best gradient) over a speed grid,
// refining around each maximum.
func vsClimbSweep(fuel, alt, stall float64, engines float64, reheat bool) (vx, gx, vy, ry float64) {
	type point struct{ v, rate, grad float64 }
	measure := func(v float64) point {
		r := vsClimbPoint(fuel, alt, v, engines, reheat)
		if math.IsNaN(r) {
			return point{v, math.Inf(-1), math.Inf(-1)}
		}
		return point{v, r, r / v}
	}
	var pts []point
	lo := math.Max(1.15*stall, 80)
	for v := lo; v <= lo+220; v += 15 {
		pts = append(pts, measure(v))
	}
	cache := map[float64]point{}
	sample := func(v float64) point {
		if p, ok := cache[v]; ok {
			return p
		}
		p := measure(v)
		cache[v] = p
		return p
	}
	best := func(key func(point) float64) point {
		b := pts[0]
		for _, p := range pts {
			if key(p) > key(b) {
				b = p
			}
		}
		// Multi-stage ring refinement: hill-climb at each scale until the
		// centre is the local maximum, then halve, down to ~0.5 kt. The climb
		// may walk past the coarse grid's top — that also recovers a peak the
		// grid ceiling would otherwise clip.
		for step := 7.5; step >= 0.2; step /= 2 {
			for moved := true; moved; {
				moved = false
				for _, dv := range []float64{-step, step} {
					if p := sample(b.v + dv); key(p) > key(b) {
						b = p
						moved = true
					}
				}
			}
		}
		return b
	}
	r := best(func(p point) float64 { return p.rate })
	g := best(func(p point) float64 { return p.grad })
	return g.v, math.Asin(clamp(g.rate/g.v, -1, 1)) * 180 / math.Pi, r.v, r.rate
}

// vsGlide finds best glide — the shallowest dead-stick descent — by STATIC
// trim on the same m.forces route as vsVh: at each speed, an inner bisection
// trims the stabilator (pitch moment), a middle one trims alpha (path-normal
// balance), and a fixed-point iteration settles the slope where the
// path-tangential force balances. FLYING the glide with a speed-governor
// stick porpoised through the UA law's negative-g range (±4 g swings read
// L/D 2.3 at every speed) — like the approach, an idle steady state has no
// unique dynamic attractor for a simple governor to hold. Returns the best
// speed and its glide angle (rad, negative).
func vsGlide(fuel, alt, stall float64) (float64, float64) {
	m := vsJet(fuel, alt, 150, false)
	m.State.Damage.Engine[0] = 1
	m.State.Damage.Engine[1] = 1
	c := m.Airframe.Control
	local := air(alt, Environment{})
	weight := (10700 + fuel) * 9.81
	glide := func(v float64) float64 {
		q := 0.5 * local.Density * v * v
		blow := clamp(c.Blowdown/math.Max(q, 1), 0, 1)
		gamma := -0.1
		for iteration := 0; iteration < 14; iteration++ {
			evaluate := func(alpha float64) (normal, tangential float64) {
				trial := m.State
				trial.Velocity = Vec3{X: v * math.Cos(gamma), Y: v * math.Sin(gamma)}
				trial.Attitude = Axis(Vec3{Z: 1}, alpha+gamma)
				trial.Engine[0] = EngineState{}
				trial.Engine[1] = EngineState{}
				lo, hi := -c.Throw.Down*blow, c.Throw.Up*blow
				var total Forces
				for i := 0; i < 16; i++ {
					mid := (lo + hi) / 2
					trial.Fcs.Stabilator = Pair{Left: mid, Right: mid}
					total = m.forces(&trial, Inputs{}, local)
					if total.Moment.Z > 0 {
						lo = mid
					} else {
						hi = mid
					}
				}
				force := total.Force.Add(trial.Attitude.Unrotate(Vec3{Y: -weight}))
				path := trial.Attitude.Unrotate(trial.Velocity).Scale(1 / v)
				return force.X*(-path.Y) + force.Y*path.X, force.Dot(path)
			}
			lo, hi := -0.02, 0.35
			tangential := 0.0
			for i := 0; i < 16; i++ {
				mid := (lo + hi) / 2
				var normal float64
				normal, tangential = evaluate(mid)
				if normal < 0 {
					lo = mid
				} else {
					hi = mid
				}
			}
			// Steeper slope adds forward weight component: walk gamma to the
			// tangential balance.
			gamma = clamp(gamma+tangential/weight, -1.2, -0.005)
		}
		return gamma
	}
	lo := 1.05 * stall
	bestV, bestG := lo, glide(lo)
	for v := lo + 10; v <= lo+150; v += 10 {
		if g := glide(v); g > bestG {
			bestV, bestG = v, g
		}
	}
	for step := 5.0; step >= 0.2; step /= 2 {
		for moved := true; moved; {
			moved = false
			for _, dv := range []float64{-step, step} {
				if bestV+dv < lo {
					continue
				}
				if g := glide(bestV + dv); g > bestG {
					bestV, bestG = bestV+dv, g
					moved = true
				}
			}
		}
	}
	return bestV, bestG
}

// vsVmc finds the minimum control speed by STATIC moment balance: the yaw
// moment of full-afterburner single-engine thrust asymmetry against the yaw
// authority of full rudder at that speed, bisected to the crossing. (A dynamic
// trial is hopeless here: at light weight full AB is beyond 1:1 thrust-weight
// and any speed-hold goes near-vertical within seconds.) Returns 0 when full
// rudder overpowers the asymmetry all the way down to the stall — Vmc not
// limiting, as expected with the F404s podded ±0.55 m off centreline.
func vsVmc(fuel, alt, stall float64) float64 {
	m := vsJet(fuel, alt, 100, false)
	m.State.Damage.Engine[1] = 1 // propulsion reads damage from the MODEL state
	yaw := func(v, rudder float64) float64 {
		trial := m.State
		trial.Velocity = Vec3{X: v}
		rho := air(alt, Environment{}).Density
		trial.Attitude = Axis(Vec3{Z: 1}, clamp(2*(10700+fuel)*9.81/(rho*v*v*37.16)/4.7+0.01, 0, 0.3))
		trial.Engine[0] = EngineState{Spool: 1, Reheat: 1}
		trial.Engine[1] = EngineState{Spool: 1, Reheat: 1} // dead via damage, not spool: the worst-case book asymmetry
		trial.Fcs.Rudder = rudder
		local := air(alt, Environment{})
		return m.forces(&trial, Inputs{}, local).Moment.Y
	}
	throw := m.Airframe.Control.Throw.Rudder
	controllable := func(v float64) bool {
		neutral := yaw(v, 0)
		counter := yaw(v, -math.Copysign(throw, neutral))
		return neutral == 0 || counter*neutral <= 0 // full opposite rudder can null (or reverse) the asymmetry
	}
	if controllable(0.92 * stall) {
		return 0
	}
	low, high := 0.92*stall, 2.2*stall
	for i := 0; i < 24; i++ {
		mid := (low + high) / 2
		if controllable(mid) {
			high = mid
		} else {
			low = mid
		}
	}
	return high
}

// vsApproach measures on-speed: the E-bracket speed, where the landing
// configuration TRIMS at the airframe's on-speed alpha (8.1 deg) descending
// the 3 degree glideslope. Static trim solve, the same m.forces route as
// vsVmc: an inner bisection trims the stabilator to null the pitch moment,
// an outer one finds the speed where the body-normal force balances weight.
// A flown stick-free approach has NO unique settled speed to measure: the PA
// law's neutral demand tracks the alpha LEVEL FLIGHT needs at whatever speed
// the throttle gives it (fcs.go), so speed is set entirely by the throttle,
// and a sink-tracking throttle governor leaves the phugoid undamped — the
// averaging window sampled whatever phase it landed on (Vapp read 1.01 Vs0
// at max gross from one such sample).
func vsApproach(fuel, alt float64) float64 {
	gamma := 3 * math.Pi / 180
	m := vsJet(fuel, alt, 75, true)
	c := m.Airframe.Control
	local := air(alt, Environment{})
	weight := (10700 + fuel) * 9.81
	normal := func(v float64) float64 {
		q := 0.5 * local.Density * v * v
		schedule := clamp((c.Droop.Pressure-q)/(c.Droop.Pressure*0.55), 0, 1) // the FCS droop washout
		droop := c.Droop.Angle * schedule
		alpha := c.Onspeed
		trial := m.State
		trial.Velocity = Vec3{X: v * math.Cos(gamma), Y: -v * math.Sin(gamma)}
		trial.Attitude = Axis(Vec3{Z: 1}, alpha-gamma)
		trial.Engine[0] = EngineState{Spool: 0.6}
		trial.Engine[1] = EngineState{Spool: 0.6}
		trial.Fcs.Flaperon = Pair{Left: droop, Right: droop} // the flaperon actuator carries the droop; Fcs.Flap is a readout
		trial.Fcs.Slat = math.Max(clamp(c.Slat.Slope*(alpha-c.Slat.Offset), 0, c.Slat.Limit), 12*math.Pi/180*schedule)
		low, high := -c.Throw.Down, c.Throw.Up // positive = trailing edge down = nose-down moment
		var total Forces
		for i := 0; i < 16; i++ {
			mid := (low + high) / 2
			trial.Fcs.Stabilator = Pair{Left: mid, Right: mid}
			total = m.forces(&trial, Inputs{}, local)
			if total.Moment.Z > 0 {
				low = mid
			} else {
				high = mid
			}
		}
		return total.Force.Y + trial.Attitude.Unrotate(Vec3{Y: -weight}).Y
	}
	low, high := 35.0, 250.0
	for i := 0; i < 24; i++ {
		mid := (low + high) / 2
		if normal(mid) > 0 {
			high = mid
		} else {
			low = mid
		}
	}
	return (low + high) / 2
}

// vsVh finds the maximum level speed at full afterburner: the static
// thrust=drag crossing on the same m.forces route as vsApproach — inner
// stabilator-trim bisection (pitch moment), middle alpha bisection (normal
// balance), outer speed search on the longitudinal residual. A dynamic
// acceleration run asymptotes to this crossing far too slowly to read to a
// knot. The outer search scans DOWN from far beyond the envelope for the
// highest thrust-sufficient speed before bisecting, so a transonic pinch in
// the excess curve cannot strand the answer on a lower crossing.
func vsVh(fuel, alt float64) float64 {
	m := vsJet(fuel, alt, 200, false)
	c := m.Airframe.Control
	local := air(alt, Environment{})
	weight := (10700 + fuel) * 9.81
	excess := func(v float64) float64 {
		q := 0.5 * local.Density * v * v
		blow := clamp(c.Blowdown/math.Max(q, 1), 0, 1) // actuator authority washes out with q; the trim needs little, but stay honest
		trim := func(alpha float64) (Forces, Vec3) {
			trial := m.State
			trial.Velocity = Vec3{X: v}
			trial.Attitude = Axis(Vec3{Z: 1}, alpha)
			trial.Engine[0] = EngineState{Spool: 1, Reheat: 1}
			trial.Engine[1] = EngineState{Spool: 1, Reheat: 1}
			lo, hi := -c.Throw.Down*blow, c.Throw.Up*blow
			var total Forces
			for i := 0; i < 16; i++ {
				mid := (lo + hi) / 2
				trial.Fcs.Stabilator = Pair{Left: mid, Right: mid}
				total = m.forces(&trial, Inputs{}, local)
				if total.Moment.Z > 0 {
					lo = mid
				} else {
					hi = mid
				}
			}
			return total, trial.Attitude.Unrotate(Vec3{Y: -weight})
		}
		lo, hi := -0.05, 0.3
		var total Forces
		var down Vec3
		for i := 0; i < 16; i++ {
			mid := (lo + hi) / 2
			total, down = trim(mid)
			if total.Force.Y+down.Y < 0 {
				lo = mid
			} else {
				hi = mid
			}
		}
		return total.Force.X + down.X
	}
	top := 800.0
	if excess(top) > 0 {
		return top // beyond any speed the model should be trusted at; flag rather than chase
	}
	v := top
	for ; v > 60; v -= 20 {
		if excess(v) > 0 {
			break
		}
	}
	lo, hi := v, v+20
	for i := 0; i < 20; i++ {
		mid := (lo + hi) / 2
		if excess(mid) > 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// vsSustained bisects the Ps=0 load factor at one speed (the envelope-map
// method) and returns the sustained turn rate there (deg/s) with the turn
// radius (m). Full afterburner. A trial only counts as sustained when the FCS
// ACHIEVED the commanded g (window mean within 0.5): below the lift boundary
// the jet mushes at the alpha limiter while full burner still grows specific
// energy — the Ps test alone called that "sustaining 7.6 g at 90 m/s" and
// raced the radius search to zero speed. Rate and radius are computed from
// the achieved g, not the commanded one.
func vsSustained(fuel, alt, speed float64) (float64, float64) {
	alt = math.Max(alt, 457) // high-g trials DESCEND tens of metres while converging: from a 30 m "sea level" start they sank into sea-surface ground effect (and then under the waves — open water has no collision), where the slashed induced drag read as "sustains the limiter at 467 KTAS at max gross". The EM battery runs at 1500 ft for the same reason.
	measure := func(n float64) (float64, float64) {
		m := vsJet(fuel, alt, speed, false)
		m.State.Engine[0] = EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = EngineState{Spool: 1, Reheat: 1}
		stick := clamp((n-1)/6.5, 0.1, 1)
		target := -math.Acos(clamp(1/n, 0, 1))
		var e0, e1, sum float64
		count := 0
		for tick := 0; tick < 240*10; tick++ {
			s := &m.State
			up := s.Attitude.Rotate(Vec3{Y: 1})
			right := s.Attitude.Rotate(Vec3{Z: 1})
			bank := math.Atan2(right.Y, up.Y)
			roll := clamp((bank-target)*2.5, -1, 1)
			stick = clamp(stick+clamp((n-s.Fcs.Normal)*0.01, -0.01, 0.01), 0.05, 1)
			m.Step(Inputs{Pitch: stick, Roll: roll, Throttle: 1, Reheat: 1})
			if s.Position.Y < alt-250 {
				return -1e9, 0 // a "sustained level turn" that has dived 250 m is not one: unsustainable — without this the spiral kept diving to the sea, where ground effect (and then flying UNDER the water) read as sustained
			}
			v := s.Velocity.Length()
			if tick == 240*6 {
				e0 = s.Position.Y + v*v/19.62 // the heavy high-g turn takes ~5 s to wind up: an earlier window catches the ENTRY transient and inflates the bisected g
			}
			if tick >= 240*6 {
				sum += s.Fcs.Normal
				count++
			}
			if tick == 240*10-1 {
				e1 = s.Position.Y + v*v/19.62
			}
		}
		return (e1 - e0) / 4, sum / float64(count)
	}
	low, high := 1.2, 7.6
	held := 0.0
	for i := 0; i < 16; i++ { // 0.0001 g: fine enough that a 1 kt speed step still moves the bisected rate monotonically, not in 0.05 g quantization noise
		mid := (low + high) / 2
		if ps, achieved := measure(mid); ps > 0 && achieved > mid-0.5 {
			low = mid
			held = achieved
		} else {
			high = mid
		}
	}
	lateral := 9.81 * math.Sqrt(math.Max(held*held-1, 0))
	if lateral < 0.1 {
		return 0, math.Inf(1)
	}
	return lateral / speed * 180 / math.Pi, speed * speed / lateral
}

// vsCorner finds CORNER SPEED — the slowest speed at which a snap full-stick
// pull delivers as much g as it delivers at any speed. This is the fly-by-wire
// jet's answer to Va: with envelope protection the classical maneuvering
// speed's structural meaning is moot (abrupt full deflection is safe at any
// speed — the point of the carefree FCS). The 2.5 s snap window is kept from
// the old law's plateau doctrine, but since the kinematic-feedforward fix the
// law pegs the limiter within it (~7.5 by 2.5 s at speed), so the window now
// simply reads the honest limiter; the plateau prints alongside the speed.
func vsCorner(fuel, alt, stall float64) (float64, float64) {
	snap := func(v float64) float64 {
		m := vsJet(fuel, alt, v, false)
		m.State.Engine[0] = EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = EngineState{Spool: 1, Reheat: 1}
		peak := 0.0
		for i := 0; i < 240*5/2; i++ {
			m.Step(Inputs{Pitch: 1, Throttle: 1, Reheat: 1})
			s := &m.State
			if s.Fcs.Normal > peak && s.Velocity.Length() > 0.9*v {
				peak = s.Fcs.Normal
			}
		}
		return peak
	}
	plateau := snap(2.4 * stall)
	target := 0.97 * plateau
	if snap(stall*1.02) >= target {
		return stall * 1.02, plateau
	}
	lo, hi := 1.02*stall, 2.4*stall
	for i := 0; i < 12; i++ { // ~0.05 kt over the ~110 m/s span
		mid := (lo + hi) / 2
		if snap(mid) >= target {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, plateau
}

// vsBestRate sweeps speeds for the maximum sustained turn rate.
func vsBestRate(fuel, alt float64) (float64, float64) {
	lo, hi := 140.0, 260.0
	if alt > 3000 {
		lo, hi = 160, 300
	}
	if alt > 7000 {
		lo, hi = 190, 330
	}
	cache := map[float64]float64{}
	sample := func(v float64) float64 {
		if w, ok := cache[v]; ok {
			return w
		}
		w, _ := vsSustained(fuel, alt, v)
		cache[v] = w
		return w
	}
	bestV, bestW := 0.0, -1.0
	for v := lo; v <= hi; v += 20 {
		if w := sample(v); w > bestW {
			bestV, bestW = v, w
		}
	}
	// Multi-stage ring refinement to ~0.5 kt (see vsClimbSweep).
	for step := 10.0; step >= 0.2; step /= 2 {
		for moved := true; moved; {
			moved = false
			for _, dv := range []float64{-step, step} {
				if w := sample(bestV + dv); w > bestW {
					bestV, bestW = bestV+dv, w
					moved = true
				}
			}
		}
	}
	return bestV, bestW
}

// vsRadius sweeps the lower speed band for the TIGHTEST sustained turn — the
// minimum Ps=0 radius. It sits below the best-rate speed: giving up a little
// rate buys a disproportionately smaller circle until the thrust boundary
// collapses the sustainable g. Returns speed, radius (m), and rate (deg/s).
func vsRadius(fuel, alt, stall, ceiling float64) (float64, float64, float64) {
	cache := map[float64][2]float64{}
	sample := func(v float64) (float64, float64) {
		if r, ok := cache[v]; ok {
			return r[0], r[1]
		}
		w, radius := vsSustained(fuel, alt, v)
		cache[v] = [2]float64{w, radius}
		return w, radius
	}
	floor := 1.05 * stall
	bestV, bestW, bestR := 0.0, 0.0, math.Inf(1)
	for v := 1.1 * stall; v <= ceiling; v += 20 {
		if w, r := sample(v); r < bestR {
			bestV, bestW, bestR = v, w, r
		}
	}
	for step := 10.0; step >= 0.2; step /= 2 {
		for moved := true; moved; {
			moved = false
			for _, dv := range []float64{-step, step} {
				if bestV+dv < floor {
					continue
				}
				if w, r := sample(bestV + dv); r < bestR {
					bestV, bestW, bestR = bestV+dv, w, r
					moved = true
				}
			}
		}
	}
	return bestV, bestR, bestW
}

func TestVSpeeds(t *testing.T) {
	if os.Getenv("VSPEEDS") == "" {
		t.Skip("several minutes of simulation: run tools/vspeeds.sh (or set VSPEEDS=1)")
	}
	weights := []struct {
		label string
		fuel  float64
	}{
		{"LIGHT", lightFuel},
		{"HEAVY", heavyFuel},
	}
	fmt.Println("F/A-18C V-speed survey — standard atmosphere, calm air")
	fmt.Println("climbs at MIL; sustained turns and Vmc at full afterburner; flaps schedule automatically")
	for _, w := range weights {
		weight := (10700 + w.fuel) * 9.81
		fmt.Printf("\n===== %s: %.0f kg (%.0f kg fuel) — static T/W: %.2f MIL, %.2f AB =====\n", w.label, 10700+w.fuel, w.fuel, 2*48900/weight, 2*78700/weight)
		if 2*78700 > weight {
			fmt.Println("(T/W exceeds 1 in reheat: the jet climbs vertically, accelerating — a best CLIMB ANGLE only exists at MIL, where Vx below is measured; in AB the concept is degenerate)")
		}
		vr, vlof := vsRotate(w.fuel)
		fmt.Printf("Vr  minimum rotation (SL, MIL): %s   (liftoff %s)\n", vsBoth(vr, 0), vsBoth(vlof, 0))
		for _, at := range vspeedAlts {
			fmt.Printf("--- %s ---\n", at.label)
			vs1 := vsStall(w.fuel, at.m, false)
			fmt.Printf("Vs1  clean stall:            %s\n", vsBoth(vs1, at.m))
			vs0 := vsStall(w.fuel, at.m, true)
			note := ""
			if vs0 > 125 {
				note = "  [PA law disengaged: landing-config speeds here exceed its 130 m/s TAS boundary — no droop, effectively clean]"
			}
			fmt.Printf("Vs0  landing config stall:   %s%s\n", vsBoth(vs0, at.m), note)
			vapp := vsApproach(w.fuel, at.m)
			fmt.Printf("Vapp on-speed approach:      %s  (%.2f Vs0)%s\n", vsBoth(vapp, at.m), vapp/vs0, note)
			vx, gx, vy, ry := vsClimbSweep(w.fuel, at.m, vs1, 2, false)
			fmt.Printf("Vx   best angle (MIL):       %s  (+%.1f deg)\n", vsBoth(vx, at.m), gx)
			fmt.Printf("Vy   best rate  (MIL):       %s  (%+.0f fpm)\n", vsBoth(vy, at.m), ry*196.85)
			sigma := air(at.m, Environment{}).Density / air(0, Environment{}).Density
			if 2*78700*sigma >= weight {
				fmt.Printf("Vx   best angle (AB):        vertical — T/W %.2f here: any speed above stall sustains 90 deg\n", 2*78700*sigma/weight)
			} else {
				vxb, gxb, _, _ := vsClimbSweep(w.fuel, at.m, vs1, 2, true)
				fmt.Printf("Vx   best angle (AB):        %s  (+%.1f deg)\n", vsBoth(vxb, at.m), gxb)
			}
			_, _, vyb, ryb := vsClimbSweep(w.fuel, at.m, vs1, 2, true)
			fmt.Printf("Vy   best rate  (AB):        %s  (%+.0f fpm)\n", vsBoth(vyb, at.m), ryb*196.85)
			vxse, gxse, vyse, ryse := vsClimbSweep(w.fuel, at.m, vs1, 1, false)
			fmt.Printf("Vxse single-engine (MIL):    %s  (%+.1f deg)\n", vsBoth(vxse, at.m), gxse)
			fmt.Printf("Vyse single-engine (MIL):    %s  (%+.0f fpm)\n", vsBoth(vyse, at.m), ryse*196.85)
			vxsb, gxsb, vysb, rysb := vsClimbSweep(w.fuel, at.m, vs1, 1, true)
			fmt.Printf("Vxse single-engine (AB):     %s  (%+.1f deg)\n", vsBoth(vxsb, at.m), gxsb)
			fmt.Printf("Vyse single-engine (AB):     %s  (%+.0f fpm)\n", vsBoth(vysb, at.m), rysb*196.85)
			glide, slope := vsGlide(w.fuel, at.m, vs1)
			fmt.Printf("best glide (engines out):    %s  (%.1f deg, L/D %.1f)\n", vsBoth(glide, at.m), slope*180/math.Pi, 1/math.Tan(-slope))
			vh := vsVh(w.fuel, at.m)
			fmt.Printf("Vh   maximum level (AB):     %s  (M %.2f)\n", vsBoth(vh, at.m), vh/air(at.m, Environment{}).Sound)
			if vmc := vsVmc(w.fuel, at.m, vs1); vmc > 0 {
				fmt.Printf("Vmc  minimum control (AB):   %s\n", vsBoth(vmc, at.m))
			} else {
				fmt.Printf("Vmc  minimum control (AB):   below stall - not limiting (near-centreline engines)\n")
			}
			corner, plateau := vsCorner(w.fuel, at.m, vs1)
			fmt.Printf("corner speed (snap-pull):    %s  (%.1f g in 2.5 s — the Va-equivalent; envelope protection makes classical Va moot)\n", vsBoth(corner, at.m), plateau)
			bv, bw := vsBestRate(w.fuel, at.m)
			fmt.Printf("best sustained turn:         %s  (%.1f deg/s, AB)\n", vsBoth(bv, at.m), bw)
			tv, tr, tw := vsRadius(w.fuel, at.m, vs1, bv)
			fmt.Printf("tightest sustained turn:     %s  (radius %.0f m, %.1f deg/s, AB)\n", vsBoth(tv, at.m), tr, tw)
		}
	}
}

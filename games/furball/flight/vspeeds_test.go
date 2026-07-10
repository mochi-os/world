// V-speed survey (#89): every speed is MEASURED by flying the model — decel
// stalls, climb sweeps, engine-out trims — not computed from formulas, so the
// numbers track whatever the flight model actually does.
//
// Deliberately NOT part of the normal suite (several minutes of simulation):
// run it with tools/vspeeds.sh, or
//   VSPEEDS=1 go test ./games/furball/flight -run TestVSpeeds -v -timeout 30m
//
// Conventions: standard atmosphere, calm air. Climbs at MIL (dry) power, the
// charted convention; sustained turns at full afterburner (fighter EM
// convention); Vmc at full afterburner on the live engine (worst case).
// Speeds print as KEAS (what a pilot's gauge approximates) and KTAS.

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
func vsEAS(tas, alt float64) float64 {
	return tas * math.Sqrt(air(alt, Environment{}).Density/air(0, Environment{}).Density)
}
func vsBoth(tas, alt float64) string {
	return fmt.Sprintf("%3.0f KEAS/%3.0f KTAS", vsKnots(vsEAS(tas, alt)), vsKnots(tas))
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

// vsClimbPoint measures the climb capability at one speed as the SPECIFIC
// ENERGY rate (Ps) over a windowed MIL run — the EM-harness method: the stick
// holds the target speed only loosely, and any drift is energy-accounted, so
// the number is the true steady climb rate without waiting for a long
// geometric settle (the light jet's dry thrust-to-weight is ~0.9 and its
// steady climb angles are extreme). se kills the second engine.
func vsClimbPoint(fuel, alt, target float64, se bool) float64 {
	m := vsJet(fuel, alt, target, false)
	rho := air(alt, Environment{}).Density
	trim := clamp(2*(10700+fuel)*9.81/(rho*target*target*37.16)/4.7+0.01, 0, 0.3)
	m.State.Attitude = Axis(Vec3{Z: 1}, trim)
	m.State.Engine[0] = EngineState{Spool: 1}
	m.State.Engine[1] = EngineState{Spool: 1}
	if se {
		m.State.Damage.Engine[1] = 1
	}
	stick := 0.1
	var e0, e1 float64
	const settle, window = 240 * 4, 240 * 10
	for i := 0; i < settle+window; i++ {
		s := &m.State
		v := s.Velocity.Length()
		stick = clamp(stick+clamp((v-target)*0.001, -0.004, 0.004), -0.4, 1)
		up := s.Attitude.Rotate(Vec3{Y: 1})
		right := s.Attitude.Rotate(Vec3{Z: 1})
		bank := math.Atan2(right.Y, up.Y)
		roll := clamp(bank*2.5, -1, 1)
		pedal := clamp(-s.Omega.Y*6, -1, 1) // yaw damping (matters single-engine)
		m.Step(Inputs{Throttle: 1, Pitch: stick, Roll: roll, Yaw: pedal})
		if i == settle {
			e0 = s.Position.Y + v*v/19.62
		}
		if i == settle+window-1 {
			e1 = s.Position.Y + v*v/19.62
			if math.Abs(v-target) > 25 {
				return math.NaN() // the hold lost the point entirely
			}
		}
	}
	return (e1 - e0) / (float64(window) / 240)
}

// vsClimbSweep finds Vy (best rate) and Vx (best gradient) over a speed grid,
// refining around each maximum.
func vsClimbSweep(fuel, alt, stall float64, se bool) (vx, gx, vy, ry float64) {
	type point struct{ v, rate, grad float64 }
	measure := func(v float64) point {
		r := vsClimbPoint(fuel, alt, v, se)
		if math.IsNaN(r) {
			return point{v, math.Inf(-1), math.Inf(-1)}
		}
		return point{v, r, r / v}
	}
	var pts []point
	lo := math.Max(1.15*stall, 80)
	for v := lo; v <= lo+150; v += 15 {
		pts = append(pts, measure(v))
	}
	best := func(key func(point) float64) point {
		b := pts[0]
		for _, p := range pts {
			if key(p) > key(b) {
				b = p
			}
		}
		for _, dv := range []float64{-7.5, 7.5} { // one refinement ring
			p := measure(b.v + dv)
			if key(p) > key(b) {
				b = p
			}
		}
		return b
	}
	r := best(func(p point) float64 { return p.rate })
	g := best(func(p point) float64 { return p.grad })
	return g.v, math.Asin(clamp(g.rate/g.v, -1, 1)) * 180 / math.Pi, r.v, r.rate
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

// vsApproach lets the PA law fly stick-free (it holds on-speed alpha) down a
// ~3 degree glideslope with throttle trimmed to the descent — the real
// approach geometry; holding LEVEL gear-down flight at altitude can exceed
// available thrust and drove the settled speed below the stall, a nonsense
// reading. Returns the settled TAS: Vapp.
func vsApproach(fuel, alt float64) float64 {
	m := vsJet(fuel, alt, 75, true)
	throttle := 0.6
	sum, n := 0.0, 0
	for i := 0; i < 240*40; i++ {
		s := &m.State
		v := s.Velocity.Length()
		sink := -0.052 * v // 3 degrees
		throttle = clamp(throttle+clamp((sink-s.Velocity.Y)*0.0002, -0.003, 0.003), 0, 1) // sinking below the glideslope (vy < sink) -> positive error -> more thrust
		m.Step(Inputs{Gear: true, Throttle: throttle})
		if i >= 240*32 {
			sum += v
			n++
		}
	}
	return sum / float64(n)
}

// vsSustained bisects the Ps=0 load factor at one speed (the envelope-map
// method) and returns the sustained turn rate there, deg/s. Full afterburner.
func vsSustained(fuel, alt, speed float64) float64 {
	measure := func(n float64) float64 {
		m := vsJet(fuel, alt, speed, false)
		m.State.Engine[0] = EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = EngineState{Spool: 1, Reheat: 1}
		stick := clamp((n-1)/6.5, 0.1, 1)
		target := -math.Acos(clamp(1/n, 0, 1))
		var e0, e1 float64
		for tick := 0; tick < 240*7; tick++ {
			s := &m.State
			up := s.Attitude.Rotate(Vec3{Y: 1})
			right := s.Attitude.Rotate(Vec3{Z: 1})
			bank := math.Atan2(right.Y, up.Y)
			roll := clamp((bank-target)*2.5, -1, 1)
			stick = clamp(stick+clamp((n-s.Fcs.Normal)*0.01, -0.01, 0.01), 0.05, 1)
			m.Step(Inputs{Pitch: stick, Roll: roll, Throttle: 1, Reheat: 1})
			v := s.Velocity.Length()
			if tick == 240*4 {
				e0 = s.Position.Y + v*v/19.62
			}
			if tick == 240*7-1 {
				e1 = s.Position.Y + v*v/19.62
			}
		}
		return (e1 - e0) / 3
	}
	low, high := 1.2, 7.6
	for i := 0; i < 7; i++ {
		mid := (low + high) / 2
		if measure(mid) > 0 {
			low = mid
		} else {
			high = mid
		}
	}
	n := (low + high) / 2
	return 9.81 * math.Sqrt(math.Max(n*n-1, 0)) / speed * 180 / math.Pi
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
	bestV, bestW := 0.0, -1.0
	for v := lo; v <= hi; v += 20 {
		if w := vsSustained(fuel, alt, v); w > bestW {
			bestV, bestW = v, w
		}
	}
	for _, dv := range []float64{-10, 10} {
		if w := vsSustained(fuel, alt, bestV+dv); w > bestW {
			bestV, bestW = bestV+dv, w
		}
	}
	return bestV, bestW
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
		fmt.Printf("\n===== %s: %.0f kg (%.0f kg fuel) =====\n", w.label, 10700+w.fuel, w.fuel)
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
			vx, gx, vy, ry := vsClimbSweep(w.fuel, at.m, vs1, false)
			fmt.Printf("Vx   best angle (MIL):       %s  (+%.1f deg)\n", vsBoth(vx, at.m), gx)
			fmt.Printf("Vy   best rate  (MIL):       %s  (%+.0f fpm)\n", vsBoth(vy, at.m), ry*196.85)
			vxse, gxse, vyse, ryse := vsClimbSweep(w.fuel, at.m, vs1, true)
			fmt.Printf("Vxse single-engine angle:    %s  (%+.1f deg)\n", vsBoth(vxse, at.m), gxse)
			fmt.Printf("Vyse single-engine rate:     %s  (%+.0f fpm)\n", vsBoth(vyse, at.m), ryse*196.85)
			if vmc := vsVmc(w.fuel, at.m, vs1); vmc > 0 {
				fmt.Printf("Vmc  minimum control (AB):   %s\n", vsBoth(vmc, at.m))
			} else {
				fmt.Printf("Vmc  minimum control (AB):   below stall - not limiting (near-centreline engines)\n")
			}
			bv, bw := vsBestRate(w.fuel, at.m)
			fmt.Printf("best sustained turn:         %s  (%.1f deg/s, AB)\n", vsBoth(bv, at.m), bw)
		}
	}
}

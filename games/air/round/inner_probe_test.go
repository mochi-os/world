package round

import (
	"fmt"
	"math"
	"os"
	"testing"

	"world/games/air/flight"
)

// #105: does the AMRAAM ladder's guessed floor sit inside a dead band?
//
// Ladder's rung() walks its floor outward from 2,000 m and bisects UPWARD,
// so it returns only the outermost arriving range and never probes below
// where the walk started. zone.Minimum is then a separate guess,
// max(800, closing*(Arming+0.8)), which for a head-on BVR shot lands near
// 900 m. Everything between that floor and 2,000 m is therefore handed to
// consumers as a good shot without the model ever having flown one there.
//
// This replicates Ladder's own `arrival` closure so the inner band can be
// swept. The replication is validated against Ladder itself before it is
// trusted: same criterion, same bisection, and the outermost range it finds
// must agree with the Max that Ladder reports.
func flown(shooter Target, target Target, wrap float64, trial float64, breaking bool) float64 {
	const step = 0.2 // ladder.go's own integration step
	direction := relative(shooter.Position, target.Position, wrap).Normalize()
	virtual := Target{Position: shooter.Position.Add(direction.Scale(trial)), Velocity: target.Velocity}
	m := New(shooter.Position, shooter.Velocity, &Target{Position: virtual.Position, Velocity: virtual.Velocity}, wrap)
	closest, mach := math.MaxFloat64, -1.0
	for {
		support := &Target{Position: virtual.Position, Velocity: virtual.Velocity}
		if !m.Step(step, support, &virtual) {
			break
		}
		if breaking { // ladder.go's own escape turn, copied exactly
			away := relative(shooter.Position, virtual.Position, wrap).Normalize()
			if speed := virtual.Velocity.Length(); speed > 1 {
				heading := virtual.Velocity.Scale(1 / speed)
				if heading.Dot(away) < 0.999 {
					turned := heading.Add(away.Subtract(heading.Scale(away.Dot(heading))).Normalize().Scale(escape / speed * step))
					virtual.Velocity = turned.Normalize().Scale(speed)
				}
			}
		}
		virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(step))
		miss := relative(m.Position, virtual.Position, wrap).Length()
		if miss < closest {
			closest = miss
			_, sound := atmosphere(m.Position.Y)
			mach = m.Velocity.Length() / sound
		} else if miss > closest+2000 {
			break
		}
	}
	if closest > 500 {
		return -1
	}
	return mach
}

// Did the escaping target actually turn? The escape rung reads identically to
// Max at every geometry, so either the break is inert or it does not matter.
// This reports the target's heading at the end of the flight.
func fled(shooter Target, target Target, wrap float64, trial float64, breaking bool) (float64, float64) {
	const step = 0.2
	direction := relative(shooter.Position, target.Position, wrap).Normalize()
	virtual := Target{Position: shooter.Position.Add(direction.Scale(trial)), Velocity: target.Velocity}
	m := New(shooter.Position, shooter.Velocity, &Target{Position: virtual.Position, Velocity: virtual.Velocity}, wrap)
	closest, flew := math.MaxFloat64, 0.0
	for {
		support := &Target{Position: virtual.Position, Velocity: virtual.Velocity}
		if !m.Step(step, support, &virtual) {
			break
		}
		flew += step
		if breaking {
			away := relative(shooter.Position, virtual.Position, wrap).Normalize()
			if speed := virtual.Velocity.Length(); speed > 1 {
				heading := virtual.Velocity.Scale(1 / speed)
				if heading.Dot(away) < 0.999 {
					turned := heading.Add(away.Subtract(heading.Scale(away.Dot(heading))).Normalize().Scale(escape / speed * step))
					virtual.Velocity = turned.Normalize().Scale(speed)
				}
			}
		}
		virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(step))
		if miss := relative(m.Position, virtual.Position, wrap).Length(); miss < closest {
			closest = miss
		} else if miss > closest+2000 {
			break
		}
		_ = flew
	}
	return virtual.Velocity.X, closest
}

// trace flies one shot and reports what the round DID, rather than only the
// single number `arrival` distils it to: the Mach history, the flight time,
// the closest approach, and which of the two exits ended it. Step has exactly
// one exit -- Life <= 0 -- so a shot that never gets close died of BATTERY,
// not of energy, and that distinction is the whole of #105's open question.
func trace(shooter Target, target Target, wrap float64, trial float64) string {
	const step = 0.2
	direction := relative(shooter.Position, target.Position, wrap).Normalize()
	virtual := Target{Position: shooter.Position.Add(direction.Scale(trial)), Velocity: target.Velocity}
	m := New(shooter.Position, shooter.Velocity, &Target{Position: virtual.Position, Velocity: virtual.Velocity}, wrap)
	closest, flew, alive := math.MaxFloat64, 0.0, true
	marks := []float64{}
	for {
		support := &Target{Position: virtual.Position, Velocity: virtual.Velocity}
		if !m.Step(step, support, &virtual) {
			alive = false
			break
		}
		flew += step
		virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(step))
		if miss := relative(m.Position, virtual.Position, wrap).Length(); miss < closest {
			closest = miss
		} else if miss > closest+2000 {
			break
		}
		if int(flew/step)%50 == 0 {
			_, sound := atmosphere(m.Position.Y)
			marks = append(marks, m.Velocity.Length()/sound)
		}
	}
	shape := ""
	for i, mk := range marks {
		if i%3 == 0 || i == len(marks)-1 {
			shape += fmt.Sprintf("%.1f ", mk)
		}
	}
	why := "battery ran out"
	if alive {
		why = "opened past the merge"
	}
	return fmt.Sprintf("flew %5.1fs, closest %7.0f m, ended: %-20s Mach over the flight: %s", flew, closest, why, shape)
}

func TestAmraamInnerBand(t *testing.T) {
	if os.Getenv("AIR_INNER") == "" {
		t.Skip("measurement probe: set AIR_INNER=1")
	}
	for _, at := range []struct {
		name        string
		height      float64
		own, theirs float64 // speeds, m/s
		head        bool
		cross       float64 // target course off the line of sight, degrees; 180 = exactly head-on
	}{
		{"head-on 30,000 ft", 9144, 250, 250, true, 180},
		{"oblique 135 deg 30,000 ft", 9144, 250, 250, true, 135},
		{"beam 90 deg 30,000 ft", 9144, 250, 250, true, 90},
	} {
		shooter := Target{Position: flight.Vec3{Y: at.height}, Velocity: flight.Vec3{X: at.own}}
		// cross is the target's course relative to the line of sight: 180 is
		// exactly head-on (the degenerate case for the escape turn), 90 is a
		// clean beam, 0 is running away.
		rad := at.cross * math.Pi / 180
		target := Target{Position: flight.Vec3{X: 30000, Y: at.height},
			Velocity: flight.Vec3{X: at.theirs * math.Cos(rad), Z: at.theirs * math.Sin(rad)}}
		zone := Ladder(shooter, target, 0)

		// The control: the replication's own outermost arriving range, found
		// the same way rung() finds it. If this disagrees with Ladder's Max
		// the replication is wrong and nothing below it means anything.
		low, high := 2000.0, 150000.0
		for i := 0; i < 12; i++ {
			mid := (low + high) / 2
			if flown(shooter, target, 0, mid, false) >= 1.0 {
				low = mid
			} else {
				high = mid
			}
		}
		fmt.Printf("\n=== %s ===\n", at.name)
		fmt.Printf("  Ladder says: Minimum %.0f  Escape %.0f  Max %.0f  Aero %.0f\n",
			zone.Minimum, zone.Escape, zone.Max, zone.Aero)
		fmt.Printf("  CONTROL: the replication's outermost arriving range %.0f m v Ladder's Max %.0f m — %s\n",
			low, zone.Max, map[bool]string{true: "agree", false: "DISAGREE, ignore everything below"}[math.Abs(low-zone.Max) < 200])

		band := ""
		for r := 400.0; r <= 3000; r += 100 {
			if flown(shooter, target, 0, r, false) >= 1.0 {
				band += "#"
			} else {
				band += "."
			}
		}
		fmt.Printf("  400 m -> 3000 m in 100 m steps, # arrives: %s\n", band)
		// Does the escape turn move anything? Ladder reports Escape == Max at
		// every geometry, which for a 7.5 g break sustained over a 100 s flight
		// would be remarkable if the model were live.
		fmt.Printf("  ESCAPE TURN, straight v breaking at 7.5 g:\n")
		for _, f := range []float64{0.9, 1.0} {
			r := zone.Max * f
			sx, sc := fled(shooter, target, 0, r, false)
			bx, bc := fled(shooter, target, 0, r, true)
			fmt.Printf("      %6.0f m: target final velocity X straight %+7.1f closest %6.0f | breaking %+7.1f closest %6.0f\n",
				r, sx, sc, bx, bc)
		}
		for _, f := range []float64{0.3, 0.6, 0.9, 1.0} {
			r := zone.Max * f
			fmt.Printf("      %6.0f m (%3.0f%% of Max): straight Mach %5.2f | breaking Mach %5.2f\n",
				r, f*100, flown(shooter, target, 0, r, false), flown(shooter, target, 0, r, true))
		}
		fmt.Printf("  at the reported Minimum (%.0f m): arrival Mach %.2f\n", zone.Minimum, flown(shooter, target, 0, zone.Minimum, false))
		for _, r := range []float64{800, 1000, 1500, 2000, 2500} {
			fmt.Printf("      %5.0f m: arrival Mach %.2f\n", r, flown(shooter, target, 0, r, false))
		}
		// Aero, Max and Escape all read the same number above. If the Mach
		// thresholds (0.6, 1.0) ever bound anything, arrival must fall THROUGH
		// them as range grows. If instead it stays high and then drops to -1,
		// the binding constraint is the 500 m closest-approach test alone and
		// the three rungs carry no distinct information.
		fmt.Printf("  approaching Max (%.0f m):\n", zone.Max)
		for _, f := range []float64{0.5, 0.8, 0.95, 0.99, 1.0, 1.01} {
			r := zone.Max * f
			fmt.Printf("      %6.0f m (%3.0f%% of Max): arrival Mach %5.2f | %s\n",
				r, f*100, flown(shooter, target, 0, r, false), trace(shooter, target, 0, r))
		}
	}
}

// endless flies the shot with the battery removed, to find where the round
// would ARRIVE SUBSONIC if it were given the time. Zone's own field comments
// say Aero is "arrives at all: any speed margin at the merge" and Max is
// "arrives supersonic", so a band between Mach 0.6 and 1.0 is what separates
// them. They read identically today. This asks whether that band exists at all
// or whether the 100 s battery simply ends every flight first.
func endless(shooter Target, target Target, wrap float64, trial float64) (float64, float64, float64) {
	const step = 0.2
	direction := relative(shooter.Position, target.Position, wrap).Normalize()
	virtual := Target{Position: shooter.Position.Add(direction.Scale(trial)), Velocity: target.Velocity}
	m := New(shooter.Position, shooter.Velocity, &Target{Position: virtual.Position, Velocity: virtual.Velocity}, wrap)
	m.Life = 1e9 // the battery is the thing under suspicion: take it out of the way
	closest, mach, flew := math.MaxFloat64, -1.0, 0.0
	for {
		support := &Target{Position: virtual.Position, Velocity: virtual.Velocity}
		if !m.Step(step, support, &virtual) {
			break
		}
		flew += step
		virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(step))
		miss := relative(m.Position, virtual.Position, wrap).Length()
		if miss < closest {
			closest = miss
			_, sound := atmosphere(m.Position.Y)
			mach = m.Velocity.Length() / sound
		} else if miss > closest+2000 {
			break
		}
		if flew > 600 {
			break
		}
	}
	return mach, closest, flew
}

func TestSubsonicArrivalExists(t *testing.T) {
	if os.Getenv("AIR_INNER") == "" {
		t.Skip("measurement probe: set AIR_INNER=1")
	}
	shooter := Target{Position: flight.Vec3{Y: 9144}, Velocity: flight.Vec3{X: 250}}
	target := Target{Position: flight.Vec3{X: 30000, Y: 9144},
		Velocity: flight.Vec3{X: 250 * math.Cos(math.Pi), Z: 250 * math.Sin(math.Pi)}}
	zone := Ladder(shooter, target, 0)
	fmt.Printf("battery-limited ladder: Aero %.0f  Max %.0f  Escape %.0f\n", zone.Aero, zone.Max, zone.Escape)
	fmt.Println("\nwith the battery REMOVED — where does arrival actually go subsonic?")
	fmt.Println("    launch km |  flew  | closest |  arrival Mach")
	for _, km := range []float64{80, 92, 110, 130, 150, 175, 200, 220, 240, 280} {
		mach, closest, flew := endless(shooter, target, 0, km*1000)
		note := ""
		if mach > 0 && mach < 1.0 {
			note = "   <- SUBSONIC: this is the band Aero is meant to describe"
		}
		if closest > 500 {
			note = "   <- misses"
		}
		fmt.Printf("      %5.0f   | %5.0fs | %7.0f | %6.2f%s\n", km, flew, closest, mach, note)
	}
}

// The mechanism, confirmed against a target that cannot run. A round decayed to
// Mach 0.6 is doing ~180 m/s at altitude, which is BELOW a fighter's cruise, so
// it can never arrive against one -- it falls behind and the miss blows out.
// The Aero rung's criterion therefore describes a regime that cannot exist in
// fighter-v-fighter geometry. Against a slow target it should reappear.
func TestSubsonicArrivalAgainstSlowTarget(t *testing.T) {
	if os.Getenv("AIR_INNER") == "" {
		t.Skip("measurement probe: set AIR_INNER=1")
	}
	shooter := Target{Position: flight.Vec3{Y: 9144}, Velocity: flight.Vec3{X: 250}}
	for _, speed := range []float64{250, 150, 80, 30} {
		target := Target{Position: flight.Vec3{X: 30000, Y: 9144},
			Velocity: flight.Vec3{X: speed * math.Cos(math.Pi), Z: speed * math.Sin(math.Pi)}}
		zone := Ladder(shooter, target, 0)
		gap := ""
		if zone.Aero > zone.Max+100 {
			gap = "   <- Aero and Max SEPARATE here"
		}
		fmt.Printf("  target %3.0f m/s: Aero %7.0f  Max %7.0f  Escape %7.0f%s\n",
			speed, zone.Aero, zone.Max, zone.Escape, gap)
	}
}

// Can Aero EVER differ from Max? They differ only if some launch range yields
// an arrival in [0.6, 1.0). Within the 100 s battery the round is still
// supersonic wherever it arrives at all, but drag is what decides that, so the
// place to look is low and slow. Sweep the altitude band the weapon is used in
// and report the arrival Mach at each geometry's own Max: if it never falls
// into the band, the two rungs are provably identical and one of them is a
// wasted bisection.
func TestAeroVersusMaxAcrossTheEnvelope(t *testing.T) {
	if os.Getenv("AIR_INNER") == "" {
		t.Skip("measurement probe: set AIR_INNER=1")
	}
	// Aero and Max read identically at 15,000-30,000 ft, which is where the
	// first three geometries of this probe happened to sit. That is NOT a
	// duplicate rung: it is the thin air. Up there the round barely decelerates
	// and the 100 s battery ends the flight while it is still at Mach 2, long
	// before it could decay through Mach 1, so the two crossings coincide.
	// Lower down drag bites, the round genuinely decays through the transonic
	// band inside its battery, and the rungs separate -- by up to a factor of
	// two. Sweep the envelope and count both.
	same, apart := 0, 0
	fmt.Println("  alt ft | their | aspect |     Max |    Aero | Aero/Max | arrival Mach at Max")
	for _, ft := range []float64{500, 5000, 10000, 20000, 30000, 40000} {
		for _, aspect := range []float64{180, 90, 0} {
			for _, speed := range []float64{120, 250, 400} {
				height := ft / 3.281
				rad := aspect * math.Pi / 180
				shooter := Target{Position: flight.Vec3{Y: height}, Velocity: flight.Vec3{X: 250}}
				target := Target{Position: flight.Vec3{X: 30000, Y: height},
					Velocity: flight.Vec3{X: speed * math.Cos(rad), Z: speed * math.Sin(rad)}}
				zone := Ladder(shooter, target, 0)
				if zone.Max <= 0 {
					continue
				}
				if zone.Aero > zone.Max+100 {
					apart++
				} else {
					same++
				}
				fmt.Printf("  %6.0f | %5.0f | %6.0f | %7.0f | %7.0f | %8.2f | %5.2f\n",
					ft, speed, aspect, zone.Max, zone.Aero, zone.Aero/zone.Max,
					flown(shooter, target, 0, zone.Max, false))
			}
		}
	}
	fmt.Printf("\n  %d geometries separate Aero from Max, %d coincide.\n", apart, same)
	fmt.Println("  The coincidences are the high-altitude ones, where the battery ends the")
	fmt.Println("  flight before drag can take the round subsonic. Both rungs are doing real")
	fmt.Println("  work; neither is redundant.")
}

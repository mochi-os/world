// Mochi world: Headless flight-model validation tool
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Validation harness for the air flight core. Subcommands:
//
//	trim  -speed 200 -altitude 3000        bare-airframe glide trim
//	fly   -seconds 20 [doublets]           scripted flight, CSV on stdout
//	modes -speed 200 -altitude 3000        natural-mode identification
//	em    -altitude 0 -fuel 0.5            energy-manoeuvrability sweep
//	bench                                  step cost
//	dump                                   airframe literals as JSON
//
// Analysis runs in calm ISA air (no wind, no turbulence) so the numbers are
// reproducible; the simulated aircraft in the game flies the weathered field.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

const gravity = 9.80665
const knots = 1.9438445
const feet = 3.2808399

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: headless trim|fly|modes|em|bench|dump [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "trim":
		command_trim(os.Args[2:])
	case "fly":
		command_fly(os.Args[2:])
	case "modes":
		command_modes(os.Args[2:])
	case "em":
		command_em(os.Args[2:])
	case "bench":
		command_bench()
	case "dump":
		command_dump()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

// selected is the airframe under analysis (-aircraft flag, default fa18f).
var selected = ""

// airframe resolves the selection, failing loudly on unknown names.
func airframe() *flight.Airframe {
	a := aircraft.Get(selected)
	if a == nil {
		fmt.Fprintf(os.Stderr, "unknown aircraft %q (have %v)\n", selected, aircraft.Names())
		os.Exit(2)
	}
	return a
}

// model builds a calm-air analysis model at a fuel fraction.
func model(fraction float64) *flight.Model {
	m := flight.New(airframe(), flight.Environment{}, flight.World{})
	m.State.Fuel = fraction * airframe().Mass.Fuel
	return m
}

// weight is the analysis weight in newtons at a fuel fraction.
func weight(fraction float64) float64 {
	return (airframe().Mass.Empty + fraction*airframe().Mass.Fuel) * gravity
}

func command_trim(arguments []string) {
	flags := flag.NewFlagSet("trim", flag.ExitOnError)
	flags.StringVar(&selected, "aircraft", "", "airframe (default fa18f)")
	speed := flags.Float64("speed", 200, "true airspeed, m/s")
	altitude := flags.Float64("altitude", 3000, "altitude, m")
	fuel := flags.Float64("fuel", 0.5, "fuel fraction")
	flags.Parse(arguments)
	m := model(*fuel)
	theta, stabilator, path, ok := flight.Glide(m, *speed, *altitude)
	if !ok {
		fmt.Println("trim did not converge")
		os.Exit(1)
	}
	degrees := 180 / math.Pi
	fmt.Printf("glide trim at %.0f m/s, %.0f m (fuel %.0f%%)\n", *speed, *altitude, *fuel*100)
	fmt.Printf("  pitch      %+.2f°\n", theta*degrees)
	fmt.Printf("  alpha      %+.2f°\n", (theta-path)*degrees)
	fmt.Printf("  stabilator %+.2f°\n", stabilator*degrees)
	fmt.Printf("  path       %+.2f° (%.1f m/s down)\n", path*degrees, -*speed*math.Sin(path))
}

// doublet is a timed control application parsed from start:stop:value.
type doublet struct {
	start, stop, value float64
}

func window(specification string) doublet {
	parts := strings.Split(specification, ":")
	if len(parts) != 3 {
		fmt.Fprintf(os.Stderr, "bad doublet %q (want start:stop:value)\n", specification)
		os.Exit(2)
	}
	start, _ := strconv.ParseFloat(parts[0], 64)
	stop, _ := strconv.ParseFloat(parts[1], 64)
	value, _ := strconv.ParseFloat(parts[2], 64)
	return doublet{start, stop, value}
}

func (d doublet) at(t float64) float64 {
	if t >= d.start && t < d.stop {
		return d.value
	}
	return 0
}

func command_fly(arguments []string) {
	flags := flag.NewFlagSet("fly", flag.ExitOnError)
	flags.StringVar(&selected, "aircraft", "", "airframe (default fa18f)")
	seconds := flags.Float64("seconds", 20, "duration")
	speed := flags.Float64("speed", 200, "initial true airspeed, m/s")
	altitude := flags.Float64("altitude", 3000, "initial altitude, m")
	throttle := flags.Float64("throttle", 0.85, "throttle 0..1")
	reheat := flags.Float64("reheat", 0, "reheat zone fraction 0..1 (quantized to the five zones)")
	fuel := flags.Float64("fuel", 0.5, "fuel fraction")
	pitch := flags.String("pitch", "", "pitch doublet start:stop:value")
	roll := flags.String("roll", "", "roll doublet start:stop:value")
	yaw := flags.String("yaw", "", "yaw doublet start:stop:value")
	flags.Parse(arguments)
	m := model(*fuel)
	m.State.Position = flight.Vec3{Y: *altitude}
	m.State.Velocity = flight.Vec3{X: *speed}
	m.State.Engine[0] = flight.EngineState{Spool: *throttle}
	m.State.Engine[1] = flight.EngineState{Spool: *throttle}
	inputs := flight.Inputs{Throttle: *throttle, Reheat: *reheat}
	var doublets [3]doublet
	for i, specification := range []string{*pitch, *roll, *yaw} {
		if specification != "" {
			doublets[i] = window(specification)
		}
	}
	fmt.Println("time,x,y,z,speed,mach,alpha,beta,pitch,bank,heading,p,q,r,nz,fuel")
	steps := int(*seconds / flight.Dt)
	for step := 0; step < steps; step++ {
		t := float64(step) * flight.Dt
		inputs.Pitch = doublets[0].at(t)
		inputs.Roll = doublets[1].at(t)
		inputs.Yaw = doublets[2].at(t)
		m.Step(inputs)
		if (step+1)%12 == 0 {
			fmt.Println(row(m))
		}
	}
}

// row formats one CSV sample of derived flight quantities.
func row(m *flight.Model) string {
	s := &m.State
	degrees := 180 / math.Pi
	forward := s.Attitude.Rotate(flight.Vec3{X: 1})
	up := s.Attitude.Rotate(flight.Vec3{Y: 1})
	right := s.Attitude.Rotate(flight.Vec3{Z: 1})
	pitch := math.Asin(clamp(forward.Y, -1, 1))
	bank := math.Atan2(-right.Y, up.Y)
	heading := math.Atan2(-forward.Z, forward.X)
	return fmt.Sprintf("%.3f,%.1f,%.1f,%.1f,%.2f,%.3f,%.2f,%.2f,%.2f,%.2f,%.2f,%.3f,%.3f,%.3f,%.2f,%.1f",
		s.Time, s.Position.X, s.Position.Y, s.Position.Z,
		s.Velocity.Length(), m.Mach(), m.Alpha()*degrees, m.Beta()*degrees,
		pitch*degrees, bank*degrees, heading*degrees,
		s.Omega.X, s.Omega.Z, -s.Omega.Y, m.Nz(), s.Fuel)
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Min(math.Max(v, low), high)
}

// command_modes identifies the classical dynamic modes by perturb-and-fit.
func command_modes(arguments []string) {
	flags := flag.NewFlagSet("modes", flag.ExitOnError)
	flags.StringVar(&selected, "aircraft", "", "airframe (default fa18f)")
	speed := flags.Float64("speed", 200, "true airspeed, m/s")
	altitude := flags.Float64("altitude", 3000, "altitude, m")
	fuel := flags.Float64("fuel", 0.5, "fuel fraction")
	direct := flags.Bool("direct", false, "bare airframe (no augmentation)")
	flags.Parse(arguments)
	fmt.Printf("modes at %.0f m/s, %.0f m, %s\n", *speed, *altitude,
		map[bool]string{true: "bare airframe", false: "augmented"}[*direct])

	fit("short period", sample(*speed, *altitude, *fuel, *direct, 8, func(m *flight.Model) {
		m.State.Omega.Z += 0.10
	}, signal_alpha), 0.3, 0)
	fit("phugoid", sample(*speed, *altitude, *fuel, *direct, 240, func(m *flight.Model) {
		m.State.Velocity.X += 8
	}, signal_speed), 15, 10)
	fit("dutch roll", sample(*speed, *altitude, *fuel, *direct, 12, func(m *flight.Model) {
		m.State.Omega.Y += 0.10
	}, signal_beta), 0.4, 0)
	rollmode(*speed, *altitude, *fuel, *direct)
	spiral(*speed, *altitude, *fuel, *direct)
}

type reader func(m *flight.Model) float64

func signal_alpha(m *flight.Model) float64 {
	body := m.State.Attitude.Unrotate(m.State.Velocity)
	return math.Atan2(-body.Y, body.X)
}

func signal_speed(m *flight.Model) float64 { return m.State.Velocity.Length() }

func signal_beta(m *flight.Model) float64 {
	body := m.State.Attitude.Unrotate(m.State.Velocity)
	speed := body.Length()
	if speed < 1 {
		return 0
	}
	return math.Asin(clamp(body.Z/speed, -1, 1))
}

// settle establishes steady level flight: bare airframes get the glide trim
// (throttle idle, descending — honest, unpiloted); augmented flight holds 1g
// with thrust matched to drag.
func settle(speed float64, altitude float64, fuel float64, direct bool) (*flight.Model, flight.Inputs) {
	m := model(fuel)
	m.Direct = direct
	inputs := flight.Inputs{}
	if direct {
		theta, stabilator, path, ok := flight.Glide(m, speed, altitude)
		if !ok {
			fmt.Println("  (trim failed)")
		}
		m.State.Position = flight.Vec3{Y: altitude}
		m.State.Velocity = flight.Vec3{X: speed * math.Cos(path), Y: speed * math.Sin(path)}
		m.State.Attitude = flight.Axis(flight.Vec3{Z: 1}, theta)
		inputs.Pitch = -stabilator / 0.42 // Direct gearing in fcs.go
	} else {
		m.State.Position = flight.Vec3{Y: altitude}
		m.State.Velocity = flight.Vec3{X: speed}
		clean := model(fuel)
		a := angle(clean, speed, altitude, weight(fuel)/(pressure(speed, altitude)*airframe().Reference.Area))
		_, drag := clean.Evaluate(speed, a, altitude)
		dry, _ := clean.Thrust(speed, altitude)
		inputs.Throttle = clamp(drag*pressure(speed, altitude)*airframe().Reference.Area/math.Max(dry, 1), 0.1, 1)
		m.State.Engine[0] = flight.EngineState{Spool: inputs.Throttle}
		m.State.Engine[1] = flight.EngineState{Spool: inputs.Throttle}
		for step := 0; step < 240*10; step++ {
			m.Step(inputs)
		}
	}
	return m, inputs
}

// sample perturbs settled flight and records a signal at 240 Hz.
func sample(speed float64, altitude float64, fuel float64, direct bool, seconds float64, perturb func(*flight.Model), read reader) []float64 {
	m, inputs := settle(speed, altitude, fuel, direct)
	reference := read(m)
	perturb(m)
	values := make([]float64, 0, int(seconds*240))
	for step := 0; step < int(seconds*240); step++ {
		m.Step(inputs)
		values = append(values, read(m)-reference)
	}
	return values
}

// fit reports frequency and damping from successive same-sign peaks (at
// least gap seconds apart, ignoring the first skip seconds of transient and
// anything below a share of the largest excursion), or the divergence when
// the motion grows.
func fit(name string, values []float64, gap float64, skip float64) {
	first := int(skip * 240)
	scale := 0.0
	for _, v := range values[first:] {
		scale = math.Max(scale, math.Abs(v))
	}
	floor := scale / 20
	separation := int(gap * 240)
	peaks := []int{}
	for i := first + 1; i < len(values)-1; i++ {
		if values[i] > values[i-1] && values[i] >= values[i+1] && values[i] > floor {
			if len(peaks) == 0 || i-peaks[len(peaks)-1] >= separation {
				peaks = append(peaks, i)
			}
		}
	}
	if len(peaks) < 2 {
		final, initial := math.Abs(values[len(values)-1]), math.Abs(values[first])
		if final < math.Max(initial, 1e-9)/2 {
			fmt.Printf("  %-12s overdamped (no oscillation)\n", name)
		} else {
			fmt.Printf("  %-12s no clean peaks (final %.4g from %.4g)\n", name, final, initial)
		}
		return
	}
	period := float64(peaks[len(peaks)-1]-peaks[0]) / float64(len(peaks)-1) * flight.Dt
	head, tail := values[peaks[0]], values[peaks[len(peaks)-1]]
	decrement := math.Log(math.Abs(head/tail)) / float64(len(peaks)-1)
	damping := decrement / math.Sqrt(4*math.Pi*math.Pi+decrement*decrement)
	frequency := 2 * math.Pi / period / math.Sqrt(math.Max(1-damping*damping, 0.01))
	tag := ""
	if damping < 0 {
		tag = fmt.Sprintf("  DIVERGENT (doubling %.1f s)", period*math.Ln2/(-decrement))
	}
	fmt.Printf("  %-12s ω %.2f rad/s  T %.1f s  ζ %+.2f  (peaks %.3g → %.3g)%s\n", name, frequency, period, damping, head, tail, tag)
}

// rollmode measures the roll-subsidence time constant from a lateral step.
func rollmode(speed float64, altitude float64, fuel float64, direct bool) {
	m, inputs := settle(speed, altitude, fuel, direct)
	inputs.Roll = 0.5
	rates := []float64{}
	for step := 0; step < 240*2; step++ {
		m.Step(inputs)
		rates = append(rates, m.State.Omega.X)
	}
	steady := rates[len(rates)-1]
	if math.Abs(steady) < 0.05 {
		fmt.Printf("  %-12s no roll response\n", "roll")
		return
	}
	for i, rate := range rates {
		if rate/steady >= 0.632 {
			fmt.Printf("  %-12s τ %.2f s (steady %.0f°/s at half lateral stick)\n", "roll", float64(i+1)*flight.Dt, steady*180/math.Pi)
			return
		}
	}
	fmt.Printf("  %-12s did not reach steady state in 2 s\n", "roll")
}

// spiral banks the settled aircraft and watches the bank angle drift.
func spiral(speed float64, altitude float64, fuel float64, direct bool) {
	m, inputs := settle(speed, altitude, fuel, direct)
	m.State.Attitude = m.State.Attitude.Multiply(flight.Axis(flight.Vec3{X: 1}, 10*math.Pi/180)).Normalize()
	banks := []float64{}
	for step := 0; step < 240*20; step++ {
		m.Step(inputs)
		up := m.State.Attitude.Rotate(flight.Vec3{Y: 1})
		right := m.State.Attitude.Rotate(flight.Vec3{Z: 1})
		banks = append(banks, math.Atan2(-right.Y, up.Y))
	}
	initial, final := banks[240], banks[len(banks)-1]
	ratio := final / initial
	switch {
	case math.Abs(final) < 0.02:
		fmt.Printf("  %-12s convergent (level inside 20 s)\n", "spiral")
	case ratio > 1.05:
		fmt.Printf("  %-12s divergent (doubling %.0f s)\n", "spiral", 19*math.Ln2/math.Log(ratio))
	case ratio < 0.95:
		fmt.Printf("  %-12s convergent (halving %.0f s)\n", "spiral", 19*math.Ln2/(-math.Log(ratio)))
	default:
		fmt.Printf("  %-12s neutral over 20 s (%.1f° → %.1f°)\n", "spiral", initial*180/math.Pi, final*180/math.Pi)
	}
}

// pressure is dynamic pressure at a flight condition in ISA air.
func pressure(speed float64, altitude float64) float64 {
	local := flight.Atmosphere(altitude, flight.Environment{})
	return 0.5 * local.Density * speed * speed
}

// angle finds the body alpha giving a demanded lift coefficient by bisection
// over the attached range; returns the stall alpha when unreachable.
func angle(m *flight.Model, speed float64, altitude float64, demand float64) float64 {
	low, high := -0.1, 0.6
	best, ceiling := high, -10.0
	for sweep := low; sweep <= high; sweep += 0.01 {
		cl, _ := m.Evaluate(speed, sweep, altitude)
		if cl > ceiling {
			ceiling, best = cl, sweep
		}
		if cl >= demand {
			return sweep
		}
	}
	return best
}

// ceiling scans for the maximum lift coefficient and its alpha.
func ceiling(m *flight.Model, speed float64, altitude float64) (float64, float64) {
	best, at := -10.0, 0.0
	for sweep := 0.0; sweep <= 0.6; sweep += 0.01 {
		cl, _ := m.Evaluate(speed, sweep, altitude)
		if cl > best {
			best, at = cl, sweep
		}
	}
	return best, at
}

// command_em sweeps climb and turn performance at one altitude.
func command_em(arguments []string) {
	flags := flag.NewFlagSet("em", flag.ExitOnError)
	flags.StringVar(&selected, "aircraft", "", "airframe (default fa18f)")
	altitude := flags.Float64("altitude", 0, "altitude, m")
	fuel := flags.Float64("fuel", 0.5, "fuel fraction")
	table := flags.Bool("table", false, "print the full sweep table")
	flags.Parse(arguments)
	m := model(*fuel)
	w := weight(*fuel)
	area := airframe().Reference.Area
	local := flight.Atmosphere(*altitude, flight.Environment{})
	fmt.Printf("energy-manoeuvrability at %.0f m (%.0f ft), fuel %.0f%%, weight %.0f kg\n",
		*altitude, *altitude*feet, *fuel*100, w/gravity)
	if *table {
		fmt.Println("tas,mach,kcas,climb_dry,climb_wet,limit_g,sustained_g,rate,radius")
	}
	type point struct{ speed, value float64 }
	var dry, wet, rate point
	corner := 0.0
	for speed := 60.0; speed <= 460; speed += 2 {
		q := pressure(speed, *altitude)
		mach := speed / local.Sound
		lift, _ := ceiling(m, speed, *altitude)
		limit := math.Min(lift*q*area/w, 7.5)
		if limit < 1 {
			continue
		}
		thrustDry, thrustWet := m.Thrust(speed, *altitude)
		level := angle(m, speed, *altitude, w/(q*area))
		_, drag := m.Evaluate(speed, level, *altitude)
		excess := func(thrust float64) float64 { return (thrust - drag*q*area) * speed / w }
		if v := excess(thrustDry); v > dry.value {
			dry = point{speed, v}
		}
		if v := excess(thrustWet); v > wet.value {
			wet = point{speed, v}
		}
		if corner == 0 && limit >= 7.5 {
			corner = speed
		}
		sustained := 1.0
		for n := limit; n >= 1; n -= 0.05 {
			pull := angle(m, speed, *altitude, n*w/(q*area))
			_, cd := m.Evaluate(speed, pull, *altitude)
			if cd*q*area <= thrustWet {
				sustained = n
				break
			}
		}
		turn := 0.0
		if sustained > 1 {
			turn = gravity * math.Sqrt(sustained*sustained-1) / speed * 180 / math.Pi
		}
		if turn > rate.value {
			rate = point{speed, turn}
		}
		if *table {
			radius := 0.0
			if sustained > 1.001 {
				radius = speed * speed / (gravity * math.Sqrt(sustained*sustained-1))
			}
			fmt.Printf("%.0f,%.3f,%.0f,%.1f,%.1f,%.2f,%.2f,%.2f,%.0f\n",
				speed, mach, cas(speed, *altitude)*knots, excess(thrustDry), excess(thrustWet), limit, sustained, turn, radius)
		}
	}
	report := func(label string, p point, unit string, scale float64) {
		fmt.Printf("  %-24s %.0f m/s TAS (%.0f KCAS, M%.2f) → %.1f %s\n",
			label, p.speed, cas(p.speed, *altitude)*knots, p.speed/local.Sound, p.value*scale, unit)
	}
	report("best climb rate (dry)", dry, "m/s", 1)
	report("best climb rate (reheat)", wet, "m/s", 1)
	report("best sustained turn", rate, "°/s", 1)
	fmt.Printf("  %-24s %.0f m/s TAS (%.0f KCAS, M%.2f)\n", "corner speed", corner, cas(corner, *altitude)*knots, corner/local.Sound)
}

// cas approximates calibrated airspeed as equivalent airspeed.
func cas(speed float64, altitude float64) float64 {
	local := flight.Atmosphere(altitude, flight.Environment{})
	sea := flight.Atmosphere(0, flight.Environment{})
	return speed * math.Sqrt(local.Density/sea.Density)
}

func command_bench() {
	m := model(0.5)
	m.State.Position = flight.Vec3{Y: 3000}
	m.State.Velocity = flight.Vec3{X: 200}
	m.Environment.Turbulence = 1
	inputs := flight.Inputs{Throttle: 0.9, Pitch: 0.3, Roll: 0.2}
	for step := 0; step < 1000; step++ {
		m.Step(inputs)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	count := 200000
	start := time.Now()
	for step := 0; step < count; step++ {
		m.Step(inputs)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	fmt.Printf("Step: %.1f µs, %d allocations over %d steps\n",
		float64(elapsed.Nanoseconds())/float64(count)/1000, after.Mallocs-before.Mallocs, count)
}

func command_dump() {
	bytes, err := json.MarshalIndent(airframe(), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(bytes))
}

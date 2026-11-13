package flight

import (
	"math"
	"testing"
)

// settled runs the control law alone, holding the state, until the actuators
// have long finished moving, and hands back what they settled at.
func settled(m *Model, in Inputs) FcsState {
	local := air(m.State.Position.Y, m.Environment)
	for i := 0; i < 240*10; i++ {
		m.fcs(in, local)
	}
	return m.State.Fcs
}

// dirty is a jet in level flight at an approach speed with the gear down,
// its flaps at whatever the test sets.
func dirty(speed float64) *Model {
	m := calm()
	launch(m, speed)
	m.State.Gear.Extension = 1
	return m
}

// The flaps' actuator paces every selection at its own rate (TM-107601 table
// 8.7: 18 deg/s), from AUTO's up to FULL's 45 deg in two and a half seconds.
func TestFlapsRate(t *testing.T) {
	c := &Fighter.Control
	m := dirty(65)
	local := air(m.State.Position.Y, m.Environment)
	steps := 0
	for ; steps < 240*10 && m.State.Fcs.Trailing.Left < c.Flaps.Full.Trailing-1e-9; steps++ {
		m.fcs(Inputs{Gear: true, Flap: 2}, local)
	}
	want := c.Flaps.Full.Trailing / c.Rate.Trailing
	if got := float64(steps) / 240; math.Abs(got-want) > 0.05 {
		t.Errorf("the flaps took %.2f s to FULL, not the %.2f s their 18 deg/s sets", got, want)
	}
}

// HALF and FULL set the flaps and the ailerons' droop (NATOPS 2.8.4.1), whole
// at approach speeds; AUTO on the ground sets both to zero.
func TestFlapSettings(t *testing.T) {
	c := &Fighter.Control
	for _, case_ := range []struct {
		name     string
		flap     float64
		trailing float64
		aileron  float64
	}{
		{"HALF", 1, c.Flaps.Half.Trailing, c.Flaps.Half.Aileron},
		{"FULL", 2, c.Flaps.Full.Trailing, c.Flaps.Full.Aileron},
	} {
		f := settled(dirty(65), Inputs{Gear: true, Flap: case_.flap})
		if math.Abs(f.Trailing.Left-case_.trailing) > 1e-6 || math.Abs(f.Trailing.Right-case_.trailing) > 1e-6 ||
			math.Abs(f.Aileron.Left-case_.aileron) > 1e-6 || math.Abs(f.Aileron.Right-case_.aileron) > 1e-6 {
			t.Errorf("%s: flaps %.1f/%.1f and ailerons %.1f/%.1f deg, want %.0f and %.0f", case_.name,
				f.Trailing.Left/degree, f.Trailing.Right/degree, f.Aileron.Left/degree, f.Aileron.Right/degree, case_.trailing/degree, case_.aileron/degree)
		}
	}
	if c.Flaps.Half.Trailing != 30*degree || c.Flaps.Half.Aileron != 30*degree || c.Flaps.Full.Trailing != 45*degree || c.Flaps.Full.Aileron != 42*degree {
		t.Errorf("the settings %+v %+v are not NATOPS's 30/30 and 45/42", c.Flaps.Half, c.Flaps.Full)
	}
	// AUTO rolling on the ground fast enough that the deck's HALF latch is off
	m := dirty(60)
	m.State.Gear.Wow = true
	f := settled(m, Inputs{Gear: true})
	if f.Trailing.Left != 0 || f.Trailing.Right != 0 || f.Aileron.Left != 0 || f.Aileron.Right != 0 {
		t.Errorf("AUTO on the ground: flaps %.2f/%.2f and ailerons %.2f/%.2f deg, want all 0", f.Trailing.Left/degree, f.Trailing.Right/degree, f.Aileron.Left/degree, f.Aileron.Right/degree)
	}
}

// AUTO's schedule against alpha and Mach under its impact-pressure ceiling
// (TM-107601 figures 9.2(d) and (e)), and up above Mach 1 (NATOPS 2.8.4).
func TestAutoFlaps(t *testing.T) {
	c := &Fighter.Control
	sea := Air{Density: 1.225, Pressure: 101325, Temperature: 288.15, Sound: 340.3}
	high := Air{Density: 0.3, Pressure: 18800, Temperature: 216.65, Sound: 295.1}
	for _, case_ := range []struct {
		name  string
		alpha float64
		local Air
		mach  float64
		want  float64
	}{
		{"the plateau at Mach 0.5, high up", 13 * degree, high, 0.5, 17 * degree},
		{"halfway up the rise at Mach 0.5", 6.05 * degree, high, 0.5, 8.5 * degree},
		{"halfway down at Mach 0.5", 19.55 * degree, high, 0.5, 8.5 * degree},
		{"the plateau at Mach 0.9, high up", 10 * degree, high, 0.9, 9 * degree},
		{"between the sheets at Mach 0.75", 13 * degree, high, 0.75, 13 * degree},
		{"supersonic", 10 * degree, high, 1.1, 0},
		{"no alpha", 0, high, 0.5, 0},
		{"the ceiling low and fast", 13 * degree, sea, 0.8, 5 * degree},
	} {
		got := c.automatic(case_.alpha, case_.local, case_.mach*case_.local.Sound)
		if math.Abs(got-case_.want) > 0.01*degree {
			t.Errorf("%s: %.2f deg, want %.2f", case_.name, got/degree, case_.want/degree)
		}
	}
}

// AUTO's flaps share the roll command at low alpha only (TM-107601 figure
// 9.4(g)), and HALF and FULL give them none.
func TestDifferentialFlaps(t *testing.T) {
	// the flaps on their schedule first, then a second of full roll: the
	// split runs through its lag (fcs.go), slower than the ailerons
	split := func(m *Model, in Inputs) float64 {
		local := air(m.State.Position.Y, m.Environment)
		level := in
		level.Roll = 0
		for i := 0; i < 480; i++ {
			m.fcs(level, local)
		}
		for i := 0; i < 240; i++ {
			m.fcs(in, local)
		}
		return m.State.Fcs.Trailing.Left - m.State.Fcs.Trailing.Right
	}
	low := calm()
	launch(low, 220)
	if got := split(low, Inputs{Roll: 1, Throttle: 0.8}); got < 1*degree {
		t.Errorf("AUTO at low alpha: the flaps split %.2f deg under full roll, want them sharing it", got/degree)
	}
	pulled := calm()
	launch(pulled, 220)
	pulled.State.Attitude = Axis(Vec3{Z: 1}, 0.25)
	if got := split(pulled, Inputs{Roll: 1, Throttle: 0.8}); math.Abs(got) > 1e-9 {
		t.Errorf("AUTO at 14 deg alpha: the flaps split %.2f deg, want none", got/degree)
	}
	if got := split(dirty(65), Inputs{Roll: 1, Gear: true, Flap: 2}); math.Abs(got) > 1e-9 {
		t.Errorf("FULL: the flaps split %.2f deg, want none", got/degree)
	}
}

// Rudder toe-in (NATOPS 2.8.2.8): 30 deg with weight on the wheels or below
// 2 deg alpha in the takeoff and landing modes, through 0 to 15 deg toe-out at
// 11; none in AUTO.
func TestToe(t *testing.T) {
	m := dirty(65)
	m.State.Gear.Wow = true
	if got := settled(m, Inputs{Gear: true, Flap: 1}).Toe; math.Abs(got-30*degree) > 1e-6 {
		t.Errorf("on the wheels at HALF: toe-in %.2f deg, want 30", got/degree)
	}
	for _, case_ := range []struct{ alpha, want float64 }{{1 * degree, 30 * degree}, {6.5 * degree, 7.5 * degree}, {11 * degree, -15 * degree}, {15 * degree, -15 * degree}} {
		m := dirty(65)
		m.State.Attitude = Axis(Vec3{Z: 1}, case_.alpha)
		if got := settled(m, Inputs{Gear: true, Flap: 2}).Toe; math.Abs(got-case_.want) > 1e-6 {
			t.Errorf("airborne at FULL, %.1f deg alpha: toe %.2f deg, want %.2f", case_.alpha/degree, got/degree, case_.want/degree)
		}
	}
	clean := calm()
	launch(clean, 200)
	clean.State.Attitude = Axis(Vec3{Z: 1}, 1*degree)
	if got := settled(clean, Inputs{Throttle: 0.8}).Toe; got != 0 {
		t.Errorf("AUTO: toe %.2f deg, want none", got/degree)
	}
}

// landed is the trimmed lift coefficient at a body alpha, gear down, from the
// aero pass alone: with FULL's flaps, droop and approach slat floor out, or
// with the wing clean; the stabilator bisected to zero pitching moment.
func landed(m *Model, angle float64, full bool) float64 {
	const speed, altitude = 65.0, 300.0
	c := &m.Airframe.Control
	local := air(altitude, m.Environment)
	pressure := 0.5 * local.Density * speed * speed
	share, slat := m.Approaching(pressure)
	if !full {
		share, slat = 0, 0
	}
	evaluate := func(stabilator float64) (float64, float64) {
		s := &m.State
		s.Position = Vec3{Y: altitude}
		s.Velocity = Vec3{X: speed}
		s.Attitude = Axis(Vec3{Z: 1}, angle)
		s.Omega = Vec3{}
		s.Engine = [4]EngineState{}
		s.Fuel = 2500
		s.Fcs = FcsState{}
		s.Fcs.Trailing = Pair{Left: c.Flaps.Full.Trailing * share, Right: c.Flaps.Full.Trailing * share}
		s.Fcs.Aileron = Pair{Left: c.Flaps.Full.Aileron * share, Right: c.Flaps.Full.Aileron * share}
		s.Fcs.Slat = math.Max(clamp(c.Slat.Slope*(angle-c.Slat.Offset), 0, c.Slat.Limit), slat)
		s.Fcs.Stabilator = Pair{Left: stabilator, Right: stabilator}
		s.Gear = GearState{Extension: 1, Catapult: -1, Stroke: -1, Wire: -1, Contact: -1}
		m.weigh()
		m.gust = Vec3{}
		var forces Forces
		m.aero(s, &forces, local)
		lift := (forces.Force.X*math.Sin(angle) + forces.Force.Y*math.Cos(angle)) / (pressure * m.Airframe.Reference.Area)
		return lift, forces.Moment.Z
	}
	low, high := -c.Throw.Down, c.Throw.Up
	_, atLow := evaluate(low)
	for i := 0; i < 40; i++ {
		mid := (low + high) / 2
		if _, moment := evaluate(mid); (moment > 0) == (atLow > 0) {
			low = mid
		} else {
			high = mid
		}
	}
	lift, _ := evaluate((low + high) / 2)
	return lift
}

// Flaps.Lift is what the PA law's blend into its level-flight trim prices
// FULL at (the on-speed CL it anchors on): the share of the lift FULL adds to
// the clean wing, trimmed at zero alpha, the law was tuned with when the flaps
// were one 26 deg flaperon (0.48 of 0.629). Held to the aero's own increment,
// so the blend cannot drift from the wing when the flaps' model changes.
func TestFlapsLift(t *testing.T) {
	const tuned = 0.48 / 0.629
	m := calm()
	added := landed(m, 0, true) - landed(m, 0, false)
	t.Logf("FULL over clean, trimmed, zero alpha: CL %.3f; Flaps.Lift %.3f is %.3f of it, tuned at %.3f", added, m.Airframe.Control.Flaps.Lift, m.Airframe.Control.Flaps.Lift/added, tuned)
	if math.Abs(m.Airframe.Control.Flaps.Lift/added-tuned) > 0.02 {
		t.Errorf("Flaps.Lift %.3f is %.3f of FULL's %.3f, not the %.3f the law was tuned with", m.Airframe.Control.Flaps.Lift, m.Airframe.Control.Flaps.Lift/added, added, tuned)
	}
}

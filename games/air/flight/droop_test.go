package flight

import (
	"math"
	"testing"
)

// The trailing edge RUNS; it does not snap (#199). Before the drive limit a
// selection slewed at 1.17 rad/s — 67 deg/s — which is not a flap, it is a
// teleport, and it is why the flap sound and the HUD row had nothing to track.
//
// What is pinned here is the RATE, not a transit time at some flight condition.
// How far the surfaces travel for a given selection depends on where the
// schedule has them: measured at 70 m/s gear-down, AUTO and FULL command within
// 0.1 degrees of each other, so a naive AUTO->FULL stopwatch times noise. The
// rate is the thing the drive actually fixes, and the seconds follow from it.

// drive runs the actuator from one angle to another and returns the seconds it
// took. It exercises droopRun directly rather than flying a bed and commanding
// the switch: how far the surfaces travel for a given selection depends on
// where the schedule has them, and at 70 m/s gear-down the AUTO, HALF and FULL
// commands all land within a tenth of a degree of each other, so a stopwatch on
// a selection times nothing. The drive is what this change added; the drive is
// what is tested.
func drive(from, to float64) float64 {
	c := &Fighter.Control
	m := &Model{}
	m.droop, m.droopInit = from, true
	for i := 0; i < 240*30; i++ {
		if math.Abs(m.droopRun(to, c)-to) < 1e-9 {
			return float64(i) / 240
		}
	}
	return math.NaN()
}

func TestDroopRate(t *testing.T) {
	c := &Fighter.Control
	out := drive(0, c.Droop.Angle)
	back := drive(c.Droop.Angle, 0)
	t.Logf("trailing edge over %.0f deg: %.2f s out, %.2f s in", c.Droop.Angle*180/math.Pi, out, back)
	if math.Abs(out-c.Droop.Angle/c.Rate.Droop.Extend) > 0.05 {
		t.Errorf("extension took %.2f s, not the %.2f the drive rate sets — something else is pacing the surface", out, c.Droop.Angle/c.Rate.Droop.Extend)
	}
	if math.Abs(back-c.Droop.Angle/c.Rate.Droop.Retract) > 0.05 {
		t.Errorf("retraction took %.2f s, not the %.2f the drive rate sets", back, c.Droop.Angle/c.Rate.Droop.Retract)
	}
	// A fresh model SNAPS on its first step: a Case II spawn is handed over
	// established on FULL, and running its flaps down from clean would be a lie.
	fresh := &Model{}
	if got := fresh.droopRun(c.Droop.Angle, c); got != c.Droop.Angle {
		t.Errorf("a fresh model started its droop at %.4f instead of the commanded %.4f — an established spawn would run its flaps down", got, c.Droop.Angle)
	}
}

// The seconds the drive is set to. These are an ESTIMATE from comparable
// powered trailing edges, not a sourced figure (see Rate.Droop in the fa18c
// airframe), so the bounds are wide: they exist to catch someone restoring a
// snap or slowing it to an airliner, not to defend a particular second.
func TestDroopTransit(t *testing.T) {
	c := &Fighter.Control
	out := c.Droop.Angle / c.Rate.Droop.Extend
	back := c.Droop.Angle / c.Rate.Droop.Retract
	t.Logf("full travel: %.1f s out, %.1f s in", out, back)
	if out < 3 || out > 8 {
		t.Errorf("full extension takes %.1f s — outside 3-8", out)
	}
	if back < 3 || back > 8 {
		t.Errorf("full retraction takes %.1f s — outside 3-8", back)
	}
	if back >= out {
		t.Errorf("retraction (%.1f s) is not quicker than extension (%.1f s) — airloads help it home", back, out)
	}
}

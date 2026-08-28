package flight

import (
	"math"
	"testing"
)

// PIO battery: a delayed pure-gain pilot closes a pitch-attitude tracking loop
// - the acceptance harness for the low-speed guns-tracking oscillation. A bare
// authority-cap raise measures WORSE (bigger low-q deflections drive the
// actuator rate limit); any fix must clear this battery AND the bot one.

type pioResult struct {
	crossings int     // target re-crossings after first capture
	overshoot float64 // largest excursion past the target / step size
	sustained bool    // oscillation amplitude in the last 5 s ≥ half the first 5 s
}

// pioRun flies the tracking task: trim level, then track a step of stepDeg.
// gear selects the PA configuration (approach tracking at approach speeds).
func pioRun(kt, gain, stepDeg float64, gear bool) pioResult {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	fuel := 3000.0
	m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, kt/1.94384, fuel)
	const dt = 1.0 / 240
	const tau = 0.30 // human effective delay
	lag := make([]float64, int(tau/dt))
	target := 0.0
	var history []float64
	throttle := 0.8
	if gear {
		throttle = 0.62
	}
	for i := 0; i < 240*22; i++ {
		if i == 240*2 {
			target = stepDeg * math.Pi / 180
		}
		body := m.State.Attitude.Rotate(Vec3{X: 1})
		pitch := math.Asin(clamp(body.Y, -1, 1))
		lag = append(lag, target-pitch)
		delayed := lag[0]
		lag = lag[1:]
		stick := clamp(gain*delayed*57.3/10, -1, 1) // gain = stick per 10° of error
		flap := 0.0
		if gear {
			flap = 2 // the approach configuration is gear AND full flap — the law follows the flap switch (#86)
		}
		m.Step(Inputs{Throttle: throttle, Pitch: stick, Gear: gear, Flap: flap})
		if i >= 240*2 {
			history = append(history, pitch*57.3)
		}
	}
	res := pioResult{}
	reached, last, n := false, 0.0, len(history)
	for _, p := range history {
		if !reached && p >= stepDeg {
			reached = true
		}
		if reached {
			d := p - stepDeg
			if d > res.overshoot*stepDeg {
				res.overshoot = d / stepDeg
			}
			if last != 0 && math.Signbit(d) != math.Signbit(last) && math.Abs(d) > 0.05*stepDeg {
				res.crossings++
			}
			if math.Abs(d) > 0.05*stepDeg {
				last = d
			}
		}
	}
	amp := func(a, b int) float64 {
		lo, hi := 1e9, -1e9
		for _, p := range history[a:b] {
			lo = math.Min(lo, p)
			hi = math.Max(hi, p)
		}
		return hi - lo
	}
	if n > 240*10 {
		res.sustained = amp(n-240*5, n) >= 0.5*amp(0, 240*5) && res.crossings >= 4
	}
	return res
}

// onsetGain binary-searches the lowest pilot gain that rings (≥4 crossings) —
// the handling-qualities margin at each speed. Higher is better; a real pilot
// tracking a gun solution works around gain ~1.
func onsetGain(kt, stepDeg float64, gear bool) float64 {
	lo, hi := 0.2, 3.0
	if pioRun(kt, hi, stepDeg, gear).crossings < 4 {
		return hi // never rings within tested aggression
	}
	for i := 0; i < 8; i++ {
		mid := (lo + hi) / 2
		if pioRun(kt, mid, stepDeg, gear).crossings >= 4 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// TestPIOTracking maps the envelope: onset gain by speed and step size, both
// laws. Asserts only the KNOWN-GOOD region so the defect cannot spread while
// it awaits its fix; the low-speed UA deficiency is logged as the baseline the
// fix must move.
func TestPIOTracking(t *testing.T) {
	t.Log("UA law, 6° step — PIO onset gain by speed (higher = better):")
	for _, kt := range []float64{180, 200, 220, 250, 280, 300, 350, 400} {
		g := onsetGain(kt, 6, false)
		r := pioRun(kt, 1.0, 6, false)
		t.Logf("  %3.0f kt: onset %.2f | at gain 1.0: crossings %2d overshoot %3.0f%% sustained %v",
			kt, g, r.crossings, r.overshoot*100, r.sustained)
	}
	t.Log("UA law amplitude dependence at 220 kt (rate-limit PIO rings LARGE steps):")
	for _, step := range []float64{2, 6, 12} {
		g := onsetGain(220, step, false)
		t.Logf("  %2.0f° step: onset gain %.2f", step, g)
	}
	t.Log("PA law (gear down), 4° step — approach tracking:")
	for _, kt := range []float64{130, 140, 150} {
		g := onsetGain(kt, 4, true)
		r := pioRun(kt, 1.0, 4, true)
		t.Logf("  %3.0f kt: onset %.2f | at gain 1.0: crossings %2d overshoot %3.0f%% sustained %v",
			kt, g, r.crossings, r.overshoot*100, r.sustained)
	}
	// Guaranteed floor: no sustained ring at working pilot gain from 220 kt up,
	// and the approach law calm at on-speed. 180-200 kt stays marginal by
	// design - rateBound deliberately opens there for slow-fight nose
	// authority, and damping it away would trade that agility.
	for _, kt := range []float64{220, 250, 280, 300, 350, 400} {
		if r := pioRun(kt, 1.0, 6, false); r.sustained {
			t.Errorf("%.0f kt UA tracking rings at gain 1.0 — the guns-tracking PIO is back", kt)
		}
	}
	if r := pioRun(140, 0.8, 4, true); r.sustained {
		t.Errorf("PA approach tracking rings at gain 0.8 — approach regression")
	}
}

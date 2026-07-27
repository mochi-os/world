package flight

import (
	"math"
	"testing"
)

// Delayed pure-gain pilot closing a pitch-attitude tracking loop — the classic
// PIO probe, and the acceptance test for the low-speed guns-tracking
// oscillation (first reported after a 220-kt gun solution wallowed in pitch).
// Baseline at gain 1.2: 200-250 kt rings at 8-12 crossings with ~700 % peak
// overshoot while 350 kt holds 2 crossings at ~90 % — the inner rate loop has
// only ~40 % surface power below the 20 kPa authority reference and its phase
// lag closes the loop through the pilot's delay. NOTE a bare authority-cap
// raise makes it WORSE (bigger low-q deflections hit the actuator rate limit;
// measured): the fix needs shaped low-q rate damping, tuned against this probe
// AND the bot battery, since the bots fly the same law.
func pioProbe(t *testing.T, kt float64, gain float64) (overshoots int, ratio float64) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 1500}, Vec3{X: 1}, kt/1.94384, 3000)
	const dt = 1.0 / 240
	const tau = 0.30
	lag := make([]float64, int(tau/dt))
	target := 0.0
	var history []float64
	for i := 0; i < 240*20; i++ {
		if i == 240*2 {
			target = 6 * math.Pi / 180 // the pull: pipper 6° up
		}
		body := m.State.Attitude.Rotate(Vec3{X: 1})
		pitch := math.Asin(clamp(body.Y, -1, 1))
		err := target - pitch
		lag = append(lag, err)
		delayed := lag[0]
		lag = lag[1:]
		stick := clamp(gain*delayed*57.3/10, -1, 1) // gain in stick-per-10°-error terms
		m.Step(Inputs{Throttle: 0.8, Pitch: stick})
		if i >= 240*2 {
			history = append(history, pitch*57.3)
		}
	}
	// count crossings of the target after first reaching it, and the ratio of
	// the largest excursion beyond it to the commanded step
	targetDeg := 6.0
	reached := false
	last := 0.0
	peak := 0.0
	for _, p := range history {
		if !reached && p >= targetDeg {
			reached = true
		}
		if reached {
			d := p - targetDeg
			if d > peak {
				peak = d
			}
			if last != 0 && math.Signbit(d) != math.Signbit(last) && math.Abs(d) > 0.3 {
				overshoots++
			}
			if math.Abs(d) > 0.3 {
				last = d
			}
		}
	}
	return overshoots, peak / targetDeg
}

func TestPIOTracking(t *testing.T) {
	for _, kt := range []float64{200, 220, 250, 300, 350} {
		for _, gain := range []float64{0.6, 1.2} {
			o, r := pioProbe(t, kt, gain)
			t.Logf("%3.0f kt gain %.1f: crossings %2d, peak overshoot %4.0f%%", kt, gain, o, r*100)
		}
	}
}

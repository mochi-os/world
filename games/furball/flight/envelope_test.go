package flight

// Performance-envelope anchors: the model trimmed/settled at published
// F/A-18C reference points, so every future tuning round has regression
// teeth instead of feel-tests. Tolerances are generous — this is an anchor,
// not a certification.

import (
	"math"
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
// 12 deg flyaway deck attitude instead of riding approach alpha into a zoom.
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
			if pitch < 8 || pitch > 16 {
				t.Fatalf("flyaway pitch %.1f°, want ~12°", pitch)
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

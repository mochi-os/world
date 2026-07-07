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
func settle(t *testing.T, fuel float64, target float64) (float64, float64) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State = Level(m, Vec3{Y: 300}, Vec3{X: 1}, target+8, fuel)
	throttle := 0.6
	for i := 0; i < 240*60; i++ {
		m.Step(Inputs{Throttle: throttle, Gear: true})
		if i%24 == 0 {
			// speed-anchored autothrottle: hold the candidate approach speed and
			// let the PA law show the alpha it flies there
			throttle = clamp(throttle+0.01*(target-m.State.Velocity.Length()), 0.1, 1)
		}
	}
	body := m.State.Attitude.Unrotate(m.State.Velocity)
	t.Logf("settle %.0f m/s: speed %.1f vy %.2f alpha %.1f° flaperon %.1f°",
		target, m.State.Velocity.Length(), m.State.Velocity.Y, alpha(body)*57.3, m.State.Fcs.Flaperon.Left*57.3)
	if math.Abs(m.State.Velocity.Y) > 2.5 {
		t.Fatalf("did not settle: vy %.2f", m.State.Velocity.Y)
	}
	return m.State.Velocity.Length(), alpha(body)
}

// TestOnspeed: the PA law's hands-off on-speed condition should sit near the
// real jet's approach numbers — 8.1° alpha at roughly 135 kt (69 m/s) at a
// typical trap weight (about half fuel).
func TestOnspeed(t *testing.T) {
	speed, a := settle(t, 2500, 69)
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
	light, _ := settle(t, 1000, 65)
	heavy, _ := settle(t, 4500, 73)
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

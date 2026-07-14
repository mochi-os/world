// Mochi world: Golden traces and the step budget
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A scripted 20-second flight — accelerate, pull, roll, unload — recorded to
// a committed CSV. The test replays it and asserts every channel within
// tolerance: run natively it catches behavioural regressions; run under
// GOOS=js (make test-wasm) it is the native-versus-wasm divergence bound the
// plan requires. Regenerate deliberately with:
//   go test ./games/air/flight -run TestGolden -update
// and review the diff like any other behavioural change.

package flight

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden traces")

// script is the reference flight profile.
func script(step int) Inputs {
	t := float64(step) * Dt
	in := Inputs{Throttle: 0.9}
	switch {
	case t < 4:
	case t < 8:
		in.Pitch = 0.8
		in.Throttle = 1
		in.Reheat = 1
	case t < 12:
		in.Roll = 1
		in.Throttle = 1
		in.Reheat = 1
	case t < 16:
		in.Pitch = 0.4
		in.Yaw = 0.3
	default:
		in.Speedbrake = 1
	}
	return in
}

// trace flies the script and samples every quarter second.
func trace() []string {
	m := New(Fighter, Environment{Seed: 11, Wind: Vec3{X: 5, Z: 2}, Turbulence: 1, Wrap: 250000}, World{})
	m.State.Position = Vec3{Y: 3000}
	m.State.Velocity = Vec3{X: 200}
	m.State.Attitude = Axis(Vec3{Z: 1}, 0.05)
	m.State.Engine[0] = EngineState{Spool: 0.9}
	m.State.Engine[1] = EngineState{Spool: 0.9}
	lines := []string{"time,x,y,z,vx,vy,vz,w,qx,qy,qz,ox,oy,oz,fuel"}
	for step := 0; step < 240*20; step++ {
		m.Step(script(step))
		if (step+1)%60 == 0 {
			s := &m.State
			lines = append(lines, fmt.Sprintf("%.4f,%.4f,%.4f,%.4f,%.5f,%.5f,%.5f,%.6f,%.6f,%.6f,%.6f,%.5f,%.5f,%.5f,%.3f",
				s.Time, s.Position.X, s.Position.Y, s.Position.Z,
				s.Velocity.X, s.Velocity.Y, s.Velocity.Z,
				s.Attitude.W, s.Attitude.X, s.Attitude.Y, s.Attitude.Z,
				s.Omega.X, s.Omega.Y, s.Omega.Z, s.Fuel))
		}
	}
	return lines
}

// TestGolden replays the reference flight against the committed trace.
func TestGolden(t *testing.T) {
	compare(t, "testdata/golden/reference.csv", trace())
}

// compare checks a freshly-flown trace against its committed golden copy,
// or rewrites the copy under -update.
func compare(t *testing.T, path string, lines []string) {
	t.Helper()
	if *update {
		os.MkdirAll("testdata/golden", 0o755)
		os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		t.Logf("golden trace rewritten: %d samples", len(lines)-1)
		return
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden trace (generate with -update): %v", err)
	}
	want := strings.Split(strings.TrimSpace(string(committed)), "\n")
	if len(want) != len(lines) {
		t.Fatalf("sample count changed: %d vs %d", len(lines), len(want))
	}
	// Tolerances: loose enough for cross-architecture float drift, tight
	// enough that any behavioural change trips.
	limits := []float64{1e-9, 2.0, 2.0, 2.0, 0.5, 0.5, 0.5, 0.01, 0.01, 0.01, 0.01, 0.05, 0.05, 0.05, 0.5}
	for i := 1; i < len(lines); i++ {
		got := strings.Split(lines[i], ",")
		expected := strings.Split(want[i], ",")
		for c := range got {
			g, _ := strconv.ParseFloat(got[c], 64)
			e, _ := strconv.ParseFloat(expected[c], 64)
			if math.Abs(g-e) > limits[c] {
				t.Fatalf("sample %d column %d diverged: %f vs %f (tolerance %g)", i, c, g, e, limits[c])
			}
		}
	}
}

// reheat converts a legacy on/off demand into the full-zone command.
func reheat(on bool) float64 {
	if on {
		return 1
	}
	return 0
}

// deck flies the contact-path reference: attach to the catapult, launch,
// gear up, climb out — the numerically touchiest code under regression watch.
func deck() []string {
	m := aboard()
	park(m, 42.7, -0.6)
	lines := []string{"time,x,y,z,vx,vy,vz,w,qx,qy,qz,ox,oy,oz,extension"}
	for step := 0; step < 240*14; step++ {
		in := Inputs{Gear: true, Throttle: 1, Reheat: reheat(step > 120)}
		in.Launch = step > 480
		if m.State.Gear.Catapult < 0 && step > 480 {
			in.Gear = m.State.Time < 4 // airborne: clean up
		}
		m.Step(in)
		if (step+1)%60 == 0 {
			s := &m.State
			lines = append(lines, fmt.Sprintf("%.4f,%.4f,%.4f,%.4f,%.5f,%.5f,%.5f,%.6f,%.6f,%.6f,%.6f,%.5f,%.5f,%.5f,%.3f",
				s.Time, s.Position.X, s.Position.Y, s.Position.Z,
				s.Velocity.X, s.Velocity.Y, s.Velocity.Z,
				s.Attitude.W, s.Attitude.X, s.Attitude.Y, s.Attitude.Z,
				s.Omega.X, s.Omega.Y, s.Omega.Z, s.Gear.Extension))
		}
	}
	return lines
}

// TestGoldenDeck replays the catapult trace against the committed copy.
func TestGoldenDeck(t *testing.T) {
	compare(t, "testdata/golden/deck.csv", deck())
}

// TestBudget: the step must stay inside the real-time budget with a wide
// ceiling so CI noise cannot flake it (target ≤30 µs native; ceiling ×5).
func TestBudget(t *testing.T) {
	m := New(Fighter, Environment{Turbulence: 1}, World{})
	m.State.Position = Vec3{Y: 3000}
	m.State.Velocity = Vec3{X: 200}
	in := Inputs{Throttle: 0.9, Pitch: 0.3, Roll: 0.2}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			m.Step(in)
		}
	})
	each := result.NsPerOp()
	t.Logf("Step: %d ns", each)
	if each > 150_000 {
		t.Fatalf("Step costs %d ns — over the 150 µs ceiling", each)
	}
}

func BenchmarkStep(b *testing.B) {
	m := New(Fighter, Environment{Turbulence: 1}, World{})
	m.State.Position = Vec3{Y: 3000}
	m.State.Velocity = Vec3{X: 200}
	in := Inputs{Throttle: 0.9, Pitch: 0.3, Roll: 0.2}
	for i := 0; i < b.N; i++ {
		m.Step(in)
	}
}

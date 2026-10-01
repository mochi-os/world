// Mochi world: Cruise performance
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"reflect"
	"testing"
)

const foot = 0.3048

// cruiser is a clean jet with a fuel load, kg, for the cruise figures.
func cruiser(fuel float64) *Model {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State.Fuel = fuel
	return m
}

// TestCruiseAgreesWithFlight: Cruise's flow is what the stepping model burns
// held level at the same Mach and altitude - the same trim, thrust lapse and
// fuel law, not a parallel estimate. The last point is slow enough for the
// leading edge flaps to be down.
func TestCruiseAgreesWithFlight(t *testing.T) {
	for _, c := range []struct{ feet, mach float64 }{{30000, 0.8}, {10000, 0.5}, {15000, 0.35}, {15000, 0.30}} {
		m := cruiser(3629)
		altitude := c.feet * foot
		speed := c.mach * air(altitude, m.Environment).Sound
		m.State = Level(m, Vec3{Y: altitude}, Vec3{X: 1}, speed, 3629)
		throttle := lever(m.State.Engine[0].Spool)
		// A minute and a half to settle under a speed hold on the throttle and
		// an altitude hold on the stick, then a minute measured.
		const settle, measure = 90, 60
		var start, machs, heights float64
		for i := 0; i < 240*(settle+measure); i++ {
			v := m.State.Velocity.Length()
			throttle = clamp(throttle+0.0005*(speed-v)*Dt, 0, 1)
			pitch := clamp(0.004*(altitude-m.State.Position.Y)-0.03*m.State.Velocity.Y, -0.3, 0.3)
			m.Step(Inputs{Throttle: clamp(throttle+0.02*(speed-v), 0, 1), Pitch: pitch})
			if i == 240*settle {
				start = m.State.Fuel
			}
			if i >= 240*settle {
				machs += m.State.Velocity.Length() / air(m.State.Position.Y, m.Environment).Sound
				heights += m.State.Position.Y
			}
		}
		burned := (start - m.State.Fuel) / measure
		mach, height := machs/(240*measure), heights/(240*measure)
		if math.Abs(mach-c.mach) > 0.005 || math.Abs(height-altitude) > 20 { // the stick's height hold settles a little low at the slow point's alpha; Cruise is asked at the height held
			t.Fatalf("%.0f ft M %.2f: the held flight wandered to M %.3f at %.0f m", c.feet, c.mach, mach, height)
		}
		flow, _, ok := m.Cruise(height, mach)
		if !ok || math.Abs(flow/burned-1) > 0.01 {
			t.Errorf("%.0f ft M %.2f: Cruise %.4f kg/s (ok %v) against %.4f kg/s burned in flight", c.feet, c.mach, flow, ok, burned)
		}
	}
}

// TestCruiseWeightAndDrag: the figure is for the jet as it is - more fuel,
// fuller external tanks, a draggier airframe and a hotter day each cost flow
// at the same Mach and altitude.
func TestCruiseWeightAndDrag(t *testing.T) {
	at := func(m *Model) float64 {
		t.Helper()
		flow, _, ok := m.Cruise(25000*foot, 0.7)
		if !ok {
			t.Fatal("no cruise at 25,000 ft and Mach 0.7")
		}
		return flow
	}
	light, heavy := at(cruiser(1500)), at(cruiser(4500))
	if heavy <= light*1.02 {
		t.Errorf("4,500 kg of fuel cruised on %.4f kg/s against 1,500 kg's %.4f, want more", heavy, light)
	}
	clean := cruiser(3000)
	base := at(clean)
	loaded := cruiser(3000)
	loaded.Stores(Fighter.Default | mask(t, "pylon3", "tank3", "pylon7", "tank7"))
	tanks := at(loaded)
	if tanks <= base*1.05 {
		t.Errorf("two full wing tanks cruised on %.4f kg/s against the clean jet's %.4f, want more", tanks, base)
	}
	loaded.State.External.Wing /= 4
	if drained := at(loaded); drained >= tanks {
		t.Errorf("the tanks a quarter full cruised on %.4f kg/s against %.4f full, want less", drained, tanks)
	}
	clean.State.Damage.Drag = 0.4 // m² of torn skin, no mass
	if torn := at(clean); torn <= base*1.05 {
		t.Errorf("0.4 m² of damage drag cruised on %.4f kg/s against %.4f, want more", torn, base)
	}
	// A hurt engine burns in proportion to the thrust it still makes, as it does
	// in flight (burn): the other makes up the thrust, and the jet cruises on
	// much the same flow.
	hurt := cruiser(3000)
	hurt.State.Damage.Engine[0] = 0.5
	if limping := at(hurt); math.Abs(limping/base-1) > 0.05 {
		t.Errorf("one engine at half health cruised on %.4f kg/s against the sound jet's %.4f, want much the same", limping, base)
	}
	// The scratch model follows the live one's environment: the same jet on a
	// day 30 K hotter flies the same Mach faster through thinner air.
	day := cruiser(3000)
	cool := at(day)
	day.Environment.Temperature = 30
	if hot := at(day); hot == cool {
		t.Errorf("a 30 K hotter day left the cruise flow at %.4f kg/s", hot)
	}
}

// TestCruiseBestMach: at a cruise altitude the flow per metre has a minimum
// inside the dry envelope (best range), and the least flow per second (best
// endurance) is at a slower Mach.
func TestCruiseBestMach(t *testing.T) {
	m := cruiser(3629)
	var machs, flows, per []float64
	for mach := 0.30; mach < 1.0; mach += 0.01 {
		flow, speed, ok := m.Cruise(30000*foot, mach)
		if !ok {
			continue
		}
		machs, flows, per = append(machs, mach), append(flows, flow), append(per, flow/speed)
	}
	if len(machs) < 30 {
		t.Fatalf("only %d cruise points between Mach 0.30 and 1.0 at 30,000 ft", len(machs))
	}
	lowest := func(v []float64) int {
		at := 0
		for i := range v {
			if v[i] < v[at] {
				at = i
			}
		}
		return at
	}
	distance, endurance := lowest(per), lowest(flows)
	t.Logf("30,000 ft, 8,000 lb of fuel, clean: best range Mach %.2f at %.0f lb/h (%.3f nm/lb); best endurance Mach %.2f at %.0f lb/h; flyable Mach %.2f to %.2f",
		machs[distance], flows[distance]*7936.6, 1/(per[distance]*1852*2.2046), machs[endurance], flows[endurance]*7936.6, machs[0], machs[len(machs)-1])
	if distance == 0 || distance == len(machs)-1 {
		t.Errorf("best range sits at the edge of the envelope, Mach %.2f", machs[distance])
	}
	if endurance == 0 || endurance >= distance {
		t.Errorf("best endurance at Mach %.2f against best range at Mach %.2f, want it slower and inside the envelope", machs[endurance], machs[distance])
	}
	if machs[distance] < 0.7 || machs[distance] > 0.9 {
		t.Errorf("best range at Mach %.2f at 30,000 ft, want a high subsonic cruise", machs[distance])
	}
}

// TestCruiseEnvelope: no figure where the jet cannot fly level on dry power,
// or for a point outside the atmosphere the search covers.
func TestCruiseEnvelope(t *testing.T) {
	m := cruiser(3629)
	for _, c := range []struct {
		name           string
		altitude, mach float64
	}{
		{"too fast for military power", 30000 * foot, 1.2},
		{"too slow for the wing", 30000 * foot, 0.3},
		{"below the sea", -100, 0.5},
		{"above the search", 25000, 0.8},
		{"no Mach", 5000, 0},
		{"not a number", 5000, math.NaN()},
	} {
		if flow, _, ok := m.Cruise(c.altitude, c.mach); ok {
			t.Errorf("%s: %.0f m Mach %.2f gave %.4f kg/s", c.name, c.altitude, c.mach, flow)
		}
	}
	// Inside the envelope there are no holes: every Mach between the slowest
	// and the fastest the jet can hold has a figure.
	for _, feet := range []float64{4000, 30000} {
		flyable, gap := 0, false
		for mach := 0.15; mach < 1.2; mach += 0.01 {
			_, _, ok := m.Cruise(feet*foot, mach)
			if ok && gap {
				t.Errorf("%.0f ft: no figure just below Mach %.2f, inside the envelope", feet, mach)
			}
			if ok {
				flyable++
			}
			gap = flyable > 0 && !ok
		}
		if flyable < 40 {
			t.Errorf("%.0f ft: only %d flyable Mach numbers", feet, flyable)
		}
	}
	if _, speed, ok := m.Cruise(30000*foot, 0.8); !ok || math.Abs(speed-0.8*air(30000*foot, m.Environment).Sound) > 1e-9 {
		t.Errorf("Mach 0.8 at 30,000 ft: ok %v, true airspeed %.2f m/s", ok, speed)
	}
}

// TestCruiseLeavesTheJetAlone: the solve runs on a scratch model. The flying
// state, damage arrays included, is untouched, and a jet that asks between
// steps flies exactly as one that never asked.
func TestCruiseLeavesTheJetAlone(t *testing.T) {
	build := func() *Model {
		m := cruiser(3000)
		m.Stores(Fighter.Default | mask(t, "pylon5", "tank5"))
		m.State = Level(m, Vec3{Y: 6000}, Vec3{X: 1}, 200, 3000)
		m.State.External.Centre = 400
		m.State.Damage.Element = make([]float64, Elements)
		m.State.Damage.Element[3] = 0.25
		m.State.Damage.Engine[1] = 0.1
		for i := 0; i < 240; i++ {
			m.Step(Inputs{Throttle: 0.6})
		}
		return m
	}
	m, twin := build(), build()
	before := m.State
	before.Damage = m.State.Damage.Copy()
	mass, center := m.mass, m.center
	for _, c := range [][2]float64{{6000, 0.6}, {12000, 0.85}, {9000, 1.3}, {3000, 0.2}} {
		m.Cruise(c[0], c[1])
	}
	if !reflect.DeepEqual(before, m.State) || m.mass != mass || m.center != center {
		t.Fatal("Cruise changed the flying model")
	}
	for i := 0; i < 240; i++ {
		m.Cruise(8000, 0.75)
		m.Step(Inputs{Throttle: 0.6})
		twin.Step(Inputs{Throttle: 0.6})
	}
	if !reflect.DeepEqual(m.State, twin.State) {
		t.Fatal("a jet that asked for cruise figures between steps flew differently")
	}
}

// Mochi world: stores catalog and external fuel tests (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// mask returns the bit for a named catalog entry, failing the test on a typo.
func mask(t *testing.T, names ...string) uint64 {
	t.Helper()
	m := uint64(0)
	for _, name := range names {
		found := false
		for i := range Fighter.Stores {
			if Fighter.Stores[i].Name == name {
				m |= 1 << uint(i)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no catalog entry named %q", name)
		}
	}
	return m
}

// TestCatalog: the catalog keeps the wingtips at bits 0 and 1 (the
// count-to-mask mappings and the calibrated bot rely on those indices), the
// default mask flies exactly them, and every entry is named and stationed.
func TestCatalog(t *testing.T) {
	if Fighter.Stores[0].Name != "tip1" || Fighter.Stores[1].Name != "tip9" {
		t.Fatalf("wingtips moved off bits 0/1: %q %q", Fighter.Stores[0].Name, Fighter.Stores[1].Name)
	}
	if Fighter.Default != 0b11 {
		t.Fatalf("default mask %b — the bare jet must fly wingtips only", Fighter.Default)
	}
	for i := range Fighter.Stores {
		s := &Fighter.Stores[i]
		if s.Name == "" || s.Station < 1 || s.Station > 9 {
			t.Fatalf("entry %d (%q station %d) is unnamed or unstationed", i, s.Name, s.Station)
		}
		if s.Mass <= 0 {
			t.Fatalf("entry %q has no mass", s.Name)
		}
	}
}

// TestTankFill: attaching a tank fills it (External rises by its capacity),
// detaching clamps to the remaining capacity, and re-asserting an unchanged
// mask changes nothing.
func TestTankFill(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	if m.State.External != 0 {
		t.Fatalf("bare jet spawned with %f kg external", m.State.External)
	}
	both := Fighter.Default | mask(t, "pylon3", "tank3", "pylon7", "tank7")
	m.Stores(both)
	if m.State.External != 2020 {
		t.Fatalf("two tanks filled to %f kg, want 2020", m.State.External)
	}
	m.Stores(both) // idempotent re-assert
	if m.State.External != 2020 {
		t.Fatalf("re-assert changed external to %f kg", m.State.External)
	}
	m.Stores(Fighter.Default | mask(t, "pylon3", "tank3")) // starboard tank departs part-full
	if m.State.External != 1010 {
		t.Fatalf("one tank remaining holds %f kg, want the 1010 clamp", m.State.External)
	}
	m.Stores(Fighter.Default)
	if m.State.External != 0 {
		t.Fatalf("no tanks but %f kg external", m.State.External)
	}
}

// TestBurnOrder: external fuel drains first while internal holds level;
// internal only falls once the externals are dry.
func TestBurnOrder(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.Stores(Fighter.Default | mask(t, "pylon5", "tank5"))
	m.State = Level(m, Vec3{Y: 2000}, Vec3{X: 1}, 200, 3000)
	m.State.External = 5 // nearly dry, so the crossover happens inside the test
	internal := m.State.Fuel
	in := Inputs{Throttle: 1}
	for tick := 0; tick < 240*10 && m.State.External > 0; tick++ {
		m.Step(in)
	}
	if m.State.Fuel < internal-0.001 {
		t.Fatalf("internal fell %.3f kg while external fuel remained", internal-m.State.Fuel)
	}
	for tick := 0; tick < 240; tick++ {
		m.Step(in)
	}
	if m.State.Fuel >= internal {
		t.Fatalf("internal did not burn after the externals ran dry")
	}
}

// TestTankWeigh: a mounted tank carries dry mass plus its fuel share, and a
// single wing tank pulls the CG laterally toward its station.
func TestTankWeigh(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State.Fuel = 2000
	m.weigh()
	bare := m.mass
	m.Stores(Fighter.Default | mask(t, "pylon7", "tank7"))
	m.weigh()
	added := m.mass - bare
	want := 136.0 + 158 + 1010 // wet pylon + dry tank + full fuel
	if math.Abs(added-want) > 0.5 {
		t.Fatalf("one full wing tank added %.1f kg, want %.1f", added, want)
	}
	if m.center.Z < 0.1 {
		t.Fatalf("starboard tank left the CG at Z %.3f — no lateral shift", m.center.Z)
	}
	m.State.External = 0 // burned dry: only the hardware remains
	m.weigh()
	if math.Abs((m.mass-bare)-(136.0+158)) > 0.5 {
		t.Fatalf("dry tank still carries fuel mass: %.1f kg added", m.mass-bare)
	}
}

// TestTwinWeigh: the Fox 2 fighter loadout (tips + outboard singles) adds its
// summed hardware, and inertia about the roll axis grows with the outboard
// rounds — the loaded jet must feel heavier in roll.
func TestTwinWeigh(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.State.Fuel = 2000
	m.weigh()
	clean := m.inertia[0][0]
	m.Stores(Fighter.Default | mask(t, "rail2", "9m2", "rail8", "9m8"))
	m.weigh()
	if m.inertia[0][0] <= clean {
		t.Fatalf("outboard rounds did not grow roll inertia: %.0f vs %.0f", m.inertia[0][0], clean)
	}
	if math.Abs(m.center.Z) > 0.001 {
		t.Fatalf("symmetric loadout shifted the CG laterally to Z %.4f", m.center.Z)
	}
}

// TestExternalEncode: the external quantity survives the encode round trip at
// the appended tail word.
func TestExternalEncode(t *testing.T) {
	s := State{Fuel: 1234, External: 987.5}
	out := make([]float64, Size)
	s.Encode(out)
	back := Decode(out)
	if back.External != 987.5 || back.Fuel != 1234 {
		t.Fatalf("round trip lost fuel state: %f %f", back.Fuel, back.External)
	}
}

// TestTankJettisonShare: a PART-FULL tank departs with its share of the fuel
// (#42). The old clamp only bit when the remainder exceeded the new capacity,
// so punching a part-full tank kept every kilogram — shedding the mass and
// drag for free. Proportional is exact: attached tanks drain in step.
func TestTankJettisonShare(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	all := Fighter.Default | mask(t, "pylon3", "tank3", "pylon5", "tank5", "pylon7", "tank7")
	m.Stores(all)
	if m.State.External != 3030 {
		t.Fatalf("three tanks filled to %f kg, want 3030", m.State.External)
	}
	m.State.External = 1500 // burned down: every tank at the same 49.5% fill
	m.Stores(Fighter.Default | mask(t, "pylon3", "tank3", "pylon7", "tank7"))
	if math.Abs(m.State.External-1000) > 0.001 {
		t.Fatalf("dropping one of three part-full tanks left %f kg, want 1000 (a third of the fuel leaves with its tank)", m.State.External)
	}
	// Dropping the rest takes the rest.
	m.Stores(Fighter.Default)
	if m.State.External != 0 {
		t.Fatalf("no tanks but %f kg external", m.State.External)
	}
	// The full-tank drop and the rearm refill keep their exact semantics.
	m.Stores(all)
	if m.State.External != 3030 {
		t.Fatalf("re-arm filled to %f kg, want 3030", m.State.External)
	}
}

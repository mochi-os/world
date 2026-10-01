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
	if m.State.External.Total() != 0 {
		t.Fatalf("bare jet spawned with %f kg external", m.State.External.Total())
	}
	both := Fighter.Default | mask(t, "pylon3", "tank3", "pylon7", "tank7")
	m.Stores(both)
	if m.State.External != (Tanks{Wing: 2020}) {
		t.Fatalf("two wing tanks filled to %+v, want 2020 kg in the wing group", m.State.External)
	}
	m.Stores(both) // idempotent re-assert
	if m.State.External != (Tanks{Wing: 2020}) {
		t.Fatalf("re-assert changed external to %+v", m.State.External)
	}
	m.Stores(both | mask(t, "pylon5", "tank5")) // the centreline tank is the CTR group's
	if m.State.External != (Tanks{Wing: 2020, Centre: 1010}) {
		t.Fatalf("centreline tank filled to %+v, want 1010 kg in the centre group", m.State.External)
	}
	m.Stores(Fighter.Default | mask(t, "pylon3", "tank3")) // starboard and centreline tanks depart
	if m.State.External != (Tanks{Wing: 1010}) {
		t.Fatalf("one tank remaining holds %+v, want the 1010 wing clamp", m.State.External)
	}
	m.Stores(Fighter.Default)
	if m.State.External.Total() != 0 {
		t.Fatalf("no tanks but %f kg external", m.State.External.Total())
	}
}

// TestBurnOrder: the external fuel transfers into the internal tanks ahead of
// them, so internal holds level; internal only falls once the externals are
// dry.
func TestBurnOrder(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	m.Stores(Fighter.Default | mask(t, "pylon5", "tank5"))
	m.State = Level(m, Vec3{Y: 2000}, Vec3{X: 1}, 200, Fighter.Mass.Fuel) // internal full: the transfer only keeps it there
	m.State.External = Tanks{Centre: 5}                                   // nearly dry, so the crossover happens inside the test
	internal := m.State.Fuel
	in := Inputs{Throttle: 1}
	for tick := 0; tick < 240*10 && m.State.External.Total() > 0; tick++ {
		m.Step(in)
		if m.State.External.Total() > 0 && m.State.Fuel < internal-1e-9 {
			t.Fatalf("internal fell %.6f kg while external fuel remained", internal-m.State.Fuel)
		}
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
	m.State.External = Tanks{} // burned dry: only the hardware remains
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

// TestExternalEncode: the external groups, the spin recovery latch and the
// inflight IDLE stop's latch survive the encode round trip at the appended
// tail words.
func TestExternalEncode(t *testing.T) {
	s := State{Fuel: 1234, External: Tanks{Wing: 987.5, Centre: 321.25}, Retracted: true}
	s.Fcs.Recovery = true
	out := make([]float64, Size)
	s.Encode(out)
	back := Decode(out)
	if back.External != s.External || back.Fuel != 1234 || !back.Fcs.Recovery || !back.Retracted {
		t.Fatalf("round trip lost state: fuel %f external %+v recovery %v retracted %v", back.Fuel, back.External, back.Fcs.Recovery, back.Retracted)
	}
	if out[Size-4] != 987.5 || out[Size-3] != 321.25 || out[Size-2] != 1 || out[Size-1] != 1 {
		t.Fatalf("tail words %v, want the wing and centre fuel, the spin latch, then the idle stop's last", out[Size-4:])
	}
	s.Retracted = false
	s.Encode(out)
	if out[Size-1] != 0 || Decode(out).Retracted {
		t.Fatalf("the idle stop's latch read %v when clear", out[Size-1])
	}
}

// TestTankJettisonShare: a PART-FULL tank departs with its proportional share
// of the external fuel (#42) - exact, because attached tanks drain in step.
func TestTankJettisonShare(t *testing.T) {
	m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
	all := Fighter.Default | mask(t, "pylon3", "tank3", "pylon5", "tank5", "pylon7", "tank7")
	m.Stores(all)
	if m.State.External != (Tanks{Wing: 2020, Centre: 1010}) {
		t.Fatalf("three tanks filled to %+v, want 2020 wing and 1010 centre", m.State.External)
	}
	m.State.External = Tanks{Wing: 1000, Centre: 500} // burned down: every tank at the same 49.5% fill
	m.Stores(Fighter.Default | mask(t, "pylon3", "tank3", "pylon7", "tank7"))
	if math.Abs(m.State.External.Total()-1000) > 0.001 || m.State.External.Centre != 0 {
		t.Fatalf("dropping the part-full centreline tank left %+v, want 1000 kg of wing fuel (its share leaves with it)", m.State.External)
	}
	m.Stores(Fighter.Default | mask(t, "pylon3", "tank3"))
	if math.Abs(m.State.External.Wing-500) > 0.001 {
		t.Fatalf("dropping one of two part-full wing tanks left %f kg, want 500", m.State.External.Wing)
	}
	// Dropping the rest takes the rest.
	m.Stores(Fighter.Default)
	if m.State.External.Total() != 0 {
		t.Fatalf("no tanks but %f kg external", m.State.External.Total())
	}
	// The full-tank drop and the rearm refill keep their exact semantics.
	m.Stores(all)
	if m.State.External != (Tanks{Wing: 2020, Centre: 1010}) {
		t.Fatalf("re-arm filled to %+v, want 2020 and 1010", m.State.External)
	}
}

// TestTransferSwitches: the EXT TANKS switches (NATOPS 2.2.4, 2.2.4.1). NORM
// feeds both groups in step by capacity, the transfer covering the burn and
// refilling beyond it; STOP holds a group until FUEL LO; ORIDE pressurizes and
// feeds on the deck; without pressure - weight on the wheels, the probe out, or
// the hook and gear handles both down - nothing transfers; and a group running
// dry hands the rest to the other.
func TestTransferSwitches(t *testing.T) {
	all := Fighter.Default | mask(t, "pylon3", "tank3", "pylon5", "tank5", "pylon7", "tank7")
	type result struct{ internal, wing, centre float64 }
	run := func(in Inputs, wow bool, fuel float64, external Tanks) result {
		m := New(Fighter, Environment{Seed: 1}, World{Sea: 0})
		m.Stores(all)
		m.State.Fuel, m.State.External, m.State.Gear.Wow = fuel, external, wow
		for i := 0; i < 240; i++ { // one second at 2 kg/s of burn
			m.transfer(in, 2)
		}
		return result{m.State.Fuel - fuel, external.Wing - m.State.External.Wing, external.Centre - m.State.External.Centre}
	}
	full := Tanks{Wing: 2020, Centre: 1010}
	near := func(got result, want result) bool {
		return math.Abs(got.internal-want.internal) < 1e-6 && math.Abs(got.wing-want.wing) < 1e-6 && math.Abs(got.centre-want.centre) < 1e-6
	}
	moved := (2 + refill) // kg in the second
	cases := []struct {
		name string
		in   Inputs
		wow  bool
		fuel float64
		from Tanks
		want result
	}{
		{"NORM both, by capacity", Inputs{}, false, 3000, full, result{moved, moved * 2 / 3, moved / 3}},
		{"WING at STOP", Inputs{Transfer: [2]int{-1, 0}}, false, 3000, full, result{moved, 0, moved}},
		{"CTR at STOP", Inputs{Transfer: [2]int{0, -1}}, false, 3000, full, result{moved, moved, 0}},
		{"both at STOP", Inputs{Transfer: [2]int{-1, -1}}, false, 3000, full, result{}},
		{"both at STOP at FUEL LO", Inputs{Transfer: [2]int{-1, -1}}, false, 800, full, result{moved, moved * 2 / 3, moved / 3}},
		{"FUEL LO with the probe out, WING at ORIDE, CTR at STOP", Inputs{Transfer: [2]int{1, -1}, Probe: true}, false, 800, full, result{moved, moved, 0}},
		{"on the deck", Inputs{}, true, 3000, full, result{}},
		{"on the deck, WING at ORIDE", Inputs{Transfer: [2]int{1, 0}}, true, 3000, full, result{moved, moved * 2 / 3, moved / 3}},
		{"on the deck, WING at ORIDE, CTR at STOP", Inputs{Transfer: [2]int{1, -1}}, true, 3000, full, result{moved, moved, 0}},
		{"probe out", Inputs{Probe: true}, false, 3000, full, result{}},
		{"hook and gear down", Inputs{Hook: true, Gear: true}, false, 3000, full, result{}},
		{"hook down, gear up", Inputs{Hook: true}, false, 3000, full, result{moved, moved * 2 / 3, moved / 3}},
		{"wing tanks running dry", Inputs{}, false, 3000, Tanks{Wing: 0.5, Centre: 1010}, result{moved, 0.5, moved - 0.5}},
		{"internal full", Inputs{}, false, Fighter.Mass.Fuel, full, result{}},
		{"internal over-full", Inputs{}, false, Fighter.Mass.Fuel + 10, full, result{}},
	}
	for _, c := range cases {
		if got := run(c.in, c.wow, c.fuel, c.from); !near(got, c.want) {
			t.Errorf("%s: moved %+v, want %+v", c.name, got, c.want)
		}
	}
	// At STOP the FUEL LO transfer ends as the internal fuel climbs back past it.
	if got := run(Inputs{Transfer: [2]int{-1, -1}}, false, 900, full); got.internal < caution-900 || got.internal > caution-900+moved/240 {
		t.Errorf("both at STOP from 900 kg: the internal fuel rose %.3f kg, want it to stop just past FUEL LO (%.1f kg)", got.internal, caution)
	}
}

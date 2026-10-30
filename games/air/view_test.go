// Mochi world: Air - the frame a bot thinks in, tested
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"world/games/air/flight"
)

// vectors lists the path of every flight.Vec3 reachable from a type: struct
// fields, pointers, maps and slices followed, each type once.
func vectors(t reflect.Type, path string, seen map[reflect.Type]bool, out *[]string) {
	vec := reflect.TypeOf(flight.Vec3{})
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		element := t.Elem()
		if element == vec {
			*out = append(*out, path+"[]")
			return
		}
		vectors(element, path+"[]", seen, out)
	case reflect.Struct:
		if t == vec {
			*out = append(*out, path)
			return
		}
		if seen[t] || strings.HasPrefix(t.PkgPath(), "world/games/air/flight") || t.PkgPath() == "world/games/air/round" {
			return
		}
		seen[t] = true
		for k := 0; k < t.NumField(); k++ {
			f := t.Field(k)
			vectors(f.Type, path+"."+f.Name, seen, out)
		}
		delete(seen, t)
	}
}

// TestBrainRemembersEverything: every vector a brain keeps between ticks is
// one rebase carries when the frame it thinks in moves. A field added to the
// brain, a track, an orbit, the routine or the journal fails here until
// rebase knows whether it is a point or a direction.
func TestBrainRemembersEverything(t *testing.T) {
	var found []string
	vectors(reflect.TypeOf(brain{}), "brain", map[reflect.Type]bool{}, &found)
	sort.Strings(found)
	carried := []string{
		"brain.aim", "brain.beam", "brain.contact", "brain.journal[].path[]", "brain.journal[].pending[].point",
		"brain.known[][].blown", "brain.known[][].nose", "brain.known[][].position", "brain.known[][].swing", "brain.known[][].velocity",
		"brain.lane", "brain.pictured[].blown", "brain.pictured[].nose", "brain.pictured[].position", "brain.pictured[].swing", "brain.pictured[].velocity",
		"brain.prey[].blown", "brain.prey[].nose", "brain.prey[].position", "brain.prey[].swing", "brain.prey[].velocity",
		"brain.residual", "brain.ring.centre", "brain.ring.drift", "brain.ring.normal",
		"brain.routine.away", "brain.routine.beam", "brain.routine.course", "brain.routine.incoming", "brain.routine.side", "brain.routine.velocity",
		"brain.sampleP", "brain.sampleV", "brain.sampleW",
	}
	sort.Strings(carried)
	if !reflect.DeepEqual(found, carried) {
		t.Fatalf("the brain keeps vectors rebase does not know:\n found   %v\n carried %v", found, carried)
	}
}

// TestRebaseCarriesMemories: rebase carries every remembered point through
// the motion's turn and shift and every direction through its turn alone, a
// track shared between the map and the prey once, and a memory not yet made
// stays unmade.
func TestRebaseCarriesMemories(t *testing.T) {
	r := flight.Between(flight.Home, flight.Tangent(0.6, -2.9))
	p := func(k float64) flight.Vec3 { return flight.Vec3{X: 1000 + k, Y: 3000 - k, Z: -500 + 2*k} }
	shared := &track{position: p(1), velocity: p(2), swing: p(3), nose: p(4), blown: p(5)}
	b := &brain{known: map[int]*track{3: shared}, prey: shared, pictured: &track{position: p(6)},
		lane: p(7), residual: p(8), aim: p(9), sampleP: p(10), sampleV: p(11), sampleW: p(12), beam: p(13),
		ring:    orbit{centre: p(14), normal: p(15), drift: p(16)},
		routine: routine{beam: p(17), incoming: p(18), side: p(19), course: p(20), velocity: p(21), away: p(22)},
		journal: &journal{pending: []pending{{point: p(23)}}, path: []flight.Vec3{p(24)}}}
	b.rebase(r)
	near := func(name string, got, want flight.Vec3) {
		if got.Subtract(want).Length() > 1e-6 {
			t.Errorf("%s carried to %v, want %v", name, got, want)
		}
	}
	near("a track's position", shared.position, r.Point(p(1)))
	near("a track's velocity", shared.velocity, r.Vector(p(2)))
	near("the picture's position", b.pictured.position, r.Point(p(6)))
	near("the lane", b.lane, r.Vector(p(7)))
	near("the sampled point", b.sampleP, r.Point(p(10)))
	near("the contact", b.contact, flight.Vec3{})
	near("the orbit's centre", b.ring.centre, r.Point(p(14)))
	near("the orbit's normal", b.ring.normal, r.Vector(p(15)))
	near("the routine's course", b.routine.course, r.Vector(p(20)))
	near("a forecast's point", b.journal.pending[0].point, r.Point(p(23)))
	near("the traced path", b.journal.path[0], r.Point(p(24)))
}

// flier is a single-player duel, flown from the bandit's spawn by the world
// frame's motion into place: the bandit free, the pilot flying straight and
// level past it, the bandit's stick and throttle each tick returned.
func flier(place func(flight.Vec3) flight.Vec3, turn flight.Quat, ticks int) []flight.Inputs {
	b := NewBandit("expert", 3, "", false, true, "guns", 0, false)
	defer b.arena.Close()
	b.Spawn(place(flight.Vec3{Y: 4000}), turn.Rotate(flight.Vec3{X: 220}))
	pm := flight.New(b.craft.model.Airframe, b.craft.model.Environment, flight.World{Sea: sea})
	pm.State = flight.Level(pm, place(flight.Vec3{X: 2500, Y: 4300, Z: 600}), turn.Rotate(flight.Vec3{X: -1}), 230, fuel)
	words := make([]float64, flight.Size)
	var out []flight.Inputs
	for tick := 0; tick < ticks; tick++ {
		pm.Step(flight.Inputs{Throttle: 0.8})
		pm.State.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
		out = append(out, b.craft.latest)
	}
	return out
}

// TestBanditThinksAnywhere: the same duel flown at Midway and 2,000 km away,
// where the world frame's up leans 18 degrees off the jets' own, draws the
// same stick from the bandit tick for tick: it thinks in a frame level under
// it, wherever it is.
func TestBanditThinksAnywhere(t *testing.T) {
	far, level := flight.Home.Sea(2000000, -500000)
	home := flier(func(p flight.Vec3) flight.Vec3 { return p }, flight.Quat{W: 1}, 120)
	away := flier(func(p flight.Vec3) flight.Vec3 { return far.Add(level.Rotate(p)) }, level, 120)
	for k := range home {
		h, a := home[k], away[k]
		if math.Abs(h.Pitch-a.Pitch) > 0.05 || math.Abs(h.Roll-a.Roll) > 0.05 || math.Abs(h.Throttle-a.Throttle) > 0.05 {
			t.Fatalf("tick %d: the bandit at Midway flies pitch %.3f roll %.3f throttle %.3f, 2,000 km out %.3f %.3f %.3f",
				k, h.Pitch, h.Roll, h.Throttle, a.Pitch, a.Roll, a.Throttle)
		}
	}
	moved := 0.0
	for k := 1; k < len(home); k++ {
		moved += math.Abs(home[k].Roll-home[k-1].Roll) + math.Abs(home[k].Pitch-home[k-1].Pitch)
	}
	if moved < 0.5 {
		t.Fatalf("the bandit held its stick still for two seconds (%.2f of travel): the case asks it nothing", moved)
	}
}

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
	r := flight.Between(scene, flight.Tangent(0.6, -2.9))
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

// recall carries a brain's memory into the session's frame, where a test
// writes into it and reads it. The brain carries it back into the frame it
// thinks in when it next thinks, as it does a memory from before it ever
// thought. A bot thinks in a frame laid under it, which is the session's only
// when it stands at the session's anchor.
func (i *instance) recall(b *brain) {
	if b.frame.Set() {
		b.rebase(flight.Between(b.frame, i.environment.Theatre()))
	}
	b.frame = flight.Frame{}
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
	far, level := scene.Sea(2000000, -500000)
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

// laid poses a craft as posed does, in the level frame at a place, carried
// into the session's frame.
func laid(i *instance, c *craft, place flight.Place, heading, speed float64) {
	level := flight.Tangent(place.Latitude, place.Longitude)
	posed(c, flight.Vec3{Y: place.Altitude}, heading, speed)
	r := flight.Between(level, i.environment.Theatre())
	s := &c.model.State
	s.Position, s.Velocity, s.Attitude = r.Point(s.Position), r.Vector(s.Velocity), r.Attitude(s.Attitude)
}

// TestEarthCentred: a session kept as the server keeps one, Earth-centred,
// where y is up nowhere: at Midway the plane y = 0 runs some 300 km from the
// jets. Its bots spawn over the sea and fly, fight and fire there, and the sea
// kills a jet only at the sea. Code reading y as height fails here at once.
func TestEarthCentred(t *testing.T) {
	was := setting
	setting.Frame, setting.Chart = flight.Frame{}, flight.Frame{}
	defer func() { setting = was }()

	// Unarmed, four experts fight for twenty seconds and nobody dies: a sea
	// read from y would take them all at once.
	i := build(t, "furball", map[string]any{"bots": map[string]any{"expert": 4.0}}, 0)
	if !i.environment.Theatre().Same(flight.Earth) {
		t.Fatalf("the session is laid in %v, want the Earth-centred frame", i.environment.Theatre())
	}
	for tick := uint64(0); tick < 20*60; tick++ {
		i.Step(tick, nil)
		for _, slot := range i.slots() {
			a := i.aircraft[slot]
			if !a.bot {
				continue
			}
			altitude := i.environment.Theatre().Altitude(a.model.State.Position)
			if tick == 0 && (altitude < 300 || altitude > 15000) {
				t.Fatalf("slot %d spawned %.0f m above the sea", slot, altitude)
			}
			if !a.alive {
				t.Fatalf("slot %d died %.1f s in, %.0f m above the sea", slot, float64(tick)/60, altitude)
			}
		}
	}
	// Armed, their AMRAAMs and their heaters fly: a round's sea read from y
	// would end it on the rail. Each kind is asked for, since four experts
	// denied one fire the other.
	i = build(t, "furball", map[string]any{"missiles": true, "bots": map[string]any{"expert": 4.0}}, 0)
	launched, flown := map[bool]int{}, map[bool]float64{}
	seen := map[*missile]bool{}
	for tick := uint64(0); tick < 60*60 && (flown[true] < 2 || flown[false] < 2); tick++ {
		i.Step(tick, nil)
		for _, m := range i.flying {
			radar := m.radar != nil
			if !seen[m] {
				seen[m] = true
				launched[radar]++
			}
			flown[radar] = math.Max(flown[radar], m.flew)
		}
	}
	for _, radar := range []bool{true, false} {
		kind := map[bool]string{true: "AMRAAM", false: "heater"}[radar]
		if launched[radar] == 0 {
			t.Errorf("no %s was in the air at the end of any tick in a minute: none was fired, or the sea took each the tick it left", kind)
		} else if flown[radar] < 2 {
			t.Errorf("%d %ss launched, the longest flew %.2f s: the sea took them in the air", launched[radar], kind, flown[radar])
		}
	}

	// 100 km north of Midway the sea lies 785 m under the level there: the sea
	// kills a jet two metres above it and not one 500 m up.
	for _, c := range []struct {
		altitude float64
		alive    bool
	}{{2, false}, {500, true}} {
		i := build(t, "furball", nil, 1)
		a := i.aircraft[0]
		laid(i, a, flight.Place{Latitude: (28.2334 + 0.9) * math.Pi / 180, Longitude: -177.3668 * math.Pi / 180, Altitude: c.altitude}, 0, 200)
		i.Step(1, nil)
		if a.alive != c.alive {
			t.Errorf("%.0f m above the sea 100 km north of Midway: alive %v, want %v", c.altitude, a.alive, c.alive)
		}
	}
}

// TestCloudsAtTheirHeight: a jet 11 km off at my height is seen over a cloud
// layer whose top lies 5 m below us both, and one 20 m lower, inside it, is
// not. Read from the frame's y he would stand 9.5 m lower than he is, inside
// the layer, and the cloud would hide him.
func TestCloudsAtTheirHeight(t *testing.T) {
	layers["test"] = layer{base: 2500, top: 2995, cover: 1}
	defer delete(layers, "test")
	here := scene.Place(flight.Vec3{})
	here.Altitude = 3000
	for _, c := range []struct {
		below float64
		seen  bool
	}{{0, true}, {20, false}} {
		i := build(t, "furball", nil, 2)
		i.sky, i.night = "test", false
		me, him := i.aircraft[0], i.aircraft[1]
		there := here
		there.Longitude += 11000 / (flight.Equator * math.Cos(here.Latitude))
		there.Altitude -= c.below
		laid(i, me, here, 0, 200)
		laid(i, him, there, math.Pi, 200)
		if seen := i.visible(me, him, 0); seen != c.seen {
			t.Errorf("a jet 11 km off %.0f m below me, the cloud's top 5 m below me: seen %v, want %v", c.below, seen, c.seen)
		}
	}
}

// TestFrameUnderTheBot: a bot thinks in a frame of its own laid on the sea
// under it, keeps it while it flies within a kilometre of the frame's anchor
// and has it laid again under it beyond, so its own y is its height to about
// a decimetre: the curve's 8 cm in that kilometre and the geoid's slope. A frame on a fixed grid
// would put a bot anywhere in its cell, as far as 15 km from the anchor.
func TestFrameUnderTheBot(t *testing.T) {
	i := build(t, "furball", map[string]any{"bots": map[string]any{"expert": 2.0}}, 0)
	frames := map[flight.Vec3]bool{}
	flying := map[int]bool{} // alive the tick before: a jet respawns after the bots have thought
	for tick := uint64(0); tick < 20*60; tick++ {
		i.Step(tick, nil)
		for _, slot := range i.slots() {
			a := i.aircraft[slot]
			was := flying[slot]
			flying[slot] = a.alive
			if !a.bot || !a.alive || !was {
				continue
			}
			frame := a.brain.frame
			if !frame.Tangent() {
				t.Fatalf("tick %d: slot %d thinks in %v, not a frame laid on the sea", tick, slot, frame)
			}
			frames[frame.Centred(flight.Vec3{})] = true
			p := flight.Between(i.environment.Theatre(), frame).Point(a.model.State.Position)
			if off := math.Hypot(p.X, p.Z); off > reach+10 {
				t.Fatalf("tick %d: slot %d is %.0f m from the anchor of the frame it thinks in", tick, slot, off)
			}
			if miss := p.Y - i.environment.Theatre().Altitude(a.model.State.Position); math.Abs(miss) > 0.15 {
				t.Fatalf("tick %d: slot %d's y in its frame misses its height by %.2f m", tick, slot, miss)
			}
		}
	}
	if len(frames) < 4 {
		t.Errorf("two bots flew twenty seconds in %d frames: the frame is never laid again under them", len(frames))
	}
}

// TestSunStandsWhereItDid: the day's sun stands 60 degrees up due south over
// the chart, and at that one instant it lies the same way from everywhere:
// 1,000 km east of the chart it stands lower and west of south, and on the
// far side of the earth it has set.
func TestSunStandsWhereItDid(t *testing.T) {
	here := scene.Place(flight.Vec3{})
	at := func(east float64) flight.Vec3 { // the sun in the level frame `east` metres east of the chart's anchor
		return sunward(flight.Tangent(here.Latitude, here.Longitude+east/(flight.Equator*math.Cos(here.Latitude))), scene)
	}
	if over := at(0); math.Abs(over.Y-0.866) > 0.001 || math.Abs(over.Z-0.5) > 0.001 || math.Abs(over.X) > 0.001 {
		t.Errorf("over the chart the day's sun stands at %v, want 60 degrees up due south", over)
	}
	if east := at(1000000); east.Y >= at(0).Y || east.X >= 0 { // at Midway ten degrees of longitude, the sun 40 minutes on and 58 degrees up
		t.Errorf("1,000 km east the day's sun stands at %v, want it lower and in the west", east)
	}
	if far := sunward(flight.Tangent(-here.Latitude, here.Longitude+math.Pi), scene); far.Y > 0 {
		t.Errorf("on the far side of the earth the day's sun stands %.0f degrees up", math.Asin(far.Y)*180/math.Pi)
	}
}

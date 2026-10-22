// Mochi world: the BVR stand-in pilot, and the jousts it flies against each tier
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/flight"
	"world/games/air/round"
)

// lancer flies a BVR joust on the Fox 3 fit as a pilot flies it from the
// cockpit, and stands in for the user beyond visual range as mimic does inside
// it. Inside the merge it hands the jet to mimic.
//
// It knows what the cockpit shows and nothing more. The bandit's position comes
// from its own radar, painted inside the 70 degree gimbal and the detection range
// and out of the notch, and held by an STT to 1.15 times that range and four
// seconds of memory, as radar.ts holds it. Otherwise it flies the last look
// carried on. An inbound AMRAAM is known from when its seeker wakes, when the
// RWR calls MISSILE, until it has gone by, and a heater only from its smoke
// inside 4 km. There is no launch warning: the spike of his STT is all it has
// before that.
//
// Every variant answers the warning with a round of its own when it holds a lock
// inside the zone, and then beams the inbound one low against the sea. The
// variants differ in what a pilot chooses before that: where in the zone to
// shoot, whether to crank while the round wants support, and whether to crank
// as soon as he is spiked inside the zone. Turning cold at the warning was
// flown and lost five fights in six to the intermediate without ever closing
// inside 16 km: by the time a seeker wakes the round has too little range left
// to outrun.
type lancer struct {
	depth float64 // the shot's place in the zone: 0 the no-escape range, 1 the maximum
	crank bool    // offsets the nose while its own round wants support
	wary  bool    // offsets the nose while he is spiked inside the zone, short of its shot
	react float64 // seconds from the missile warning to the defence
	fight *hornet // the fight inside the merge

	block    float64     // the altitude it holds, m: its own at the start
	position flight.Vec3 // the picture: where it last saw him
	velocity flight.Vec3 // flying how
	seen     uint64      // and when
	locked   bool        // its STT holds him
	starved  uint64      // one past the tick the lock went starved (0: fed)
	zone     round.Zone  // the zone the HUD shows, refreshed each second
	zoned    uint64      // one past the tick it was read (0: never)
	warned   uint64      // one past the tick the warning sounded (0: quiet)
	beam     flight.Vec3 // the beam being flown
	squared  uint64      // one past the tick it was chosen (0: none)
	chaffed  uint64      // one past the tick of the last bundle (0: none)
	side     float64     // the crank's side, chosen at the shot: +1 or -1
	merged   bool        // inside the merge: mimic has the jet
	mode     string      // what it flew this tick, for the trace
}

// lancers are the stand-in's variants, named for the trace and the tally.
var lancers = []struct {
	name string
	make func() *lancer
}{
	// Shoots near the maximum range and keeps coming, as a pilot new to the
	// AMRAAM does: the round goes early and the jet follows it in.
	{"early", func() *lancer { return &lancer{depth: 0.9, react: 1.5, fight: mimic(1)} }},
	// Holds the shot for the no-escape range and cranks while the round wants
	// support.
	{"deep", func() *lancer { return &lancer{depth: 0, crank: true, react: 1, fight: mimic(1)} }},
	// Shoots in the middle of the zone, cranks, and offsets as soon as his STT is
	// on it inside the zone.
	{"wary", func() *lancer { return &lancer{depth: 0.6, crank: true, wary: true, react: 1, fight: mimic(-1)} }},
}

// turned is a horizontal direction turned by angle radians about the vertical,
// the sense hunt.go's crank uses.
func turned(direction flight.Vec3, angle float64) flight.Vec3 {
	sin, cos := math.Sin(angle), math.Cos(angle)
	return flight.Vec3{X: direction.X*cos - direction.Z*sin, Z: direction.X*sin + direction.Z*cos}
}

// side is the crank's side, +1 or -1: the 50 degree offset from the line to
// him nearer the course the jet is on, so the turn into it is the shorter.
func side(line, velocity flight.Vec3) float64 {
	flat := flight.Vec3{X: line.X, Z: line.Z}.Normalize()
	if turned(flat, -0.87).Dot(velocity) > turned(flat, 0.87).Dot(velocity) {
		return -1
	}
	return 1
}

// climb points a horizontal direction with the climb or descent that walks the
// jet back to its block.
func climb(direction flight.Vec3, altitude, block float64) flight.Vec3 {
	d := flight.Vec3{X: direction.X, Z: direction.Z}.Normalize()
	d.Y = clamp((block-altitude)/4000, -0.1, 0.25)
	return d.Normalize()
}

// steer is the stick that turns the flight path onto want with no more than
// budget g: the lift that holds the path against gravity, a turn toward want on
// top of it sized by how far off it is, the lift vector rolled onto that, and
// the pull let in as it arrives. The pull goes through the law's own envelope,
// so the g asked for is the g flown.
func steer(m *flight.Model, want flight.Vec3, budget float64) (pitch, roll float64) {
	s := &m.State
	air := s.Velocity.Subtract(m.Gust())
	speed := air.Length()
	if speed < 1 {
		return 0, 0
	}
	path := air.Scale(1 / speed)
	up := s.Attitude.Rotate(flight.Vec3{Y: 1})
	right := s.Attitude.Rotate(flight.Vec3{Z: 1})
	// The turn is cut to the budget, never the 1 g against gravity: cutting the
	// two together let a 3 g reversal sink 5,400 m.
	lift := flight.Vec3{Y: 1}.Subtract(path.Scale(path.Y))
	if across := want.Subtract(path.Scale(path.Dot(want))); across.Length() > 1e-6 {
		angle := math.Acos(clamp(path.Dot(want), -1, 1))
		turn := across.Normalize().Scale(angle * 0.5 * speed / 9.80665)
		if lift.Add(turn).Length() > budget {
			// The share k of the turn that brings |lift + k turn| to the budget.
			b, c := lift.Dot(turn), lift.Dot(lift)-budget*budget
			turn = turn.Scale(math.Max(0, (-b+math.Sqrt(math.Max(0, b*b-turn.Dot(turn)*c)))/turn.Dot(turn)))
		}
		lift = lift.Add(turn)
	}
	if lift.Length() > 0.3 {
		roll = clamp(math.Atan2(lift.Dot(right), lift.Dot(up))*0.8, -1, 1)
	}
	demand := math.Max(lift.Dot(up), 0.3)
	level, ceiling := m.Envelope(roll, false)
	if demand >= level {
		pitch = (demand - level) / math.Max(ceiling-level, 0.1)
	} else {
		pitch = (demand - level) / math.Max(level-m.Airframe.Limit.Negative, 0.1)
	}
	return clamp(pitch, -1, 1), roll
}

// look is the radar: whether the STT holds him after this tick, and the
// picture refreshed while it is fed.
func (l *lancer) look(i *instance, seat, bandit int, tick uint64) {
	a, b := i.aircraft[seat], i.aircraft[bandit]
	me, him := &a.model.State, &b.model.State
	line, span := i.bearing(me.Position, him.Position)
	inside := me.Attitude.Rotate(flight.Vec3{X: 1}).Dot(line) >= math.Cos(1.222) // radar.ts GIMBAL
	reach := i.detection(a, b)
	notched := round.Notched(me.Position, him.Position, him.Velocity, i.environment.Wrap)
	if l.locked {
		switch {
		case !inside || span > reach*radar_hold:
			l.locked = false
		case notched:
			if l.starved == 0 {
				l.starved = tick + 1
			}
			if tick+1-l.starved > radar_memory {
				l.locked = false
			}
		default:
			l.starved = 0
		}
	}
	if !l.locked {
		l.starved = 0
		if inside && span <= reach && !notched {
			l.locked = true
		}
	}
	if l.locked && l.starved == 0 {
		l.position, l.velocity, l.seen = him.Position, him.Velocity, tick
	}
}

// fly is one tick: the inputs, and the radar's mode and locked slot, as a
// client reports them.
func (l *lancer) fly(i *instance, seat, bandit int, tick uint64) (map[string]any, int, int) {
	a, b := i.aircraft[seat], i.aircraft[bandit]
	me, him := &a.model.State, &b.model.State
	wrap := i.environment.Wrap
	if l.block == 0 {
		l.block = me.Position.Y
		l.position, l.velocity, l.seen = him.Position, him.Velocity, tick // where the joust puts him is no secret
		l.side = 1
	}
	l.look(i, seat, bandit, tick)
	mode, lock := 1, -1
	if l.locked {
		mode, lock = 2, bandit
	}
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})
	_, span := i.bearing(me.Position, him.Position)
	where := l.position.Add(l.velocity.Scale(float64(tick-l.seen) / 60))
	toward, distance := i.bearing(me.Position, where)

	// What is in the air: its own round still wanting support, or still flying
	// at all, the nearest active seeker on it, and any heater close enough to
	// see.
	own, flying := false, false
	var seeker *missile
	nearest, heater := math.Inf(1), false
	for _, m := range i.flying {
		if m.shooter == seat && m.radar != nil && m.radar.Phase < round.Loose {
			flying = true
			own = own || m.radar.Phase < round.Pitbull
		}
		if m.target != seat {
			continue
		}
		line, d := i.bearing(me.Position, m.position)
		if m.radar == nil {
			if d < 4000 && m.velocity.Subtract(me.Velocity).Dot(line) < 0 {
				heater = true
			}
			continue
		}
		if i.incoming(m, me.Position, me.Velocity) && d < nearest {
			seeker, nearest = m, d
		}
	}
	if seeker == nil {
		l.warned, l.squared = 0, 0
	} else if l.warned == 0 {
		l.warned = tick + 1
	}
	defending := seeker != nil && float64(tick+1-l.warned) >= l.react*60 && span > 3000

	// The shot: locked and fed, the nose on him, inside its place in the zone and
	// beyond the minimum, and the last round seen to its end first. Shoot, look,
	// shoot: at the merge a round goes pitbull off the rail, and waiting only for
	// that rippled the rack in four seconds. The warning moves the place to the
	// zone's edge: a pilot about to defend answers with the round he has, as the
	// bot's counterfire does, or he defends with every round still aboard.
	if l.locked && l.starved == 0 && (l.zoned == 0 || tick+1-l.zoned >= 60) {
		l.zone = round.Ladder(fired(a), round.Target{Position: l.position, Velocity: l.velocity}, wrap)
		l.zoned = tick + 1
	}
	window := l.zone.Escape + l.depth*math.Max(l.zone.Max-l.zone.Escape, 0)
	if seeker != nil {
		window = l.zone.Max
	}
	shoot := false
	if l.locked && l.starved == 0 && !defending && !flying && a.amraams > 0 && a.release > 1.0 {
		line, _ := i.bearing(me.Position, him.Position)
		shoot = line.Dot(nose) >= math.Cos(15*math.Pi/180) && span <= window && span >= l.zone.Minimum
		if shoot {
			l.side = side(toward, me.Velocity)
		}
	}

	if !defending && !l.merged && (span < 2600 || heater) {
		l.merged = true
	} else if l.merged && span > 8000 && !heater {
		l.merged = false
	}
	if l.merged && !defending {
		l.fight.wind = a.model.Gust()
		data := l.fight.fly(me, him, inbound(i, seat), tick)
		data["radar"] = shoot
		l.mode = "fight"
		return data, mode, lock
	}

	data := map[string]any{"throttle": 1.0, "reheat": 0.0, "radar": shoot}
	var want flight.Vec3
	budget := 3.0
	flat := flight.Vec3{X: toward.X, Z: toward.Z}.Normalize()
	spiked := b.emitter == 2 && b.lock == seat // the RWR's lock: his STT is on it
	switch {
	case defending:
		// The beam nearer the course it is on, re-squared as the line turns, and
		// down until the round looks at it against the sea.
		line, d := i.bearing(me.Position, seeker.position)
		level := flight.Vec3{X: line.X, Z: line.Z}.Normalize()
		if l.squared == 0 || tick+1-l.squared >= 90 {
			l.beam = turned(level, math.Pi/2)
			if other := turned(level, -math.Pi/2); other.Dot(me.Velocity) > l.beam.Dot(me.Velocity) {
				l.beam = other
			}
			l.squared = tick + 1
		}
		want = l.beam
		if round.Clutter(seeker.position, me.Position, wrap) < 0.75 && me.Position.Y > 2000 {
			want = flight.Vec3{X: l.beam.X, Y: -0.35, Z: l.beam.Z}.Normalize()
		} else if me.Position.Y < 1500 {
			want = flight.Vec3{X: l.beam.X, Y: 0.05, Z: l.beam.Z}.Normalize()
		}
		budget = 5
		l.mode = "beam"
		if (l.chaffed == 0 || tick+1-l.chaffed >= 84) && d > round.Resolve && a.chaff > 0 &&
			round.Notched(seeker.position, me.Position, me.Velocity, wrap) {
			data["chaff"] = true
			l.chaffed = tick + 1
		}
	case l.crank && own && distance > radar_seam:
		want = climb(turned(flat, l.side*0.87), me.Position.Y, l.block)
		l.mode = "crank"
	case l.wary && spiked && distance <= l.zone.Max && distance > window && distance > radar_seam:
		if l.mode != "offset" {
			l.side = side(toward, me.Velocity)
		}
		want = climb(turned(flat, l.side*0.87), me.Position.Y, l.block)
		l.mode = "offset"
	case l.locked && a.amraams > 0 && !flying && distance <= l.zone.Max:
		// Inside the zone with a round to fire: the nose onto him, as the
		// steering dot asks, above or below the block.
		want = flight.Vec3{X: toward.X, Y: clamp(toward.Y, -0.5, 0.4), Z: toward.Z}.Normalize()
		l.mode = "point"
	default:
		want = climb(toward, me.Position.Y, l.block)
		l.mode = "press"
	}
	if l.mode == "point" || l.mode == "press" {
		// Military power to close on a jet coming at it, burner to run down one
		// that is not: in military power it held 17-20 km behind a novice dragging
		// in burner for the last 150 s of a fight and never closed.
		if me.Velocity.Subtract(l.velocity).Dot(toward) < 150 {
			data["reheat"] = 1.0
		}
	}
	data["pitch"], data["roll"] = steer(a.model, want, budget)
	return data, mode, lock
}

// quiver is the join request of a pilot who pressed the client's Fox 3 preset
// (stores.ts PRESETS and FUEL): six AMRAAMs, the wingtip heaters and the
// centreline tank, on 10,800 lb.
func quiver() map[string]any {
	fit := map[string]any{"fuel": 10800.0}
	for station, store := range map[string][2]string{"1": {"rail", "9m"}, "2": {"rail", "120c"}, "3": {"pylon", "120c"}, "4": {"rail", "120c"},
		"5": {"pylon", "tank"}, "6": {"rail", "120c"}, "7": {"pylon", "120c"}, "8": {"rail", "120c"}, "9": {"rail", "9m"}} {
		fit[station] = map[string]any{"fixture": store[0], "stores": []any{store[1]}}
	}
	return fit
}

// fray is one joust's account, from the stand-in's side.
type fray struct {
	outcome            string   // "won", "lost", "traded", or "" with both still flying
	seconds            float64  // how long it lasted
	spent, carried     [2]int   // AMRAAMs: the stand-in's, the bandit's
	merged             bool     // came inside 3 km
	full               [2]bool  // and came in with every AMRAAM aboard
	closest            float64  // m
	launches, outside  int      // the bandit's AMRAAMs, and those beyond its zone or inside its minimum
	support, midcourse int      // ticks a bandit round in midcourse at the stand-in had the bandit's STT, of all such ticks
	hot                int      // ticks the bandit flew at the stand-in with the stand-in's round in the air and no defence
	weapon             string   // what decided it: the last hit inside the minute before, "amraam", "heater" or "gun", or "other" with none
	ends               [2][]end // how each side's AMRAAMs ended: the stand-in's, the bandit's
}

// end is how one AMRAAM's flight ended: the phase it was in, and its closest
// approach to its target, m (0: never measured).
type end struct {
	phase int
	least float64
}

// weapon names what hit a jet in a step that hit it: a round on it that left
// the air that step, or else the gun.
func weapon(before, after []*missile, victim int) string {
	gone := func(radar bool) bool {
		count := 0
		for _, m := range before {
			if m.target == victim && (m.radar != nil) == radar {
				count++
			}
		}
		for _, m := range after {
			if m.target == victim && (m.radar != nil) == radar {
				count--
			}
		}
		return count > 0
	}
	switch {
	case gone(true):
		return "amraam"
	case gone(false):
		return "heater"
	}
	return "gun"
}

// joust flies one BVR joust of the stand-in against a bot tier. It may run off
// the test's goroutine, so a failure is reported and the joust abandoned.
func joust(t *testing.T, level string, seed uint64, pilot *lancer, seconds int, trace bool) fray {
	made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("lance%s%d", level, seed),
		Game: "air", Mode: "joust", Seed: seed,
		Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{level: 1.0}}})
	if err != nil {
		t.Error(err)
		return fray{}
	}
	i := made.(*instance)
	defer i.Close()
	if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0, Stores: quiver()}); err != nil {
		t.Error(err)
		return fray{}
	}
	bandit := -1
	for slot, a := range i.aircraft {
		if a != nil && a.brain != nil {
			bandit = slot
		}
	}
	if bandit < 0 {
		t.Error("no bandit in the joust")
		return fray{}
	}
	me, him := i.aircraft[0], i.aircraft[bandit]
	f := fray{closest: math.Inf(1)}
	f.carried = [2]int{len(stores_amraams(me.loadout)), len(stores_amraams(him.loadout))}
	rack, magazine := him.amraams, me.amraams
	type struck struct {
		weapon string
		tick   uint64
	}
	hit := map[*craft]struck{} // the last weapon to hit each jet
	for tick := uint64(0); tick < uint64(seconds*60); tick++ {
		data, mode, lock := pilot.fly(i, 0, bandit, tick)
		i.Radar(0, mode, lock) // the report a client sends
		before := append([]*missile{}, i.flying...)
		i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
		f.seconds = float64(tick+1) / 60
		for _, m := range before {
			if m.radar == nil || slices.Contains(i.flying, m) {
				continue
			}
			side := 0
			if m.shooter == bandit {
				side = 1
			}
			f.ends[side] = append(f.ends[side], end{m.radar.Phase, m.radar.Least})
		}
		for _, c := range []*craft{me, him} {
			if c.condition.Damager >= 0 && c.condition.Damaged < 1.5/60 { // hit this step: the clock restarts at a hit
				hit[c] = struck{weapon(before, i.flying, c.player.Slot), tick}
			}
		}
		if f.outcome = ending(me, him); f.outcome != "" { // from the first jet's side: the stand-in's
			victim := him
			if f.outcome == "lost" {
				victim = me
			}
			f.weapon = "other"
			if last, found := hit[victim]; found && tick-last.tick < 3600 { // credit()'s minute
				f.weapon = last.weapon
			}
			break
		}
		line, span := i.bearing(him.model.State.Position, me.model.State.Position)
		f.closest = math.Min(f.closest, span)
		if span < 3000 && !f.merged {
			f.merged = true
			f.full = [2]bool{me.amraams == f.carried[0], him.amraams == f.carried[1]}
		}
		if him.amraams < rack {
			f.launches++
			zone := round.Ladder(fired(him), round.Target{Position: me.model.State.Position, Velocity: me.model.State.Velocity}, i.environment.Wrap)
			if span > zone.Max || span < zone.Minimum {
				f.outside++
			}
			if trace {
				fmt.Printf("  * t=%5.1f bandit AMRAAM at %5.0f m (zone %5.0f-%5.0f, escape %5.0f)\n", float64(tick)/60, span, zone.Minimum, zone.Max, zone.Escape)
			}
		}
		rack = him.amraams
		if trace && me.amraams < magazine {
			fmt.Printf("  * t=%5.1f lancer AMRAAM at %5.0f m (zone %5.0f-%5.0f, escape %5.0f)\n", float64(tick)/60, span, pilot.zone.Minimum, pilot.zone.Max, pilot.zone.Escape)
		}
		magazine = me.amraams
		mine, theirs := false, false
		for _, m := range i.flying {
			if m.radar == nil {
				continue
			}
			if m.shooter == bandit {
				theirs = true
				if m.target == 0 && m.radar.Phase == round.Midcourse {
					f.midcourse++
					if him.emitter == 2 && him.lock == 0 {
						f.support++
					}
				}
			}
			if m.shooter == 0 && m.target == bandit && m.radar.Phase < round.Loose {
				mine = true
			}
		}
		nose := him.model.State.Attitude.Rotate(flight.Vec3{X: 1})
		hot := mine && !theirs && span > radar_seam && nose.Dot(line) > math.Cos(15*math.Pi/180) &&
			him.brain.mode != "notch" && him.brain.mode != "drag"
		if hot {
			f.hot++
		}
		if trace && tick%120 == 0 {
			flags := ""
			if hot {
				flags += " HOT"
			}
			for _, m := range i.flying {
				if m.radar != nil && m.shooter == bandit && m.target == 0 && m.radar.Phase == round.Midcourse && !(him.emitter == 2 && him.lock == 0) {
					flags += " UNSUPPORTED"
					break
				}
			}
			fmt.Printf("  t=%5.0f range %6.0f | lancer %-5s %s %5.0f ft %3.0f kt msl %d | bandit %-7s %-8s %s %5.0f ft %3.0f kt msl %d | air %d%s\n",
				float64(tick)/60, span, pilot.mode, map[bool]string{true: "STT", false: "srch"}[pilot.locked],
				me.model.State.Position.Y*3.281, me.model.State.Velocity.Length()*1.944, me.amraams,
				him.brain.mode, him.brain.play, map[bool]string{true: "STT", false: "srch"}[him.emitter == 2],
				him.model.State.Position.Y*3.281, him.model.State.Velocity.Length()*1.944, him.amraams, len(i.flying), flags)
		}
	}
	f.spent = [2]int{f.carried[0] - me.amraams, f.carried[1] - him.amraams}
	return f
}

// arm is one tier against one of the stand-in's variants, a joust a seed.
type arm struct {
	tier, variant string
	frays         []fray
}

// lances flies the arms named, seeds jousts each, six at a time: each joust is
// its own instance, closed when it ends, so they share nothing but the bot
// budget. traced, when not 0, is the seed whose fight is printed every two
// seconds, if only one arm flies.
func lances(t *testing.T, tiers, variants []string, seeds, seconds int, traced uint64) []*arm {
	var arms []*arm
	for _, tier := range tiers {
		for _, v := range lancers {
			if slices.Contains(variants, v.name) {
				arms = append(arms, &arm{tier: tier, variant: v.name, frays: make([]fray, seeds)})
			}
		}
	}
	gate := make(chan struct{}, 6)
	var group sync.WaitGroup
	for _, r := range arms {
		for _, v := range lancers {
			if v.name != r.variant {
				continue
			}
			for seed := uint64(1); seed <= uint64(seeds); seed++ {
				group.Add(1)
				go func(r *arm, build func() *lancer, seed uint64) {
					defer group.Done()
					gate <- struct{}{}
					defer func() { <-gate }()
					r.frays[seed-1] = joust(t, r.tier, seed, build(), seconds, traced == seed && len(arms) == 1)
				}(r, v.make, seed)
			}
		}
	}
	group.Wait()
	return arms
}

// tiers are the bandit's levels, weakest first.
var tiers = []string{"novice", "intermediate", "expert", "superhuman"}

// TestLancerLadder is #104's target as a gate, as TestScriptLadder is WVR's:
// beyond visual range the stand-in wins most fights against the novice, loses
// most against the intermediate, and loses all or almost all against the
// expert and the superhuman. 24 seeds of each of its three variants a tier.
// Measured 2026-10-21: novice 68-3, intermediate 20-52, expert 7-65,
// superhuman 9-63, from the stand-in's side.
func TestLancerLadder(t *testing.T) {
	heavy(t)
	names := []string{}
	for _, v := range lancers {
		names = append(names, v.name)
	}
	won, lost, fights := map[string]int{}, map[string]int{}, map[string]int{}
	for _, r := range lances(t, tiers, names, 24, 600, 0) {
		for _, f := range r.frays {
			fights[r.tier]++
			switch f.outcome {
			case "won":
				won[r.tier]++
			case "lost":
				lost[r.tier]++
			}
		}
	}
	for _, tier := range tiers {
		fmt.Printf("lance ladder %-12s stand-in won %2d lost %2d of %d\n", tier, won[tier], lost[tier], fights[tier])
		switch tier {
		case "novice":
			if won[tier] <= lost[tier] {
				t.Errorf("the novice lost %d and won %d: the user should win most fights against it", won[tier], lost[tier])
			}
		case "intermediate":
			if lost[tier] <= won[tier] {
				t.Errorf("the intermediate won %d and lost %d: the user should lose most fights against it", lost[tier], won[tier])
			}
		default:
			if won[tier] > fights[tier]/6 {
				t.Errorf("the %s lost %d of %d: the user should lose all or almost all fights against it", tier, won[tier], fights[tier])
			}
		}
	}
	if lost["intermediate"]-won["intermediate"] >= lost["expert"]-won["expert"] {
		t.Errorf("the intermediate's wins less its losses are %d and the expert's %d: the ladder does not climb",
			lost["intermediate"]-won["intermediate"], lost["expert"]-won["expert"])
	}
}

// TestLancerProbe flies the stand-in's variants against every tier, BVR, and
// prints the record each tier holds against it: the measure #104 tunes the
// bandit against. AIR_LANCE_SEEDS sets the seeds (default 8), AIR_LANCE_TIERS
// and AIR_LANCE_VARIANTS narrow the sweep (comma-separated), AIR_LANCE_SECONDS
// caps a fight (default 600), AIR_LANCE_EACH prints every fight's line, and
// AIR_LANCE_TRACE=<seed> prints that seed's fight every two seconds when the
// sweep is narrowed to one arm.
func TestLancerProbe(t *testing.T) {
	heavy(t)
	seeds := 8
	if v, err := strconv.Atoi(os.Getenv("AIR_LANCE_SEEDS")); err == nil && v > 0 {
		seeds = v
	}
	seconds := 600
	if v, err := strconv.Atoi(os.Getenv("AIR_LANCE_SECONDS")); err == nil && v > 0 {
		seconds = v
	}
	traced := uint64(0)
	if v, err := strconv.ParseUint(os.Getenv("AIR_LANCE_TRACE"), 10, 64); err == nil {
		traced = v
	}
	chosen := func(all []string, list string) []string {
		if list == "" {
			return all
		}
		var out []string
		for _, item := range strings.Split(list, ",") {
			if name := strings.TrimSpace(item); slices.Contains(all, name) {
				out = append(out, name)
			}
		}
		return out
	}
	names := []string{}
	for _, v := range lancers {
		names = append(names, v.name)
	}
	arms := lances(t, chosen(tiers, os.Getenv("AIR_LANCE_TIERS")), chosen(names, os.Getenv("AIR_LANCE_VARIANTS")), seeds, seconds, traced)
	for _, r := range arms {
		won, lost, traded, none, merged, full, fullHis, launches, outside, support, midcourse, hot, close := 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
		by := map[string]map[string]int{"won": {}, "lost": {}}
		spent, spentHis, carried, carriedHis, length, closest := 0, 0, 0, 0, 0.0, math.Inf(1)
		for _, f := range r.frays {
			if by[f.outcome] != nil {
				by[f.outcome][f.weapon]++
			}
			switch f.outcome {
			case "won":
				won++
			case "lost":
				lost++
			case "traded":
				traded++
			default:
				none++
			}
			if f.merged {
				merged++
				if f.full[0] {
					full++
				}
				if f.full[1] {
					fullHis++
				}
			}
			if f.closest < 150 {
				close++
			}
			spent += f.spent[0]
			spentHis += f.spent[1]
			carried += f.carried[0]
			carriedHis += f.carried[1]
			launches += f.launches
			outside += f.outside
			support += f.support
			midcourse += f.midcourse
			hot += f.hot
			length += f.seconds
			closest = math.Min(closest, f.closest)
		}
		if os.Getenv("AIR_LANCE_EACH") != "" {
			for k, f := range r.frays {
				fmt.Printf("  %s v %s seed %d: %-6s %-6s at %3.0f s, AMRAAMs %d v %d, closest %5.0f m\n",
					r.tier, r.variant, k+1, f.outcome, f.weapon, f.seconds, f.spent[0], f.spent[1], f.closest)
			}
		}
		n := float64(len(r.frays))
		share := 0.0
		if midcourse > 0 {
			share = 100 * float64(support) / float64(midcourse)
		}
		fmt.Printf("lance %-12s v %-9s won %2d lost %2d traded %d none %d | %3.0f s | AMRAAMs %.1f of %.0f v %.1f of %.0f | merged %2d, full %d v %d | bandit outside %d of %d, support %3.0f%%, hot %4.0f s | closest %4.0f m, inside 150 m %d\n",
			r.tier, r.variant, won, lost, traded, none, length/n,
			float64(spent)/n, float64(carried)/n, float64(spentHis)/n, float64(carriedHis)/n,
			merged, full, fullHis, outside, launches, share, float64(hot)/60, closest, close)
		fmt.Printf("         won by AMRAAM %d, heater %d, gun %d, other %d | lost to AMRAAM %d, heater %d, gun %d, other %d\n",
			by["won"]["amraam"], by["won"]["heater"], by["won"]["gun"], by["won"]["other"],
			by["lost"]["amraam"], by["lost"]["heater"], by["lost"]["gun"], by["lost"]["other"])
		for side, name := range []string{"stand-in", "bandit"} {
			var ends []end
			for _, f := range r.frays {
				ends = append(ends, f.ends[side]...)
			}
			fused, phases, misses := 0, [4]int{}, []float64{}
			for _, e := range ends {
				if e.least > 0 && e.least < round.Fuse {
					fused++
				}
				phases[e.phase]++
				if e.least > 0 {
					misses = append(misses, e.least)
				}
			}
			slices.Sort(misses)
			median := 0.0
			if len(misses) > 0 {
				median = misses[len(misses)/2]
			}
			fmt.Printf("         %s AMRAAMs ended %d: fused %d | in midcourse %d, active %d, pitbull %d, loose %d | median closest %.0f m\n",
				name, len(ends), fused, phases[round.Midcourse], phases[round.Active], phases[round.Pitbull], phases[round.Loose], median)
		}
	}
}

// bench is two jets for the stand-in's own tests, on the Fox 3 fit: the
// stand-in in slot 0 at 6,000 m heading east at 260 m/s, and in slot 1 a
// bandit where the caller puts it, flying level along heading.
func bench(position, heading flight.Vec3) (*instance, *craft, *craft) {
	i := &instance{aircraft: map[int]*craft{}, environment: flight.Environment{Seed: 1}, missiles: true, started: true}
	for slot, at := range [2][2]flight.Vec3{{{Y: 6000}, {X: 1}}, {position, heading}} {
		m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: 0})
		m.State = flight.Level(m, at[0], at[1].Normalize(), 260, 4000)
		a := &craft{model: m, alive: true, lock: -1, flared: 1e9, clouded: 1e9, loadout: stores_grant(quiver(), "open")}
		a.arm()
		i.aircraft[slot] = a
	}
	return i, i.aircraft[0], i.aircraft[1]
}

// sortie flies the stand-in on the bench for seconds, the bandit straight on at
// full power. The rounds rounds() names are in the air for the stand-in to see
// each tick and taken out before the step, so they stay where the test put
// them.
func sortie(i *instance, l *lancer, seconds float64, rounds func(tick uint64) []*missile) {
	for tick := uint64(0); tick < uint64(seconds*60); tick++ {
		if rounds != nil {
			i.flying = rounds(tick)
		}
		data, mode, lock := l.fly(i, 0, 1, tick)
		i.aircraft[0].emitter, i.aircraft[0].lock = mode, lock
		if rounds != nil {
			i.flying = nil
		}
		i.Step(tick, map[int][]game.Input{0: {{Data: data}}, 1: {{Data: map[string]any{"throttle": 1.0}}}})
	}
}

// amraam is a radar round at position, its seeker in the phase given, from
// shooter at target.
func amraam(shooter, target int, position flight.Vec3, phase int) *missile {
	return &missile{shooter: shooter, target: target, position: position, life: round.Battery,
		radar: &round.Model{Position: position, Phase: phase, Life: round.Battery}}
}

// off is the angle in degrees between a jet's course and the line from it to a
// point, both taken level.
func off(s *flight.State, point flight.Vec3) float64 {
	course := flight.Vec3{X: s.Velocity.X, Z: s.Velocity.Z}.Normalize()
	line := flight.Vec3{X: point.X - s.Position.X, Z: point.Z - s.Position.Z}.Normalize()
	return math.Acos(clamp(course.Dot(line), -1, 1)) * 180 / math.Pi
}

// TestLancerSteer: the stand-in's stick turns the flight path onto the
// direction it wants inside the g it is allowed, and holds its height doing it.
func TestLancerSteer(t *testing.T) {
	for _, c := range []struct {
		name string
		want flight.Vec3
	}{
		{"ahead", flight.Vec3{X: 1}},
		{"abeam", flight.Vec3{Z: 1}},
		{"behind", flight.Vec3{X: -1, Z: 0.05}.Normalize()},
	} {
		i, me, _ := bench(flight.Vec3{X: 200000, Y: 6000}, flight.Vec3{X: -1})
		peak := 0.0
		for tick := uint64(0); tick < 60*60; tick++ {
			pitch, roll := steer(me.model, c.want, 3)
			i.Step(tick, map[int][]game.Input{0: {{Data: map[string]any{"pitch": pitch, "roll": roll, "throttle": 1.0}}}})
			peak = math.Max(peak, me.model.Nz())
		}
		path := me.model.State.Velocity.Normalize()
		if miss := math.Acos(clamp(path.Dot(c.want), -1, 1)) * 180 / math.Pi; miss > 3 {
			t.Errorf("%s: the flight path ended %.1f degrees off the direction wanted", c.name, miss)
		}
		if lost := me.model.State.Position.Y - 6000; math.Abs(lost) > 300 {
			t.Errorf("%s: the turn moved the jet %.0f m off its height", c.name, lost)
		}
		if peak > 3.5 {
			t.Errorf("%s: the turn pulled %.1f g on a 3 g budget", c.name, peak)
		}
	}
}

// TestLancerRadar: the stand-in's picture is its radar's - a lock inside the
// gimbal and the detection range, none outside them, and a lock starved by the
// notch held on memory for four seconds with the picture frozen, then dropped.
func TestLancerRadar(t *testing.T) {
	for _, c := range []struct {
		name     string
		position flight.Vec3
		locked   bool
	}{
		{"ahead at 30 km", flight.Vec3{X: 30000, Y: 6000}, true},
		{"abeam at 30 km", flight.Vec3{Z: 30000, Y: 6000}, false},
		{"ahead at 95 km", flight.Vec3{X: 95000, Y: 6000}, false},
	} {
		i, _, _ := bench(c.position, flight.Vec3{X: -1})
		l := &lancer{}
		l.look(i, 0, 1, 0)
		if l.locked != c.locked {
			t.Errorf("%s: locked %v, want %v", c.name, l.locked, c.locked)
		}
	}
	i, _, him := bench(flight.Vec3{X: 20000, Y: 6000}, flight.Vec3{X: -1})
	l := &lancer{}
	l.look(i, 0, 1, 0)
	him.model.State.Position.Y = 300 // he dives to the sea and beams it
	him.model.State.Velocity = flight.Vec3{Z: 260}
	for tick := uint64(1); tick <= radar_memory+1; tick++ {
		l.look(i, 0, 1, tick)
		if !l.locked {
			t.Fatalf("the starved lock dropped at %.2f s, inside the memory", float64(tick)/60)
		}
	}
	if l.position.Y != 6000 || l.seen != 0 {
		t.Errorf("the picture moved while the lock was starved: %.0f m high, seen at tick %d", l.position.Y, l.seen)
	}
	l.look(i, 0, 1, radar_memory+2)
	if l.locked {
		t.Errorf("the starved lock outlived its memory")
	}
	i, _, him = bench(flight.Vec3{X: 20000, Y: 6000}, flight.Vec3{X: -1})
	l = &lancer{}
	l.look(i, 0, 1, 0)
	him.model.State.Position = flight.Vec3{Z: 20000, Y: 6000} // past the gimbal, in the sky and fed
	if l.look(i, 0, 1, 1); l.locked {
		t.Errorf("the lock held him 90 degrees off the nose")
	}
}

// TestLancerShot: the stand-in shoots at its place in the zone with the nose on
// him, answers the missile warning from anywhere inside the zone, and looks
// before it shoots again.
func TestLancerShot(t *testing.T) {
	_, me, him := bench(flight.Vec3{X: 40000, Y: 6000}, flight.Vec3{X: -1})
	zone := round.Ladder(fired(me), round.Target{Position: him.model.State.Position, Velocity: him.model.State.Velocity}, 0)
	if zone.Escape <= 2*zone.Minimum || zone.Max <= 1.5*zone.Escape {
		t.Fatalf("the bench's zone has no room to test in: %+v", zone)
	}
	middle := (zone.Escape + zone.Max) / 2
	for _, c := range []struct {
		name   string
		depth  float64
		span   float64
		angle  float64 // degrees his bearing sits off the nose
		warned bool
		flying int // a round of its own still in the air, in this phase (-1: none)
		shoots bool
	}{
		{"deep inside the no-escape range", 0, 0.9 * zone.Escape, 0, false, -1, true},
		{"deep short of it", 0, middle, 0, false, -1, false},
		{"deep short of it, warned", 0, middle, 0, true, -1, true},
		{"early near the maximum", 0.9, zone.Escape + 0.85*(zone.Max-zone.Escape), 0, false, -1, true},
		{"deep 30 degrees off the nose", 0, 0.9 * zone.Escape, 30, false, -1, false},
		{"deep with its round gone pitbull", 0, 0.9 * zone.Escape, 0, false, round.Pitbull, false},
		{"deep with its round gone loose", 0, 0.9 * zone.Escape, 0, false, round.Loose, true},
	} {
		angle := c.angle * math.Pi / 180
		where := flight.Vec3{X: c.span * math.Cos(angle), Y: 6000, Z: c.span * math.Sin(angle)}
		i, _, _ := bench(where, flight.Vec3{X: -where.X, Z: -where.Z})
		l := &lancer{depth: c.depth, react: 1, fight: mimic(1)}
		if c.warned {
			i.flying = append(i.flying, amraam(1, 0, where.Scale(0.5), round.Active))
		}
		if c.flying >= 0 {
			i.flying = append(i.flying, amraam(0, 1, where.Scale(0.5), c.flying))
		}
		data, _, _ := l.fly(i, 0, 1, 0)
		if shot, _ := data["radar"].(bool); shot != c.shoots {
			t.Errorf("%s (%.0f m, zone %.0f-%.0f): shoots %v, want %v", c.name, c.span, zone.Escape, zone.Max, shot, c.shoots)
		}
	}
}

// TestLancerBeam: at the missile warning the stand-in beams the round after its
// reaction time, down until the round sees it against the sea, and dispenses
// chaff while it sits in the round's notch.
func TestLancerBeam(t *testing.T) {
	for _, c := range []struct {
		name    string
		height  float64 // the round's
		dive    bool    // it must go down to put the sea behind it
		seconds float64
	}{
		{"round above", 9000, false, 20},
		{"round below", 4000, true, 40},
	} {
		i, me, _ := bench(flight.Vec3{X: 60000, Y: 6000}, flight.Vec3{X: -1})
		l := &lancer{depth: 0, crank: true, react: 1, fight: mimic(1)}
		where := flight.Vec3{X: 15000, Y: c.height}
		bundles, early := me.chaff, ""
		sortie(i, l, c.seconds, func(tick uint64) []*missile {
			if tick == 50 {
				early = l.mode
			}
			m := amraam(1, 0, where, round.Active)
			m.velocity = me.model.State.Position.Subtract(where).Normalize().Scale(900) // inbound
			return []*missile{m}
		})
		if early == "beam" {
			t.Errorf("%s: beaming 0.8 s after the warning, inside the reaction time", c.name)
		}
		if l.mode != "beam" {
			t.Errorf("%s: flying %q at the warning, not the beam", c.name, l.mode)
		}
		if a := off(&me.model.State, where); math.Abs(a-90) > 20 {
			t.Errorf("%s: the round sits %.0f degrees off the course, not on the beam", c.name, a)
		}
		if height := me.model.State.Position.Y; c.dive != (height < 5000) {
			t.Errorf("%s: ended at %.0f m from 6,000, dive wanted %v", c.name, height, c.dive)
		}
		if behind := round.Clutter(where, me.model.State.Position, 0); behind < 0.5 {
			t.Errorf("%s: the round still sees it against the sky (clutter %.2f)", c.name, behind)
		}
		if spent := bundles - me.chaff; spent < 3 {
			t.Errorf("%s: %d chaff bundles in the beam", c.name, spent)
		}
	}
}

// TestLancerCrank: while its round wants support the cranking stand-in flies
// 50 degrees off the line to him, and one that does not crank keeps pointing.
func TestLancerCrank(t *testing.T) {
	for _, c := range []struct {
		name  string
		crank bool
		low   float64
		high  float64
	}{
		{"cranking", true, 40, 60},
		{"pressing", false, 0, 8},
	} {
		i, me, him := bench(flight.Vec3{X: 60000, Y: 6000}, flight.Vec3{X: -1})
		l := &lancer{depth: 0, crank: c.crank, react: 1, fight: mimic(1)}
		sortie(i, l, 20, func(uint64) []*missile {
			return []*missile{amraam(0, 1, him.model.State.Position.Scale(0.5), round.Midcourse)}
		})
		if a := off(&me.model.State, him.model.State.Position); a < c.low || a > c.high {
			t.Errorf("%s: he sits %.0f degrees off the course, want %.0f-%.0f", c.name, a, c.low, c.high)
		}
	}
}

// TestLancerWary: the wary stand-in offsets while his STT is on it inside the
// zone and short of its own shot, and flies at him when he is not looking.
func TestLancerWary(t *testing.T) {
	_, me, him := bench(flight.Vec3{X: 50000, Y: 6000}, flight.Vec3{X: -1})
	zone := round.Ladder(fired(me), round.Target{Position: him.model.State.Position, Velocity: him.model.State.Velocity}, 0)
	window := zone.Escape + 0.6*(zone.Max-zone.Escape)
	start := (window + zone.Max) / 2
	if start-window < 6000 {
		t.Fatalf("the bench's zone has no room between the shot and the maximum: %+v", zone)
	}
	for _, spiked := range []bool{true, false} {
		i, me, him := bench(flight.Vec3{X: start, Y: 6000}, flight.Vec3{X: -1})
		if spiked {
			him.emitter, him.lock = 2, 0
		}
		l := &lancer{depth: 0.6, crank: true, wary: true, react: 1, fight: mimic(-1)}
		sortie(i, l, 10, nil)
		a := off(&me.model.State, him.model.State.Position)
		if spiked && (l.mode != "offset" || a < 30) {
			t.Errorf("spiked inside the zone: flying %q with him %.0f degrees off the course", l.mode, a)
		}
		if !spiked && (l.mode == "offset" || a > 8) {
			t.Errorf("not spiked: flying %q with him %.0f degrees off the course", l.mode, a)
		}
	}
}

// TestLancerMerge: inside the merge the stand-in hands the jet to mimic, a
// heater in sight does the same, and the BVR law takes it back once the jets
// are well apart again.
func TestLancerMerge(t *testing.T) {
	i, _, _ := bench(flight.Vec3{X: 2000, Y: 6000}, flight.Vec3{X: -1})
	l := &lancer{depth: 0, react: 1, fight: mimic(1)}
	if l.fly(i, 0, 1, 0); l.mode != "fight" {
		t.Errorf("inside the merge flying %q, not mimic's fight", l.mode)
	}
	i, me, _ := bench(flight.Vec3{X: 20000, Y: 6000}, flight.Vec3{X: -1})
	l = &lancer{depth: 0, react: 1, fight: mimic(1)}
	heater := &missile{shooter: 1, target: 0, position: flight.Vec3{X: 3000, Y: 6000}, velocity: flight.Vec3{X: -600}}
	i.flying = []*missile{heater}
	if l.fly(i, 0, 1, 0); l.mode != "fight" {
		t.Errorf("a heater 3 km out flying %q, not mimic's fight", l.mode)
	}
	i.flying = nil
	me.model.State.Position.X = 11000 // he has gone 9 km away
	if l.fly(i, 0, 1, 1); l.mode == "fight" {
		t.Errorf("9 km apart and still in mimic's fight")
	}
}

// TestLancerChase: the stand-in runs a fleeing jet down in burner and closes on
// one coming at it in military power.
func TestLancerChase(t *testing.T) {
	for _, c := range []struct {
		name    string
		heading flight.Vec3
		reheat  float64
	}{
		{"fleeing", flight.Vec3{X: 1}, 1},
		{"coming", flight.Vec3{X: -1}, 0},
	} {
		i, _, _ := bench(flight.Vec3{X: 20000, Y: 6000}, c.heading)
		l := &lancer{depth: 0, react: 1, fight: mimic(1)}
		if data, _, _ := l.fly(i, 0, 1, 0); data["reheat"] != c.reheat {
			t.Errorf("%s: reheat %v, want %v", c.name, data["reheat"], c.reheat)
		}
	}
}

// TestLancerPoint: inside its shot with him well below the block, the stand-in
// puts the nose on him and takes it, where holding the block left him outside
// the shot's cone for a whole run-in.
func TestLancerPoint(t *testing.T) {
	_, me, him := bench(flight.Vec3{X: 14000, Y: 2500}, flight.Vec3{X: -1})
	zone := round.Ladder(fired(me), round.Target{Position: him.model.State.Position, Velocity: him.model.State.Velocity}, 0)
	if zone.Escape < 15000 {
		t.Fatalf("the bench's no-escape range is %.0f m, inside the 14 km it starts at", zone.Escape)
	}
	i, me, _ := bench(flight.Vec3{X: 14000, Y: 2500}, flight.Vec3{X: -1})
	l := &lancer{depth: 0, react: 1, fight: mimic(1)}
	rack := me.amraams
	sortie(i, l, 8, nil)
	if me.amraams != rack-1 {
		t.Errorf("fired %d rounds at him 3.5 km below inside the no-escape range, want 1", rack-me.amraams)
	}
}

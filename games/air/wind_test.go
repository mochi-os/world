// Mochi world: Air game module tests - the match's wind, and the weapons in it
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/battle"
	"world/games/air/flight"
	"world/games/air/round"
)

// TestMatchWind: a match flies single player's wind, the trades - 25 kt from
// 070 - every jet in it flies that air, and the welcome hands it to the
// client's core, whose prediction must fly the same air as the server.
func TestMatchWind(t *testing.T) {
	i, welcome := underway_match(t)
	speed := i.environment.Wind.Length()
	from := math.Mod(math.Atan2(i.environment.Wind.X, -i.environment.Wind.Z)*180/math.Pi+180+360, 360) // the wind points where the air goes: it comes from the reciprocal
	if math.Abs(speed-12.9) > 1e-9 || math.Abs(from-70) > 1e-9 || i.environment.Wind.Y != 0 {
		t.Fatalf("the match blows %.2f m/s from %.1f°, not single player's 12.9 m/s from 070°", speed, from)
	}
	wind, _ := welcome["wind"].([]float64)
	if len(wind) != 3 || wind[0] != i.environment.Wind.X || wind[1] != i.environment.Wind.Y || wind[2] != i.environment.Wind.Z {
		t.Fatalf("the welcome carries the wind %v, the match blows %v", welcome["wind"], i.environment.Wind)
	}
	for slot, a := range i.aircraft {
		if a.model.Environment.Wind != i.environment.Wind {
			t.Errorf("slot %d flies the wind %v, the match blows %v", slot, a.model.Environment.Wind, i.environment.Wind)
		}
	}
}

// blowing sets a test range's air: the session's and every jet's.
func blowing(i *instance, wind flight.Vec3) {
	i.environment.Wind = wind
	for _, a := range i.aircraft {
		a.model.Environment.Wind = wind
	}
}

// TestRailInTheWind: a round leaves the rail at the jet's speed THROUGH THE
// AIR along the nose, and the wind carries it as it carries the jet - the
// AMRAAM's departure and the heater's separation alike, and the heater zone's
// launcher. Over the ground, a jet flying into a wind fired its rounds slower
// through the air than it flew, and one flying downwind faster.
func TestRailInTheWind(t *testing.T) {
	place, velocity := aim(900, 0, 0, 200)
	i, _, _ := missileRange(t, place, velocity)
	i.flying = i.flying[:0]
	a := i.aircraft[0]
	nose := a.model.State.Attitude.Rotate(flight.Vec3{X: 1}).Normalize()
	blowing(i, flight.Vec3{})
	a.model.State.Velocity = nose.Scale(250)
	still := departure(a)
	blowing(i, flight.Vec3{X: -15, Z: 6})
	wind := flight.Wind(a.model.State.Position, a.model.State.Time, i.environment)
	a.model.State.Velocity = nose.Scale(250).Add(wind)
	if off := departure(a).Subtract(wind).Subtract(still); off.Length() > 1e-9 {
		t.Errorf("the AMRAAM leaves the rail %.3f m/s through the air off its still-air departure", off.Length())
	}
	if !i.launch(0, a, false) {
		t.Fatal("the shooter could not acquire the target dead ahead")
	}
	if off := i.flying[len(i.flying)-1].velocity.Subtract(wind).Subtract(nose.Scale(280)); off.Length() > 1e-9 {
		t.Errorf("the heater leaves the rail %.3f m/s through the air off the jet's own speed and the rail's 30", off.Length())
	}
	if off := launcher(&a.model.State, i.environment).Velocity.Subtract(wind).Subtract(nose.Scale(250)); off.Length() > 1e-9 {
		t.Errorf("the heater zone launches %.3f m/s through the air off the jet's airspeed along the nose", off.Length())
	}
}

// heater fires one AIM-9 from launch() at a target `span` metres along line
// from a shooter at 3,000 m flying line at 250 m/s through the air, the target
// flying `way` at 200 m/s through it, in the wind given. It returns the time
// the round flew and its closest approach.
func heater(t *testing.T, wind flight.Vec3, line flight.Vec3, way flight.Vec3, span float64) (float64, float64) {
	t.Helper()
	place, velocity := aim(span, 0, 0, 200)
	i, target, _ := missileRange(t, place, velocity)
	i.flying = i.flying[:0]
	blowing(i, wind)
	shooter := i.aircraft[0]
	shooter.model.State.Position = flight.Vec3{Y: 3000}
	shooter.model.State.Attitude = flight.Look(line)
	shooter.model.State.Velocity = line.Scale(250).Add(flight.Wind(shooter.model.State.Position, 0, i.environment))
	target.model.State.Position = shooter.model.State.Position.Add(line.Scale(span))
	target.model.State.Attitude = flight.Look(way)
	if !i.launch(0, shooter, false) || i.flying[len(i.flying)-1].target != 1 {
		t.Fatalf("the shooter could not acquire the target %.0f m along %v", span, line)
	}
	m := i.flying[len(i.flying)-1]
	dt, closest := 1.0/60, math.MaxFloat64
	for tick := 0; tick < int(missile_life*60); tick++ {
		clock := float64(tick) / 60
		target.model.State.Velocity = way.Scale(200).Add(flight.Wind(target.model.State.Position, clock, i.environment))
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
		relative := target.model.State.Position.Subtract(target.model.State.Velocity.Scale(dt)).Subtract(m.position) // the target at the round's time, as pursue reads it
		closure := target.model.State.Velocity.Subtract(m.velocity)
		step := 0.0
		if squared := closure.Dot(closure); squared > 1e-9 {
			step = clamp(-relative.Dot(closure)/squared, 0, dt)
		}
		closest = math.Min(closest, relative.Add(closure.Scale(step)).Length())
		i.pursue(dt, uint64(tick))
		if len(i.flying) == 0 {
			return m.flew, closest
		}
	}
	return m.flew, closest
}

// TestHeaterFliesTheAir: the AIM-9 flies through the air, so the same shot -
// the same jets at the same speeds through the air - flies alike downwind,
// upwind and in still air: the same time to the fuse, the same miss.
func TestHeaterFliesTheAir(t *testing.T) {
	east, west := flight.Vec3{X: 1}, flight.Vec3{X: -1}
	gust := flight.Vec3{X: 15}
	for _, s := range []struct {
		name      string
		line, way flight.Vec3
		span      float64
	}{
		{"tail chase", east, east, 1500},
		{"long tail chase", east, east, 2600},
		{"head-on", east, west, 700},
	} {
		still, near := heater(t, flight.Vec3{}, s.line, s.way, s.span)
		for _, x := range []float64{1, -1} {
			flew, closest := heater(t, gust.Scale(x), s.line, s.way, s.span)
			t.Logf("%s %s: %.2f s, closest %.1f m (still air %.2f s, %.1f m)", s.name, map[float64]string{1: "downwind", -1: "upwind"}[x], flew, closest, still, near)
			if (near < missile_fuse) != (closest < missile_fuse) || math.Abs(flew-still) > 0.05 {
				t.Errorf("the %s %s flew %.2f s and passed at %.1f m, in still air %.2f s at %.1f m", s.name, map[float64]string{1: "downwind", -1: "upwind"}[x], flew, closest, still, near)
			}
		}
	}
}

// TestHeatFliesTheAir: the heater's zone flies its round and target through
// the air as pursue() does. Its rungs are capped by the seeker's reach, so the
// flight itself is asked: the farthest range from which a round reaches a fast
// jet running away, or one breaking into the beam, the closest from which one
// turns onto a jet crossing, and how far off the nose a round still holds its
// target, are the same in a wind, downwind, upwind or across, as in still air.
func TestHeatFliesTheAir(t *testing.T) {
	east, south := flight.Vec3{X: 1}, flight.Vec3{Z: 1}
	type geometry struct {
		name      string
		way       flight.Vec3
		speed     float64
		breaking  bool
		outer     bool
		low, high float64
		angle     bool // bisect the bearing off the nose (degrees) at 1,500 m, not the range
	}
	edge := func(wind flight.Vec3, g geometry) float64 {
		environment := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: wind}
		shooter := round.Target{Position: flight.Vec3{Y: 3000}}
		shooter.Velocity = launcher(&flight.State{Position: shooter.Position, Velocity: east.Scale(250).Add(flight.Wind(shooter.Position, 0, environment)), Attitude: flight.Look(east)}, environment).Velocity
		good := func(trial float64) bool {
			line, span := east, trial
			if g.angle {
				line, span = flight.Vec3{X: math.Cos(trial * math.Pi / 180), Z: math.Sin(trial * math.Pi / 180)}, 1500
			}
			at := shooter.Position.Add(line.Scale(span))
			target := round.Target{Position: at, Velocity: g.way.Scale(g.speed).Add(flight.Wind(at, 0, environment))}
			return reaches(shooter, target, line, flight.Vec3{}, environment, 0, span, g.breaking)
		}
		low, high := g.low, g.high
		if good(low) == good(high) {
			t.Fatalf("%s: no edge between %.0f and %.0f in the wind %v", g.name, low, high, wind)
		}
		for k := 0; k < 16; k++ {
			if mid := (low + high) / 2; good(mid) == g.outer {
				low = mid
			} else {
				high = mid
			}
		}
		return low
	}
	for _, g := range []geometry{
		{name: "farthest tail chase", way: east, speed: 400, outer: true, low: 200, high: 20000},
		{name: "farthest shot at a jet breaking into the beam", way: east, speed: 250, breaking: true, outer: true, low: 200, high: 20000},
		{name: "closest crossing", way: south, speed: 200, low: 200, high: 4000},
		{name: "farthest bearing off the nose", way: east, speed: 200, outer: true, low: 0, high: 70, angle: true},
	} {
		still := edge(flight.Vec3{}, g)
		for _, wind := range []flight.Vec3{{X: 15}, {X: -15}, {Z: 15}, {Z: -15}} {
			got := edge(wind, g)
			t.Logf("%s in %v: %.1f (still air %.1f)", g.name, wind, got, still)
			if math.Abs(got-still) > 0.02*still {
				t.Errorf("the %s reads %.1f in the wind %v, in still air %.1f", g.name, got, wind, still)
			}
		}
	}
}

// TestFlareDrifts: a seduced heater chases a flare that falls through the air,
// down the level under it, and drifts with the wind.
func TestFlareDrifts(t *testing.T) {
	place, velocity := aim(900, 0, 0, 200)
	i, _, m := missileRange(t, place, velocity)
	blowing(i, flight.Vec3{X: -15, Z: 6})
	m.blind, m.lure = 1, place.Add(flight.Vec3{Y: -30})
	was := m.lure
	i.pursue(1.0/60, 600)
	want := was.Add(flight.Wind(was, 10, i.environment).Add(i.environment.Theatre().Up(was).Scale(-45)).Scale(1.0 / 60))
	if m.lure.Subtract(want).Length() > 1e-9 {
		t.Fatalf("the flare moved to %v, where the wind carries a falling flare %v", m.lure, want)
	}
}

// TestVolleyInTheWind: a server jet's volley leaves in the wind at its muzzle.
func TestVolleyInTheWind(t *testing.T) {
	i, a, _ := pair(t, "furball")
	a.latest.Fire = true
	i.guns(0.1, 1)
	if len(i.rounds) == 0 {
		t.Fatal("the gun fired nothing")
	}
	for _, r := range i.rounds {
		if want := flight.Wind(r.Position, a.model.State.Time, i.environment); r.Wind != want || want.Length() < 10 {
			t.Fatalf("a round flies in %v, the wind at the muzzle is %v", r.Wind, want)
		}
	}
	// And on its way it takes the wind where it is, every battle.Sampled seconds.
	a.latest.Fire = false
	for tick := uint64(2); tick < 2+uint64(battle.Sampled*60)+3; tick++ { // a little past it: sixtieths add up to just under a quarter
		i.fly(1.0/60, tick)
	}
	for _, r := range i.rounds {
		if r.Felt < battle.Sampled {
			t.Fatalf("a round %.2f s out still flies the muzzle's air (last took the wind at %.2f s)", r.Age, r.Felt)
		}
	}
}

// TestWreckageInTheWind: a kill's wreckage slows into the air where the jet
// broke up, and the wind carries it.
func TestWreckageInTheWind(t *testing.T) {
	i, a, _ := pair(t, "furball")
	fell := a.model.State.Position
	i.stepped = 600
	i.kill(0, -1)
	if want := flight.Wind(fell, 10, i.environment); len(i.debris) != 1 || i.debris[0].Wind != want || want.Length() < 10 {
		t.Fatalf("the wreckage falls in %+v, the wind where the jet broke up is %v", i.debris, want)
	}
}

// TestBloomDrifts: chaff hangs in the air, so the cloud a seeker is offered is
// where the wind has carried it since it bloomed, not where it was dropped.
func TestBloomDrifts(t *testing.T) {
	i, a, b := pair(t, "furball")
	blowing(i, flight.Vec3{X: -15, Z: 6})
	_, sound := 0.0, 316.0
	a.model.State.Position, a.model.State.Velocity = flight.Vec3{Y: 6000}, flight.Vec3{X: 0.9 * sound}
	b.model.State.Position, b.model.State.Velocity = flight.Vec3{X: 14000, Y: 2000}, flight.Vec3{Z: 0.9 * sound} // beaming, the sea behind him
	b.model.State.Time = 30
	b.cloud, b.clouded = b.model.State.Position.Add(flight.Vec3{X: -20}), 1.5
	m := &missile{shooter: 0, target: 1, radar: round.New(a.model.State.Position, a.model.State.Velocity,
		&round.Target{Position: b.model.State.Position, Velocity: b.model.State.Velocity}, i.environment, 30)}
	m.radar.Phase = round.Pitbull
	i.flying = append(i.flying[:0], m)
	i.fly_radar(m, 1.0/60, 1800)
	bloom := b.cloud.Add(flight.Wind(b.cloud, 30, i.environment).Scale(1.5 - 1.0/60))                                         // where the wind has carried it by the round's own time, a tick behind the jets
	drift := flight.Wind(bloom, m.radar.Clock+m.radar.Time-1.0/60, i.environment).Subtract(i.environment.Theatre().Up(bloom)) // sinking down the level up there
	if want := bloom.Add(drift.Scale(1.0 / 60)); m.radar.Estimate.Subtract(want).Length() > 1e-6 {
		t.Fatalf("the seeker stares at %v, where the wind has carried the cloud %v", m.radar.Estimate, want)
	}
}

// open is a BVR joust with an expert bot and a pilot: the bot's slot, the
// pilot's craft and the bot's.
func open(t *testing.T) (*instance, *craft, int, *craft) {
	t.Helper()
	made, err := (&Air{}).Create(game.Session{Identifier: "breeze", Game: "air", Mode: "joust", Seed: 1,
		Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{"expert": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	t.Cleanup(i.Close)
	if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0, Stores: quiver()}); err != nil {
		t.Fatal(err)
	}
	for slot, a := range i.aircraft {
		if a != nil && a.brain != nil {
			return i, i.aircraft[0], slot, a
		}
	}
	t.Fatal("no bot in the joust")
	return nil, nil, 0, nil
}

// TestRadarRoundInTheAir: the server's AMRAAM flies the match's air, from the
// moment it leaves: launched into the session's environment on the shooter's
// clock.
func TestRadarRoundInTheAir(t *testing.T) {
	i, me, bot, _ := open(t)
	i.Radar(0, 2, bot)
	if !i.fox3(0, me, false) {
		t.Fatal("the radar trigger launched nothing")
	}
	m := i.flying[len(i.flying)-1]
	if m.radar == nil || m.radar.Environment != i.environment || m.radar.Clock != me.model.State.Time || m.radar.Environment.Wind.Length() < 12 {
		t.Fatalf("the AMRAAM flies %+v from %.2f s, the match's air is %+v at %.2f s", m.radar.Environment, m.radar.Clock, i.environment, me.model.State.Time)
	}
}

// TestHuntZoneInTheAir: a bot judges its AMRAAM shots on the zone the round
// will fly, in the air its jet flies - not in still air, where the same jets
// over the ground are different jets through the air.
func TestHuntZoneInTheAir(t *testing.T) {
	i, me, bot, a := open(t)
	b := a.brain
	nose := a.model.State.Attitude.Rotate(flight.Vec3{X: 1}).Normalize()
	me.model.State.Position = a.model.State.Position.Add(nose.Scale(40000))
	me.model.State.Velocity = nose.Scale(-250)
	tick := uint64(600)
	b.target = 0
	b.known[0] = &track{when: tick, position: me.model.State.Position, velocity: me.model.State.Velocity}
	a.emitter, a.lock, b.assessed = 1, -1, 0
	i.hunt(bot, a, tick)
	if b.assessed != tick {
		t.Fatal("the bot never assessed its zone")
	}
	target := round.Target{Position: me.model.State.Position, Velocity: me.model.State.Velocity}
	want := round.Ladder(fired(a), target, a.model.Environment, a.model.State.Time)
	still := round.Ladder(fired(a), target, flight.Environment{Frame: scene, Chart: scene}, a.model.State.Time)
	if b.zone != want || math.Abs(still.Max-want.Max) < 0.01*want.Max {
		t.Fatalf("the bot's zone %+v, the round's in the match's air %+v, in still air %+v", b.zone, want, still)
	}
}

// TestSeekerAspectThroughTheAir: the heater's seeker reach goes with the
// target's aspect, which is where he points - along his way through the air,
// not along his track over the sea, which a crosswind swings off his nose.
func TestSeekerAspectThroughTheAir(t *testing.T) {
	zone := func(wind flight.Vec3) round.Zone {
		environment := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: wind}
		shooter := round.Target{Position: flight.Vec3{Y: 3000}}
		target := round.Target{Position: flight.Vec3{X: 2000, Y: 3000}}
		shooter.Velocity = flight.Vec3{X: 250}.Add(flight.Wind(shooter.Position, 0, environment))
		target.Velocity = flight.Vec3{X: 200}.Add(flight.Wind(target.Position, 0, environment)) // dead astern, through the air
		return Heat(shooter, target, flight.Vec3{}, 0, environment, 0)
	}
	still, crossed := zone(flight.Vec3{}), zone(flight.Vec3{Z: 25})
	if math.Abs(crossed.Max-still.Max) > 1 {
		t.Fatalf("crabbing across a 25 m/s wind, a jet dead astern gives the seeker %.0f m, in still air %.0f m", crossed.Max, still.Max)
	}
}

// heated is one state of a heater in flight, set through the air: the round's
// and the target's velocities through it, the target's bearing off the
// round's axis (rad, level, + toward +Z), and what the round is doing.
type heated struct {
	name          string
	round, target flight.Vec3
	off, span     float64
	burn          float64
	loose         bool
	blind         float64 // seconds left on a flare: the lure hangs 30 m below the target
	slew          float64 // rad the seeker's last sight trails the line: the line-of-sight rate it sees
	wind          flight.Vec3
}

// tick flies one pursue() tick of a heater in the state given, in the wind
// given, and returns its velocity through the air after the tick, whether it
// still flies and whether its lock has broken.
func (s heated) tick(t *testing.T, wind flight.Vec3, tick uint64) (flight.Vec3, bool, bool) {
	t.Helper()
	place, velocity := aim(900, 0, 0, 200)
	i, target, m := missileRange(t, place, velocity)
	i.flying = append(i.flying[:0], m)
	blowing(i, wind)
	clock := float64(tick) / 60
	axis := s.round.Normalize()
	line := flight.Vec3{X: axis.X*math.Cos(s.off) - axis.Z*math.Sin(s.off), Z: axis.X*math.Sin(s.off) + axis.Z*math.Cos(s.off)}
	m.position = flight.Vec3{Y: 3000}
	seen := m.position.Add(line.Scale(s.span)) // where the target is at the round's time
	target.model.State.Velocity = s.target.Add(flight.Wind(seen, clock, i.environment))
	target.model.State.Position = seen.Add(target.model.State.Velocity.Scale(1.0 / 60)) // and the jets have stepped since
	target.model.State.Attitude = flight.Look(s.target.Normalize())
	target.flared = 1e9
	m.velocity = s.round.Add(flight.Wind(m.position, clock, i.environment))
	m.burn, m.loose, m.blind, m.flew, m.life, m.window = s.burn, s.loose, s.blind, 2, 10, true
	mark := seen
	if s.blind > 0 {
		// The lure falls and drifts before the seeker looks this tick; the
		// sight it last took was of where the lure is now, from where the round
		// was a tick ago, and in a wind the round's own last tick carried it as
		// far. Taken as the lure's next place from here, the sight line's rate
		// is the slew alone.
		m.lure = mark.Add(flight.Vec3{Y: -30})
		mark = m.lure.Add(flight.Wind(m.lure, clock, i.environment).Add(flight.Vec3{Y: -45}).Scale(1.0 / 60))
	}
	sight, _ := i.bearing(m.position, mark)
	m.sight = flight.Vec3{X: sight.X*math.Cos(s.slew) - sight.Z*math.Sin(s.slew), Y: sight.Y, Z: sight.X*math.Sin(s.slew) + sight.Z*math.Cos(s.slew)}
	was := m.position
	i.pursue(1.0/60, tick)
	return m.velocity.Subtract(flight.Wind(was, clock, i.environment)), len(i.flying) > 0, m.loose
}

// TestHeaterStepsThroughTheAir: one tick of the heater, in any state, does
// to its velocity through the air in a wind exactly what it does in still
// air, and lives or dies, holds or breaks its lock, the same: its speed and
// its turn limit, the axis its seeker looks along and its motor pushes along,
// its drag, coasting or turning, its chase of a drifting flare, its energy
// running out and its second look after a flare are all the air's.
func TestHeaterStepsThroughTheAir(t *testing.T) {
	across, along := flight.Vec3{Z: 15}, flight.Vec3{X: 15}
	east := flight.Vec3{X: 1}
	lean := func(degrees float64) flight.Vec3 {
		return flight.Vec3{X: math.Cos(degrees * math.Pi / 180), Z: math.Sin(degrees * math.Pi / 180)}
	}
	// A tick on which the 9M's second look after a flare comes up good.
	look := uint64(0)
	{
		place, velocity := aim(900, 0, 0, 200)
		i, _, m := missileRange(t, place, velocity)
		for look = 600; look < 2000 && battle.Roll(i.environment.Seed, m.number, look, 53) >= flare_reject; look++ {
		}
	}
	for _, s := range []heated{
		{name: "boosting after a crossing target", round: east.Scale(600), target: flight.Vec3{Z: 200}, off: 0.2, span: 800, burn: 1, slew: 0.002, wind: across},
		{name: "slow, its turn at the limit", round: east.Scale(200), target: flight.Vec3{Z: -250}, off: 0.3, span: 800, slew: 0.004, wind: along},
		{name: "coasting loose", round: east.Scale(500), target: east.Scale(200), span: 800, loose: true, wind: across},
		{name: "chasing a flare", round: east.Scale(500), target: east.Scale(200), off: 0.5, span: 800, blind: 1, slew: 0.002, wind: across},
		{name: "at the gimbal's edge", round: east.Scale(500), target: east.Scale(200), off: -39 * math.Pi / 180, span: 800, wind: across},
		{name: "slower than he is, but not by enough to fall away", round: lean(-35).Scale(275), target: east.Scale(200), off: 35 * math.Pi / 180, span: 800, wind: along},
		{name: "its second look after the flare", round: east.Scale(500), target: east.Scale(200), off: -39 * math.Pi / 180, span: 800, blind: 0.5 / 60, wind: across},
	} {
		tick := uint64(600)
		if s.blind > 0 && s.blind < 1.0/60 {
			tick = look
		}
		still, flies, broke := s.tick(t, flight.Vec3{}, tick)
		blown, flying, breaking := s.tick(t, s.wind, tick)
		t.Logf("%s: %.4f m/s off, flying %v/%v, broken %v/%v", s.name, blown.Subtract(still).Length(), flying, flies, breaking, broke)
		if off := blown.Subtract(still).Length(); off > 0.01 || flies != flying || broke != breaking {
			t.Errorf("%s: in a %.0f m/s wind the tick leaves it %.4f m/s through the air off still air's, flying %v (still %v), lock broken %v (still %v)",
				s.name, flight.Wind(flight.Vec3{Y: 3000}, 10, flight.Environment{Frame: scene, Chart: scene, Seed: 3, Wind: s.wind}).Length(), off, flying, flies, breaking, broke)
		}
	}
}

// TestHeaterFirstLookIsTheWorlds: a heater leaves the rail before the jets
// step and is flown after they have, so its first look sees the target where
// he was when the round was where it is. Fired at a jet in loose formation
// 250 m off the nose, nothing moving between them, it holds him in still air
// and in a wind from any side. Looking at the jet where he ended the tick, the
// round read his whole step over the ground as line of sight rate, 0.42 rad/s
// past its 0.35 track limit in still air, and lost him on its first look; in
// a wind the rate changed with the wind across the line.
func TestHeaterFirstLookIsTheWorlds(t *testing.T) {
	dt := 1.0 / 60
	off := 25 * math.Pi / 180
	place := flight.Vec3{X: 250 * math.Cos(off), Y: 3000, Z: 250 * math.Sin(off)}
	for _, wind := range []flight.Vec3{{}, {Z: 15}, {Z: -15}, {X: 15}, {X: -15}} {
		i, target, m := missileRange(t, place, flight.Vec3{X: 250})
		blowing(i, wind)
		shooter := &i.aircraft[0].model.State
		blown := flight.Wind(shooter.Position, 0, i.environment)
		shooter.Velocity = flight.Vec3{X: 250}.Add(blown)
		target.model.State.Velocity = flight.Vec3{X: 250}.Add(flight.Wind(place, 0, i.environment))
		target.flared = 1e9
		// launch(): off the rail along the nose, the seeker on him.
		m.position = shooter.Position.Add(flight.Vec3{X: 3})
		m.velocity = rail(flight.Vec3{X: 1}, 250).Add(blown)
		m.sight, _ = i.bearing(m.position, place)
		m.flew, m.burn = 0, missile_boost
		// The jets step, then the round flies.
		target.model.State.Position = place.Add(target.model.State.Velocity.Scale(dt))
		i.pursue(dt, 600)
		if len(i.flying) == 0 || m.loose {
			t.Errorf("in a wind %v the heater lost a jet flying formation with its shooter on its first look", wind)
		}
		i.Close()
	}
}

// TestHeaterFusesOnTheTargetAtItsTime: the fuse finds the closest approach
// across the tick the round is about to fly, from where the round and its
// target were as the tick began, and the target has already flown it. A round
// passing 10 m from a jet crossing at 250 m/s fuses, in still air and in a
// wind along his path or against it. Read against the jet where he ended the
// tick, the pass stood 4.2 m further along his path in still air, outside the
// 12 m fuse, and the round flew on; in a wind the error was his ground speed's.
func TestHeaterFusesOnTheTargetAtItsTime(t *testing.T) {
	dt := 1.0 / 60
	start := flight.Vec3{X: 10, Y: 3000, Z: 5} // the jet as the tick began: 10 m off the round's line, 5 m ahead of it
	for _, wind := range []flight.Vec3{{}, {X: 15}, {X: -15}} {
		i, target, m := missileRange(t, start, flight.Vec3{X: 250})
		blowing(i, wind)
		crossing := flight.Vec3{X: 250}.Add(flight.Wind(start, 0, i.environment))
		target.model.State.Velocity = crossing
		target.flared = 1e9
		target.condition.Damager = -1
		m.position = flight.Vec3{Y: 3000}
		m.velocity = crossing.Add(flight.Vec3{Z: 600}) // closing along his line of flight's square alone: the pass is the 10 m
		m.flew, m.burn = 1, 0
		target.model.State.Position = start.Add(crossing.Scale(dt)) // the jets have stepped
		i.pursue(dt, 600)
		if len(i.flying) != 0 || target.condition.Damager != 0 {
			t.Errorf("in a wind %v a heater passing 10 m from the jet flew on unfused", wind)
		}
		i.Close()
	}
}

// TestHeaterLureIsWhereTheJetWas: a heater a flare seduces takes the flare
// where the jet dropped it, 30 m below him as he was when the round was where
// it is, and from there it falls and drifts. Placed below the jet where he
// ended the tick, the lure stood his whole step over the ground along his
// path from the flare, 4.2 m at 250 m/s in still air, and in a wind his
// ground speed's.
func TestHeaterLureIsWhereTheJetWas(t *testing.T) {
	dt := 1.0 / 60
	start := flight.Vec3{X: 1500, Y: 3000} // the jet as the tick began, on the round's line, crossing it: the flare's best aspect
	for _, wind := range []flight.Vec3{{}, {X: 15}, {Z: -15}} {
		i, target, m := missileRange(t, start, flight.Vec3{Z: 250})
		blowing(i, wind)
		crossing := flight.Vec3{Z: 250}.Add(flight.Wind(start, 0, i.environment))
		target.model.State.Velocity = crossing
		target.flared = 0.1 // a flare just left
		m.position = flight.Vec3{Y: 3000}
		m.velocity = flight.Vec3{X: 600}.Add(flight.Wind(m.position, 0, i.environment))
		m.sight, _ = i.bearing(m.position, start)
		m.flew, m.burn, m.window = 1, 0, false
		tick := uint64(600)
		for decoy := seduction(0, target.latest.Reheat); battle.Roll(i.environment.Seed, m.number, tick) >= decoy; tick++ {
		} // a tick on which the flare takes the seeker
		target.model.State.Position = start.Add(crossing.Scale(dt)) // the jets have stepped
		i.pursue(dt, tick)
		frame := i.environment.Theatre()
		lure := start.Add(frame.Up(start).Scale(-30)) // below him, down the level under him
		want := lure.Add(flight.Wind(lure, float64(tick)/60, i.environment).Add(frame.Up(lure).Scale(-45)).Scale(dt))
		if m.blind <= 0 {
			t.Errorf("in a wind %v the flare never took the seeker", wind)
		} else if off := m.lure.Subtract(want).Length(); off > 1e-6 {
			t.Errorf("in a wind %v the lure stands %.2f m from where the flare left the jet and fell", wind, off)
		}
		i.Close()
	}
}

// TestHeaterFlareReadsTheAspectAtItsTime: whether a flare takes the seeker
// goes with the aspect it sees him at: from where the round is to where he was
// at the round's time. On a tick whose roll falls between the chance at that
// aspect and the chance at his aspect a tick later, it decides as the round's
// own aspect does. Read against the jet where he ended the tick, a crossing
// jet 150 m off was 1.6 degrees further round, and the flare's chance with it.
func TestHeaterFlareReadsTheAspectAtItsTime(t *testing.T) {
	dt := 1.0 / 60
	start := flight.Vec3{X: 150, Y: 3000} // the jet at the round's time, crossing its line: his tail square to it
	crossing := flight.Vec3{Z: 250}
	i, target, m := missileRange(t, start, crossing)
	defer i.Close()
	target.flared = 0.1 // a flare just left
	m.position, m.velocity = flight.Vec3{Y: 3000}, flight.Vec3{X: 600}
	m.sight, _ = i.bearing(m.position, start)
	m.flew, m.burn, m.window = 1, 0, false
	ahead := start.Add(crossing.Scale(dt))
	chance := func(him flight.Vec3) float64 {
		direction, _ := i.bearing(m.position, him)
		return seduction(direction.Dot(target.model.State.Attitude.Rotate(flight.Vec3{X: 1})), target.latest.Reheat)
	}
	now, later := chance(start), chance(ahead)
	low, high := math.Min(now, later), math.Max(now, later)
	tick := uint64(600)
	for roll := battle.Roll(i.environment.Seed, m.number, tick); roll < low || roll >= high; roll = battle.Roll(i.environment.Seed, m.number, tick) {
		tick++
	} // a roll between the two chances
	target.model.State.Position = ahead // the jets have stepped
	i.pursue(dt, tick)
	if seduced, want := m.blind > 0, battle.Roll(i.environment.Seed, m.number, tick) < now; seduced != want {
		t.Errorf("on a roll between the chance at his aspect now (%.4f) and a tick later (%.4f), the flare took the seeker %v", now, later, seduced)
	}
}

// TestHeaterSecondLookAtTheGimbalsEdge: a seeker coming off a flare takes the
// target back only inside its gimbal, judged where he was at the round's time.
// A jet a tenth of a degree inside the gimbal, crossing out of it by a third of
// a degree a tick, is taken back; one a tenth outside, crossing in, is not.
// Judged where he ended the tick, each went the other way.
func TestHeaterSecondLookAtTheGimbalsEdge(t *testing.T) {
	dt := 1.0 / 60
	edge := math.Acos(missile_gimbal)
	for _, s := range []struct {
		name         string
		off, outward float64 // radians off the round's axis at its time, and +1 crossing out of the gimbal, -1 into it
		want         bool
	}{{"inside, crossing out", edge - 0.1*math.Pi/180, 1, true}, {"outside, crossing in", edge + 0.1*math.Pi/180, -1, false}} {
		seen := flight.Vec3{X: 800 * math.Cos(s.off), Y: 3000, Z: 800 * math.Sin(s.off)}
		crossing := flight.Vec3{X: -math.Sin(s.off), Z: math.Cos(s.off)}.Scale(250 * s.outward)
		i, target, m := missileRange(t, seen, crossing)
		m.position = flight.Vec3{Y: 3000}
		m.flew, m.burn, m.window = 1, 0, false
		m.blind, m.lure = 0.5*dt, flight.Vec3{X: 800, Y: 2970} // the flare's last moment on the seeker
		m.sight, _ = i.bearing(m.position, m.lure)
		target.flared = 1e9
		tick := uint64(600)
		for battle.Roll(i.environment.Seed, m.number, tick, 53) >= flare_reject {
			tick++
		} // a tick on which the second look comes up good
		// Its seeker looks along its path through the match's air; the jets have stepped.
		m.velocity = flight.Vec3{X: 500}.Add(flight.Wind(m.position, float64(tick)/60, i.environment))
		target.model.State.Position = seen.Add(crossing.Scale(dt))
		i.pursue(dt, tick)
		if took := m.rejected > 0; took != s.want {
			t.Errorf("%s: the seeker took the jet back %v", s.name, took)
		}
		if s.want && m.loose {
			t.Errorf("%s: the seeker took the jet back and lost him on the same tick", s.name)
		}
		i.Close()
	}
}

// TestRadarRoundReadsTheTargetAtItsTime: the AIM-120's core flies its target
// along each slice from where it is handed him, so fly_radar hands it the jet
// where he was when the round was where it is; and the warhead reads the
// burst against the jet as he was at that instant, kept at that place from
// him as he flies on to where he ends the tick. A round passing 6 m from a jet
// crossing at 400 m/s, inside the 7.7 m its warhead kills at outright,
// measures a 6 m pass and kills him. Handed the jet a tick ahead, the pass
// read 9 m; read against the jet where he ended the tick, the burst stood
// 8.7 m off him and he flew on.
func TestRadarRoundReadsTheTargetAtItsTime(t *testing.T) {
	dt := 1.0 / 60
	crossing := flight.Vec3{X: 400}
	start := flight.Vec3{Y: 3006, Z: 1} // the jet as the tick began: 6 m above the round's line, 1 m ahead of it
	i, target, m := missileRange(t, start, crossing)
	defer i.Close()
	m.radar = round.New(flight.Vec3{Y: 3000}, crossing.Add(flight.Vec3{Z: 1000}), nil, i.environment, 0)
	m.radar.Time = 10 // armed
	m.position, m.velocity = m.radar.Position, m.radar.Velocity
	target.clouded = 1e9
	target.model.State.Position = start.Add(crossing.Scale(dt)) // the jets have stepped
	i.pursue(dt, 600)
	if math.Abs(m.radar.Least-6) > 0.1 {
		t.Errorf("a round passing 6 m from a crossing jet measured a %.2f m pass", m.radar.Least)
	}
	if target.alive {
		t.Errorf("a burst 6 m from the jet, inside the 7.7 m its warhead kills at, left him flying")
	}
}

// TestSearchHoldsItsCourse: a bot with nothing known holds the course it was
// on as its search began, in the match's wind as in still air. Re-reading the
// course from its own track every tick, a novice opening a BVR joust in the
// trades turned with its own crab, walked thirty degrees downwind in two
// minutes and lost its target off the radar's scan.
func TestSearchHoldsItsCourse(t *testing.T) {
	made, err := (&Air{}).Create(game.Session{Identifier: "lane", Game: "air", Mode: "joust", Seed: 5,
		Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{"novice": 1.0, "superhuman": 1.0}}})
	if err != nil {
		t.Fatal(err)
	}
	i := made.(*instance)
	defer i.Close()
	var novice *craft
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a != nil && a.bot && !a.brain.skill.machine {
			novice = a
		}
	}
	if i.environment.Wind.Length() < 12 {
		t.Fatalf("the joust blows %v, not the trades", i.environment.Wind)
	}
	bearing := func() float64 {
		v := novice.model.State.Velocity
		return math.Atan2(v.X, -v.Z) * 180 / math.Pi
	}
	course, drift := 0.0, 0.0
	for tick := uint64(0); tick < 60*90 && novice.brain.target < 0; tick++ {
		i.Step(tick, nil)
		if tick == 300 {
			course = bearing()
		}
		if tick > 300 {
			drift = math.Max(drift, math.Abs(math.Remainder(bearing()-course, 360)))
		}
	}
	if drift > 2 {
		t.Fatalf("searching with nothing known, the novice wandered %.1f degrees off its course in the trades", drift)
	}
}

// TestBrainSpeeds: the brain judges its wing and its limits in equivalent
// airspeed and its energy in true airspeed through the air. The corner is an
// equivalent airspeed, the same at every height where the true corner climbs
// a third from 1,500 to 7,500 m; a speed the doctrine was tuned at as a true
// airspeed means at the reference height what it meant there.
func TestBrainSpeeds(t *testing.T) {
	gust := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: flight.Vec3{X: 15}}
	m := flight.New(aircraft.Get("fa18c"), gust, flight.World{Sea: sea})
	m.State = flight.Level(m, flight.Vec3{Y: reference}, flight.Vec3{X: 1}, 200, 3000)
	if speed := airspeed(&m.State, gust); math.Abs(speed-200) > 1e-6 || m.State.Velocity.Length() < 230 {
		t.Fatalf("a jet trimmed at 200 m/s through a %v wind reads %.2f m/s through the air, %.2f over the ground", gust.Wind, speed, m.State.Velocity.Length())
	}
	if got, want := equivalent(m), 200*math.Sqrt(flight.Atmosphere(reference, gust).Density/1.225); math.Abs(got-want) > 1e-9 || got > 170 {
		t.Fatalf("its equivalent airspeed reads %.2f m/s, its dynamic pressure's %.2f", got, want)
	}
	if mo := (moment{me: &m.State, wind: flight.Wind(m.State.Position, m.State.Time, gust), air: gust}); mo.airspeed() != airspeed(&m.State, gust) || mo.equivalent() != equivalent(m) {
		t.Fatalf("a play reads %.2f m/s true and %.2f equivalent, the brain %.2f and %.2f", mo.airspeed(), mo.equivalent(), airspeed(&m.State, gust), equivalent(m))
	}
	if got, want := tuned(150), flight.Equivalent(150, reference, flight.Environment{Frame: scene, Chart: scene}); got != want || tuned(-40) != -tuned(40) {
		t.Fatalf("tuned(150) is %.3f, the reference height reads %.3f; tuned(-40) %.3f", got, want, tuned(-40))
	}
	low, high := *m, *m
	low.State.Position.Y, high.State.Position.Y = 1500, 7500
	if a, b := corner(&low), corner(&high); a != b || a < 100 || a > 250 {
		t.Fatalf("the corner, as an equivalent airspeed, reads %.1f m/s at 1,500 m and %.1f at 7,500", a, b)
	}
}

// TestEvolveThroughTheAir: a turning jet's arc is flown through the air, and
// a steady wind carries the whole of it: the predicted jet sits where the
// still-air prediction does, displaced by the wind's travel, moving through
// the air as it does.
func TestEvolveThroughTheAir(t *testing.T) {
	blown := flight.Vec3{X: -25, Z: 12}
	still := track{position: flight.Vec3{Y: 4000}, velocity: flight.Vec3{X: 230}, swing: flight.Vec3{Z: 45}}
	carried := still
	carried.velocity, carried.blown = still.velocity.Add(blown), blown
	for _, dt := range []float64{1, 4, 12} {
		p0, v0 := evolve(&still, dt)
		p1, v1 := evolve(&carried, dt)
		if off := p1.Subtract(p0).Subtract(blown.Scale(dt)); off.Length() > 1e-6 {
			t.Errorf("after %.0f s the prediction is %.2f m off the still-air arc carried by the wind", dt, off.Length())
		}
		if off := v1.Subtract(blown).Subtract(v0); off.Length() > 1e-9 {
			t.Errorf("after %.0f s it moves through the air %.4f m/s off the still-air arc", dt, off.Length())
		}
	}
}

// TestCircleThroughTheAir: his turning circle's size and rate are read
// through the air, the same in a wind as in still air.
func TestCircleThroughTheAir(t *testing.T) {
	blown := flight.Vec3{X: -25, Z: 12}
	turn := func(angle float64) flight.Vec3 {
		return flight.Vec3{X: 230 * math.Cos(angle), Z: 230 * math.Sin(angle)}
	}
	p1, p2 := flight.Vec3{Y: 4000}, flight.Vec3{X: 220, Y: 4000, Z: 20}
	still := circle(p1, turn(0), 60, p2, turn(0.2), 120, flight.Vec3{})
	carried := circle(p1, turn(0).Add(blown), 60, p2, turn(0.2).Add(blown), 120, blown)
	if !still.valid || !carried.valid || math.Abs(carried.radius-still.radius) > 1e-6 || math.Abs(carried.omega-still.omega) > 1e-9 {
		t.Fatalf("in a wind his circle reads %.1f m at %.3f rad/s, in still air %.1f m at %.3f", carried.radius, carried.omega, still.radius, still.omega)
	}
}

// TestTheCircleDriftsWithTheAir: his turn is flown in the air, so the circle
// read from two looks drifts with the wind from where the looks placed it.
// Read seconds later in a steady wind, the lag point behind him on it, and
// where I stand against it in the rehearsal's appraisal, are still air's
// carried by the wind for as long. Left where the looks placed it over the
// sea, the circle fell behind the jet flying it at the wind's speed, 150 m
// in six seconds of a 25 m/s wind.
func TestTheCircleDriftsWithTheAir(t *testing.T) {
	blown := flight.Vec3{X: -25, Z: 12}
	turn := func(angle float64) flight.Vec3 {
		return flight.Vec3{X: 230 * math.Cos(angle), Z: 230 * math.Sin(angle)}
	}
	p1, p2 := flight.Vec3{Y: 4000}, flight.Vec3{X: 220, Y: 4000, Z: 20} // looks at ticks 60 and 120, one and two seconds in
	still := circle(p1, turn(0), 60, p2, turn(0.2), 120, flight.Vec3{})
	carried := circle(p1.Add(blown), turn(0).Add(blown), 60, p2.Add(blown.Scale(2)), turn(0.2).Add(blown), 120, blown)
	sk := skills["superhuman"] // the tier that reads his circle
	tactic := standard()
	for _, later := range []float64{2, 3.5, 6} {
		him, hisV := flight.Vec3{X: 300, Y: 4000, Z: 120}, turn(0.6) // where he is in still air by then, and his velocity
		calm := still.behind(him, 0.5, later)
		blew := carried.behind(him.Add(blown.Scale(later)), 0.5, later)
		if off := blew.Subtract(calm.Add(blown.Scale(later))).Length(); off > 1e-6 {
			t.Errorf("%.1f s on, the lag point on his circle stands %.2f m from still air's carried by the wind", later, off)
		}
		me := still.centre.Add(flight.Vec3{X: 600, Z: -200}) // inside his circle, where standing counts
		score := func(wind flight.Vec3, ring orbit) (float64, float64) {
			s := flight.State{Position: me.Add(wind.Scale(later)), Velocity: flight.Vec3{X: 220}.Add(wind), Attitude: flight.Quat{W: 1}, Time: later}
			return appraise(&s, him.Add(wind.Scale(later)), hisV.Add(wind), 160, stances[""], &sk, ring, &tactic, wind, wind, flight.Environment{Frame: scene, Chart: scene})
		}
		a, b := score(flight.Vec3{}, still)
		c, d := score(blown, carried)
		if math.Abs(a-c) > 1e-4 || math.Abs(b-d) > 1e-4 { // the jets carried 150 m along the frame's level stand millimetres higher on the curved earth
			t.Errorf("%.1f s on, standing against his circle scores %.6f / %.6f in still air and %.6f / %.6f in the wind", later, a, b, c, d)
		}
	}
}

// TestTheLagPointRidesHisCircle: the rehearsal's lag point sits on his circle
// as it has drifted by the moment's own time. Seconds after the circle was
// read, in a steady wind, it is still air's carried by the wind for as long.
func TestTheLagPointRidesHisCircle(t *testing.T) {
	blown := flight.Vec3{X: -25, Z: 12}
	turn := func(angle float64) flight.Vec3 {
		return flight.Vec3{X: 230 * math.Cos(angle), Z: 230 * math.Sin(angle)}
	}
	p1, p2 := flight.Vec3{Y: 4000}, flight.Vec3{X: 220, Y: 4000, Z: 20} // looks at ticks 60 and 120
	still := circle(p1, turn(0), 60, p2, turn(0.2), 120, flight.Vec3{})
	carried := circle(p1.Add(blown), turn(0).Add(blown), 60, p2.Add(blown.Scale(2)), turn(0.2).Add(blown), 120, blown)
	for _, later := range []float64{2, 3.5, 6} {
		lag := func(wind flight.Vec3, ring orbit) flight.Vec3 {
			me := flight.State{Position: flight.Vec3{X: -600, Y: 4000}.Add(wind.Scale(later)), Velocity: flight.Vec3{X: 220}.Add(wind), Attitude: flight.Quat{W: 1}, Time: later}
			m := moment{me: &me, prey: flight.Vec3{X: 300, Y: 4000, Z: 120}.Add(wind.Scale(later)), velocity: turn(0.6).Add(wind), ring: ring, pace: 160, pull: 7, wind: wind, blown: wind, air: flight.Environment{Frame: scene, Chart: scene}}
			m.derive()
			return m.lag()
		}
		if off := lag(blown, carried).Subtract(lag(flight.Vec3{}, still).Add(blown.Scale(later))).Length(); off > 1e-6 {
			t.Errorf("%.1f s on, the rehearsal's lag point on his circle stands %.2f m from still air's carried by the wind", later, off)
		}
	}
}

// TestPursuerReadsAStaleLookInTheAir: a jet behind me I last saw half a
// second ago is where that look puts him in the air, carried by the wind
// since. Across a sweep of his range through the 2 km the reflex watches, the
// same geometry through the air names the same pursuer in still air and in a
// steady wind. Read where the look left him over the sea, a wind along the
// line moved him 12 m, and the reflex fired at ranges still air did not.
func TestPursuerReadsAStaleLookInTheAir(t *testing.T) {
	i, _, _ := missileRange(t, flight.Vec3{X: 5000, Y: 3000}, flight.Vec3{X: 200})
	defer i.Close()
	b := mind("expert")
	named := map[bool]int{}
	for _, blown := range []flight.Vec3{{X: 25}, {X: -25}, {Z: 25}} {
		for span := 1900.0; span <= 2100; span += 2 {
			pursued := func(wind flight.Vec3) bool {
				now := 10.0
				me := flight.State{Position: flight.Vec3{Y: 3000}.Add(wind.Scale(now)), Attitude: flight.Look(flight.Vec3{X: 1}), Time: now}
				seen := flight.Vec3{X: -span, Y: 3000}.Add(wind.Scale(now - 0.5)) // where he was half a second ago, behind me
				b.known = map[int]*track{1: {when: 570, position: seen, velocity: flight.Vec3{X: 250}.Add(wind), blown: wind}}
				slot, _ := i.pursuer(b, &me, []int{1})
				return slot == 1
			}
			calm := pursued(flight.Vec3{})
			named[calm] = named[calm] + 1
			if calm != pursued(blown) {
				t.Errorf("in a wind %v with him %.0f m behind me, pursued %v, in still air %v", blown, span, !calm, calm)
			}
		}
	}
	if named[true] == 0 || named[false] == 0 {
		t.Fatalf("the sweep never crosses the reflex's range: %v", named)
	}
}

// TestDecideReadsStaleLooksInTheAir: looks come no faster than the pilot's
// perception delay, and none while a jet is out of sight, and between them the
// brain reads a jet where its last look put him in the air, carried by the
// wind since. In a steady wind three of its reads are still air's: the range
// to its target five ticks after a look while it holds its mode, its doctrine
// flare against a jet on its six unseen for half a second, across a sweep
// through the 2.6 km the flare covers, and the way a crippled jet turns from
// the nearest jet eight ticks after a look. Read
// where the looks left him over the sea, the range was off by the wind's
// drift since the look, and the flare went at other ranges.
func TestDecideReadsStaleLooksInTheAir(t *testing.T) {
	const tick = 600
	now := float64(tick) / 60
	// decided is the bot's brain after one decide, its jet and the pilot's
	// set at me and him through the air as the bot sees them, the look age
	// ticks old, in a steady wind.
	decided := func(wind, me, mine, him, his flight.Vec3, age uint64, engine float64, prime func(b *brain)) *brain {
		made, err := New().Create(game.Session{Identifier: "stale", Game: "air", Mode: "furball", Capacity: 8, Seed: 1,
			Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{"expert": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		defer i.Close()
		if _, err := i.Join(game.Player{Name: "human", Slot: 0, Stores: preset(true)}); err != nil {
			t.Fatal(err)
		}
		blowing(i, wind)
		bot := -1
		for slot, a := range i.aircraft {
			if a != nil && a.brain != nil {
				bot = slot
			}
		}
		a, pilot := i.aircraft[bot], i.aircraft[0]
		at := func(p flight.Vec3) flight.Vec3 { return flight.Wind(p, now, i.environment) } // the air where each jet is
		a.model.State.Position, a.model.State.Velocity, a.model.State.Attitude, a.model.State.Time = me, mine.Add(at(me)), flight.Look(mine), now
		a.model.State.Damage.Engine[0], a.model.State.Damage.Engine[1] = engine, engine
		pilot.model.State.Position, pilot.model.State.Velocity, pilot.model.State.Attitude = him, his.Add(at(him)), flight.Look(his)
		// The look: where he was age ticks ago, his flight through the air and
		// the air's own drift both since behind him.
		a.brain.known = map[int]*track{0: {when: tick - age, position: him.Subtract(his.Add(at(him)).Scale(float64(age) / 60)), velocity: his.Add(at(him)), nose: his.Normalize(), blown: at(him)}}
		a.brain.target = 0
		prime(a.brain)
		i.decide(bot, a, tick)
		return a.brain
	}
	east := flight.Vec3{X: 200}
	held := func(b *brain) { b.hold = tick + 30 } // holding its mode, it reads the range off the look
	free := func(b *brain) {}
	for _, blown := range []flight.Vec3{{X: 25}, {X: -25}, {Z: 25}} {
		calm := decided(flight.Vec3{}, flight.Vec3{Y: 4000}, east, flight.Vec3{X: 1500, Y: 4000, Z: 300}, flight.Vec3{X: -200}, 5, 0, held).distance
		windy := decided(blown, flight.Vec3{Y: 4000}, east, flight.Vec3{X: 1500, Y: 4000, Z: 300}, flight.Vec3{X: -200}, 5, 0, held).distance
		if math.Abs(calm-windy) > 1e-6 {
			t.Errorf("in a wind %v the range to him five ticks after a look is %.2f m, in still air %.2f", blown, windy, calm)
		}
		dropped := map[bool]int{}
		for span := 2500.0; span <= 2700; span += 4 {
			behind := flight.Vec3{X: -span, Y: 4000}
			calm := decided(flight.Vec3{}, flight.Vec3{Y: 4000}, east, behind, east, 30, 0, free).drop
			dropped[calm]++
			if windy := decided(blown, flight.Vec3{Y: 4000}, east, behind, east, 30, 0, free).drop; windy != calm {
				t.Errorf("in a wind %v with him %.0f m on my six, unseen half a second, the doctrine flare goes %v, in still air %v", blown, span, windy, calm)
			}
		}
		if dropped[true] == 0 || dropped[false] == 0 {
			t.Errorf("the sweep never crosses the doctrine flare's reach: %v", dropped)
		}
		near := flight.Vec3{X: 800, Y: 4000, Z: 900}
		still := decided(flight.Vec3{}, flight.Vec3{Y: 4000}, east, near, east, 8, 0.8, free) // inside the delay: in sight, the look stands
		carried := decided(blown, flight.Vec3{Y: 4000}, east, near, east, 8, 0.8, free)
		if still.mode != "limp" || carried.mode != "limp" {
			t.Fatalf("crippled, the expert flies %q in still air and %q in the wind", still.mode, carried.mode)
		}
		if off := math.Acos(clamp(still.aim.Dot(carried.aim), -1, 1)) * 180 / math.Pi; off > 1e-6 {
			t.Errorf("in a wind %v the crippled jet turns from him %.4f degrees off still air's way", blown, off)
		}
	}
}

// TestSectionReadsStaleLooksInTheAir: the section's reads of jets it last saw
// eight ticks ago, inside the expert's perception delay, put them where the air
// has carried them since. In a steady
// wind across the line, the same geometry through the air makes the same
// calls as still air: an attacker counts as running on my teammate across a
// sweep of his range through the 2.2 km that commits him, the BREAK call names
// the side he comes from across a sweep of him through my teammate's six, and
// a jet only heard on the radio is supported across a sweep through the 6 km
// it is supported inside. Read where the looks left them over the sea, each
// stood 3.3 m off in a 25 m/s wind, and the calls changed with the wind.
func TestSectionReadsStaleLooksInTheAir(t *testing.T) {
	const tick, age = 600, 8 // inside the expert's nine-tick delay: the looks stand
	now, then := float64(tick)/60, float64(tick-age)/60
	type jet struct {
		at, flying flight.Vec3 // where, and the velocity through the air, as still air has it now
		heard      bool
	}
	// section flies one decide for the expert, the section's jets placed as
	// still air has them carried by a steady wind, and its looks at the blue
	// pair age ticks old.
	section := func(wind flight.Vec3, human bool, me, mate, one, two jet) (*instance, *craft, *craft, *craft, *craft) {
		i, expert, buddy, blue1, blue2 := teams(t)
		blowing(i, wind)
		buddy.bot = !human
		expert.brain.known = map[int]*track{}
		for _, j := range []struct {
			c *craft
			j jet
		}{{expert, me}, {buddy, mate}, {blue1, one}, {blue2, two}} {
			s := &j.c.model.State
			s.Position, s.Velocity, s.Attitude, s.Time = j.j.at.Add(wind.Scale(now)), j.j.flying.Add(wind), flight.Look(j.j.flying), now
			if j.c.team == "blue" {
				expert.brain.known[j.c.player.Slot] = &track{when: tick - age, position: j.j.at.Subtract(j.j.flying.Scale(now - then)).Add(wind.Scale(then)),
					velocity: j.j.flying.Add(wind), nose: j.j.flying.Normalize(), blown: wind, heard: j.j.heard}
			}
		}
		i.decide(expert.player.Slot, expert, tick)
		return i, expert, buddy, blue1, blue2
	}
	east, base := flight.Vec3{X: 220}, flight.Vec3{Y: 4000}
	station := base.Add(flight.Vec3{X: 3800, Z: 600})
	wide := jet{at: base.Add(flight.Vec3{X: -9000, Z: 9000}), flying: flight.Vec3{Z: 220}} // far out of it
	for _, blown := range []flight.Vec3{{Z: 25}, {Z: -25}, {X: 25}} {
		// Running on my teammate: from astern of him, his nose on him.
		committed := map[bool]int{}
		near := jet{at: base.Add(flight.Vec3{X: 1000, Z: -300}), flying: flight.Vec3{Z: -220}} // nearer, minding its own business
		// His look is 32 m further back: he has closed since.
		for span := 2158.0; span <= 2178; span += 0.5 {
			targeted := func(wind flight.Vec3) bool {
				attacker := station.Subtract(flight.Vec3{X: span})
				i, expert, _, _, blue2 := section(wind, false, jet{at: base, flying: east}, jet{at: station, flying: east}, near, jet{at: attacker, flying: station.Subtract(attacker).Normalize().Scale(240)})
				defer i.Close()
				return expert.brain.target == blue2.player.Slot
			}
			calm := targeted(flight.Vec3{})
			committed[calm]++
			if calm != targeted(blown) {
				t.Errorf("in a wind %v with an attacker %.0f m astern of my teammate, his nose on him, I target him %v in still air and not in the wind", blown, span, calm)
			}
		}
		if committed[true] == 0 || committed[false] == 0 {
			t.Errorf("the sweep never crosses the attacker's commitment: %v", committed)
		}
		// The BREAK call, my teammate a pilot: which side the attacker sits.
		sides := map[string]int{}
		for lateral := -8.25; lateral <= 8.25; lateral += 0.5 { // never square on his line, where the side is a coin
			called := func(wind flight.Vec3) string {
				attacker := station.Subtract(flight.Vec3{X: 1200, Z: lateral})
				i, _, _, _, _ := section(wind, true, jet{at: base, flying: east}, jet{at: station, flying: east}, wide, jet{at: attacker, flying: station.Subtract(attacker).Normalize().Scale(240)})
				defer i.Close()
				for _, e := range i.events {
					if e["call"] == "break" {
						return e["direction"].(string)
					}
				}
				return ""
			}
			calm := called(flight.Vec3{})
			sides[calm]++
			if windy := called(blown); calm != windy {
				t.Errorf("in a wind %v with the attacker %.0f m across my teammate's six, the call is %q in still air and %q in the wind", blown, lateral, calm, windy)
			}
		}
		if sides["left"] == 0 || sides["right"] == 0 {
			t.Errorf("the sweep never crosses my teammate's six: %v", sides)
		}
		// Support: a jet only heard on the radio, against one I see farther off.
		supported := map[bool]int{}
		for span := 6019.0; span <= 6039; span += 0.5 { // his look is 29 m nearer: he has run since
			heard := func(wind flight.Vec3) bool {
				far := jet{at: base.Add(flight.Vec3{Z: -span}), flying: flight.Vec3{Z: -220}, heard: true} // running away, nobody's attacker
				seen := jet{at: base.Add(flight.Vec3{Z: 6600}), flying: flight.Vec3{Z: 220}}
				i, expert, _, blue1, _ := section(wind, false, jet{at: base, flying: east}, jet{at: station, flying: east}, far, seen)
				defer i.Close()
				return expert.brain.target == blue1.player.Slot
			}
			calm := heard(flight.Vec3{})
			supported[calm]++
			if calm != heard(blown) {
				t.Errorf("in a wind %v with a jet only heard %.0f m off, I take him %v in still air and not in the wind", blown, span, calm)
			}
		}
		if supported[true] == 0 || supported[false] == 0 {
			t.Errorf("the sweep never crosses the support range: %v", supported)
		}
	}
}

// TestGlideThroughTheAir: the rehearsal's surrogate flies through the air the
// wind moves, and flies its aim onto its path through the air, as the live law
// does. The same order flown in still air and in a steady wind - down the wind
// and across it, straight on and turning - gathers the same speed and turns the
// same way through the air, its drag and its thrust and its turn the air's, and
// the wind carries the jet. Turned onto the track instead, a crosswind's drift
// would be an error to pull out.
func TestGlideThroughTheAir(t *testing.T) {
	for _, blown := range []flight.Vec3{{X: 30}, {Z: 30}} {
		for _, aim := range []flight.Vec3{{X: 1}, flight.Axis(flight.Vec3{Y: 1}, 25*math.Pi/180).Rotate(flight.Vec3{X: 1})} {
			fly := func(wind flight.Vec3) *flight.Model {
				m := flight.New(aircraft.Get("fa18c"), flight.Environment{Frame: scene, Chart: scene}, flight.World{Sea: sea})
				m.State = flight.Level(m, flight.Vec3{Y: reference}, flight.Vec3{X: 1}, 180, 3000)
				m.State.Velocity = m.State.Velocity.Add(wind)
				for k := 0; k < 240; k++ {
					glide(m, order{aim: aim, g: 4, throttle: 1, reheat: 1}, 1.0/60, wind)
				}
				return m
			}
			still, carried := fly(flight.Vec3{}), fly(blown)
			if off := carried.State.Velocity.Subtract(blown).Subtract(still.State.Velocity); off.Length() > 1e-6 {
				t.Errorf("in a wind %v aiming %v, after 4 s the jet flies through the air %.4f m/s off its still-air twin", blown, aim, off.Length())
			}
			if off := carried.State.Position.Subtract(still.State.Position).Subtract(blown.Scale(4)); off.Length() > 1e-4 {
				t.Errorf("in a wind %v aiming %v, after 4 s the jet is %.3f m from where the wind carried its still-air twin", blown, aim, off.Length())
			}
		}
	}
}

// TestPlaysReadTheAir: every play reads its own speed and his through the
// air, and his aspect and the point behind his tail from his heading through
// it. I fly along a uniform wind; he flies along it, across it, and at the
// headings around the high yo-yo's sixty-degree aspect, where the wind sets
// his track off his heading. Each play's throttle, burner, brake and pull are
// what it orders in still air, across a sweep of speeds and ranges that
// crosses every play's thresholds, and the lag point behind him is where
// still air puts it.
func TestPlaysReadTheAir(t *testing.T) {
	blown := flight.Vec3{X: -25}
	cases, differ := 0, 0
	for _, p := range plays {
		for mine := 100.0; mine <= 280; mine += 6 {
			for _, his := range []float64{120, 170, 220, 270} {
				for _, distance := range []float64{300, 600, 850, 1100, 1400, 2000, 3000} {
					for _, height := range []float64{1500, 4500, 7500} {
						for _, heading := range []float64{0, 50, 57, 60, 63, 70, 120} {
							heading := flight.Axis(flight.Vec3{Y: 1}, heading*math.Pi/180).Rotate(flight.Vec3{X: 1})
							read := func(wind flight.Vec3) (order, flight.Vec3) {
								me := flight.State{Position: flight.Vec3{Y: height}, Velocity: flight.Vec3{X: mine}.Add(wind), Attitude: flight.Quat{W: 1}}
								m := moment{me: &me, prey: flight.Vec3{X: distance, Y: height + 100}, velocity: heading.Scale(his).Add(wind), pace: 160, pull: 7, wind: wind, blown: wind, air: flight.Environment{Frame: scene, Chart: scene}}
								m.derive()
								return p.law(&m), m.lag()
							}
							still, behind := read(flight.Vec3{})
							carried, lagged := read(blown)
							if lagged.Subtract(behind).Length() > 0.01 {
								t.Fatalf("the lag point behind a jet %.0f m off, its heading %v, is %v in still air and %v in the wind", distance, heading, behind, lagged)
							}
							cases++
							apart := func(a, b float64) bool { return math.Abs(a-b) > 1e-6 } // the wind cancels out of his speed relative to mine to rounding, not exactly
							if apart(still.throttle, carried.throttle) || apart(still.reheat, carried.reheat) || apart(still.brake, carried.brake) || apart(still.g, carried.g) {
								differ++
								if differ <= 5 {
									t.Errorf("%s at %.0f m/s against %.0f heading %.2f, %.0f m apart at %.0f m: in still air throttle %.4f reheat %.4f brake %.4f g %.4f, in the wind %.4f %.4f %.4f %.4f",
										p.name, mine, his, heading, distance, height, still.throttle, still.reheat, still.brake, still.g, carried.throttle, carried.reheat, carried.brake, carried.g)
								}
							}
						}
					}
				}
			}
		}
	}
	if differ > 0 {
		t.Errorf("%d of %d orders differ between still air and the wind", differ, cases)
	}
}

// TestAppraiseReadsTheAir: the appraiser prices my energy, my corner and his
// energy through the air. Both jets fly along the wind, so its geometry is the
// still air's: every score is the still air's, at heights where the corner's
// equivalent airspeed and the energy scale both move. And it holds my speed
// against the corner as an equivalent airspeed: slow under a jet straight
// above and faster, so neither closes and the energy edge is his whatever the
// height, the same equivalent airspeed scores the same at 1,500 m as at 7,500.
func TestAppraiseReadsTheAir(t *testing.T) {
	sk := skills["expert"]
	tactic := standard()
	for _, blown := range []flight.Vec3{{X: 25}, {Z: 25}} { // along the line between us, and across it
		for _, height := range []float64{1500, 4500, 7500} {
			for mine := 90.0; mine <= 280; mine += 10 {
				for _, his := range []float64{110, 180, 250, -110, -180, -250} { // flying my way, or at me
					for _, distance := range []float64{400, 900, 2500, -400, -900} { // ahead of me, or behind
						score := func(wind flight.Vec3) (float64, float64) {
							s := flight.State{Position: flight.Vec3{Y: height}, Velocity: flight.Vec3{X: mine}.Add(wind), Attitude: flight.Quat{W: 1}}
							return appraise(&s, flight.Vec3{X: distance, Y: height + 300}, flight.Vec3{X: his}.Add(wind), 160, stances[""], &sk, orbit{}, &tactic, wind, wind, flight.Environment{Frame: scene, Chart: scene})
						}
						a, b := score(flight.Vec3{})
						c, d := score(blown)
						if math.Abs(a-c) > 1e-9 || math.Abs(b-d) > 1e-9 {
							t.Fatalf("in a wind %v at %.0f m/s against %.0f, %.0f m apart at %.0f m, the score is %.6f / %.6f in still air and %.6f / %.6f in the wind", blown, mine, his, distance, height, a, b, c, d)
						}
					}
				}
			}
		}
	}
	for _, slow := range []float64{90, 110, 130} {
		score := func(height float64) float64 {
			actual := slow * math.Sqrt(1.225/flight.Atmosphere(height, flight.Environment{Frame: scene, Chart: scene}).Density)
			s := flight.State{Position: flight.Vec3{Y: height}, Velocity: flight.Vec3{X: actual}, Attitude: flight.Quat{W: 1}}
			a, _ := appraise(&s, flight.Vec3{Y: height + 2000}, flight.Vec3{X: 300}, 160, stances[""], &sk, orbit{}, &tactic, flight.Vec3{}, flight.Vec3{}, flight.Environment{Frame: scene, Chart: scene})
			return a
		}
		if low, high := score(1500), score(7500); math.Abs(low-high) > 1e-9 {
			t.Errorf("slow at %.0f m/s equivalent, the score is %.6f at 1,500 m and %.6f at 7,500", slow, low, high)
		}
	}
}

// TestBotPlansThroughTheAir: the merge's game plan weighs my energy against
// his through the air. Head-on along the wind, the same airspeeds give the
// same plan in the match's wind as in still air, across a sweep of my speed
// that crosses from the one-circle to the two-circle; read over the ground,
// the jet running downwind would think itself richer and the one into it
// poorer.
func TestBotPlansThroughTheAir(t *testing.T) {
	stage := func(speed float64, still bool) string {
		made, _ := New().Create(game.Session{Identifier: "plan", Game: "air", Mode: "teams", Capacity: 16, Seed: 4,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		var expert, mark *craft
		for _, slot := range i.slots() {
			if c := i.aircraft[slot]; c != nil && c.brain != nil {
				expert = c
			} else if c != nil {
				mark = c
			}
		}
		if still {
			i.environment.Wind = flight.Vec3{}
			expert.model.Environment.Wind, mark.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		along := trades.Scale(-1 / trades.Length()) // into the wind
		mark.model.State.Position = flight.Vec3{Y: 4500}
		mark.model.State.Velocity = along.Scale(220).Add(flight.Wind(mark.model.State.Position, 0, i.environment))
		mark.model.State.Attitude = flight.Look(along)
		expert.model.State.Position = along.Scale(500).Add(flight.Vec3{Y: 4450}).Add(along.Cross(flight.Vec3{Y: 1}).Scale(30))
		expert.model.State.Velocity = along.Scale(-speed).Add(flight.Wind(expert.model.State.Position, 0, i.environment))
		expert.model.State.Attitude = flight.Look(along.Scale(-1))
		i.Step(0, nil)
		return expert.brain.plan
	}
	plans := map[string]int{}
	for speed := 140.0; speed <= 320; speed += 5 {
		still, blown := stage(speed, true), stage(speed, false)
		plans[still]++
		if still != blown {
			t.Errorf("at %.0f m/s through the air the plan is %q in still air and %q in the wind", speed, still, blown)
		}
	}
	if plans["one"] == 0 || plans["two"] == 0 {
		t.Fatalf("the sweep never crosses from the one-circle to the two-circle: %v", plans)
	}
}

// TestWingmanHoldsStationThroughTheAir: a wingman on station matches his lead's
// AIRSPEED, so the throttle he sets there is the one still air sets, whichever
// way the pair flies through the wind. Matched to the lead's ground speed, he
// would sit a wind's worth fast going downwind and slow going up it.
func TestWingmanHoldsStationThroughTheAir(t *testing.T) {
	throttle := func(along flight.Vec3, still bool) float64 {
		made, _ := New().Create(game.Session{Identifier: "wing", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"intermediate": 1.0}}}})
		i := made.(*instance)
		i.Join(game.Player{Name: "human", Slot: 0, Team: "red"})
		human, bot := i.aircraft[0], i.aircraft[99]
		if still {
			i.environment.Wind = flight.Vec3{}
			human.model.Environment.Wind, bot.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		lead := flight.Vec3{Y: 4000}
		station := lead.Add(along.Cross(flight.Vec3{Y: 1}).Normalize().Scale(bot.brain.tactics.form.abeam))
		for tick := uint64(0); tick < 30; tick++ {
			aloft(human, lead, along.Scale(220))
			aloft(bot, station, along.Scale(220))
			i.Step(tick, nil)
		}
		defer i.Close() // the server-wide bot budget
		if bot.brain.mode != "form" {
			t.Fatalf("bot mode %q, want form on the human lead", bot.brain.mode)
		}
		return bot.brain.throttle
	}
	down := trades.Scale(1 / trades.Length())
	across := down.Cross(flight.Vec3{Y: 1})
	for name, along := range map[string]flight.Vec3{"downwind": down, "upwind": down.Scale(-1), "across": across} {
		calm, blown := throttle(along, true), throttle(along, false)
		if math.Abs(blown-calm) > 0.03 {
			t.Errorf("on station flying %s the wingman sets throttle %.2f in the wind and %.2f in still air", name, blown, calm)
		}
	}
}

// TestSaddleMatchesHisAirspeed: parked behind a target, the saddle matches his
// AIRSPEED with a closing bias, so the throttle it sets there is the one still
// air sets, whichever way the pair flies through the wind. Matched to his
// ground speed, it would chase a wind's worth of closure that is not there
// going downwind and fall back going up it.
func TestSaddleMatchesHisAirspeed(t *testing.T) {
	type run struct {
		i            *instance
		expert, prey *craft
	}
	build := func(still bool) run {
		made, _ := New().Create(game.Session{Identifier: "saddle", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		r := run{i, i.aircraft[99], i.aircraft[98]}
		if still {
			i.environment.Wind = flight.Vec3{}
			r.expert.model.Environment.Wind, r.prey.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		r.expert.brain.plan, r.expert.brain.planned = "two", 1 // mid-fight: the merge is history
		return r
	}
	down := trades.Scale(1 / trades.Length())
	base := flight.Vec3{Y: 4000}
	for name, along := range map[string]flight.Vec3{"downwind": down, "upwind": down.Scale(-1)} {
		calm, blown := build(true), build(false)
		defer calm.i.Close() // the server-wide bot budget
		defer blown.i.Close()
		compared := 0
		for tick := uint64(0); tick < 60*4; tick++ {
			for _, r := range []run{calm, blown} {
				aloft(r.expert, base, along.Scale(220))
				aloft(r.prey, base.Add(along.Scale(500)), along.Scale(220))
				r.i.Step(tick, nil)
			}
			if calm.expert.brain.mode != "saddle" || blown.expert.brain.mode != "saddle" {
				continue
			}
			compared++
			if c, b := calm.expert.brain.throttle, blown.expert.brain.throttle; math.Abs(b-c) > 0.03 {
				t.Fatalf("saddled %s at tick %d the expert sets throttle %.2f in the wind and %.2f in still air", name, tick, b, c)
			}
		}
		if compared < 60 {
			t.Fatalf("flying %s the saddle held in both airs for only %d ticks", name, compared)
		}
	}
}

// TestNearestFliesTheAir: the pass predictor turns each jet's velocity through
// the air and lets the wind carry the circles, so two jets turning in one
// steady wind pass each other as they do in still air: the wind carries them
// both alike. Turned over the ground, each circle turned the wind with it.
func TestNearestFliesTheAir(t *testing.T) {
	blown := flight.Vec3{X: -25, Z: 8}
	for _, pull := range []float64{-50, -20, 20, 50} {
		read := func(wind flight.Vec3) (flight.Vec3, float64) {
			me := flight.State{Position: flight.Vec3{Y: 4000}, Velocity: flight.Vec3{X: 200}.Add(wind)}
			prey := track{position: flight.Vec3{X: 900, Y: 4000, Z: 400}, velocity: flight.Vec3{X: -150, Z: -60}.Add(wind), swing: flight.Vec3{X: -18, Z: 45}, blown: wind}
			return nearest(&prey, &me, prey.position, flight.Vec3{Z: pull}, wind, 8)
		}
		calm, at := read(flight.Vec3{})
		carried, when := read(blown)
		if at <= 0.1 || at >= 8 {
			t.Fatalf("turning at %.0f m/s² the pass comes at %.2f s, not inside the horizon", pull, at)
		}
		if carried.Subtract(calm).Length() > 0.5 || math.Abs(when-at) > 0.05 {
			t.Errorf("turning at %.0f m/s² the pass misses by %.0f m at %.2f s in still air and by %.0f m at %.2f s in the wind",
				pull, calm.Length(), at, carried.Length(), when)
		}
	}
}

// TestComposeTrimsThroughTheAir: the stick's 1 g reference is the FCS's own,
// cos γ of the path through the air, which is the path the lift holds up.
// Read over the ground, a climb with the wind behind it is shallower than the
// one the wing flies and the reference sits high; into the wind, low. The push
// law shows it bare: pushing for nothing, the stick is the reference over 3.5.
func TestComposeTrimsThroughTheAir(t *testing.T) {
	made, _ := New().Create(game.Session{Identifier: "trim", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
		Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}}}})
	i := made.(*instance)
	defer i.Close() // the server-wide bot budget
	c := i.aircraft[99]
	climb := 40 * math.Pi / 180
	down := trades.Scale(1 / trades.Length())
	for name, along := range map[string]flight.Vec3{"downwind": down, "upwind": down.Scale(-1)} {
		s := &c.model.State
		s.Position = flight.Vec3{Y: 4000}
		path := along.Scale(220 * math.Cos(climb)).Add(flight.Vec3{Y: 220 * math.Sin(climb)})
		s.Velocity = path.Add(flight.Wind(s.Position, s.Time, c.model.Environment))
		s.Attitude = flight.Look(path.Normalize())
		in := c.brain.compose(c.model, s.Velocity.Normalize(), 0, 1, 0, 0, false, 0)
		if level := -3.5 * in.Pitch; math.Abs(level-math.Cos(climb)) > 0.005 {
			t.Errorf("climbing 40 degrees through the air %s, the reference is %.3f g, not cos γ %.3f", name, level, math.Cos(climb))
		}
	}
}

// TestDefenceReadsTheAir: under attack, the expert prices the attacker's
// energy and overshoot through the air, as it prices its own, and reads where
// his nose points from his heading through the air, not his track. Along the
// wind, the rope-a-dope (climb away from an attacker holding less energy) and
// the scissors (reverse under one overshooting faster than me) begin and end
// at the speeds still air puts them; over the ground the wind behind him lends
// him its speed, worth 500 m of height to a 220 m/s jet. Across the wind,
// his nose on me (a guns solution forming, a threat, neither) is judged at the
// angles still air judges it, and so is the rope's own nose gate; over the
// ground the drift angle, seven degrees, swings them.
func TestDefenceReadsTheAir(t *testing.T) {
	// defend flies the expert for half a second with the drone placed astern
	// each tick and its track handed over (astern, where nobody sees), and names
	// what it chose: its mode, and whether the guns jink ran.
	defend := func(still bool, place func(expert, drone *craft)) (string, flight.Vec3) {
		made, _ := New().Create(game.Session{Identifier: "defence", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		defer i.Close() // the server-wide bot budget: a sweep builds hundreds of these
		expert, drone := i.aircraft[99], i.aircraft[98]
		if still {
			i.environment.Wind = flight.Vec3{}
			expert.model.Environment.Wind, drone.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		for tick := uint64(0); tick < 30; tick++ {
			place(expert, drone)
			s := &drone.model.State
			i.recall(expert.brain)
			expert.brain.known[98] = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, drone.model.Environment)}
			i.Step(tick, nil)
		}
		i.recall(expert.brain) // its aim, in the session's frame
		if expert.brain.jink > 0 {
			return expert.brain.mode + ", jinking", expert.brain.aim
		}
		return expert.brain.mode, expert.brain.aim
	}
	sweep := func(what string, from, to, step float64, place func(x float64) func(expert, drone *craft), want ...string) {
		chosen := map[string]int{}
		for x := from; x <= to; x += step {
			calm, aim := defend(true, place(x))
			blown, aimed := defend(false, place(x))
			chosen[calm]++
			if calm != blown {
				t.Errorf("%s at %.0f: the expert flies %q in still air and %q in the wind", what, x, calm, blown)
			} else if off := math.Acos(clamp(aim.Dot(aimed), -1, 1)) * 180 / math.Pi; off > 0.5 {
				t.Errorf("%s at %.0f: flying %q the expert aims %.1f degrees apart in still air and the wind", what, x, calm, off)
			}
		}
		for _, w := range want {
			if chosen[w] == 0 {
				t.Fatalf("%s: the sweep never reaches %q: %v", what, w, chosen)
			}
		}
	}
	turned := func(along flight.Vec3, degrees float64) flight.Vec3 {
		return flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(along)
	}
	down := trades.Scale(1 / trades.Length())
	base := flight.Vec3{Y: 4000}
	for name, along := range map[string]flight.Vec3{"downwind": down, "upwind": down.Scale(-1)} {
		sweep(name+", his speed 1,300 m astern", 160, 220, 4, func(speed float64) func(expert, drone *craft) {
			return func(expert, drone *craft) {
				aloft(expert, base, along.Scale(220))
				aloft(drone, base.Subtract(along.Scale(1300)), turned(along, 35).Scale(speed)) // his nose 35 degrees off me: a threat, not a solution
			}
		}, "rope", "defense")
		sweep(name+", his overshoot 600 m astern", 40, 80, 4, func(faster float64) func(expert, drone *craft) {
			return func(expert, drone *craft) {
				aloft(expert, base, along.Scale(220))
				aloft(drone, base.Subtract(along.Scale(600)), along.Scale(220+faster))
			}
		}, "scissors", "defense, jinking")
	}
	across := down.Cross(flight.Vec3{Y: 1})
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1)} {
		sweep(name+", his nose 800 m astern", 0, 64, 1, func(off float64) func(expert, drone *craft) {
			return func(expert, drone *craft) {
				aloft(expert, base, along.Scale(220))
				aloft(drone, base.Subtract(along.Scale(800)), turned(along, off).Scale(220))
			}
		}, "defense, jinking", "defense")
		sweep(name+", a slower attacker's nose 1,300 m astern", 14, 40, 1, func(off float64) func(expert, drone *craft) {
			return func(expert, drone *craft) {
				aloft(expert, base, along.Scale(220))
				aloft(drone, base.Subtract(along.Scale(1300)), turned(along, off).Scale(170)) // a thousand metres of energy under mine
			}
		}, "rope", "spiral")
	}
}

// alone builds a red expert alone in a match, in still air or the match's wind,
// and hands back the instance's close: the bot budget is server-wide.
func alone(still bool) (*craft, func()) {
	made, _ := New().Create(game.Session{Identifier: "alone", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
		Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}}}})
	i := made.(*instance)
	c := i.aircraft[99]
	if still {
		i.environment.Wind = flight.Vec3{}
		c.model.Environment.Wind = flight.Vec3{}
	}
	return c, i.Close
}

// flown sets a jet at a height on a path through its air, at an equivalent
// airspeed, nose on the path.
func flown(c *craft, height, equivalent float64, path flight.Vec3) {
	s := &c.model.State
	s.Position = flight.Vec3{Y: height}
	tas := equivalent / math.Sqrt(flight.Atmosphere(height, c.model.Environment).Density/1.225)
	air := path.Normalize().Scale(tas)
	s.Velocity = air.Add(flight.Wind(s.Position, s.Time, c.model.Environment))
	s.Attitude = flight.Look(air)
}

// TestWeaveHoldsEquivalentAirspeed: the cruise weave's throttle holds an
// equivalent airspeed, so a jet at the same one sets the same throttle low
// and high; in true airspeed the thin air at 7,500 m reads half as fast again.
func TestWeaveHoldsEquivalentAirspeed(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	for _, equivalent := range []float64{120, 140, 160} {
		throttle := map[float64]float64{}
		for _, height := range []float64{1500, 7500} {
			c, done := alone(false)
			flown(c, height, equivalent, down)
			weave(99, c, 0)
			throttle[height] = c.latest.Throttle
			done()
		}
		if math.Abs(throttle[1500]-throttle[7500]) > 1e-6 {
			t.Errorf("weaving at %.0f m/s equivalent the throttle is %.3f at 1,500 m and %.3f at 7,500 m", equivalent, throttle[1500], throttle[7500])
		}
	}
}

// TestRecoveryPushesBelowTheStall: the floor's recovery pulls, unless the wing
// is stalled, when it pushes through and flies out; the stall is an equivalent
// airspeed, so the push comes below tuned(80) of it at 800 m too, where the
// true airspeed reads four per cent higher.
func TestRecoveryPushesBelowTheStall(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	sink := down.Add(flight.Vec3{Y: -0.18}) // a shallow descent under the floor's 900 m: the recovery has the stick
	for equivalent := tuned(80) - 4; equivalent <= tuned(80)+4; equivalent += 0.25 {
		c, done := alone(false)
		flown(c, 800, equivalent, sink)
		in := c.brain.steer(c.model, 0)
		done()
		if pushed := in.Pitch < 0; pushed != (equivalent < tuned(80)) {
			t.Errorf("recovering at %.2f m/s equivalent, under the stall at %.2f: stick %.2f", equivalent, tuned(80), in.Pitch)
		}
	}
}

// TestLimiterReadsEquivalentAirspeed: the rolling-pull compensation applies
// above the corner, which is an equivalent airspeed, so rolling hard at the
// same equivalent airspeed the stick is the same low and high.
func TestLimiterReadsEquivalentAirspeed(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	right := down.Cross(flight.Vec3{Y: 1}).Scale(-1)
	pitch := map[float64]float64{}
	for _, height := range []float64{1500, 7500} {
		c, done := alone(false)
		flown(c, height, 0.95*corner(c.model), down)
		var in flight.Inputs
		for tick := uint64(0); tick < 20; tick++ {
			in = c.brain.compose(c.model, right, 6, 1, 0, 0, false, tick) // a hard turn the roll has to come round for
		}
		pitch[height] = in.Pitch
		done()
	}
	if math.Abs(pitch[1500]-pitch[7500]) > 1e-3 {
		t.Errorf("rolling into a hard turn at 0.95 corner the stick is %.3f at 1,500 m and %.3f at 7,500 m", pitch[1500], pitch[7500])
	}
}

// TestComposeTurnsThroughTheAir: the law steers the path through the air, and
// the g a turn rate needs goes with the true airspeed along it, so a jet flying
// with the wind pulls up onto an aim three degrees above its path with the
// same stick as in still air. Over the ground the wind behind it would ask for
// more g, ahead of it less, and a crosswind would add its drift to the error.
func TestComposeTurnsThroughTheAir(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	for name, along := range map[string]flight.Vec3{"downwind": down, "upwind": down.Scale(-1)} {
		read := func(still bool) flight.Inputs {
			c, done := alone(still)
			defer done()
			flown(c, 4000, 170, along)
			path := airflow(&c.model.State, c.model.Environment).Normalize()
			aim := flight.Axis(path.Cross(flight.Vec3{Y: 1}).Normalize(), 3*math.Pi/180).Rotate(path) // up, in the plane the wings already lift in: no roll to wait for
			var in flight.Inputs
			for tick := uint64(0); tick < 4; tick++ { // past the first tick's rate estimate, which has no previous error to difference
				in = c.brain.compose(c.model, aim, 6, 1, 0, 0, false, tick)
			}
			return in
		}
		calm, blown := read(true), read(false)
		if calm.Pitch <= 0 {
			t.Fatalf("three degrees under the aim flying %s the stick is %.3f: no pull", name, calm.Pitch)
		}
		if math.Abs(calm.Pitch-blown.Pitch) > 1e-3 {
			t.Errorf("three degrees under the aim flying %s the stick is %.3f in still air and %.3f in the wind", name, calm.Pitch, blown.Pitch)
		}
	}
}

// TestJudgeReadsTheAir: the fight's intent prices both jets' energy through
// the air and holds his speed against the doctrine's "slow" as an equivalent
// airspeed. Committed to converting high up, a slow opponent breaks the
// commitment for the finish at the equivalent airspeed it does low down,
// where his true airspeed reads half as fast again; and along the wind, the
// finish begins at the speeds, his and mine, still air begins it at.
func TestJudgeReadsTheAir(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	const tick = 100000
	// judged hands the expert's judge one instant: me at a height and speed,
	// him 1,500 m ahead and 300 m below on the same heading, and returns the
	// intent it settles on.
	judged := func(still bool, intent string, along flight.Vec3, height, mine, his float64) string {
		c, done := alone(still)
		defer done()
		b := c.brain
		flown(c, height, mine, along)
		position := c.model.State.Position.Add(along.Scale(1500)).Add(flight.Vec3{Y: -300})
		tas := his / math.Sqrt(flight.Atmosphere(position.Y, c.model.Environment).Density/1.225)
		blown := flight.Wind(position, 0, c.model.Environment)
		prey := &track{when: tick, position: position, velocity: along.Scale(tas).Add(blown), blown: blown}
		b.intent, b.minded, b.chanced, b.promise = intent, tick-60, tick, 0
		b.judge(&c.model.State, prey, tick, 1530, -1, 2000, c.model.Environment)
		return b.intent
	}
	if intent := judged(false, "convert", down, 7500, 190, tuned(170)-10); intent != "finish" { // my energy a fifth over his: the finish is earned
		t.Errorf("committed to converting at 7,500 m against a jet at %.0f m/s equivalent, under the slow %.0f, the intent stays %q", tuned(170)-10, tuned(170), intent)
	}
	for name, along := range map[string]flight.Vec3{"downwind": down, "upwind": down.Scale(-1)} {
		intents := map[string]int{}
		for his := tuned(170) - 8; his <= tuned(170)+8; his += 1 {
			calm, blown := judged(true, "", along, 4000, 190, his), judged(false, "", along, 4000, 190, his)
			intents[calm]++
			if calm != blown {
				t.Errorf("flying %s against a jet at %.0f m/s equivalent the intent is %q in still air and %q in the wind", name, his, calm, blown)
			}
		}
		for mine := 120.0; mine <= 175; mine += 2.5 {
			calm, blown := judged(true, "", along, 4000, mine, 120), judged(false, "", along, 4000, mine, 120)
			intents[calm]++
			if calm != blown {
				t.Errorf("flying %s at %.0f m/s equivalent against a slow jet the intent is %q in still air and %q in the wind", name, mine, calm, blown)
			}
		}
		if intents["finish"] == 0 || intents["convert"] == 0 {
			t.Fatalf("flying %s the sweeps never cross between convert and finish: %v", name, intents)
		}
	}
}

// TestPerchReadsTheAir: the perch-and-pounce diagnosis reads him through the
// air. He has dived on me when his speed at me through the air passes 120 m/s,
// and still air and the wind agree on when that is; he loiters slow on his
// perch, and I am too slow to keep climbing for the sky, at the equivalent
// airspeeds the doctrine names, high up where the true airspeed reads two
// fifths more.
func TestPerchReadsTheAir(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	const tick = 100000
	// perched runs the duel's tick for the expert at a height and equivalent
	// airspeed with him placed off it, and returns the brain to read.
	perched := func(still bool, height, mine float64, offset flight.Vec3, his func(position flight.Vec3, density float64) flight.Vec3, prime func(b *brain), age uint64) *brain {
		made, _ := New().Create(game.Session{Identifier: "perch", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		c := i.aircraft[99]
		if still {
			i.environment.Wind = flight.Vec3{}
			c.model.Environment.Wind = flight.Vec3{}
		}
		flown(c, height, mine, down)
		b := c.brain
		position := c.model.State.Position.Add(offset)
		blown := flight.Wind(position, 0, c.model.Environment)
		velocity := his(position, flight.Atmosphere(position.Y, c.model.Environment).Density).Add(blown)
		prey := &track{when: tick - age, position: position.Subtract(velocity.Scale(float64(age) / 60)), velocity: velocity, blown: blown} // the look age ticks old
		b.intent, b.minded, b.chanced, b.promise = "convert", tick-60, tick, 0
		prime(b)
		direction, distance := i.bearing(c.model.State.Position, prey.carried(float64(tick)/60))
		i.duel(99, c, tick, prey, direction, distance, direction.Dot(prey.velocity.Subtract(prey.blown).Normalize()), -1, 2000)
		return b
	}
	// He dives at me from 600 m above and 1,000 m ahead, down the line between
	// us at a speed through the air.
	ahead := down.Scale(1000).Add(flight.Vec3{Y: 600})
	dove := map[bool]int{}
	for speed := 100.0; speed <= 140; speed += 2 {
		at := func(position flight.Vec3, density float64) flight.Vec3 { return ahead.Normalize().Scale(-speed) }
		calm := perched(true, 3000, 160, ahead, at, func(b *brain) {}, 0).dove != 0
		blown := perched(false, 3000, 160, ahead, at, func(b *brain) {}, 0).dove != 0
		dove[calm]++
		if calm != blown {
			t.Errorf("diving at me at %.0f m/s through the air he has dived in still air: %v, in the wind: %v", speed, calm, blown)
		}
	}
	if dove[true] == 0 || dove[false] == 0 {
		t.Fatalf("the dive sweep never crosses 120 m/s: %v", dove)
	}
	// He sits 600 m above me, 1,500 m ahead, flying my way, after a dive on me.
	above := down.Scale(1500).Add(flight.Vec3{Y: 600})
	recently := func(b *brain) { b.dove, b.ceded = tick-100, tick-400 }
	for his := tuned(200) - 9.5; his <= tuned(200)+10; his += 1 { // off the boundary itself, which the round trip through true airspeed blurs
		at := func(position flight.Vec3, density float64) flight.Vec3 {
			return down.Scale(his / math.Sqrt(density/1.225))
		}
		if calm := perched(false, 6500, 170, above, at, recently, 0).audit["calm"] > 0; calm != (his < tuned(200)) {
			t.Errorf("perched at %.0f m/s equivalent against the slow of %.0f, he reads loitering: %v", his, tuned(200), calm)
		}
	}
	// Mid-regain, climbing out from under him.
	climbing := func(b *brain) { b.dove, b.ceded, b.regained = tick-100, tick-400, tick-10 }
	floor := tuned(154) * 0.85
	for mine := floor - 9.5; mine <= floor+10; mine += 1 {
		at := func(position flight.Vec3, density float64) flight.Vec3 { return down.Scale(220) }
		if slow := perched(false, 6500, mine, above, at, climbing, 0).audit["slow"] > 0; slow != (mine < floor) {
			t.Errorf("regaining at %.0f m/s equivalent against the floor's %.0f, the climb ends slow: %v", mine, floor, slow)
		}
	}
	// Holding the regain, the climb turns away from where he is on a look half
	// a second old: where the air has carried him since, as still air puts him.
	// Read where the look left him over the sea, the climb turned off it by the
	// trades' drift, 15 m across 1,500.
	for _, age := range []uint64{0, 30} {
		// He crosses my nose.
		at := func(position flight.Vec3, density float64) flight.Vec3 {
			return down.Cross(flight.Vec3{Y: 1}).Scale(220)
		}
		calm := perched(true, 6500, 200, above, at, climbing, age)
		blown := perched(false, 6500, 200, above, at, climbing, age)
		if calm.play != "regain" || blown.play != "regain" {
			t.Fatalf("mid-regain on a look %d ticks old the expert flies %q in still air and %q in the wind", age, calm.play, blown.play)
		}
		if off := math.Acos(clamp(calm.aim.Dot(blown.aim), -1, 1)) * 180 / math.Pi; off > 1e-6 {
			t.Errorf("holding the regain on a look %d ticks old, the climb turns %.4f degrees off still air's", age, off)
		}
	}
}

// TestOffenceReadsTheAir: on the attack, the expert reads his tail from his
// heading through the air. Across the wind his track sits the drift angle off
// it, seven degrees at 220 m/s, and still air and the wind must agree on when
// I am behind his 3/9 line (offence), when a slow jet close behind him is
// saddled rather than starved, and where the stalemate's lag cut toward his
// six goes.
func TestOffenceReadsTheAir(t *testing.T) {
	// engage flies the expert for half a second with the drone placed ahead
	// each tick and its track handed over, and returns its brain.
	engage := func(still bool, place func(expert, drone *craft), prime func(b *brain)) *brain {
		made, _ := New().Create(game.Session{Identifier: "offence", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		expert, drone := i.aircraft[99], i.aircraft[98]
		if still {
			i.environment.Wind = flight.Vec3{}
			expert.model.Environment.Wind, drone.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		prime(expert.brain)
		for tick := uint64(0); tick < 30; tick++ {
			place(expert, drone)
			s := &drone.model.State
			i.recall(expert.brain)
			expert.brain.known[98] = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, drone.model.Environment)}
			i.Step(tick, nil)
		}
		i.recall(expert.brain) // what it remembers, in the session's frame
		return expert.brain
	}
	down := trades.Scale(1 / trades.Length())
	base := flight.Vec3{Y: 4000}
	across := down.Cross(flight.Vec3{Y: 1})
	turned := func(along flight.Vec3, degrees float64) flight.Vec3 {
		return flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(along)
	}
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1)} {
		// His tail: 1,500 m ahead, his heading swung off the line from me, from
		// well behind his 3/9 line to converging on me.
		modes := map[string]int{}
		for off := 55.0; off <= 125; off += 1 {
			place := func(expert, drone *craft) {
				aloft(expert, base, along.Scale(220))
				aloft(drone, base.Add(along.Scale(1500)), turned(along, off).Scale(220))
			}
			calm, blown := engage(true, place, func(*brain) {}), engage(false, place, func(*brain) {})
			modes[calm.mode]++
			if calm.mode != blown.mode || math.Abs(calm.tail-blown.tail) > 0.01 {
				t.Errorf("flying %s, his heading %.0f degrees off the line from me, the expert flies %q at tail %.3f in still air and %q at %.3f in the wind", name, off, calm.mode, calm.tail, blown.mode, blown.tail)
			} else if apart := math.Acos(clamp(calm.aim.Dot(blown.aim), -1, 1)) * 180 / math.Pi; apart > 0.5 {
				t.Errorf("flying %s, his heading %.0f degrees off the line from me, flying %q the expert aims %.1f degrees apart in still air and the wind", name, off, calm.mode, apart)
			}
		}
		if modes["offense"] == 0 || modes["neutral"] == 0 {
			t.Fatalf("flying %s the heading sweep never crosses from his tail to converging: %v", name, modes)
		}
		// Saddled or starved: under the floor, 700 m behind him.
		modes = map[string]int{}
		planned := func(b *brain) { b.plan, b.planned = "two", 1 } // mid-fight: the floor arms once a merge plan exists
		for off := 60.0; off <= 80; off += 1 {
			place := func(expert, drone *craft) {
				aloft(expert, base, along.Scale(130)) // 106 m/s equivalent at 4,000 m: under the expert's floor
				aloft(drone, base.Add(along.Scale(700)), turned(along, off).Scale(130))
			}
			calm, blown := engage(true, place, planned), engage(false, place, planned)
			modes[calm.mode]++
			if calm.mode != blown.mode {
				t.Errorf("flying %s slow, 700 m from him with his heading %.0f degrees off the line, the expert flies %q in still air and %q in the wind", name, off, calm.mode, blown.mode)
			} else if apart := math.Acos(clamp(calm.aim.Dot(blown.aim), -1, 1)) * 180 / math.Pi; apart > 0.5 {
				t.Errorf("flying %s slow, 700 m from him with his heading %.0f degrees off the line, flying %q the expert aims %.1f degrees apart in still air and the wind", name, off, calm.mode, apart)
			}
		}
		if modes["rebuild"] == 0 || len(modes) < 2 {
			t.Fatalf("flying %s slow the heading sweep never crosses from saddled to starved: %v", name, modes)
		}
		// The stalemate's cut toward his six.
		place := func(expert, drone *craft) {
			aloft(expert, base, along.Scale(220))
			aloft(drone, base.Add(along.Scale(1500)), turned(along, 38).Scale(220)) // crossing, closing under 60 m/s: a circle that does not resolve
		}
		stuck := func(b *brain) { b.stuck = 1000 }
		calm, blown := engage(true, place, stuck), engage(false, place, stuck)
		if calm.mode != "displace" || blown.mode != "displace" {
			t.Fatalf("flying %s in a stalemate the expert flies %q in still air and %q in the wind, not the displacement", name, calm.mode, blown.mode)
		}
		if angle := math.Acos(clamp(calm.aim.Dot(blown.aim), -1, 1)) * 180 / math.Pi; angle > 0.5 {
			t.Errorf("flying %s the cut toward his six aims %.1f degrees apart in still air and the wind", name, angle)
		}
	}
}

// TestHeaterReachReadsHisHeading: the heater's reach grows as I sit behind his
// tailpipe, and his tailpipe points down his heading through the air. Crossing
// the wind, the range at which the shot is in reach is the still air's; over
// the ground his track's drift angle would swing his tail toward or away from
// me.
func TestHeaterReachReadsHisHeading(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	across := down.Cross(flight.Vec3{Y: 1})
	const tick = 1000
	reach := func(still bool, along flight.Vec3, distance float64) bool {
		made, _ := New().Create(game.Session{Identifier: "reach", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
			Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		expert, drone := i.aircraft[99], i.aircraft[98]
		if still {
			i.environment.Wind = flight.Vec3{}
			expert.model.Environment.Wind, drone.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		aloft(expert, flight.Vec3{Y: 4000}, along.Scale(220))
		aloft(drone, flight.Vec3{Y: 4000}.Add(along.Scale(distance)), flight.Axis(flight.Vec3{Y: 1}, 50*math.Pi/180).Rotate(along).Scale(220))
		s := &drone.model.State
		b := expert.brain
		b.prey = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, drone.model.Environment)}
		b.known[98], b.target, b.shoot = b.prey, 98, true
		i.trigger(99, expert, tick)
		if !b.gate.live {
			t.Fatalf("the trigger never judged the heater shot at %.0f m", distance)
		}
		return b.gate.reach
	}
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1)} {
		reached := map[bool]int{}
		for distance := 0.3 * missile_range; distance <= 1.5*missile_range; distance += 0.01 * missile_range {
			calm, blown := reach(true, along, distance), reach(false, along, distance)
			reached[calm]++
			if calm != blown {
				t.Errorf("flying %s at %.0f m behind a jet crossing at 50 degrees, the heater is in reach in still air: %v, in the wind: %v", name, distance, calm, blown)
			}
		}
		if reached[true] == 0 || reached[false] == 0 {
			t.Fatalf("flying %s the range sweep never crosses the heater's reach: %v", name, reached)
		}
	}
}

// TestSandwichReadsHisHeading: an enemy whose nose is committed on my
// teammate outranks a nearer one, and his nose is his heading through the
// air. Crossing the wind, the expert turns on him at the angle still air
// does; read off his track, the drift angle would move the commitment.
func TestSandwichReadsHisHeading(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	across := down.Cross(flight.Vec3{Y: 1})
	turned := func(along flight.Vec3, degrees float64) flight.Vec3 {
		return flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(along)
	}
	base := flight.Vec3{Y: 4000}
	chosen := func(still bool, along flight.Vec3, off float64) (int, int) {
		i, expert, mate, blue1, blue2 := teams(t)
		defer i.Close()
		if still {
			i.environment.Wind = flight.Vec3{}
			for _, c := range []*craft{expert, mate, blue1, blue2} {
				c.model.Environment.Wind = flight.Vec3{}
			}
		}
		side := along.Cross(flight.Vec3{Y: 1})
		station := base.Add(along.Scale(3800)).Add(side.Scale(600))
		for tick := uint64(0); tick < 90; tick++ {
			aloft(expert, base, along.Scale(220))
			aloft(mate, station, along.Scale(220))
			aloft(blue1, base.Add(along.Scale(1800)).Subtract(side.Scale(400)), turned(along, -30).Scale(220)) // nearer, minding its own business
			aloft(blue2, station.Subtract(along.Scale(1200)), turned(along, off).Scale(240))                   // astern of the mate, his nose off the line to it
			i.recall(expert.brain)
			for _, c := range []*craft{blue1, blue2} {
				s := &c.model.State
				expert.brain.known[c.player.Slot] = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, c.model.Environment)}
			}
			i.Step(tick, nil)
		}
		return expert.brain.target, blue2.player.Slot
	}
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1)} {
		targets := map[bool]int{}
		for off := 14.0; off <= 32; off += 1 {
			calm, attacker := chosen(true, along, off)
			blown, _ := chosen(false, along, off)
			targets[calm == attacker]++
			if calm != blown {
				t.Errorf("flying %s with the attacker's nose %.0f degrees off my teammate, the expert targets %d in still air and %d in the wind", name, off, calm, blown)
			}
		}
		if targets[true] == 0 || targets[false] == 0 {
			t.Fatalf("flying %s the heading sweep never crosses the attacker's commitment: %v", name, targets)
		}
	}
}

// choice is what the scripted fight chose: its mode and branch, whether the
// run-in is over, whether it is clearing a pass and which way (over, under,
// level), and whether it is chasing.
type choice struct {
	mode, branch    string
	passed, cleared bool
	way             string
	chasing         bool
}

// scripted holds a scripted bandit and the pilot it fights on fixed lines
// through the air for a number of ticks, the bandit turning at one rate and
// the pilot at another (rad/s), the pilot accelerating along his path (m/s2),
// and returns what the bandit chose. mine and his are velocities through the
// air; the pilot sits at the offset, and the air carries the pair: a track a
// few ticks old has him where his speed over the ground has since taken him.
// fighting puts the fight, not the run-in, in hand from the tick given.
func scripted(still bool, level string, missiles bool, ticks int, height float64, mine, his, offset flight.Vec3, bank, turn, push float64, fighting bool, from int) choice {
	air := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: trades}
	if still {
		air.Wind = flight.Vec3{}
	}
	b := NewBandit(level, 1, "", false, missiles, "", 0, false)
	defer b.arena.Close()
	b.Air(air)
	at := flight.Vec3{Y: height}
	b.Spawn(at, mine)
	pm := flight.New(aircraft.Get("fa18c"), air, flight.World{Sea: sea})
	pm.State = flight.Level(pm, at.Add(offset), his.Normalize(), his.Length(), fuel)
	words := make([]float64, flight.Size)
	for tick := 0; tick < ticks; tick++ {
		if tick == from {
			b.craft.brain.routine.passed = fighting
		}
		s := &b.craft.model.State
		carried := at.Add(flight.Wind(at, 0, air).Scale(float64(tick) / 60))
		path := flight.Axis(flight.Vec3{Y: 1}, bank*float64(tick)/60).Rotate(mine)
		s.Position, s.Velocity, s.Attitude = carried, path.Add(flight.Wind(carried, s.Time, air)), flight.Look(path)
		heading := flight.Axis(flight.Vec3{Y: 1}, turn*float64(tick)/60).Rotate(his)
		heading = heading.Scale((heading.Length() + push*float64(tick)/60) / heading.Length())
		p := &pm.State
		p.Position, p.Velocity, p.Attitude = carried.Add(offset), heading.Add(flight.Wind(carried.Add(offset), s.Time, air)), flight.Look(heading)
		p.Encode(words)
		b.Mirror(words, false, true)
		b.Step()
	}
	r := &b.craft.brain.routine
	scriptedAim = b.craft.brain.aim
	c := choice{mode: b.craft.brain.mode, branch: r.branch, passed: r.passed, cleared: r.away.Length() > 0.5, chasing: r.chasing}
	switch {
	case !c.cleared:
	case r.away.Y > 0.7:
		c.way = "over"
	case r.away.Y < -0.7:
		c.way = "under"
	default:
		c.way = "level"
	}
	return c
}

// scriptedAim is the aim the last scripted() run left the bandit flying.
var scriptedAim flight.Vec3

// TestScriptReadsTheAir: the scripted fight reads where each jet points from
// its heading through the air, and both jets' speeds through it. Across 1,500
// random crossing geometries, a tick of the fight after a third of a second
// of looking, the bandit's choices are the still air's. Then aimed across the
// wind, at the lines the drift angle would move when read off the tracks: his
// first look at me, his 3/9 line with me on the gun, my 3/9 line with him
// coming, and his turn toward me as he comes nose on.
func TestScriptReadsTheAir(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	cases, differ := 0, 0
	seen := map[string]int{}
	for k := 0; k < 1500; k++ {
		height := 2500 + 4000*rng.Float64()
		mine := flight.Axis(flight.Vec3{Y: 1}, 2*math.Pi*rng.Float64()).Rotate(flight.Vec3{X: 1}).Scale(130 + 130*rng.Float64())
		his := flight.Axis(flight.Vec3{Y: 1}, 2*math.Pi*rng.Float64()).Rotate(flight.Vec3{X: 1}).Scale(130 + 130*rng.Float64())
		offset := flight.Axis(flight.Vec3{Y: 1}, 2*math.Pi*rng.Float64()).Rotate(flight.Vec3{X: 250 + 1750*rng.Float64()}).Add(flight.Vec3{Y: 120*rng.Float64() - 60}) // near one height: the shear between two heights is a real difference
		bank, turn := 0.6*rng.Float64()-0.3, 0.6*rng.Float64()-0.3                                                                                                     // my turn and his, rad/s
		fighting := rng.Intn(2) == 0                                                                                                                                   // past the run-in, or at its first look
		calm, blown := scripted(true, "expert", true, 20, height, mine, his, offset, bank, turn, 0, fighting, 0), scripted(false, "expert", true, 20, height, mine, his, offset, bank, turn, 0, fighting, 0)
		cases++
		seen[calm.branch]++
		if calm.cleared {
			seen["cleared"]++
		}
		if calm.passed && !fighting {
			seen["passed at the first look"]++
		}
		if calm != blown {
			differ++
			if differ <= 5 {
				t.Errorf("at %.0f m, mine %v turning %.2f, his %v turning %.2f, him at %v off: still air %+v, the wind %+v", height, mine, bank, his, turn, offset, calm, blown)
			}
		}
	}
	if differ > 0 {
		t.Errorf("%d of %d scripted ticks choose differently in still air and the wind", differ, cases)
	}
	if seen["cleared"] == 0 || seen["passed at the first look"] == 0 || len(seen) < 5 {
		t.Fatalf("the geometries never reach the choices they test: %v", seen)
	}

	down := trades.Scale(1 / trades.Length())
	across := down.Cross(flight.Vec3{Y: 1})
	turned := func(along flight.Vec3, degrees float64) flight.Vec3 {
		return flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(along)
	}
	sweep := func(what string, from, to, step float64, fly func(still bool, x float64) choice, tell func(c choice) string) {
		told := map[string]int{}
		crossing := tell != nil // a line the choices show; nil when the line is inside the choice, not on it
		if tell == nil {
			tell = func(c choice) string { return "" }
		}
		for x := from; x <= to; x += step {
			calm, blown := fly(true, x), fly(false, x)
			told[tell(calm)]++
			if calm != blown {
				t.Errorf("%s at %.1f: still air %+v, the wind %+v", what, x, calm, blown)
			}
		}
		if crossing && len(told) < 2 {
			t.Errorf("%s: the sweep never crosses the line it tests: %v", what, told)
		}
	}
	// collision is my velocity through the air, at my speed, that meets a jet
	// at the offset flying his: the pass comes inside the bubble.
	collision := func(offset, his flight.Vec3, speed float64) flight.Vec3 {
		u := offset.Normalize()
		along := his.Dot(u)
		across := his.Subtract(u.Scale(along))
		return across.Add(u.Scale(math.Sqrt(speed*speed - across.Dot(across))))
	}
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1)} {
		// His 3/9 line, me on the gun closing on a collision course.
		sweep(name+", me on his 3/9 line", 80.5, 100, 1, func(still bool, abeam float64) choice { // never on the line itself, where rounding decides
			his := along.Scale(200)
			offset := turned(along, 180-abeam).Scale(-350) // me abeam his heading by the angle, 350 m off: the pass inside the overtake's three seconds
			return scripted(still, "expert", true, 20, 4000, collision(offset, his, 260), his, offset, 0, 0, 0, true, 0)
		}, func(c choice) string { return fmt.Sprint(c.cleared) })
		// My 3/9 line, him coming on a collision course: the intermediate clears
		// a pass whenever he is not behind me.
		sweep(name+", him on my 3/9 line", 80.5, 100, 1, func(still bool, abeam float64) choice {
			mine := along.Scale(200)
			offset := turned(along, abeam).Scale(700) // him off my heading by the angle
			return scripted(still, "intermediate", true, 60, 4000, mine, collision(offset.Scale(-1), mine, 240), offset, 0, 0, 0, true, 0)
		}, func(c choice) string { return fmt.Sprint(c.cleared) })
		// His nose on me, turning hard across the wind, in a guns match, my wing
		// too slow to keep the miss's own way: unchased the clear goes under for
		// gravity, chased - his turn carrying his nose toward me - it keeps his
		// height and goes level or over. The fight begins once the track has
		// measured his turn, three looks in: the clear's way is chosen on its
		// first tick. Still air keeps level across the sweep, chased or not, so
		// the line is not one the choices show; read over his track, the drift
		// angle unchases the near side and sends it under.
		sweep(name+", his nose on me turning", -6, 6, 0.5, func(still bool, off float64) choice {
			mine := along.Scale(-120)
			offset := along.Scale(-900)
			const rate, from = 0.1, 26                                    // a turn the track reads as one (22 m/s2), gentle enough to pass inside the bubble
			his := turned(along, off-rate*from/60*180/math.Pi).Scale(220) // his nose at the angle as the fight begins
			return scripted(still, "expert", false, 40, 4000, mine, his, offset, 0, rate, 0, true, from)
		}, nil)
		// My own hard turn into a head-on pass in a guns match: the pass is
		// cleared when my circle and his line come inside two bubbles, and my
		// circle turns through the air, the wind carrying it.
		sweep(name+", my turn into the pass", -700, 700, 20, func(still bool, aside float64) choice {
			offset := along.Scale(1500).Add(along.Cross(flight.Vec3{Y: 1}).Scale(aside))
			return scripted(still, "expert", false, 40, 4000, along.Scale(200), along.Scale(-200), offset, 0.25, 0, 0, true, 13)
		}, func(c choice) string { return fmt.Sprint(c.cleared) })
	}
	// The run-in, head-on with him off to one side and above or below, from 5
	// km and from 1.6 km, where the lead turn and the hold come: the line, the
	// lead turn and the hold are flown onto my path through the air, to where we
	// meet in the air we share, climbing over the distance flown through it, so
	// each aims where still air aims it - across the wind and along it.
	for name, along := range map[string]flight.Vec3{"across": across, "downwind": down, "upwind": down.Scale(-1)} {
		for _, far := range []float64{5000, 1600} {
			for _, high := range []float64{-400, 0, 400} {
				for aside := -900.0; aside <= 900; aside += 300 {
					run := func(still bool) (choice, flight.Vec3) {
						offset := along.Scale(far).Add(along.Cross(flight.Vec3{Y: 1}).Scale(aside)).Add(flight.Vec3{Y: high})
						c := scripted(still, "expert", true, 30, 4500, along.Scale(220), along.Scale(-220), offset, 0, 0, 0, false, 0)
						return c, scriptedAim
					}
					calm, aim := run(true)
					blown, aimed := run(false)
					if calm != blown {
						t.Errorf("%s, running in from %.0f m with him %.0f m aside and %.0f m above: still air %+v, the wind %+v", name, far, aside, high, calm, blown)
					} else if off := math.Acos(clamp(aim.Dot(aimed), -1, 1)) * 180 / math.Pi; off > 0.5 {
						t.Errorf("%s, running in from %.0f m with him %.0f m aside and %.0f m above, the %s branch aims %.1f degrees apart in still air and the wind", name, far, aside, high, calm.branch, off)
					}
				}
			}
		}
	}
}

// TestBendingIsTheTurnThroughTheAir: how hard he is turning is his swing across
// his path through the air. Accelerating straight across the wind he is not
// turning at all; across his track, a seventh of his acceleration reads as a turn.
func TestBendingIsTheTurnThroughTheAir(t *testing.T) {
	blown := flight.Vec3{Z: 28}
	straight := track{velocity: flight.Vec3{X: 200}.Add(blown), swing: flight.Vec3{X: 10}, blown: blown}
	if turn := bending(&straight); turn > 1e-9 {
		t.Errorf("accelerating straight at 10 m/s2 across the wind he bends at %.2f m/s2", turn)
	}
	hard := track{velocity: flight.Vec3{X: 200}.Add(blown), swing: flight.Vec3{Z: 20}, blown: blown}
	if turn := bending(&hard); math.Abs(turn-20) > 1e-9 {
		t.Errorf("turning at 20 m/s2 across the wind he bends at %.2f m/s2", turn)
	}
}

// TestScriptBurnerConeIsEquivalent: in the heater span the scripted bandit
// hides its plume, military power, while his nose bears on it, inside a cone
// that widens above a speed where the energy is there to spend. The speed is an
// equivalent airspeed, as the floors are, so the wide cone begins at tuned(130).
func TestScriptBurnerConeIsEquivalent(t *testing.T) {
	n := standard().script.cold
	for speed := tuned(n.speed) - 3.25; speed <= tuned(n.speed)+3; speed += 0.5 {
		bandit := NewBandit("expert", 1, "", false, true, "fox2", 0, false)
		i, b := bandit.arena, bandit.craft.brain
		me := &bandit.craft.model.State
		me.Position, me.Velocity = flight.Vec3{Y: 4000}, flight.Vec3{X: 200}
		him := me.Position.Subtract(flight.Vec3{X: 1500})
		back := me.Position.Subtract(him).Normalize()
		nose := flight.Axis(flight.Vec3{Y: 1}, (n.cone+n.wide)/2*math.Pi/180).Rotate(back) // between the cones
		b.prey = &track{position: him, velocity: me.Velocity, nose: nose}
		lit := i.reheat(b, me, 1500, speed, flight.Vec3{}, 0)
		i.Close()
		if hidden := lit == 0; hidden != (speed > tuned(n.speed)) {
			t.Errorf("at %.1f m/s equivalent, the wide cone from %.1f, his nose %.0f degrees off me: burner %.0f", speed, tuned(n.speed), (n.cone+n.wide)/2, lit)
		}
	}
}

// TestOvertakeClearsOnItsWing: closing on him from astern into a pass inside
// the bubble, the bandit clears the way the wing it has can open, and the wing
// goes with the equivalent airspeed through the air it flies. High up, where
// the true airspeed reads half as fast again, a true-airspeed wing would claim
// g it does not have and keep a way it cannot open; in the wind, an airspeed
// read over the ground would claim or lose the wind's share.
func TestOvertakeClearsOnItsWing(t *testing.T) {
	telling := 0
	for name, wind := range map[string]flight.Vec3{"still air": {}, "a headwind": trades, "a tailwind": trades.Scale(-1)} {
		for equivalent := 100.0; equivalent <= 130; equivalent += 5 {
			air := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: wind}
			bandit := NewBandit("expert", 1, "", false, true, "fox2", 0, false)
			bandit.Air(air)
			i, a := bandit.arena, bandit.craft
			b, me, quarry := a.brain, &a.model.State, i.aircraft[0]
			const height = 7500
			path := trades.Scale(1 / trades.Length()).Scale(-1) // into the trades; a tailwind is the trades reversed
			tas := equivalent / math.Sqrt(flight.Atmosphere(height, a.model.Environment).Density/1.225)
			me.Position = flight.Vec3{Y: height}
			blown := flight.Wind(me.Position, 0, air)
			me.Velocity, me.Attitude = path.Scale(tas).Add(blown), flight.Look(path)
			him := &quarry.model.State
			aside := path.Cross(flight.Vec3{Y: 1}).Scale(40)
			him.Position = me.Position.Add(path.Scale(350)).Add(aside) // the pass 2.7 s out, where the wing decides the way
			him.Velocity, him.Attitude = path.Scale(tas-130).Add(flight.Wind(him.Position, 0, air)), flight.Look(path)
			quarry.alive = true
			b.prey, b.target = &track{position: him.Position, velocity: him.Velocity, blown: flight.Wind(him.Position, 0, air)}, 0
			i.overtake(a, b)
			away := b.routine.away
			bandit.arena.Close()
			if away.Length() < 0.5 {
				t.Fatalf("in %s at %.0f m/s equivalent, closing at 130 m/s from 350 m astern, 40 m off his line, the bandit never clears", name, equivalent)
			}
			seen := *b.prey
			miss, when := nearest(&seen, me, him.Position, flight.Vec3{}, blown, 3)
			wing := func(speed float64) float64 {
				return math.Min(b.skill.pull, a.model.Airframe.Limit.Positive*math.Pow(speed/corner(a.model), 2))
			}
			has := escape(miss, when, me, blown, false, false, wing(equivalent))
			if claimed := escape(miss, when, me, blown, false, false, wing(tas)); has.Subtract(claimed).Length() > 0.1 {
				telling++
			}
			if away.Subtract(has).Length() > 1e-6 {
				t.Errorf("in %s at %.0f m/s equivalent, on a %.1f g wing the bandit clears %v; the way that wing opens is %v", name, equivalent, wing(equivalent), away, has)
			}
		}
	}
	if telling == 0 {
		t.Fatal("no case clears differently on the true airspeed's wing: the sweep cannot tell the wings apart")
	}
}

// TestScriptHidesOnItsHeading: inside his heater's fuse floor the burner buys
// the turn, and the floor goes with how much of my tailpipe he sees, which
// points down my heading through the air. Across the wind, my heading 45
// degrees off him, the range the burner lights at is the still air's, on a
// look taken now or half a second ago: an old look puts him where the air has
// carried him since. Read where the look left him over the sea, the trades
// moved him 15 m in that half second, and the burner lit at other ranges.
func TestScriptHidesOnItsHeading(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	across := down.Cross(flight.Vec3{Y: 1})
	lit := func(still bool, along flight.Vec3, span, stale float64) float64 {
		air := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: trades}
		if still {
			air.Wind = flight.Vec3{}
		}
		bandit := NewBandit("expert", 1, "", false, true, "fox2", 0, false)
		defer bandit.arena.Close()
		bandit.Air(air)
		i, b := bandit.arena, bandit.craft.brain
		me := &bandit.craft.model.State
		me.Position = flight.Vec3{Y: 4000}
		wind := flight.Wind(me.Position, 0, air)
		me.Velocity = along.Scale(200).Add(wind)
		back := flight.Axis(flight.Vec3{Y: 1}, -45*math.Pi/180).Rotate(along) // him to me: my heading 45 degrees off it, my tailpipe half on him
		him := me.Position.Subtract(back.Scale(span))
		blown := flight.Wind(him, 0, air)
		velocity := back.Scale(400).Add(blown)                                                                       // closing fast, his nose on me
		b.prey = &track{position: him.Subtract(velocity.Scale(stale)), velocity: velocity, nose: back, blown: blown} // seen stale seconds ago
		return i.reheat(b, me, span, 200, wind, uint64(stale*60))
	}
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1), "downwind": down, "upwind": down.Scale(-1)} {
		for _, stale := range []float64{0, 0.5} {
			burned := map[float64]int{}
			for span := 250.0; span <= 700; span += 2 {
				calm, blown := lit(true, along, span, stale), lit(false, along, span, stale)
				burned[calm]++
				if calm != blown {
					t.Errorf("flying %s %.0f m ahead of his nose, seen %.1f s ago, the burner is %.0f in still air and %.0f in the wind", name, span, stale, calm, blown)
				}
			}
			if burned[0] == 0 || burned[1] == 0 {
				t.Fatalf("flying %s, seen %.1f s ago, the range sweep never crosses his fuse floor: %v", name, stale, burned)
			}
		}
	}
}

// TestOvertakeReadsHisHeading: the overtake reflex clears a pass only when it
// closes on him from astern, and astern is behind his 3/9 line, which his
// heading through the air sets. Across the wind, closing on a collision course
// from near his beam, it clears at the angles still air clears at.
func TestOvertakeReadsHisHeading(t *testing.T) {
	down := trades.Scale(1 / trades.Length())
	across := down.Cross(flight.Vec3{Y: 1})
	cleared := func(still bool, along flight.Vec3, abeam float64) (bool, flight.Vec3) {
		air := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: trades}
		if still {
			air.Wind = flight.Vec3{}
		}
		bandit := NewBandit("expert", 1, "", false, true, "fox2", 0, false)
		defer bandit.arena.Close()
		bandit.Air(air)
		i, a := bandit.arena, bandit.craft
		b, me, quarry := a.brain, &a.model.State, i.aircraft[0]
		his := along.Scale(200)
		from := flight.Axis(flight.Vec3{Y: 1}, (180-abeam)*math.Pi/180).Rotate(along) // him to me: abeam his heading by the angle
		him := &quarry.model.State
		him.Position = flight.Vec3{Y: 4000}
		him.Velocity, him.Attitude = his.Add(flight.Wind(him.Position, 0, air)), flight.Look(his)
		me.Position = him.Position.Add(from.Scale(300))
		u := from.Scale(-1) // me to him
		side := his.Subtract(u.Scale(his.Dot(u)))
		mine := side.Add(u.Scale(math.Sqrt(260*260 - side.Dot(side)))) // a collision course at 260 m/s
		me.Velocity, me.Attitude = mine.Add(flight.Wind(me.Position, 0, air)), flight.Look(mine)
		quarry.alive = true
		b.prey, b.target = &track{position: him.Position, velocity: him.Velocity, blown: flight.Wind(him.Position, 0, air)}, 0
		i.overtake(a, b)
		return b.routine.away.Length() > 0.5, b.aim
	}
	for name, along := range map[string]flight.Vec3{"across": across, "across the other way": across.Scale(-1)} {
		clears := map[bool]int{}
		for abeam := 78.0; abeam <= 102; abeam += 1 {
			calm, aim := cleared(true, along, abeam)
			blown, aimed := cleared(false, along, abeam)
			clears[calm]++
			if calm != blown {
				t.Errorf("flying %s, closing from %.0f degrees off his tail, the overtake clears in still air: %v, in the wind: %v", name, 180-abeam, calm, blown)
			} else if off := math.Acos(clamp(aim.Dot(aimed), -1, 1)) * 180 / math.Pi; calm && off > 0.5 {
				t.Errorf("flying %s, closing from %.0f degrees off his tail, the overtake's clear aims %.1f degrees apart in still air and the wind", name, 180-abeam, off)
			}
		}
		if clears[true] == 0 || clears[false] == 0 {
			t.Fatalf("flying %s the sweep never crosses his 3/9 line: %v", name, clears)
		}
	}
}

// TestCrabHoldsTheTrack: the notch is the one manoeuvre fixed to the sea - the
// clutter hides a jet whose speed over the ground runs square to the radar's
// line - and the law flies its aim through the air, so the notch's aim is the
// heading that crabs the jet along the beam: through the air at its airspeed,
// carried by the wind, it makes good the beam over the ground.
func TestCrabHoldsTheTrack(t *testing.T) {
	for _, wind := range []flight.Vec3{{}, {X: -30, Z: 10}, {X: 12, Z: -25}} {
		for _, degrees := range []float64{0, 40, 90, 160, 250} {
			beam := flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(flight.Vec3{X: 1})
			heading := crab(beam, 220, wind)
			ground := heading.Scale(220).Add(wind)
			if math.Abs(heading.Length()-1) > 1e-9 || ground.Normalize().Dot(beam) < 1-1e-9 {
				t.Errorf("crabbing onto a beam %.0f degrees round in a wind %v, the jet makes good %v", degrees, wind, ground.Normalize())
			}
		}
	}
}

// TestNotchCrabsThroughTheAir: beaming an inbound radar round, the bot's aim
// is flown through the air, and what the notch needs is its speed over the
// ground across the round's line: the beam. Flown at its airspeed and carried
// by the match's wind, the aim makes good the beam; the beam itself, flown
// through the air, would leave the wind's share along the line and the jet
// out of the notch.
func TestNotchCrabsThroughTheAir(t *testing.T) {
	i, c, a, self := radar_pair(t)
	slot := -1
	for _, s := range i.slots() {
		if i.aircraft[s] == c {
			slot = s
		}
	}
	park(c, flight.Vec3{X: 60000, Y: 3000}, flight.Vec3{X: 250})
	s := &a.model.State
	s.Position = flight.Vec3{Y: 3000}
	wind := flight.Wind(s.Position, s.Time, a.model.Environment)
	if wind.Length() < 10 {
		t.Fatalf("the match's air at 3,000 m is %v: no wind to crab against", wind)
	}
	s.Velocity, s.Attitude = flight.Vec3{Z: 250}.Add(wind), flight.Look(flight.Vec3{Z: 1})
	a.brain.alerted = 1
	inbound := round.New(flight.Vec3{X: -8000, Y: 3000}, flight.Vec3{X: 900}.Add(wind), nil, a.model.Environment, 0)
	i.flying = append(i.flying, &missile{radar: inbound, shooter: slot, target: self, position: inbound.Position, velocity: inbound.Velocity, life: 60, number: 1})
	i.defend(self, a, 600)
	if a.brain.mode != "notch" {
		t.Fatalf("an active round closing on the beam: mode %q, want the notch", a.brain.mode)
	}
	ground := a.brain.aim.Scale(airspeed(s, a.model.Environment)).Add(wind)
	if off := math.Acos(clamp(ground.Normalize().Dot(a.brain.beam), -1, 1)) * 180 / math.Pi; off > 0.5 {
		t.Errorf("flown through the air, the notch's aim makes good a track %.1f degrees off the beam: out of the notch", off)
	}
}

// TestOwnPathReadsTheAir: the law flies its aim onto my path through the air,
// so every aim that means "on as I fly" - the merge's lead turn, the plays'
// swing off my flight path, which side of it he sits, the deck recovery's wings
// level climb - is built on that path. Carried by a wind across it, each is
// what still air builds; built on my track, the drift angle would turn them.
func TestOwnPathReadsTheAir(t *testing.T) {
	path := flight.Vec3{X: 200}
	for _, surface := range []flight.Vec3{{Z: 12}, {X: -9, Z: -10}} {
		air := flight.Environment{Frame: scene, Chart: scene, Wind: surface}
		at := flight.Vec3{Y: 4000}
		blown := flight.Wind(at, 0, air) // the field's own wind where I fly: shear and veer aloft
		calm := flight.State{Position: at, Velocity: path, Attitude: flight.Look(path)}
		carried := flight.State{Position: at, Velocity: path.Add(blown), Attitude: flight.Look(path)}
		var b brain
		if a, c := b.commit(&calm, flight.Environment{Frame: scene, Chart: scene}, 0.6), b.commit(&carried, air, 0.6); a.Subtract(c).Length() > 1e-9 {
			t.Errorf("in a wind %v the lead turn aims %v, in still air %v", blown, c, a)
		}
		for _, offset := range []flight.Vec3{{X: 900, Z: 300}, {X: 900, Z: -300}, {X: -400, Z: 50}} {
			moment := func(s *flight.State, wind flight.Vec3) moment {
				m := moment{me: s, prey: s.Position.Add(offset), velocity: flight.Vec3{X: -150}.Add(wind), pace: 160, pull: 7, wind: wind, blown: wind, air: flight.Environment{Frame: scene, Chart: scene}}
				m.derive()
				return m
			}
			m, n := moment(&calm, flight.Vec3{}), moment(&carried, blown)
			if m.flat().Subtract(n.flat()).Length() > 1e-9 || m.swing(0.4).Subtract(n.swing(0.4)).Length() > 1e-9 || m.side() != n.side() {
				t.Errorf("in a wind %v, him at %v: my flight path %v and %v, his side %v and %v", blown, offset, m.flat(), n.flat(), m.side(), n.side())
			}
		}
	}
	// The deck recovery: wings level and climbing on my path, 800 m up and
	// sinking across the match's wind.
	recover := func(still bool) flight.Inputs {
		c, done := alone(still)
		defer done()
		s := &c.model.State
		s.Position = flight.Vec3{Y: 800}
		sink := trades.Cross(flight.Vec3{Y: 1}).Normalize().Scale(170).Add(flight.Vec3{Y: -30})
		s.Velocity = sink.Add(flight.Wind(s.Position, s.Time, c.model.Environment))
		s.Attitude = flight.Look(sink)
		return c.brain.steer(c.model, 0)
	}
	if a, c := recover(true), recover(false); math.Abs(a.Pitch-c.Pitch) > 1e-6 || math.Abs(a.Roll-c.Roll) > 1e-6 {
		t.Errorf("recovering off the deck the stick is pitch %.4f roll %.4f in still air and %.4f %.4f in the wind", a.Pitch, a.Roll, c.Pitch, c.Roll)
	}
}

// TestRebuildReadsTheAir: too slow to fight, the bot unloads on its path, nose
// a little down, and rebuilds: the path is the one it flies through the air,
// which the law flies the aim onto. Across the wind the rebuild aims where
// still air aims it - in a section, and teamless, starved with him far off.
func TestRebuildReadsTheAir(t *testing.T) {
	across := trades.Cross(flight.Vec3{Y: 1}).Normalize()
	rebuild := func(still bool, teamless bool) (string, flight.Vec3) {
		var i *instance
		var me, him *craft
		other := 98
		if teamless {
			made, _ := New().Create(game.Session{Identifier: "rebuild", Game: "air", Mode: "furball", Capacity: 16, Seed: 5,
				Parameters: map[string]any{"bots": map[string]any{"expert": 1.0, "drone": 1.0}}})
			i = made.(*instance)
			for _, s := range i.slots() {
				if c := i.aircraft[s]; c.brain != nil && c.brain.skill.library >= 4 {
					me = c
				} else {
					him, other = c, s
				}
			}
		} else {
			made, _ := New().Create(game.Session{Identifier: "rebuild", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
				Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
			i = made.(*instance)
			me, him = i.aircraft[99], i.aircraft[98]
		}
		defer i.Close()
		if still {
			i.environment.Wind = flight.Vec3{}
			me.model.Environment.Wind, him.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		me.brain.plan, me.brain.planned = "two", 1 // mid-fight: the floor arms once a merge plan exists
		base := flight.Vec3{Y: 4000}
		for tick := uint64(0); tick < 30; tick++ {
			flown(me, base.Y, 80, across) // well under 0.55 of the corner
			aloft(him, base.Add(across.Scale(-2500)), across.Scale(-200))
			s := &him.model.State
			i.recall(me.brain)
			me.brain.known[other] = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, him.model.Environment)}
			i.Step(tick, nil)
		}
		i.recall(me.brain) // its aim, in the session's frame
		return me.brain.mode, me.brain.aim
	}
	for _, teamless := range []bool{false, true} {
		calm, aim := rebuild(true, teamless)
		blown, aimed := rebuild(false, teamless)
		if calm != "rebuild" || blown != "rebuild" {
			t.Fatalf("teamless %v, slow with him 2.5 km off: the expert flies %q in still air and %q in the wind, not the rebuild", teamless, calm, blown)
		}
		if off := math.Acos(clamp(aim.Dot(aimed), -1, 1)) * 180 / math.Pi; off > 0.5 {
			t.Errorf("teamless %v, the rebuild aims %.1f degrees apart in still air and the wind", teamless, off)
		}
	}
}

// TestLimpReadsTheAir: crippled with nobody known, the bot extends on its path,
// low and fast, and the path is the one it flies through the air, which the law
// flies the aim onto. Across the wind the extension aims where still air aims it.
func TestLimpReadsTheAir(t *testing.T) {
	across := trades.Cross(flight.Vec3{Y: 1}).Normalize()
	limp := func(still bool) (string, flight.Vec3) {
		made, _ := New().Create(game.Session{Identifier: "limp", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		c := i.aircraft[99]
		if still {
			i.environment.Wind = flight.Vec3{}
			c.model.Environment.Wind = flight.Vec3{}
		}
		for tick := uint64(0); tick < 30; tick++ {
			flown(c, 4000, 170, across)
			c.model.State.Damage.Engine[0], c.model.State.Damage.Engine[1] = 0.8, 0.8 // most of the thrust gone
			i.Step(tick, nil)
		}
		return c.brain.mode, c.brain.aim
	}
	calm, aim := limp(true)
	blown, aimed := limp(false)
	if calm != "limp" || blown != "limp" {
		t.Fatalf("crippled: the expert flies %q in still air and %q in the wind, not the limp home", calm, blown)
	}
	if off := math.Acos(clamp(aim.Dot(aimed), -1, 1)) * 180 / math.Pi; off > 0.5 {
		t.Errorf("limping home the expert aims %.1f degrees apart in still air and the wind", off)
	}
}

// TestMergeReadsTheAir: closing head-on, the expert lead-turns toward the side
// he will pass on, which side of my path he sits, and the path is the one I fly
// through the air. Across the wind, with him a few metres either side of my
// path, the turn goes the way still air sends it.
func TestMergeReadsTheAir(t *testing.T) {
	across := trades.Cross(flight.Vec3{Y: 1}).Normalize()
	side := across.Cross(flight.Vec3{Y: 1})
	merge := func(still bool, aside float64) float64 {
		made, _ := New().Create(game.Session{Identifier: "merge", Game: "air", Mode: "teams", Capacity: 16, Seed: 4,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		expert, drone := i.aircraft[99], i.aircraft[98]
		if still {
			i.environment.Wind = flight.Vec3{}
			expert.model.Environment.Wind, drone.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		base := flight.Vec3{Y: 4500}
		for tick := uint64(0); tick < 12; tick++ {
			aloft(expert, base, across.Scale(220))
			aloft(drone, base.Add(across.Scale(800)).Add(side.Scale(aside)), across.Scale(-220)) // inside the lead turn's two seconds of closure
			s := &drone.model.State
			i.recall(expert.brain)
			expert.brain.known[98] = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, drone.model.Environment)}
			i.Step(tick, nil)
		}
		return expert.brain.turning
	}
	turns := map[float64]int{}
	for aside := -40.0; aside <= 40; aside += 5 {
		calm, blown := merge(true, aside), merge(false, aside)
		turns[calm]++
		if calm != blown {
			t.Errorf("head-on with him %.0f m off my path, the expert lead-turns %+.1f in still air and %+.1f in the wind", aside, calm, blown)
		}
	}
	if len(turns) < 2 {
		t.Fatalf("the sweep never crosses from one lead turn to the other: %v", turns)
	}
}

// TestDuelBearingReadsTheAir: the duel watches his bearing off my path for the
// overshoot - behind my 3/9 line one look, ahead of it the next - and my path
// is the one I fly through the air. Across the wind, with him at bearings all
// round, it reads what still air reads.
func TestDuelBearingReadsTheAir(t *testing.T) {
	across := trades.Cross(flight.Vec3{Y: 1}).Normalize()
	bearing := func(still bool, degrees float64) float64 {
		made, _ := New().Create(game.Session{Identifier: "bearing", Game: "air", Mode: "teams", Capacity: 16, Seed: 3,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		c := i.aircraft[99]
		if still {
			i.environment.Wind = flight.Vec3{}
			c.model.Environment.Wind = flight.Vec3{}
		}
		flown(c, 4000, 170, across)
		position := c.model.State.Position.Add(flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(across).Scale(600))
		blown := flight.Wind(position, 0, c.model.Environment)
		prey := &track{when: 100000, position: position, velocity: across.Scale(200).Add(blown), blown: blown}
		direction, distance := i.bearing(c.model.State.Position, prey.position)
		c.brain.intent, c.brain.minded, c.brain.chanced = "convert", 100000-60, 100000
		i.duel(99, c, 100000, prey, direction, distance, direction.Dot(across), -1, 2000)
		return c.brain.flanked
	}
	for degrees := 0.0; degrees < 360; degrees += 15 {
		if calm, blown := bearing(true, degrees), bearing(false, degrees); math.Abs(calm-blown) > 1e-6 {
			t.Errorf("him %.0f degrees off my path, his bearing reads %.4f in still air and %.4f in the wind", degrees, calm, blown)
		}
	}
}

// TestGlareReadsTheAir: running in on his tail by day, the expert drifts the
// run-in toward the up-sun station while that is nearly free - within about 41
// degrees of my path - and my path is the one I fly through the air. Across
// the wind, my heading swept through that edge, it drifts where still air does.
func TestGlareReadsTheAir(t *testing.T) {
	across := trades.Cross(flight.Vec3{Y: 1}).Normalize()
	run := func(still bool, off float64) (string, flight.Vec3) {
		made, _ := New().Create(game.Session{Identifier: "glare", Game: "air", Mode: "teams", Capacity: 16, Seed: 5,
			Parameters: map[string]any{"bots": map[string]any{"red": map[string]any{"expert": 1.0}, "blue": map[string]any{"drone": 1.0}}}})
		i := made.(*instance)
		defer i.Close()
		expert, drone := i.aircraft[99], i.aircraft[98]
		if still {
			i.environment.Wind = flight.Vec3{}
			expert.model.Environment.Wind, drone.model.Environment.Wind = flight.Vec3{}, flight.Vec3{}
		}
		if i.night {
			t.Fatal("the session flies at night: no sun to drift toward")
		}
		base := flight.Vec3{Y: 4000}
		for tick := uint64(0); tick < 30; tick++ {
			aloft(expert, base, flight.Axis(flight.Vec3{Y: 1}, off*math.Pi/180).Rotate(across).Scale(220))
			aloft(drone, base.Add(across.Scale(1800)), across.Scale(200))
			s := &drone.model.State
			i.recall(expert.brain)
			expert.brain.known[98] = &track{when: tick, position: s.Position, velocity: s.Velocity, blown: flight.Wind(s.Position, s.Time, drone.model.Environment)}
			i.Step(tick, nil)
		}
		i.recall(expert.brain) // its aim, in the session's frame
		return expert.brain.mode, expert.brain.aim
	}
	drifted := map[bool]int{}
	for _, sign := range []float64{1, -1} {
		for off := 20.0; off <= 60; off += 1 {
			calm, aim := run(true, sign*off)
			blown, aimed := run(false, sign*off)
			if calm != blown {
				t.Errorf("my heading %.0f degrees off the line to him, the expert flies %q in still air and %q in the wind", sign*off, calm, blown)
				continue
			}
			apart := math.Acos(clamp(aim.Dot(aimed), -1, 1)) * 180 / math.Pi
			if apart > 0.5 {
				t.Errorf("my heading %.0f degrees off the line to him, flying %q the expert aims %.1f degrees apart in still air and the wind", sign*off, calm, apart)
			}
			line := across.Scale(1800).Normalize()
			drifted[calm == "offense" && aim.Dot(line) < 0.999]++
		}
	}
	if drifted[true] == 0 || drifted[false] == 0 {
		t.Fatalf("the heading sweep never crosses the up-sun drift's edge: %v", drifted)
	}
}

// TestBreakReadsTheAir: an inbound heater goes on the scripted bandit's 3/9
// line, where its sight-line rate is highest and its gimbal runs out, and the
// line is square to my path through the air, the one the round's own flight
// and mine share. Across the wind, a round coming in from all round, the
// break's beam is the still air's.
func TestBreakReadsTheAir(t *testing.T) {
	across := trades.Cross(flight.Vec3{Y: 1}).Normalize()
	beam := func(still bool, degrees float64) flight.Vec3 {
		air := flight.Environment{Frame: scene, Chart: scene, Seed: 1, Wind: trades}
		if still {
			air.Wind = flight.Vec3{}
		}
		b := NewBandit("expert", 1, "", false, true, "fox2", 0, false)
		defer b.arena.Close()
		b.Air(air)
		at := flight.Vec3{Y: 4000}
		b.Spawn(at, across.Scale(200))
		pm := flight.New(aircraft.Get("fa18c"), air, flight.World{Sea: sea})
		pm.State = flight.Level(pm, at.Add(across.Scale(4000)), across.Scale(-1), 200, fuel)
		words := make([]float64, flight.Size)
		b.craft.brain.routine.passed = true
		from := flight.Axis(flight.Vec3{Y: 1}, degrees*math.Pi/180).Rotate(across) // where the round comes from
		for tick := 0; tick < 45; tick++ {
			s := &b.craft.model.State
			carried := at.Add(flight.Wind(at, 0, air).Scale(float64(tick) / 60))
			s.Position, s.Velocity, s.Attitude = carried, across.Scale(200).Add(flight.Wind(carried, s.Time, air)), flight.Look(across)
			p := &pm.State
			p.Position, p.Velocity = carried.Add(across.Scale(4000)), across.Scale(-200).Add(flight.Wind(carried, s.Time, air))
			p.Encode(words)
			b.Mirror(words, false, true)
			round := carried.Add(from.Scale(2500))
			v := from.Scale(-600).Add(flight.Wind(round, s.Time, air))
			b.Menace([]float64{round.X, round.Y, round.Z, v.X, v.Y, v.Z, 0, -1})
			b.Step()
		}
		return b.craft.brain.routine.beam
	}
	broke := 0
	for degrees := 0.0; degrees < 360; degrees += 30 {
		calm, blown := beam(true, degrees), beam(false, degrees)
		if calm.Length() < 0.5 || blown.Length() < 0.5 {
			continue // seen too late, or not at all, from this side
		}
		broke++
		if off := math.Acos(clamp(calm.Dot(blown), -1, 1)) * 180 / math.Pi; off > 0.5 {
			t.Errorf("a heater coming from %.0f degrees: the break's beam lies %.1f degrees apart in still air and the wind", degrees, off)
		}
	}
	if broke == 0 {
		t.Fatal("no inbound round was ever broken across")
	}
}

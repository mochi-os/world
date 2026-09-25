// Mochi world: Client bandit tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"
	"world/games/air/aircraft"

	"world/games/air/flight"
)

func TestBanditSpawnTrimmed(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, false, "", 0, false)
	b.Spawn(flight.Vec3{Y: 2000}, flight.Vec3{X: 200})
	s := &b.craft.model.State
	v := s.Attitude.Unrotate(s.Velocity)
	if alpha := math.Atan2(-v.Y, v.X); alpha < 0.01 {
		t.Fatalf("bandit spawned at %.2f° alpha — off trim", alpha*180/math.Pi)
	}
	if s.Gear.Catapult != -1 || s.Gear.Stroke != -1 || s.Gear.Wire != -1 || s.Gear.Contact != -1 {
		t.Fatalf("live-looking gear sentinels at spawn: %+v", s.Gear)
	}
	if s.Engine[0].Spool < 0.85 {
		t.Fatalf("merge-entry power lost: spool %.2f", s.Engine[0].Spool)
	}
}

// TestBanditFliesThePlayersAir: given the player's environment, the bandit
// flies the same wind, and the joust's two jets, spawned at the same speed
// nose to nose, read the same airspeed. The bandit flew in still air while the
// player's core had the trade wind, so each joust began with the pilot about
// 53 kt faster or slower by the coin flip that picked his end.
func TestBanditFliesThePlayersAir(t *testing.T) {
	air := flight.Environment{Seed: 1, Wind: flight.Vec3{X: -12.1, Z: 4.4}, Wrap: 250000}
	for _, east := range []float64{1, -1} {
		b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, true)
		b.Air(air)
		b.Spawn(flight.Vec3{X: -2778 * east, Y: 4572}, flight.Vec3{X: 220 * east})
		player := flight.New(aircraft.Get("fa18c"), air, flight.World{Sea: sea})
		player.State = flight.Level(player, flight.Vec3{X: 2778 * east, Y: 4572}, flight.Vec3{X: -east}, 220, fuel)
		player.Step(flight.Inputs{Throttle: player.State.Engine[0].Spool})
		b.craft.model.Step(flight.Inputs{Throttle: b.craft.model.State.Engine[0].Spool})
		if b.craft.model.Environment.Wind != air.Wind {
			t.Fatalf("the bandit's jet flies wind %+v, not the player's %+v", b.craft.model.Environment.Wind, air.Wind)
		}
		mine, his := b.craft.model.Cas()*1.944, player.Cas()*1.944
		if math.Abs(mine-his) > 2 {
			t.Errorf("bandit heading %+.0f: %.0f kt against the player's %.0f kt at the joust's start", east, mine, his)
		}
	}
}

// TestBanditBvr: the SP bandit flies the same BVR brain the server does - the
// open class arms it, its radar acquires beyond visual range, a DLZ shot leaves
// the rail, and the guns class stays byte-inert.
func TestBanditBvr(t *testing.T) {
	b := NewBandit("ace", 3, 250000, "", false, true, "open", 0, false)
	if b.craft.amraams == 0 {
		t.Fatal("open-class bandit spawned without AMRAAMs")
	}
	b.Spawn(flight.Vec3{X: 25000, Y: 6096}, flight.Vec3{X: -272})

	environment := flight.Environment{Seed: 3, Wrap: 250000}
	player := flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea})
	player.State = flight.Level(player, flight.Vec3{X: 0, Y: 6096}, flight.Vec3{X: 1}, 272, fuel)
	words := make([]float64, flight.Size)

	launched, stt := uint64(0), uint64(0)
	for tick := uint64(1); tick <= 60*120 && launched == 0; tick++ {
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(nil)
		_, _, launch, _, _ := b.Step()
		if b.craft.emitter == 2 && stt == 0 {
			stt = tick
		}
		if launch {
			launched = tick
		}
	}
	if stt == 0 {
		t.Fatal("the bandit never built an STT on a hot player 25 km out")
	}
	if launched == 0 {
		t.Fatal("the bandit never launched in two minutes with the player inside its DLZ")
	}
	if !b.Locked() {
		t.Fatal("Locked() false at the moment of a datalinked launch")
	}
	if b.Emitter() != 2 {
		t.Fatalf("Emitter() reads %d at launch; the RWR would miss the hard lock", b.Emitter())
	}

	// The look: while the client reports the bandit's own round in
	// midcourse, no second round may leave; once it reports pitbull, the
	// next shot frees. Eight words: position, velocity, shooter, phase.
	stub := func(phase float64) []float64 {
		return []float64{12000, 6000, 0, -800, 0, 0, 1, phase}
	}
	for tick := uint64(0); tick < 60*10; tick++ {
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(stub(0)) // Midcourse
		if _, _, launch, _, _ := b.Step(); launch {
			t.Fatal("a second round left the rail with the first still in midcourse: the look discipline is not reading the client's report")
		}
	}
	second := false
	for tick := uint64(0); tick < 60*20 && !second; tick++ {
		player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
		player.State.Encode(words)
		b.Mirror(words, false, true)
		b.Menace(stub(2)) // Pitbull: the supporter is free
		_, _, second, _, _ = b.Step()
	}
	if !second {
		t.Fatal("no follow-up shot after the client reported pitbull")
	}

	// The guns class: today's exact joust, nothing radiates.
	quiet := NewBandit("ace", 3, 250000, "", false, false, "guns", 0, false)
	quiet.Spawn(flight.Vec3{X: 8000, Y: 3000}, flight.Vec3{X: -272})
	for tick := 0; tick < 60*5; tick++ {
		player.State.Encode(words)
		quiet.Mirror(words, false, true)
		quiet.Menace(nil)
		quiet.Step()
		if quiet.craft.emitter != 0 {
			t.Fatalf("guns-class bandit radiating (emitter %d)", quiet.craft.emitter)
		}
	}
}

// TestBanditJoustHold: the single-player joust as the client starts it -
// head-on from 1.5 NM a side at 220 m/s, the player in burner - which is where
// recordings 01a0b090, 01a0c91b and 01a0c9e0 all show the ace's heaters leaving
// at about 8.4 s with neither jet past the other's 3/9 line. Unheld, the same
// geometry must still produce that shot, or the held run proves nothing. Held,
// nothing leaves the rails before the crossing, Free reports the crossing on
// the frame the geometry makes it, and the stores are all aboard at the merge.
// The gun half: the novice, who alone takes the head-on snapshot, pulls the
// trigger on the run-in, and held its core must not kick for it.
func TestBanditJoustHold(t *testing.T) {
	type outcome struct {
		early   int    // missiles that left before either jet crossed the other's 3/9 line
		pressed int    // frames the brain pulled the trigger before the crossing
		crossed uint64 // the tick the geometry first shows the crossing
		freed   uint64 // the tick Free first reported the weapons free
		stores  int    // heaters aboard at the crossing
		aboard  int    // heaters aboard at spawn
		track   []flight.Vec3
	}
	joust := func(level string, seed uint64, weapons string, hold bool, recoil bool) outcome {
		b := NewBandit(level, seed, 250000, "", false, weapons != "guns", weapons, 0, hold)
		if !recoil {
			quiet := *b.craft.model.Airframe
			quiet.Gun.Recoil = 0
			b.craft.model.Airframe = &quiet
		}
		b.Spawn(flight.Vec3{X: 2778, Y: 4572}, flight.Vec3{X: -220})
		player := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: seed, Wrap: 250000}, flight.World{Sea: sea})
		player.State = flight.Level(player, flight.Vec3{X: -2778, Y: 4572}, flight.Vec3{X: 1}, 220, fuel)
		words := make([]float64, flight.Size)
		// behind is the rule in the test's own terms: from lies behind the line
		// through of's wings, with the rule's five metres of margin.
		behind := func(from, of *flight.State) bool {
			return from.Position.Subtract(of.Position).Dot(of.Attitude.Rotate(flight.Vec3{X: 1})) < -5
		}
		result := outcome{aboard: b.craft.missiles}
		for tick := uint64(1); tick <= 60*20; tick++ {
			for substep := 0; substep < 4; substep++ {
				player.Step(flight.Inputs{Throttle: 1, Reheat: 1})
			}
			player.State.Encode(words)
			b.Mirror(words, false, true)
			b.Menace(nil)
			if result.crossed == 0 && (behind(&player.State, b.State()) || behind(b.State(), &player.State)) {
				result.crossed, result.stores = tick, b.craft.missiles
			}
			fire, _, launch, heater, _ := b.Step()
			if result.crossed == 0 {
				result.track = append(result.track, b.State().Position)
				if launch || heater {
					result.early++
				}
				if fire {
					result.pressed++
				}
			}
			if result.freed == 0 && b.Free() {
				result.freed = tick
			}
		}
		if result.crossed == 0 {
			t.Fatalf("%s seed %d never merged in twenty seconds", level, seed)
		}
		return result
	}

	free := joust("ace", 7, "fox2", false, true)
	if free.early == 0 {
		t.Fatalf("unheld, the ace fired nothing before the merge at tick %d: the geometry no longer reproduces the recorded shot, so the held run proves nothing", free.crossed)
	}
	if free.freed != 1 {
		t.Errorf("a bandit started without the hold reported its weapons free first at tick %d, want the first frame", free.freed)
	}
	held := joust("ace", 7, "fox2", true, true)
	if held.early != 0 {
		t.Errorf("held, %d missiles left the ace's rails before the merge at tick %d", held.early, held.crossed)
	}
	if held.freed != held.crossed {
		t.Errorf("Free first reported at tick %d, the crossing is at tick %d: the client's hold and the brain's would open on different frames", held.freed, held.crossed)
	}
	if held.stores != held.aboard {
		t.Errorf("%d heaters aboard at the merge, want all %d", held.stores, held.aboard)
	}

	kicked := joust("novice", 3, "guns", true, true)
	quiet := joust("novice", 3, "guns", true, false)
	if kicked.pressed == 0 {
		t.Fatal("the novice never pulled the trigger on the run-in: the gun half of the hold is untested")
	}
	for tick := range kicked.track {
		if tick >= len(quiet.track) || kicked.track[tick].Subtract(quiet.track[tick]).Length() > 1e-9 {
			t.Errorf("held, the novice's run-in left the recoil-free one at tick %d with the trigger pulled for %d frames: the gun kicked before the merge", tick+1, kicked.pressed)
			break
		}
	}
	t.Logf("ace: %d missiles before the merge at tick %d unheld, %d held, freed at tick %d; novice: %d frames on the trigger held", free.early, free.crossed, held.early, held.freed, kicked.pressed)
}

// TestMirrorShortFrame: the exported entry the panic was found through. Mirror
// takes a []float64 with no documented length and hands it straight to
// flight.Decode, so a nil or truncated frame from the wasm bridge used to kill
// the Go program page-wide rather than being refused. The reflection has to
// stay flyable afterwards, not merely not crash.
func TestMirrorShortFrame(t *testing.T) {
	b := NewBandit("ace", 1, 250000, "", false, false, "guns", 0, false)
	b.Spawn(flight.Vec3{X: 2000, Y: 6096}, flight.Vec3{X: -250})
	reflection := b.arena.aircraft[0]

	words := make([]float64, flight.Size)
	reflection.model.State.Position = flight.Vec3{X: 40, Y: 6100}
	reflection.model.State.Encode(words)
	b.Mirror(words, false, true)
	flown := reflection.model.State.Position

	b.Mirror(nil, false, true)
	if reflection.model.State.Attitude != (flight.Quat{W: 1}) {
		t.Fatalf("a nil frame left attitude %v, want the identity quaternion", reflection.model.State.Attitude)
	}
	if reflection.model.State.Gear.Wire != -1 || reflection.model.State.Gear.Contact != -1 {
		t.Errorf("a nil frame left gear %+v, want the New sentinels", reflection.model.State.Gear)
	}
	// The bandit must keep flying against the refused reflection: Step reads it
	// every tick, and a half-built state is what would fault downstream.
	for tick := 0; tick < 60; tick++ {
		b.Step()
	}

	b.Mirror(words, false, true)
	if reflection.model.State.Position != flown {
		t.Fatalf("a full frame after a refused one decoded to %v, want %v", reflection.model.State.Position, flown)
	}
}

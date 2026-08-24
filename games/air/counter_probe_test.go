// Mochi world: the counter-reversal probe (#73)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// saddler is the attacker the counter-reversal exists for: the chaser's sight
// model with the player's habits — it closes to gun range and TRACKS, hard,
// instead of standing off (the chaser's blow-through discipline keeps it
// outside 500 m, where its cone never shrinks and the blind window never
// opens). Sight is the same law: canopy reach 130 degrees relaxed, 70 under a
// hard pull, 1.5 s of flying the stale line before it even starts looking.
type saddler struct {
	blind   uint64
	where   flight.Vec3
	heading flight.Vec3
	lost    int
}

func (s *saddler) fly(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	span := math.Max(toward.Length(), 1)
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})
	off := math.Acos(clamp(toward.Scale(1/span).Dot(nose), -1, 1)) * 57.3
	reach := 130 - 60*clamp((me.Fcs.Demand-1)/6, 0, 1)
	if s.blind == 0 && off > reach {
		s.lost++
		s.blind, s.where, s.heading = 90, foe.Position, foe.Velocity
	}
	if s.blind > 0 {
		s.blind--
		if s.blind == 0 || off < 45 {
			s.blind = 0
		} else {
			ghost := *foe
			ghost.Position = s.where.Add(s.heading.Scale(float64(90-s.blind) / 60))
			ghost.Velocity = s.heading
			data := s.track(me, &ghost)
			data["fire"] = false
			return data
		}
	}
	return s.track(me, foe)
}

// track is pursue without the stand-off: aim the gun line, hold a small
// overtake, and keep tracking through the pass — the saddle the player flies.
func (s *saddler) track(me, foe *flight.State) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	span := math.Max(toward.Length(), 1)
	point := foe.Position.Subtract(foe.Velocity.Normalize().Scale(math.Min(120, span*0.3)))
	want := point.Subtract(me.Position).Normalize()
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	forward := me.Attitude.Rotate(flight.Vec3{X: 1})
	pitch := clamp(math.Asin(clamp(want.Dot(up), -1, 1))*2.5+0.15, -1, 1)
	roll := clamp(math.Asin(clamp(want.Dot(right), -1, 1))*2.2, -1, 1)
	if want.Dot(forward) < 0 {
		roll = clamp(math.Asin(clamp(want.Dot(right), -1, 1))*4, -1, 1)
	}
	overtake := me.Velocity.Length() - foe.Velocity.Length()
	throttle, reheat := 1.0, 0.0
	if span < 500 {
		throttle = clamp(0.7-(overtake-15)*0.01, 0.2, 1)
	} else if me.Velocity.Length() < 170 {
		reheat = 1
	}
	return map[string]any{"pitch": pitch, "roll": roll, "throttle": throttle, "reheat": reheat, "fire": true}
}

// TestSaddlePressure (#73): the ace against the saddler — the close gun
// tracker whose hard pulls open the sight window the scripted stand-off
// chaser never does (the chaser's closest approach in all history is 563 m,
// where its cone never shrinks). Log-only baseline for the counter-offence
// work: a licensed counter-reversal override was landed against this probe
// at three gate shapes plus a two-phase force-and-wheel law (2026-08-23),
// employed cleanly (one entry per fight, full blind-window rides), and
// bought NOTHING measurable — the defence already survives the fair-start
// saddle 12/12 and the one "won" handoff never latched a rear-quarter hold —
// so it was reverted rather than shipped inert. What this probe DID prove:
// the sight windows open here (blinded ~2 per fight), the fair-start defence
// holds, and a 700 m scripted-track spawn wrecks the engines before doctrine
// exists (71% of every fight flown limp). The next honest shape is teaching
// the ROLLOUT the opponent's sight state, so the arbiter prices reversals
// itself.
func TestSaddlePressure(t *testing.T) {
	heavy(t)
	converted, total, downed, lost, blinded := 0, 0, 0, 0, 0
	audit := map[string]int{}
	modes := map[string]int{}
	for seed := uint64(1); seed <= 12; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "counter", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
			t.Fatal(err)
		}
		bot := -1
		for slot, a := range i.aircraft {
			if a != nil && a.brain != nil {
				bot = slot
			}
		}
		if bot < 0 {
			t.Fatal("no bot in the session")
		}
		place(i, bot, 0, -1200) // converted to the six, arriving — the opening burst must be earned, not scripted (at 700 m the first seconds wrecked the engines and 71% of every fight was flown limp)
		me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
		attacker := &saddler{}
		for tick := uint64(0); tick < 120*60; tick++ {
			data := attacker.fly(me, foe, tick)
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			total++
			modes[i.aircraft[bot].brain.mode]++
			toward := foe.Position.Subtract(me.Position)
			if r := toward.Length(); r > 1 && r < 900 {
				tail := me.Velocity.Normalize().Scale(-1)
				if math.Acos(clamp(toward.Scale(1/r).Dot(tail), -1, 1))*57.3 < 45 {
					converted++
				}
			}
		}
		if i.aircraft[bot].kills > 0 {
			downed++
		}
		if !i.aircraft[bot].alive || i.aircraft[bot].model == nil {
			lost++
		}
		blinded += attacker.lost
		for k, v := range i.aircraft[bot].brain.audit {
			audit[k] += v
		}
		i.Close()
	}
	share := 100 * float64(converted) / math.Max(float64(total), 1)
	t.Logf("saddle: converted %.1f%% of %d ticks | attacker downed %d, bandit lost %d of 12 | attacker blinded %d times | audit %v | modes %v",
		share, total, downed, lost, blinded, audit, modes)
	if lost > 2 {
		t.Errorf("the ace was gunned down %d of 12 by the fair-start saddler: the close defence regressed (it held 12/12 at this probe's birth)", lost)
	}
}

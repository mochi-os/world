// Mochi world: the high yo-yo has to come back down (#153)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestHighYoyoCompletes: a chosen high yo-yo tops out within twelve hundred
// metres above the target - the old law zoomed 1,400-2,100 m and came back
// thirty seconds later; under the ace's cap the apex-staged law tops out at
// 100-900 m, the wing at 200 kt being slow to pitch over - and is back down within twenty seconds.
// The one-phase law flew the climb alone, re-aimed above the lag line every
// re-plan, and a yo-yo the ace chose straight out of the merge became a
// 7,000-ft zoom to 174 kt and thirty seconds of recovery. The opponent is the
// hornet, the scripted slow fighter built from the 2026-09-09 recording,
// because that is the fight the ace reaches for `high` in.
func TestHighYoyoCompletes(t *testing.T) {
	heavy(t)
	chosen, completed, zooms, stranded, climbs := 0, 0, 0, 0, 0
	worst := 0.0
	for seed := uint64(1); seed <= 12; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "yoyo", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
		place(i, bot, 0, 2400)
		me := &i.aircraft[0].model.State
		me.Velocity = me.Velocity.Normalize().Scale(-344 / 1.944)
		me.Attitude = flight.Look(me.Velocity.Normalize())
		foe := &i.aircraft[bot].model.State
		pilot := &hornet{armed: true}
		b := i.aircraft[bot].brain
		in := false
		var startAbove, startOver, peak float64
		var since uint64
		var trail []string // the episode's play and rise, sampled twice a second, for the trace
		pulling := false   // in the yo-yo's first phase last tick: a climb starts when this turns true
		for tick := uint64(0); tick < 120*60; tick++ {
			i.Step(tick, map[int][]game.Input{0: {{Data: pilot.fly(me, foe, inbound(i, 0), tick)}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive || i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			if up := b.play == "high" && b.aim.Y > 0.3; up && !pulling {
				climbs++
				pulling = true
			} else if !up {
				pulling = false
			}
			// A zoom is far above where the climb began AND far above him.
			// Altitude alone reads a chase up after a climbing target as a
			// zoom; rise over him alone reads his dive away as one. The excess
			// is the smaller of the two gains since the episode began.
			if b.play == "high" && !in {
				in, since = true, tick
				startAbove, startOver = foe.Position.Y, foe.Position.Y-me.Position.Y
				peak = 0
				trail = trail[:0]
				chosen++
			}
			if !in {
				continue
			}
			alt := math.Min(foe.Position.Y-startAbove, foe.Position.Y-me.Position.Y-startOver)
			if alt > peak {
				peak = alt
			}
			if (tick-since)%30 == 0 {
				trail = append(trail, fmt.Sprintf("%s%+.0f", b.play, alt))
			}
			// The episode is the manoeuvre's CONSEQUENCE, not the play's
			// tenure: it ends when the jet is back near where the climb began.
			if alt <= 150 && tick-since > 60 {
				completed++
				if peak > 1200 {
					zooms++
					if os.Getenv("AIR_TRACE") != "" {
						fmt.Printf("  seed %d t=%.1f ZOOM +%.0f m: %s\n", seed, float64(since)/60, peak, strings.Join(trail, " "))
					}
				}
				if peak > worst {
					worst = peak
				}
				in = false
			} else if tick-since > 20*60 {
				stranded++
				if peak > 1200 {
					zooms++ // a zoom that is also stranded is still a zoom
				}
				if os.Getenv("AIR_TRACE") != "" {
					fmt.Printf("  seed %d t=%.1f STRANDED +%.0f m: %s\n", seed, float64(since)/60, peak, strings.Join(trail, " "))
				}
				if peak > worst {
					worst = peak
				}
				in = false
			}
		}
		i.Close()
	}
	minutes := 12 * 120.0 / 60
	t.Logf("high chosen %d times across 12 seeds: %d came back, %d rose more than 1,200 m over him, %d never came back within 20 s; worst climb %.0f m; %d climbs, %.1f a minute",
		chosen, completed, zooms, stranded, worst, climbs, float64(climbs)/minutes)
	// The cadence (#174): the human who won with this flew nine in two hundred
	// seconds, with pursuit between them. Closure-alone re-entry pulled up
	// again at every bottom, and the climb count is what tells a manoeuvre
	// from a loop.
	if float64(climbs)/minutes > 10 {
		t.Errorf("%.1f climbs a minute: the yo-yo is a loop, not a manoeuvre", float64(climbs)/minutes)
	}
	if chosen == 0 {
		t.Errorf("the ace never chose high against the hornet: the gate measures nothing")
	}
	// A rate, not a ban: under the cap the wing at 200 kt is slow to pitch
	// over and the apex-staged law floats past 1,200 m about once in five
	// hundred (1,296 m, seed 4); the old law zoomed 1,400-2,100 m once in
	// forty and came back thirty seconds later.
	if float64(zooms) > 0.01*float64(chosen) {
		t.Errorf("%d of %d yo-yos rose more than 1,200 m over him (worst %.0f m): that is a zoom, not a yo-yo", zooms, chosen, worst)
	}
	if float64(stranded) > 0.01*float64(chosen) {
		t.Errorf("%d of %d yo-yos never came back down within 20 s", stranded, chosen)
	}
}

// TestHighYoyoOnThreat: the high yo-yo's pull starts on an overshoot threat -
// closing on a target well off its tail - and not in a tail chase, where
// closure is a shot forming and closure alone would climb out of it.
func TestHighYoyoOnThreat(t *testing.T) {
	var high play
	for _, p := range plays {
		if p.name == "high" {
			high = p
		}
	}
	if high.law == nil {
		t.Fatal("no high play")
	}
	// Level with him, 800 m ahead, closing at 100 m/s: me at 250 m/s toward
	// him, him crossing at 90 degrees (a beam) or running away (a tail chase).
	build := func(his flight.Vec3) *moment {
		me := &flight.State{Position: flight.Vec3{Y: 3000}, Velocity: flight.Vec3{X: 250}, Attitude: flight.Look(flight.Vec3{X: 1})}
		m := &moment{me: me, prey: flight.Vec3{X: 800, Y: 3000}, velocity: his, pace: 180, pull: 7.5}
		m.derive()
		return m
	}
	climb := func(o order) bool { return o.aim.Y > 0.3 } // the pull aims 450 m above the lag line; the descent aims at lead, level with him
	beam, tail := flight.Vec3{Z: 150}, flight.Vec3{X: 150}

	if o := high.law(build(beam)); !climb(o) {
		t.Errorf("beam target closing at %.0f m/s: expected the yo-yo's pull, got the descent (aim.Y %.2f)", build(beam).closure, o.aim.Y)
	}
	if o := high.law(build(tail)); climb(o) {
		t.Errorf("tail chase closing at %.0f m/s: a shot forming, not an overshoot threat - expected the descent, got a climb", build(tail).closure)
	}
}

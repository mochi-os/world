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

// TestHighYoyoCompletes: a chosen high yo-yo tops out within nine hundred
// metres above the target and is back down within twenty seconds.
// The one-phase law flew the climb alone, re-aimed above the lag line every
// re-plan, and a yo-yo the ace chose straight out of the merge became a
// 7,000-ft zoom to 174 kt and thirty seconds of recovery. The opponent is the
// hornet, the scripted slow fighter built from the 2026-09-09 recording,
// because that is the fight the ace reaches for `high` in.
func TestHighYoyoCompletes(t *testing.T) {
	heavy(t)
	chosen, completed, zooms, stranded := 0, 0, 0, 0
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
		for tick := uint64(0); tick < 120*60; tick++ {
			i.Step(tick, map[int][]game.Input{0: {{Data: pilot.fly(me, foe, tick)}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive || i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
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
				if peak > 900 {
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
				if peak > 900 {
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
	t.Logf("high chosen %d times across 12 seeds: %d came back, %d rose more than 900 m over him, %d never came back within 20 s; worst climb %.0f m", chosen, completed, zooms, stranded, worst)
	if chosen == 0 {
		t.Errorf("the ace never chose high against the hornet: the gate measures nothing")
	}
	if zooms > 0 {
		t.Errorf("%d of %d yo-yos rose more than 900 m over him (worst %.0f m): that is a zoom, not a yo-yo", zooms, chosen, worst)
	}
	if stranded > 0 {
		t.Errorf("%d of %d yo-yos never came back down within 20 s", stranded, chosen)
	}
}

// Mochi world: the perch-and-pounce probe (#64)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// pouncer flies the pattern that has won every piloted fight against the bot:
// hold a perch above, dive through with the trigger live when holding height
// and position, zoom back up, repeat. The perch re-bases toward the bot only
// GENTLY, so the sky stays contestable — a bot that climbs sustainedly can
// take it, and one that never tries is the #64 defect being measured.
type pouncer struct {
	phase   string
	ceiling float64
	centre  flight.Vec3
	since   uint64
}

func (p *pouncer) fly(me, foe *flight.State, tick uint64) map[string]any {
	if p.phase == "" {
		p.phase, p.ceiling, p.centre = "perch", me.Position.Y, foe.Position
	}
	// The perch drifts toward station above the bot at capped rates: slow
	// enough to contest, fast enough that the fight cannot simply leave.
	const dt = 1.0 / 60
	want := foe.Position.Y + 1000
	p.ceiling += clamp(want-p.ceiling, -8*dt, 8*dt)
	toward := foe.Position.Subtract(p.centre)
	toward.Y = 0
	if reach := toward.Length(); reach > 1 {
		step := math.Min(reach, 60*dt)
		p.centre = p.centre.Add(toward.Scale(step / reach))
	}
	held := tick - p.since
	gap := me.Position.Y - foe.Position.Y
	lateral := me.Position.Subtract(foe.Position)
	lateral.Y = 0
	span := lateral.Length()
	switch p.phase {
	case "pounce":
		if span < 350 || gap < 100 || held > 15*60 {
			p.phase, p.since = "zoom", tick
			break
		}
		return pursue(me, foe) // trigger live: pursue fires on parameters
	case "zoom":
		if gap > 800 || held > 20*60 {
			p.phase, p.since = "perch", tick
		}
	default: // perch
		if gap > 500 && span < 2500 && held > 3*60 {
			p.phase, p.since = "pounce", tick
			return pursue(me, foe)
		}
	}
	// Perch and zoom both fly toward a ghost: the orbit station while
	// perched, a point up and away while zooming — pursue() is the proven
	// stable controller for both.
	ghost := flight.State{}
	if p.phase == "zoom" {
		away := me.Position.Subtract(foe.Position)
		away.Y = 0
		if away.Length() > 1 {
			away = away.Normalize()
		} else {
			away = flight.Vec3{X: 1}
		}
		ghost.Position = me.Position.Add(away.Scale(1500)).Add(flight.Vec3{Y: 1200})
		ghost.Velocity = flight.Vec3{}
	} else {
		angle := float64(tick) / 60 * 0.15
		ghost.Position = flight.Vec3{X: p.centre.X + 900*math.Cos(angle), Y: p.ceiling, Z: p.centre.Z + 900*math.Sin(angle)}
		ghost.Velocity = flight.Vec3{X: -135 * math.Sin(angle), Z: 135 * math.Cos(angle)}
	}
	data := pursue(me, &ghost)
	data["fire"] = false
	return data
}

// TestPounceExposure (#64): the baseline of the defect the players keep
// winning through — how the bot fares against the perch-and-pounce pattern.
// Log-only for now: the fix's acceptance gate gets set from this baseline.
func TestPounceExposure(t *testing.T) {
	heavy(t)
	beneath, total, downed, lost, windows := 0, 0, 0, 0, 0
	modes := map[string]int{}
	for seed := uint64(1); seed <= 24; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("pounce%d", seed), Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
		place(i, bot, 0, -1800)
		me := &i.aircraft[0].model.State
		me.Position.Y = i.aircraft[bot].model.State.Position.Y + 1200
		foe := &i.aircraft[bot].model.State
		attacker := &pouncer{}
		for tick := uint64(0); tick < 150*60; tick++ {
			data := attacker.fly(me, foe, tick)
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			total++
			modes[i.aircraft[bot].brain.mode]++
			apart := me.Position.Subtract(foe.Position).Length()
			if me.Position.Y-foe.Position.Y > 300 && apart < 2500 {
				beneath++
			}
			// The window the player converts and pursue() cannot: the attacker
			// inside gun range with the bot near its boresight.
			if apart < 700 {
				los := foe.Position.Subtract(me.Position).Scale(1 / math.Max(apart, 1))
				if me.Attitude.Rotate(flight.Vec3{X: 1}).Dot(los) > 0.94 {
					windows++
				}
			}
		}
		if i.aircraft[bot].kills > 0 {
			downed++
		}
		if !i.aircraft[bot].alive || i.aircraft[bot].model == nil {
			lost++
		}
		i.Close()
	}
	share := 100 * float64(beneath) / math.Max(float64(total), 1)
	t.Logf("pounce baseline: bandit lost %d of 24, attacker downed %d | lingered beneath %.1f%% | gun windows handed over %.1f s per fight | of %d ticks | modes %v",
		lost, downed, share, float64(windows)/60/24, total, modes)
}

// Mochi world: Offensive conversion instrument
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"world/game"
	"world/games/air/battle"
	"world/games/air/flight"
)

// evade is a competent guns defence, scripted: corner-ish speed, a hard level
// turn reversed every six seconds, breaking harder with a threat close behind,
// easing out of the bank to recover the nose when low. It is the OPPOSITE of
// the compliant drone: a target that manufactures crossing geometry and never
// hands the attacker a stabilised tail shot.
func evade(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	r := toward.Length()
	behind := me.Velocity.Normalize().Scale(-1)
	off := 90.0
	if r > 1 {
		off = math.Acos(clamp(toward.Normalize().Dot(behind), -1, 1)) * 57.3
	}
	direction := 1.0
	if (tick/360)%2 == 1 { // the reversal: every six seconds the circle flips
		direction = -1
	}
	pull := 0.55
	if r < 900 && off < 60 {
		pull = 0.95 // he is close behind: break
	}
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	bank := math.Atan2(right.Y, up.Y)
	want := direction * 1.31 // ~75 degrees
	if me.Position.Y < 2200 && me.Velocity.Y < -40 {
		want *= 0.25 // sinking toward the floor: shallow the bank, the pull recovers the nose
	}
	speed := me.Velocity.Length()
	throttle, reheat := 1.0, 0.0
	if speed < 165 {
		reheat = 1
	} else if speed > 215 {
		throttle = 0.65
	}
	return map[string]any{
		"pitch":    pull,
		"roll":     clamp((bank-want)*1.5, -1, 1), // positive stick rolls right = negative bank in this convention (see weave)
		"throttle": throttle,
		"reheat":   reheat,
	}
}

// TestOffence is the #206 instrument: per tier, against a DEFENDING target the
// bot starts 600 m behind, it separates the two possible offensive deficits —
// does a firing solution ever exist (geometry), and is it taken when it does
// (discipline)? "Solution" is scored two ways: a human-standard shot (led miss
// under 15 m inside 700 m — what the SHOOT cue calls valid) and the bot's own
// gate (its tolerance at its trigger). Diagnostic: it prints, and its assert
// is only that the harness itself worked.
func TestOffence(t *testing.T) {
	if testing.Short() {
		t.Skip("simulated minutes")
	}
	for _, level := range []string{"rookie", "pilot", "veteran", "ace"} {
		sk := skills[level]
		exist, gate, fired, firedExist, total := 0, 0, 0, 0, 0
		kills, closest := 0, 1e9
		why := map[string]int{} // mode|shoot during gate-open unfired ticks
		for seed := uint64(1); seed <= 6; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: "offence", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false, "bots": map[string]any{level: 1.0}}})
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
			place(i, 0, bot, -600) // the ADVANTAGE is the bot's: 600 m behind the defender
			me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
			for tick := uint64(0); tick < 60*120; tick++ {
				data := evade(me, foe, tick)
				i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
				if !i.aircraft[0].alive {
					kills++
					break
				}
				if !i.aircraft[bot].alive {
					break // the defender's wake turbulence does not kill; a crash ends the seed
				}
				total++
				s := &i.aircraft[bot].model.State
				toward := me.Position.Subtract(s.Position)
				d := toward.Length()
				if d < closest {
					closest = d
				}
				// The led ballistic solution, exactly as the gunnery flies it.
				los := toward.Scale(1 / math.Max(d, 1))
				closure := s.Velocity.Subtract(me.Velocity).Dot(los)
				transit := d / math.Max(battle.Muzzle+closure, 200)
				spot := me.Position.Add(me.Velocity.Scale(transit)).
					Subtract(s.Velocity.Scale(transit)).
					Add(flight.Vec3{Y: 4.9 * transit * transit})
				aim := spot.Subtract(s.Position).Normalize()
				nose := s.Attitude.Rotate(flight.Vec3{X: 1})
				miss := math.Acos(clamp(nose.Dot(aim), -1, 1)) * math.Max(d, 50)
				human := miss < 15 && d < 700
				own := miss < 22+sk.trigger*d*1.5 && d < sk.open
				if human {
					exist++
				}
				if own {
					gate++
				}
				if i.aircraft[bot].latest.Fire {
					fired++
					if human {
						firedExist++
					}
				} else if own {
					b := i.aircraft[bot].brain
					state := "safe:" + b.safed
					if b.shoot {
						state = "live"
					}
					why[b.mode+"|"+state]++
				}
			}
		}
		if total == 0 {
			t.Fatal("the harness never ran a tick")
		}
		pct := func(n int) float64 { return 100 * float64(n) / float64(total) }
		unfired := []string{}
		for k, v := range why {
			unfired = append(unfired, fmt.Sprintf("%s %d", k, v))
		}
		sort.Strings(unfired)
		fmt.Printf("%-8s human-shot exists %5.2f%%   own gate open %5.2f%%   trigger down %5.2f%%   fired-during-shot %d/%d   closest %4.0f m   kills %d/6\n",
			level, pct(exist), pct(gate), pct(fired), firedExist, exist, closest, kills)
		fmt.Printf("         gate-open UNFIRED by mode|gun: %v\n", unfired)
	}
}

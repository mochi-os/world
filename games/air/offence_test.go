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

// offer flies the deliberate overshoot's aftermath: wings level, full military
// power, dead straight — the six handed over on a plate.
func offer(me *flight.State) map[string]any {
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	bank := math.Atan2(right.Y, up.Y)
	return map[string]any{
		"pitch":    clamp(-me.Velocity.Y*0.004+math.Abs(bank)*0.1, -0.3, 0.4),
		"roll":     clamp(bank*1.5, -1, 1),
		"throttle": clamp(0.5+(190-me.Velocity.Length())*0.01, 0.2, 0.9), // waiting, not running: the recorded overshoot loitered for the bot to convert
	}
}

// TestConvert is the overshoot referendum (#206): the recorded fight where a
// player deliberately overshot the bandit and flew straight, offering their
// six — and the bot made no attempt to take it. The attacker presses a real
// chase long enough to drive the bot defensive, then gives up the fight and
// flies straight ahead. Conversion is the whole job: the bot must arrive in
// the attacker's rear quarter, use the gun, and finish. The gates are armed
// from birth — the old mode ladder measured zero on both.
func TestConvert(t *testing.T) {
	heavy(t)
	ladder := map[string]int{}
	for _, level := range []string{"ace", "superhuman"} {
		converted, total := 0.0, 0.0
		killed, fired := 0, 0
		for seed := uint64(1); seed <= 12; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: "convert", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
			place(i, 0, bot, 600) // the bot ahead: the human is the established attacker
			me := &i.aircraft[0].model.State
			for tick := uint64(0); tick < 60*150; tick++ {
				var data map[string]any
				if tick < 60*8 {
					data = pursue(me, &i.aircraft[bot].model.State)
					data["fire"] = false // the chase is PRESSURE: the recorded scenario offered an undamaged bot its six, and a script-aimed hose would cripple it first
				} else {
					data = offer(me)
				}
				i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
				if !i.aircraft[0].alive {
					killed++
					break
				}
				if !i.aircraft[bot].alive {
					break
				}
				if seed == 1 && tick%600 == 0 { // the timeline, one seed: where conversion dies
					b := i.aircraft[bot].brain
					_, r := i.bearing(i.aircraft[bot].model.State.Position, me.Position)
					fmt.Printf("    t=%3.0fs mode=%-8s play=%-8s target=%d range=%5.0f speed=%3.0f fuel=%4.0f thr=%.2f rh=%.0f alt=%4.0f\n",
						float64(tick)/60, b.mode, b.play, b.target, r, i.aircraft[bot].model.State.Velocity.Length(),
						i.aircraft[bot].model.State.Fuel, i.aircraft[bot].latest.Throttle, i.aircraft[bot].latest.Reheat, i.aircraft[bot].model.State.Position.Y)
					d := &i.aircraft[bot].model.State.Damage
					if d.Loss > 0 || i.aircraft[bot].model.State.Damage.Leak > 0 {
						fmt.Printf("      damage: loss=%.2f leak=%.2f\n", d.Loss, d.Leak)
					}
				}
				if tick >= 60*8 {
					total++
					s := &i.aircraft[bot].model.State
					to := s.Position.Subtract(me.Position)
					if r := to.Length(); r > 1 && r < 1500 {
						if to.Scale(1/r).Dot(me.Velocity.Normalize().Scale(-1)) > 0.5 {
							converted++
						}
					}
					if i.aircraft[bot].latest.Fire {
						fired++
					}
				}
			}
			if seed == 1 {
				if h := i.aircraft[0]; h.model != nil {
					fmt.Printf("      offerer after: loss=%.2f leak=%.2f engines=%.2f/%.2f alive=%v\n",
						h.model.State.Damage.Loss, h.model.State.Damage.Leak,
						h.model.State.Damage.Engine[0], h.model.State.Damage.Engine[1], h.alive)
				} else {
					fmt.Printf("      offerer after: detonated (model torn down)\n")
				}
			}
		}
		pct := 100 * converted / math.Max(total, 1)
		fmt.Printf("%-8s converted %5.1f%% of the offer   fired %d ticks   killed the offerer %d/12\n", level, pct, fired, killed)
		if pct < 8 {
			t.Errorf("%s holds the attacker's rear quarter for %.1f%% of a handed-over fight — the overshoot goes unpunished", level, pct)
		}
		ladder[level] = killed
		// Kill counts at 12 deterministic seeds carry ±2 of pure reshuffle
		// resolution (the #225 lesson: bands fitted to single rolls are bands
		// fitted to noise), so the CONVERSION percentage above is the primary
		// #206 gate, and the kills are gated on the ladder's shape plus an
		// ace floor with real slack — the drone ladder's own convention.
		floor := map[string]int{"ace": 4, "superhuman": 11}[level]
		if killed < floor {
			t.Errorf("%s killed the straight-and-level offerer in %d/12 seeds (floor %d) — the free kill goes untaken", level, killed, floor)
		}
	}
	if ladder["superhuman"] < ladder["ace"]-1 {
		t.Errorf("conversion lethality inverts: ace %d kills, superhuman %d", ladder["ace"], ladder["superhuman"])
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
	heavy(t)
	for _, level := range []string{"novice", "pilot", "ace", "superhuman"} {
		sk := skills[level]
		exist, gate, fired, firedExist, total := 0, 0, 0, 0, 0
		aspect := [3]float64{} // mean angle off the DEFENDER's tail, per third of the fight: the "is he getting on my six" trace
		counts := [3]int{}
		thirds := [3]map[string]int{{}, {}, {}} // dominant modes per third
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
				{ // attacker aspect: the bot's bearing off the defender's six (0 = parked there)
					toward := s.Position.Subtract(me.Position)
					if r := toward.Length(); r > 1 {
						tail := me.Velocity.Normalize().Scale(-1)
						off := math.Acos(clamp(toward.Scale(1/r).Dot(tail), -1, 1)) * 57.3
						third := int(tick * 3 / (60 * 120))
						if third > 2 {
							third = 2
						}
						aspect[third] += off
						counts[third]++
						thirds[third][i.aircraft[bot].brain.mode]++
					}
				}
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
		for k := range aspect {
			if counts[k] > 0 {
				aspect[k] /= float64(counts[k])
			}
		}
		fmt.Printf("         angle off the defender's tail by fight third: %3.0f -> %3.0f -> %3.0f deg\n", aspect[0], aspect[1], aspect[2])
		for k := range thirds {
			top, best := "", 0
			for m, c := range thirds[k] {
				if c > best {
					top, best = m, c
				}
			}
			fmt.Printf("         third %d dominant mode: %s %d%%\n", k+1, top, 100*best/max(counts[k], 1))
		}
	}
}

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
	"world/games/air/flight"
)

// The human-fight diagnostic (#206). The doctrine was tuned and validated
// bot-versus-bot, where both sides fly the same law and the same discipline.
// A human reported guns-killing the ACE from a couple of turns in, never
// feeling threatened — a result no bot-versus-bot sweep can express.
//
// This harness stands in for that human: a scripted attacker that simply
// converts to the bandit's six and stays there (lead pursuit, energy managed
// by holding a working speed), which is what a novice does once they know to
// pull lead. It is deliberately CRUDE — no gunnery, no yo-yos, no gun-solution
// discipline. If the ace cannot shake an attacker this simple, the defensive
// doctrine is the problem, not the human's skill.
//
// The trace is the point: a replay shows what the bot DID, but the mode
// histogram shows what it CHOSE, and the two answer different questions.
//
// LIMIT OF THIS HARNESS, found while fixing #206: the scripted attacker is a
// PERFECT pursuer — it never overshoots, never loses sight, never has to break
// off to shoot. Nothing escapes that, and no real pilot faces it, so the
// tracked-in-rear-quarter share saturates near 60-70 % however good the
// defence is. The number is a useful FLOOR (a bot that cannot be tracked by
// this thing is genuinely evasive) but a poor ceiling. The honest measures of
// the #206 defect are the ones that did move: decision churn and energy state.
// A future revision should give the attacker human failings — an overshoot
// when closure is high, a lost tally under g — before treating the tracked
// share as a target to optimise.
//
// FIRST RESULTS (2026-07-27, 6 seeds x 60 s, guns-only, attacker starting at
// the bandit's six 600 m back):
//
//	                    tracked in rear quarter   switches/s   mean speed   energy gap
//	  veteran                    49 %                0.3        429 kt        -81 m
//	  ace                        67 %                0.5        363 kt       -271 m
//
// Neither ever broke past 4 km. The ace is measurably EASIER to sit behind
// than the veteran, which inverts the skill ladder, and the trace rules out
// the obvious explanation: both DO select the defensive library (defense
// ~50 %, spiral, drag, reverse), so this is not a bot that fails to notice a
// threat. The defensive maneuvers simply do not generate separation, and the
// ace's faster decision cadence (8 ticks against the veteran's 12) churns
// modes ~70 % more often while ending up slower and further down on energy —
// the shape of a jet that keeps starting maneuvers and never finishes one.
// The fix belongs in commitment/energy discipline, not in threat detection.

// pursue flies the crude attacker: point at the bandit's projected six with
// enough lead to close, hold ~350 kt (the sustained corner band), and pull.
func pursue(me, foe *flight.State) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	range_ := toward.Length()
	if range_ < 1 {
		return map[string]any{}
	}
	// Aim for a point behind the bandit — the classic novice's "get on his
	// tail" rather than a proper lead-pursuit gun solution.
	// The human failing the LIMIT note demands before the tracked-share gate
	// can be honest: a novice arriving with high closure at short range cannot
	// stop it — he blows through. Unload and ride the overshoot straight
	// through the pass. No state is needed: geometry ends it by itself, since
	// once he is past, the closure goes negative and the condition releases.
	rel := me.Velocity.Subtract(foe.Velocity)
	if closing := rel.Dot(toward.Scale(1 / range_)); range_ < 400 && closing > 70 {
		return map[string]any{"pitch": 0.1, "roll": 0.0, "throttle": 0.85, "fire": false}
	}
	six := foe.Position.Subtract(foe.Velocity.Normalize().Scale(math.Min(300, range_*0.6)))
	want := six.Subtract(me.Position).Normalize()
	forward := me.Attitude.Rotate(flight.Vec3{X: 1})
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	// Body-axis errors to the wanted direction.
	pitchError := math.Asin(clamp(want.Dot(up), -1, 1))
	yawError := math.Asin(clamp(want.Dot(right), -1, 1))
	// Roll to put the lift vector on the target, then pull — BFM 101.
	roll := clamp(yawError*2.2, -1, 1)
	if want.Dot(forward) < 0 {
		roll = clamp(yawError*4, -1, 1) // way off the nose: roll harder
	}
	pitch := clamp(pitchError*2.5+0.15, -1, 1)
	speed := me.Velocity.Length()
	throttle, reheat := 1.0, 0.0
	if speed > 200 { // ~390 kt: stop accelerating, keep the energy
		throttle = 0.85
	} else if speed < 170 {
		reheat = 1
	}
	return map[string]any{"pitch": pitch, "roll": roll, "throttle": throttle, "reheat": reheat, "fire": true}
}

// geometry reports the attacker's position in the bandit's world: range, the
// attacker's angle off the bandit's tail (0 = dead six), and each side's
// specific energy height.
func geometry(attacker, bandit *flight.State) (rangeM, angleOff, myEnergy, hisEnergy float64) {
	toward := attacker.Position.Subtract(bandit.Position)
	rangeM = toward.Length()
	banditTail := bandit.Velocity.Normalize().Scale(-1)
	if rangeM > 1 {
		angleOff = math.Acos(clamp(toward.Normalize().Dot(banditTail), -1, 1)) * 57.3
	}
	energy := func(s *flight.State) float64 {
		v := s.Velocity.Length()
		return s.Position.Y + v*v/19.62
	}
	return rangeM, angleOff, energy(attacker), energy(bandit)
}

// TestDoctrineUnderHumanPressure is diagnostic, not a gate: it prints what the
// ace CHOOSES while a crude attacker sits on its tail. It fails only on the
// claim the report makes concrete — that the top skill offers no threat and no
// escape.
func TestDoctrineUnderHumanPressure(t *testing.T) {
	heavy(t)
	levels := []string{"ace", "superhuman"}
	for _, level := range levels {
		modes := map[string]int{}
		tracked, shots, closest, escaped := 0, 0, 1e9, 0
		converted := 0 // ticks the BOT held the attacker's rear quarter: the counter-offensive that has never existed
		total, switches, slow := 0, 0, 0
		energyGap, speedSum := 0.0, 0.0
		for seed := uint64(1); seed <= 6; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: "doctrine", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false, "bots": map[string]any{level: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
				t.Fatal(err)
			}
			// Find the bot and place the attacker where the report starts:
			// already converted to its six, 600 m back, co-speed.
			bot := -1
			for slot, a := range i.aircraft {
				if a != nil && a.brain != nil {
					bot = slot
				}
			}
			if bot < 0 {
				t.Fatal("no bot in the session")
			}
			place(i, bot, 0, -600) // attacker behind the bandit
			me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
			previousMode := ""
			for tick := uint64(0); tick < 60*60; tick++ { // one simulated minute
				data := pursue(me, foe)
				i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
				if !i.aircraft[0].alive || !i.aircraft[bot].alive {
					break
				}
				total++
				mode := i.aircraft[bot].brain.mode
				if mode != previousMode {
					switches++
					previousMode = mode
				}
				modes[mode]++
				r, off, mine, his := geometry(me, foe)
				if r < closest {
					closest = r
				}
				if r < 900 && off < 45 { // in the bandit's rear quarter, guns range
					tracked++
				}
				{ // and the mirror: the bot behind the ATTACKER
					toward := foe.Position.Subtract(me.Position)
					if rr := toward.Length(); rr > 1 && rr < 900 {
						tail := me.Velocity.Normalize().Scale(-1)
						if math.Acos(clamp(toward.Scale(1/rr).Dot(tail), -1, 1))*57.3 < 45 {
							converted++
						}
					}
				}
				if r > 4000 {
					escaped++
				}
				energyGap += his - mine
				speed := foe.Velocity.Length()
				speedSum += speed
				if speed < 130 { // ~250 kt: below the corner band, out of ideas
					slow++
				}
				if i.aircraft[0].kills > 0 {
					shots++
					break
				}
			}
		}
		type entry struct {
			mode  string
			ticks int
		}
		list := []entry{}
		for mode, n := range modes {
			list = append(list, entry{mode, n})
		}
		sort.Slice(list, func(a, b int) bool { return list[a].ticks > list[b].ticks })
		fmt.Printf("\n=== %s under a crude tail-chase (6 seeds, 60 s each) ===\n", level)
		fmt.Printf("  tracked in the rear quarter: %.0f%% of the fight | escaped past 4 km: %.0f%% | closest %.0f m | killed %d/6 | CONVERTED to the attacker's rear quarter %.1f%%\n",
			100*float64(tracked)/math.Max(1, float64(total)), 100*float64(escaped)/math.Max(1, float64(total)), closest, shots,
			100*float64(converted)/math.Max(1, float64(total)))
		fmt.Printf("  mode switches: %.1f/s | bandit mean speed %.0f kt, below 250 kt for %.0f%% | energy gap %+.0f m (his minus mine)\n",
			float64(switches)/math.Max(1, float64(total)/60), speedSum/math.Max(1, float64(total))*1.944,
			100*float64(slow)/math.Max(1, float64(total)), energyGap/math.Max(1, float64(total)))
		fmt.Printf("  doctrine chosen:")
		for k, e := range list {
			if k >= 6 {
				break
			}
			fmt.Printf(" %s %.0f%%", e.mode, 100*float64(e.ticks)/math.Max(1, float64(total)))
		}
		fmt.Println()
		if level == "ace" { // re-enabled 2026-08-11: the attacker now overshoots (see pursue), which is the failing the LIMIT note required before this share could be judged
			// The report's claim, made falsifiable: an ace held at gun
			// parameters for most of a minute by an attacker this crude is
			// not a threat to anyone.
			if share := float64(tracked) / math.Max(1, float64(total)); share > 0.5 {
				t.Errorf("ace spent %.0f%% of the fight tracked in its rear quarter by a scripted novice", 100*share)
			}
		}
	}
}

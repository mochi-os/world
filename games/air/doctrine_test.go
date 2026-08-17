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

// chaser adds the second human failing the LIMIT note asks for: this pilot
// loses sight of a bandit that goes far enough off his nose, and the harder he
// is pulling the sooner it happens — a head cannot turn under g. While the
// tally is gone he flies the line he last saw and does not shoot, until the
// bandit reappears in front of him.
//
// Measured 2026-08-17: without it nothing escapes this attacker, so no play a
// defender can choose reduces the threat, the scorer's threat term is flat
// across every candidate, and the harness cannot tell a bot that chooses badly
// from one with no good choice. `pursue` is left perfect on purpose — two other
// suites use it as a fixture rather than as a defence benchmark.
type chaser struct {
	lost    int         // times the tally went, for the probe
	blew    int         // ticks spent riding an overshoot through the pass
	closest float64     // and the fastest he ever arrived, m/s of closure
	widest  float64     // and the furthest off his nose the bandit ever got
	blind   uint64      // ticks of lost tally remaining, 0 = he has him
	where   flight.Vec3 // where the bandit was when he lost him
	heading flight.Vec3 // and where he was going
}

func (c *chaser) fly(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	span := math.Max(toward.Length(), 1)
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})
	off := math.Acos(clamp(toward.Scale(1/span).Dot(nose), -1, 1)) * 57.3
	// A bubble canopy gives most of a sphere with head movement, but only
	// while the neck is free: 130 degrees relaxed, down to 70 under a hard pull.
	reach := 130 - 60*clamp((me.Fcs.Demand-1)/6, 0, 1)
	if off > c.widest {
		c.widest = off
	}
	if c.blind == 0 && off > reach {
		c.lost++
		c.blind, c.where, c.heading = 90, foe.Position, foe.Velocity // ~1.5 s before he even starts looking
	}
	if c.blind > 0 {
		c.blind--
		// Back in front of him and no longer pulling blind: he has the tally again.
		if c.blind == 0 || off < 45 {
			c.blind = 0
			return pursue(me, foe)
		}
		ghost := *foe
		ghost.Position = c.where.Add(c.heading.Scale(float64(90-c.blind) / 60))
		ghost.Velocity = c.heading
		data := pursue(me, &ghost) // the line he last saw, flown on faith
		data["fire"] = false       // and no shooting at something he cannot see
		return data
	}
	// Did the other human failing — arriving too fast to stop — ever fire?
	rel := me.Velocity.Subtract(foe.Velocity)
	if closing := rel.Dot(toward.Scale(1 / span)); closing > c.closest {
		c.closest = closing
	}
	if closing := rel.Dot(toward.Scale(1 / span)); span < 400 && closing > 70 {
		c.blew++
	}
	return pursue(me, foe)
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
		tracked, closest, escaped := 0, 1e9, 0
		converted := 0       // ticks the BOT held the attacker's rear quarter: the counter-offensive that has never existed
		downed, lost := 0, 0 // seeds where the ATTACKER died, and where the bandit did
		broke := 0           // seeds where the bandit ever broke contact past 4 km
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
			hunter := &chaser{}
			away := false                                 // this seed ever broke contact past 4 km
			for tick := uint64(0); tick < 60*60; tick++ { // one simulated minute
				data := hunter.fly(me, foe, tick)
				i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
				if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
					i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
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
					away = true
				}
				energyGap += his - mine
				speed := foe.Velocity.Length()
				speedSum += speed
				if speed < 130 { // ~250 kt: below the corner band, out of ideas
					slow++
				}
			}
			// Book the outcome from the SCORING, not from who stopped flying.
			// The attacker is a crude script and flies itself into the sea
			// often enough to matter: in seed 1 it died at 21.4 s with the bot
			// having fired not one round, so "the attacker died" credits the
			// ace with a kill it had no part in. `kills` is the game's own
			// attribution and cannot be earned by the other side's mistake.
			//
			// (The old code read `i.aircraft[0].kills` — the ATTACKER's tally,
			// the wrong side — and read it below a `break` that had already
			// fired on the death tick, so "killed n/6" could only ever be 0
			// while the bandit was in fact dying in every seed.)
			if i.aircraft[bot].kills > 0 {
				downed++
			}
			if !i.aircraft[bot].alive || i.aircraft[bot].model == nil {
				lost++
			}
			if away {
				broke++
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
		fmt.Printf("  OUTCOME: killed the attacker in %d of 6 | shot down in %d | broke contact past 4 km in %d\n", downed, lost, broke)
		fmt.Printf("  tracked in the rear quarter: %.0f%% of the fight | escaped past 4 km: %.0f%% | closest %.0f m | CONVERTED to the attacker's rear quarter %.1f%%\n",
			100*float64(tracked)/math.Max(1, float64(total)), 100*float64(escaped)/math.Max(1, float64(total)), closest,
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
			// The report's claim, made falsifiable: this tier offers NO THREAT
			// AND NO ESCAPE. So the gate asks for one or the other, as an
			// outcome — a kill or a break past 4 km, in any of the six seeds.
			//
			// It used to gate the tracked SHARE at 50%, and that share cannot
			// carry a gate (measured 2026-08-16, #38). The window begins with
			// the attacker already at dead six 600 m, so it starts at 100%,
			// and it ends when someone dies, so the denominator depends on the
			// outcome. Giving the ace the tracer cue it was missing (flinch)
			// turned a seed from "shot down at 54.8 s" into "killed the
			// attacker at 21.4 s" — a strict improvement — and the share got
			// WORSE, 71% to 83%, because winning early removed thirty seconds
			// of less-pinned flying from the denominator. A number that
			// rewards dying sooner cannot judge this. Raising the threshold
			// would only have hidden that.
			//
			// The share stays printed above, where it belongs: diagnostic.
			if downed == 0 && broke == 0 {
				t.Errorf("ace was shot down in %d of 6 and never killed its attacker or broke contact: no threat and no escape", lost)
			}
		}
	}
}

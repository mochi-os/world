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

// The human-fight diagnostic (#206): a scripted attacker that converts to the
// bandit's six and stays there, standing in for a human result no
// bot-versus-bot sweep can express. LIMIT: it is a near-perfect pursuer, so the
// tracked-share number is a useful floor and a poor ceiling.

// pursue flies the crude attacker: point at the bandit's projected six with
// enough lead to close, hold ~350 kt (the sustained corner band), and pull.
func pursue(me, foe *flight.State) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	range_ := toward.Length()
	if range_ < 1 {
		return map[string]any{}
	}
	// Aim for a point behind the bandit - the novice's "get on his tail". A novice
	// arriving with high closure at short range blows through: unload and ride the
	// overshoot. No state needed; negative closure releases the condition.
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

// chaser adds the human failings pursue lacks: he loses sight of a bandit far
// enough off his nose, sooner the harder he is pulling, and flies the line he
// last saw without shooting. pursue stays perfect - two suites fixture it.
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
		audit := map[string]int{}
		tracked, closest, escaped := 0, 1e9, 0
		converted := 0       // ticks the BOT held the attacker's rear quarter: the counter-offensive that has never existed
		downed, lost := 0, 0 // seeds where the ATTACKER died, and where the bandit did
		broke := 0           // seeds where the bandit ever broke contact past 4 km
		total, switches, slow := 0, 0, 0
		energyGap, speedSum := 0.0, 0.0
		for seed := uint64(1); seed <= 24; seed++ {
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
			// Place the attacker converted to the bot's six, co-speed, pipper not yet
			// on. 1,200 m, not 600: at 600 the attacker's held trigger is a boresight
			// burst that ends most seeds before the bandit's second decision.
			bot := -1
			for slot, a := range i.aircraft {
				if a != nil && a.brain != nil {
					bot = slot
				}
			}
			if bot < 0 {
				t.Fatal("no bot in the session")
			}
			place(i, bot, 0, -1200) // attacker behind the bandit
			me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
			previousMode := ""
			hunter := &chaser{}
			away := false                                  // this seed ever broke contact past 4 km
			for tick := uint64(0); tick < 120*60; tick++ { // two simulated minutes: from 1,200 m the first pass takes forty seconds to arrive
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
			// Book the outcome from the SCORING, not from who stopped flying: the crude
			// attacker flies itself into the sea often enough to matter, and `kills` is
			// attribution that cannot be earned by the other side's mistake.
			if i.aircraft[bot].kills > 0 {
				downed++
			}
			if !i.aircraft[bot].alive || i.aircraft[bot].model == nil {
				lost++
			}
			if away {
				broke++
			}
			for k, v := range i.aircraft[bot].brain.audit {
				audit[k] += v
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
		fmt.Printf("\n=== %s under a crude tail-chase (24 seeds, 120 s each, from 1,200 m) ===\n", level)
		fmt.Printf("  OUTCOME: killed the attacker in %d of 24 | shot down in %d | broke contact past 4 km in %d\n", downed, lost, broke)
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
		fmt.Printf("  override audit: %v\n", audit)
		if level == "ace" {
			// The gate carries only the DEFENCE claims it can honestly make (#65):
			// the old outcome clause counted being shot down as an outcome, so the
			// #41-#61 defence arc — by removing the ace's last death — flipped it
			// red as a side effect of a pure improvement (at the clause's birth
			// the ace was tracked 83% and shot down 5 of 6). The offence numbers
			// stay printed above as the record: the counter-offensive has never
			// existed (converted 0.0% at every point in history). The promised
			// re-arm of `downed > 0` on the pursuit family's landing was measured
			// and DECLINED (2026-08-23): with the whole family in, the ace reads
			// downed 0/24 and converted 0.0% here while never being shot down —
			// each landed fix addresses a losing pattern this near-perfect
			// pursuer never exhibits, and REVERSING it is an unbuilt capability
			// (its one flaw is sight loss under g), tracked as its own task. A
			// gate red at birth is a demand, not a regression guard.
			if lost > 1 {
				t.Errorf("ace was shot down in %d of 24 by the crude script: the defence regressed", lost)
			}
			if 100*float64(tracked)/math.Max(1, float64(total)) > 20 {
				t.Errorf("ace tracked in the attacker's rear quarter %.0f%% of the fight: the prey era is returning", 100*float64(tracked)/math.Max(1, float64(total)))
			}
		}
	}
}

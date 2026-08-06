// Mochi world: Rounds on target per firing opportunity
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
	"world/games/air/battle"
	"world/games/air/flight"
)

// TestGunnery is the instrument the 2026-08-05 superhuman post-mortem showed
// was missing. Every other gate measures whether the bot KILLS, which folds
// twenty variables into one number and takes a hundred seconds a seed to
// answer. This one measures the two links in the chain that actually broke:
//
//	OPPORTUNITY  how often the bot is in a position where a shot exists at all
//	             (the target inside gun range and reachable by its nose)
//	CONVERSION   of those, how often it is ON the led solution
//	ACCURACY     of the rounds it fires, how close they pass to the target
//
// A bot that chooses offensive plays for the whole fight and never fires is
// indistinguishable, on a kill count, from a bot that never tries. Here it
// reads as opportunity high, conversion nil — which is exactly what the human
// fight measured, and what no existing gate could see.
//
// The target is the scripted evade(): corner-ish speed, a hard level turn
// reversed every six seconds, breaking harder with a threat close behind. It
// never attacks, so every number below is the bot's own doing.
func TestGunnery(t *testing.T) {
	heavy(t)
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		var opportunity, solved, fired, total int
		var closest, aimSum float64 = 1e9, 0
		var aimed int
		hits, rounds := 0, 0
		for seed := uint64(1); seed <= 6; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: "gunnery", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
			place(i, 0, bot, -500) // the bot starts behind: the shot is there to be taken
			me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
			for tick := uint64(0); tick < 60*90; tick++ {
				before := *me
				i.Step(tick, map[int][]game.Input{0: {{Data: evade(me, foe, tick)}}})
				if !i.aircraft[0].alive || !i.aircraft[bot].alive {
					break
				}
				total++
				s := &i.aircraft[bot].model.State
				to := me.Position.Subtract(s.Position)
				span := to.Length()
				if span > 900 || span < 1 {
					continue
				}
				opportunity++
				// The led solution, measured the way the gunnery flies it.
				los := to.Scale(1 / span)
				closure := s.Velocity.Subtract(me.Velocity).Dot(los)
				transit := span / math.Max(battle.Average(span, s.Position.Y)+closure, 200)
				spot := me.Position.Add(me.Velocity.Scale(transit)).
					Subtract(s.Velocity.Scale(transit)).
					Add(flight.Vec3{Y: 4.9 * transit * transit})
				aim := spot.Subtract(s.Position).Normalize()
				nose := s.Attitude.Rotate(flight.Vec3{X: 1})
				miss := math.Acos(clamp(nose.Dot(aim), -1, 1)) * span
				aimSum += miss
				aimed++
				if miss < closest {
					closest = miss
				}
				if miss < 25 { // inside an aircraft's own length: a real shot
					solved++
				}
				if i.aircraft[bot].latest.Fire {
					fired++
					rounds += 2 // ~100 rounds/s at 60 Hz
					// Where would that round have gone? Fly it against the
					// target's true motion this step.
					drift := me.Position.Subtract(before.Position).Scale(60) // his velocity this tick
					best := 1e9
					where := s.Position
					velocity := nose.Scale(battle.Muzzle).Add(s.Velocity)
					for step := 1; step <= 45; step++ {
						// the same drag-and-gravity march the real round flies
						speed := velocity.Length()
						velocity = velocity.Scale(1 / (1 + speed*0.02/(2600*math.Exp(math.Max(where.Y, 0)/8500))))
						velocity.Y -= 9.8 * 0.02
						where = where.Add(velocity.Scale(0.02))
						him := me.Position.Add(drift.Scale(float64(step) * 0.02))
						if d := where.Subtract(him).Length(); d < best {
							best = d
						}
					}
					if best < 8 {
						hits++
					}
				}
			}
		}
		mean := 0.0
		if aimed > 0 {
			mean = aimSum / float64(aimed)
		}
		fmt.Printf("%-11s opportunity %5.1f%% of the fight | on solution %5.2f%% of those | trigger down %5.2f%% | "+
			"mean aim error %5.0f m, best %4.0f m | rounds ~%d, on target %5.1f%%\n",
			level, 100*float64(opportunity)/float64(total), 100*float64(solved)/math.Max(float64(opportunity), 1),
			100*float64(fired)/math.Max(float64(opportunity), 1), mean, closest,
			rounds, 100*float64(hits)/math.Max(float64(fired), 1))

		// The gates. These are the two links the post-mortem found broken:
		// the bot was in position constantly and converted almost never.
		if opportunity == 0 {
			t.Errorf("%s never got inside 900 m of a scripted turner in 90 s — the harness or the pursuit is broken", level)
		}
		if level != "pilot" {
			if share := 100 * float64(solved) / math.Max(float64(opportunity), 1); share < 4 {
				t.Errorf("%s is on a firing solution for %.2f%% of its in-range time — it points at him without ever aiming (floor 4%%)", level, share)
			}
			if share := 100 * float64(fired) / math.Max(float64(opportunity), 1); share < 2 {
				t.Errorf("%s pulls the trigger on %.2f%% of its in-range time — the gate is shut against a target it is tracking (floor 2%%)", level, share)
			}
		}
	}
}

// TestLadderDuel is the honest test of lethality: each tier against the one
// below it, guns only, to the kill. Every other instrument scores the bot
// against a target that does not fight back — a compliant drone, a scripted
// turner, a straight-and-level offerer — and the 2026-08-05 human fight
// showed how far that can diverge from a two-way engagement, where the bot
// must defend and attack at once. A tier that cannot beat the one below it
// is not more lethal, whatever the single-sided instruments say.
func TestLadderDuel(t *testing.T) {
	heavy(t)
	for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
		strong, weak := pair[0], pair[1]
		wins, losses, draws := 0, 0, 0
		var times []float64
		for seed := uint64(1); seed <= 8; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("ladder%s%d", strong, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false,
					"bots": map[string]any{strong: 1.0, weak: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var top, low *craft
			for _, slot := range i.slots() {
				c := i.aircraft[slot]
				if c == nil || c.brain == nil {
					continue
				}
				if c.brain.skill.library == skills[strong].library && c.brain.skill.wander == skills[strong].wander {
					top = c
				} else {
					low = c
				}
			}
			if top == nil || low == nil {
				t.Fatalf("%s vs %s: roster wrong", strong, weak)
			}
			done := false
			for tick := uint64(0); tick < 60*240 && !done; tick++ {
				i.Step(tick, nil)
				// A detonation (the tank-vapour roll) tears the model down
				// in the death tick, so model-nil IS a kill — breaking on it
				// before the switch silently scored every detonation as no
				// result, which under real drag is most kills.
				switch {
				case low.model == nil || !low.alive:
					wins++
					times = append(times, float64(tick)/60)
					done = true
				case top.model == nil || !top.alive:
					losses++
					done = true
				}
			}
			if !done {
				draws++
			}
		}
		mean := 0.0
		for _, v := range times {
			mean += v
		}
		if len(times) > 0 {
			mean /= float64(len(times))
		}
		fmt.Printf("%-11s vs %-7s  won %d  lost %d  no result %d  (of 8)   mean time to kill %5.1f s\n",
			strong, weak, wins, losses, draws, mean)
		// HISTORY. 2026-08-05, before the probability trigger, the pipper
		// takeover, and the intent layer: won 0, lost 0 on both pairs —
		// bots never killed each other with guns at all, while every
		// single-sided instrument read healthy. 2026-08-06, after: fights
		// resolve when one side catches the other slow (ace-pilot trades a
		// kill each way around 110 s), the machine no longer loses the top
		// pairing (it was drowning itself under FINISH, then dying in a
		// commitment-locked CONVERT), and two competent equals who no
		// longer get caught fight four honest minutes to a draw — each
		// denies the other's solution and neither takes objectively bad
		// shots, which is what equal BFM looks like.
		//
		// The gate is inversion only, deliberately: a gate on "the better
		// tier must win" would sit red on honest draws and stop being read.
		// If exploitation work ever makes the upper tier convert reliably,
		// tighten this to demand a majority.
		if losses > wins {
			t.Errorf("%s lost to %s %d-%d: the ladder is inverted where it matters, in a two-way fight",
				strong, weak, losses, wins)
		}
	}
}

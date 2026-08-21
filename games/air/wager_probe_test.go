// Mochi world: what the bot BELIEVES - instruments for the inputs every other
// gate sees only the consequences of. TestYouModel is permanent;
// TestFloaterWager (#215 item 8) is temporary, delete it once the conversion
// work is finished.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestYouModel scores the bot's opponent models against the scripted humans at
// five horizons, with a dead-straight-line baseline: a model that cannot beat
// "he keeps doing what he is doing" is not earning its place.
func TestYouModel(t *testing.T) {
	heavy(t)
	for _, script := range []string{"flounder", "jinker"} {
		// evolve() is what the rehearsal flies its phantom on, predict() is what the
		// pipper aims the gun at. The gun's transit at 600 m is about six tenths and
		// the rollout judges four seconds out.
		horizons := []float64{0.25, 0.5, 1.0, 2.0, 4.0}
		errors := map[string][]float64{}
		// One queue PER (model, horizon): a single mixed queue drains in push order,
		// scoring short-horizon guesses against the wrong tick - which reads as error
		// falling with horizon.
		type guess struct {
			due       uint64
			predicted flight.Vec3
		}
		for seed := uint64(1); seed <= 3; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("youmodel%s%d", script, seed), Game: "air", Mode: "furball",
				Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false, "bots": map[string]any{"superhuman": 1.0}}})
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
			place(i, 0, bot, 1800)
			me := &i.aircraft[0].model.State
			pending := map[string][]guess{}
			for tick := uint64(0); tick < 60*120; tick++ {
				data := flounder(me, &i.aircraft[bot].model.State, tick)
				if script == "jinker" {
					data = jinker(me, &i.aircraft[bot].model.State, tick)
				}
				i.Step(tick, map[int][]game.Input{0: {{Sequence: 1, Data: data}}})
				b := i.aircraft[bot]
				if b.model == nil || b.brain == nil || b.brain.prey == nil || i.aircraft[0].model == nil || !i.aircraft[0].alive {
					continue
				}
				for key, queue := range pending {
					for len(queue) > 0 && queue[0].due <= tick {
						errors[key] = append(errors[key], queue[0].predicted.Subtract(me.Position).Length())
						queue = queue[1:]
					}
					pending[key] = queue
				}
				if tick%30 == 0 {
					age := float64(tick-b.brain.prey.when) / 60
					for _, h := range horizons {
						due := tick + uint64(h*60)
						hisP, _ := evolve(b.brain.prey, age+h)
						push := func(model string, at flight.Vec3) {
							key := fmt.Sprintf("%s %.2fs", model, h)
							pending[key] = append(pending[key], guess{due: due, predicted: at})
						}
						push("evolve ", hisP)
						push("predict", predict(b.brain.prey, age+h, b.brain.skill.library >= 3))
						// A straight-line baseline: what "he keeps doing
						// exactly what he is doing" scores. A model that
						// cannot beat this is not earning its place.
						push("straight", b.brain.prey.position.Add(b.brain.prey.velocity.Scale(age+h)))
					}
				}
			}
		}
		keys := make([]string, 0, len(errors))
		for k := range errors {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := errors[k]
			sort.Float64s(v)
			pct := func(p float64) float64 {
				if len(v) == 0 {
					return 0
				}
				return v[int(p*float64(len(v)-1))]
			}
			fmt.Printf("%-9s %-16s error p50 %6.1f m | p90 %6.1f m | samples %d\n", script, k, pct(0.5), pct(0.9), len(v))
		}
	}
}

func TestFloaterWager(t *testing.T) {
	heavy(t)
	for _, level := range []string{"ace", "superhuman"} {
		var misses, wobbles, drifts, chances, speeds, tracked []float64
		cleared, sampled, shooting, tracking, near := 0, 0, 0, 0, 0
		for seed := uint64(1); seed <= 3; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("wager%s%d", level, seed), Game: "air", Mode: "furball",
				Capacity: 8, Seed: seed,
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
			place(i, 0, bot, 1800)
			me := &i.aircraft[0].model.State
			for tick := uint64(0); tick < 60*180; tick++ {
				i.Step(tick, map[int][]game.Input{0: {{Sequence: 1, Data: flounder(me, &i.aircraft[bot].model.State, tick)}}})
				b := i.aircraft[bot]
				if b.model == nil || b.brain == nil || b.brain.prey == nil || i.aircraft[0].model == nil || !i.aircraft[0].alive {
					continue
				}
				brain := b.brain
				span := b.model.State.Position.Subtract(i.aircraft[0].model.State.Position).Length()
				if span > 900 {
					continue
				}
				_, miss, span2, horizon := brain.pipper(b.model, tick)
				scatter := 0.004 * span2
				drift := brain.prey.wobble * horizon * horizon * horizon / 6
				waver := brain.skill.wander * span2
				sigma := math.Sqrt(scatter*scatter + drift*drift + waver*waver)
				root := sigma * math.Sqrt2
				chance := 0.5 * (math.Erf((5-miss)/root) - math.Erf((-5-miss)/root))
				price := brain.skill.trigger * (1.6 - 0.6*clamp(float64(brain.magazine)/float64(rounds), 0, 1))
				sampled++
				if chance > price {
					cleared++
				}
				if brain.shoot {
					shooting++
				}
				if brain.tracking {
					tracking++
					tracked = append(tracked, miss)
				}
				if miss < 100 {
					near++
				}
				misses = append(misses, miss)
				wobbles = append(wobbles, brain.prey.wobble)
				drifts = append(drifts, drift)
				chances = append(chances, chance)
				speeds = append(speeds, b.model.State.Velocity.Length())
			}
		}
		pct := func(v []float64, p float64) float64 {
			if len(v) == 0 {
				return 0
			}
			s := append([]float64(nil), v...)
			sort.Float64s(s)
			return s[int(p*float64(len(s)-1))]
		}
		_ = flight.Vec3{}
		fmt.Printf("%-11s %5d in-range ticks | clears %4.1f%% | shoot %4.1f%% | pipper owns stick %4.1f%% (miss p50 %4.0f m) | miss<100m %4.1f%% | miss p10/50 %5.0f/%5.0f m | wobble p50 %3.0f | drift p50 %4.1f m | speed p50 %4.0f m/s\n",
			level, sampled, 100*float64(cleared)/math.Max(float64(sampled), 1),
			100*float64(shooting)/math.Max(float64(sampled), 1),
			100*float64(tracking)/math.Max(float64(sampled), 1), pct(tracked, 0.5),
			100*float64(near)/math.Max(float64(sampled), 1),
			pct(misses, 0.1), pct(misses, 0.5),
			pct(wobbles, 0.5), pct(drifts, 0.5), pct(speeds, 0.5))
	}
}

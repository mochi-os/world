// Mochi world: measurement probes for the human-pressure harness — what a bot
// actually does, second by second, with a scripted attacker behind it; whether
// the position can be won at all; and how good the arbiter's best line looks
// when it is saddled. Set AIR_PRESSURE=1 to run any of them.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// probe gates the measurement harnesses: they print, they do not assert, and
// the doctrine battery has no use for their minutes.
func probe(t *testing.T) {
	t.Helper()
	if os.Getenv("AIR_PRESSURE") == "" {
		t.Skip("measurement harness: set AIR_PRESSURE=1 to run")
	}
	heavy(t)
}

// TestPressureProbe traces ONE seed of the human-pressure harness: what the
// ace chose at every re-plan, how the candidates ranked, and the geometry
// each second. AIR_PROBE_SEED selects the seed, AIR_PROBE_START the
// attacker's starting range in metres, AIR_PROBE_LENGTH the seconds flown.
//
// It is what found the harness's 600 m start to be an execution (2026-08-18):
// 17 of 24 seeds ended inside 2.3 s, before the bandit's second decision.
func TestPressureProbe(t *testing.T) {
	probe(t)
	seed := uint64(2)
	if s := os.Getenv("AIR_PROBE_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			seed = uint64(n)
		}
	}
	g := New()
	made, err := g.Create(game.Session{Identifier: "probe", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
	start := -600.0
	if v := os.Getenv("AIR_PROBE_START"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			start = -float64(n)
		}
	}
	place(i, bot, 0, start)
	me, foe := &i.aircraft[0].model.State, &i.aircraft[bot].model.State
	hunter := &chaser{}
	b := i.aircraft[bot].brain
	fmt.Printf("seed %d\n", seed)
	length := uint64(60)
	if v := os.Getenv("AIR_PROBE_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			length = uint64(n)
		}
	}
	for tick := uint64(0); tick < 60*length; tick++ {
		data := hunter.fly(me, foe, tick)
		i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
		if !i.aircraft[0].alive || !i.aircraft[bot].alive || i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
			fmt.Printf("t=%.1f END attacker alive=%v bandit alive=%v bandit kills=%d\n", float64(tick)/60, i.aircraft[0].alive, i.aircraft[bot].alive, i.aircraft[bot].kills)
			break
		}
		r, off, mine, his := geometry(me, foe)
		toward := foe.Position.Subtract(me.Position)
		nose := me.Attitude.Rotate(flight.Vec3{X: 1})
		offNose := math.Acos(clamp(toward.Scale(1/r).Dot(nose), -1, 1)) * 57.3
		if b.picked == tick && b.prey != nil {
			scores := map[string]float64{}
			sim := flight.New(i.aircraft[bot].model.Airframe, i.aircraft[bot].model.Environment, i.aircraft[bot].model.World)
			i.choose(bot, i.aircraft[bot], b, sim, b.prey, tick, b.distance, scores)
			type e struct {
				n string
				s float64
			}
			list := []e{}
			for n, s := range scores {
				list = append(list, e{n, s})
			}
			sort.Slice(list, func(a, c int) bool { return list[a].s > list[c].s })
			fmt.Printf("  t=%5.1f PLAN %-8s intent=%-8s |", float64(tick)/60, b.play, b.intent)
			for k, x := range list {
				if k >= 6 {
					break
				}
				fmt.Printf(" %s %.2f", x.n, x.s)
			}
			fmt.Println()
		}
		if tick%60 == 0 {
			bnose := foe.Attitude.Rotate(flight.Vec3{X: 1})
			myOff := math.Acos(clamp(toward.Scale(-1/r).Dot(bnose), -1, 1)) * 57.3
			atkThrust := 1 - (me.Damage.Engine[0]+me.Damage.Engine[1])/2
			fmt.Printf("t=%5.1f %-7s %-7s g=%.1f spd=%3.0fkt alt=%4.0f aim=%3.0f fired=%3d pace=%3.0fkt fpa=%+3.0f aimLOS=%3.0f noseAim=%3.0f thr=%.1f ring=%s | r=%4.0f atkAlt=%4.0f offTail=%3.0f offNose=%3.0f atkG=%.1f atkSpd=%3.0fkt atkThr=%.2f egap=%+5.0f thrust=%.2f\n",
				float64(tick)/60, b.mode, b.intent, b.g, foe.Velocity.Length()*1.944, foe.Position.Y, myOff, i.aircraft[bot].spent, corner(i.aircraft[bot].model)*1.944, math.Asin(clamp(foe.Velocity.Y/math.Max(foe.Velocity.Length(), 1), -1, 1))*57.3, math.Acos(clamp(toward.Scale(-1/r).Dot(b.aim), -1, 1))*57.3, math.Acos(clamp(bnose.Dot(b.aim), -1, 1))*57.3, b.throttle, ringText(b.ring), r, me.Position.Y, off, offNose, me.Fcs.Demand, me.Velocity.Length()*1.944, atkThrust, his-mine,
				1-(foe.Damage.Engine[0]+foe.Damage.Engine[1])/2)
		}
	}
	fmt.Printf("chaser: lost tally %d, blew %d ticks, closest %.0f m/s, widest %.0f deg\n", hunter.lost, hunter.blew, hunter.closest, hunter.widest)
}

func ringText(o orbit) string {
	if !o.valid {
		return "-"
	}
	return fmt.Sprintf("R%.0f/nY%+.1f/w%.2f", o.radius, o.normal.Y, o.omega)
}

// TestPressureOracle asks whether the position the human-pressure harness
// starts from can be won AT ALL: the bandit's seat is flown by the same crude
// script as the attacker (pursue), and the run reports how often each side
// held the other's rear quarter. If a novice script converts where the ace
// does not, the doctrine is worse than the crudest thing that could sit in
// its seat.
func TestPressureOracle(t *testing.T) {
	probe(t)
	converted, tracked, total, killed, died := 0, 0, 0, 0, 0
	for seed := uint64(1); seed <= 24; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: "oracle", Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		for slot := 0; slot < 2; slot++ {
			if _, err := i.Join(game.Player{Identity: "", Name: "p", Slot: slot}); err != nil {
				t.Fatal(err)
			}
		}
		place(i, 1, 0, -1200) // attacker (0) behind the "bandit" (1)
		me, foe := &i.aircraft[0].model.State, &i.aircraft[1].model.State
		hunter, defender := &chaser{}, &chaser{}
		for tick := uint64(0); tick < 120*60; tick++ {
			i.Step(tick, map[int][]game.Input{0: {{Data: hunter.fly(me, foe, tick)}}, 1: {{Data: defender.fly(foe, me, tick)}}})
			if !i.aircraft[0].alive || !i.aircraft[1].alive || i.aircraft[0].model == nil || i.aircraft[1].model == nil {
				if !i.aircraft[0].alive {
					killed++
				}
				if !i.aircraft[1].alive {
					died++
				}
				break
			}
			total++
			if r, off, _, _ := geometry(me, foe); r < 900 && off < 45 {
				tracked++
			}
			if r, off, _, _ := geometry(foe, me); r < 900 && off < 45 {
				converted++
			}
		}
	}
	fmt.Printf("oracle (pursue in the bandit's seat, 24 seeds, 120 s, from 1,200 m): attacker down %d | bandit down %d | bandit tracked %.1f%% | bandit CONVERTED %.1f%%\n",
		killed, died, 100*float64(tracked)/math.Max(1, float64(total)), 100*float64(converted)/math.Max(1, float64(total)))
}

// TestPromiseProbe: the distribution of the winning rehearsal's mean offence
// (brain.promise) across ace-v-pilot gun duels, so the FINISH-on-opportunity
// threshold is chosen from data rather than taste. Measured 2026-08-18: 410 of
// ~530 re-plans under 0.05, a tail of ~90 from 0.15 up, and 22 at 0.95+ — a
// saddle held for the whole rehearsal. Also what showed the ace holding a
// 0.9-0.99 line at 600-760 m for seventy seconds without firing (seed 1).
func TestPromiseProbe(t *testing.T) {
	probe(t)
	buckets := map[int]int{}
	for seed := uint64(1); seed <= 4; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("promise%d", seed), Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "weapons": "guns", "bots": map[string]any{"ace": 1.0, "pilot": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		var top *craft
		for _, slot := range i.slots() {
			c := i.aircraft[slot]
			if c != nil && c.brain != nil && c.brain.skill.library == skills["ace"].library && c.brain.skill.wander == skills["ace"].wander {
				top = c
			}
		}
		last := uint64(0)
		for tick := uint64(0); tick < 60*240; tick++ {
			i.Step(tick, nil)
			if top.model == nil || !top.alive {
				break
			}
			if top.brain.picked != last && top.brain.prey != nil {
				last = top.brain.picked
				buckets[int(math.Floor(top.brain.promise*20))]++
				if top.brain.promise > 0.15 {
					fmt.Printf("seed %d t=%.1f promise %.2f play %s intent %s distance %.0f fired %d shoot %v tracking %v spd %.0fkt\n", seed, float64(tick)/60, top.brain.promise, top.brain.play, top.brain.intent, top.brain.distance, top.spent, top.brain.shoot, top.brain.tracking, top.model.State.Velocity.Length()*1.944)
				}
			}
		}
	}
	keys := []int{}
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		fmt.Printf("  promise %+.2f..: %d\n", float64(k)/20, buckets[k])
	}
}

// TestGunLadderWide is TestLadderDuel's guns arm over 48 seeds instead of 16:
// with nine to twelve no-results in sixteen, that arm turns on four to seven
// decisive fights and moves by two on seed luck alone, which is not enough to
// judge a doctrine change by. This is the wider sample for that judgement.
func TestGunLadderWide(t *testing.T) {
	probe(t)
	for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
		strong, weak := pair[0], pair[1]
		wins, losses, draws := 0, 0, 0
		for seed := uint64(1); seed <= 48; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("wide%s%d", strong, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false, "weapons": "guns", "bots": map[string]any{strong: 1.0, weak: 1.0}}})
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
			done := false
			for tick := uint64(0); tick < 60*240 && !done; tick++ {
				i.Step(tick, nil)
				switch {
				case low.model == nil || !low.alive:
					wins++
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
		fmt.Printf("wide guns %-11s vs %-7s  won %d  lost %d  no result %d  (of 48)\n", strong, weak, wins, losses, draws)
	}
}

// TestFlounderWide is TestFlounder's ace row over 36 seeds instead of 12, for
// the same reason as the wide gun ladder: a kill count near five in twelve
// moves by three on seed luck.
func TestFlounderWide(t *testing.T) {
	probe(t)
	kills, landed := 0, 0
	for seed := uint64(1); seed <= 36; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("flounderwide%d", seed),
			Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
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
		place(i, 0, bot, 1800)
		me := &i.aircraft[0].model.State
		for tick := uint64(0); tick < 60*180; tick++ {
			i.events = i.events[:0]
			i.Step(tick, map[int][]game.Input{0: {{Sequence: 1, Data: flounder(me, &i.aircraft[bot].model.State, tick)}}})
			for _, e := range i.events {
				if e["kind"] == "hit" && e["slot"] != bot {
					count, _ := e["count"].(int)
					landed += count
				}
			}
			human := i.aircraft[0]
			if human.model == nil || !human.alive {
				if human.condition.Damager >= 0 {
					kills++
				}
				break
			}
			if b := i.aircraft[bot]; b.model == nil || !b.alive {
				break
			}
		}
	}
	fmt.Printf("wide flounder: ace killed it %d/36 | rounds landed %d\n", kills, landed)
}

// TestDispenseProbe counts what each tier spends from its two magazines and
// what the rounds against it did, on the missiles ladder and the BVR rung that
// moved when the magazines landed (#43).
func TestDispenseProbe(t *testing.T) {
	probe(t)
	type book struct {
		flares, chaff, left, kept, decoyed, seduced, hit int
	}
	for _, arm := range []string{"missiles", "bvr"} {
		for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
			strong, weak := pair[0], pair[1]
			tally := map[string]*book{strong: {}, weak: {}}
			for seed := uint64(1); seed <= 6; seed++ {
				var made game.Instance
				var err error
				if arm == "bvr" {
					made, err = (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("dispense%s%s%d", arm, strong, seed), Game: "air", Mode: "joust", Seed: seed,
						Parameters: map[string]any{"missiles": true, "start": "bvr", "bots": map[string]any{strong: 1.0, weak: 1.0}}})
				} else {
					made, err = New().Create(game.Session{Identifier: fmt.Sprintf("dispense%s%s%d", arm, strong, seed), Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
						Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{strong: 1.0, weak: 1.0}}})
				}
				if err != nil {
					t.Fatal(err)
				}
				i := made.(*instance)
				who := map[int]string{}
				for _, s := range i.slots() {
					a := i.aircraft[s]
					if a == nil || a.brain == nil {
						continue
					}
					name := weak
					if a.brain.skill.library == skills[strong].library && a.brain.skill.wander == skills[strong].wander {
						name = strong
					}
					who[s] = name
				}
				length := uint64(60 * 240)
				if arm == "bvr" {
					length = 60 * 600
				}
				for tick := uint64(0); tick < length; tick++ {
					i.events = i.events[:0]
					i.Step(tick, nil)
					for _, e := range i.events {
						slot, _ := e["slot"].(int)
						name, ok := who[slot]
						if !ok {
							continue
						}
						switch e["kind"] {
						case "flare":
							tally[name].flares++
						case "chaff":
							tally[name].chaff++
						case "decoy":
							tally[name].decoyed++
						case "seduce":
							tally[name].seduced++
						}
					}
					dead := false
					for s := range who {
						if a := i.aircraft[s]; a == nil || a.model == nil || !a.alive {
							dead = true
						}
					}
					if dead {
						break
					}
				}
				for s, name := range who {
					if a := i.aircraft[s]; a != nil {
						tally[name].left += a.flares
						tally[name].kept += a.chaff
					}
				}
				i.Close()
			}
			for _, name := range []string{strong, weak} {
				b := tally[name]
				fmt.Printf("%-8s %-11s flares %3d (%.1f/fight, %2d left/fight) | chaff %3d (%.1f/fight, %2d left/fight) | heaters decoyed %d | radar seekers seduced %d\n",
					arm, name, b.flares, float64(b.flares)/6, b.left/6, b.chaff, float64(b.chaff)/6, b.kept/6, b.decoyed, b.seduced)
			}
		}
	}
}

// TestSeamProbe: does an ace-v-ace BVR joust merge, across seeds and loads —
// the seam test's seed 11 stopped merging on the full-internal load.
func TestSeamProbe(t *testing.T) {
	probe(t)
	for _, pounds := range []float64{6000, 10800} {
		merged, resolved, never := 0, 0, 0
		for seed := uint64(1); seed <= 12; seed++ {
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("seamprobe%d", seed), Game: "air", Mode: "joust", Seed: seed,
				Parameters: map[string]any{"missiles": true, "start": "bvr", "fuel": pounds, "bots": map[string]any{"ace": 2.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			bots := []*craft{}
			for _, s := range i.slots() {
				if a := i.aircraft[s]; a != nil && a.bot {
					bots = append(bots, a)
				}
			}
			closest, done := math.MaxFloat64, false
			for tick := uint64(0); tick < 60*480; tick++ {
				i.Step(tick, nil)
				if bots[0].model == nil || bots[1].model == nil || !bots[0].alive || !bots[1].alive {
					done = true
					break
				}
				if span := i.span(bots[0].model, bots[1].model); span < closest {
					closest = span
				}
			}
			switch {
			case done:
				resolved++
			case closest <= 2000:
				merged++
			default:
				never++
				fmt.Printf("  %.0f lb seed %d never closed: %.0f m\n", pounds, seed, closest)
			}
			i.Close()
		}
		fmt.Printf("seam probe %5.0f lb: resolved %d, merged %d, never closed %d (of 12)\n", pounds, resolved, merged, never)
	}
}

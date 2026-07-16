// Mochi world: Air tactics battery (#143)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The measured-tactics battery: canned combat situations, each scored on its
// own metric, flown across deterministic seeds in parallel. Env-gated like
// the vspeeds harness — it never runs in CI. Usage:
//
//	AIR_BATTERY=1 go test ./games/air -run TestBattery -v
//	AIR_SEEDS=24                       # seeds per scenario (default 8)
//	AIR_TACTICS='{"drag.pace":0.72}'   # dotted doctrine overrides, both sides
//
// Every fighting bot on BOTH sides flies the amended doctrine — the battery
// measures the doctrine against the skill ladder, not against itself. The
// `BATTERY <scenario> <metric>=<mean> ...` lines are the machine interface
// world/tools/tactics.py parses; keep them stable.

package air

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// amend applies one dotted-name override to the doctrine. Explicit rather
// than reflected: the fields are unexported and a typo'd override must fail
// loudly, not silently measure the baseline.
func amend(t *tactics, name string, value float64) bool {
	switch name {
	case "drag.pace":
		t.drag.pace = value
	case "drag.span":
		t.drag.span = value
	case "bag.reach":
		t.bag.reach = value
	case "bag.bend":
		t.bag.bend = value
	case "spiral.nose":
		t.spiral.nose = value
	case "spiral.span":
		t.spiral.span = value
	case "spiral.floor":
		t.spiral.floor = value
	case "spiral.saddle":
		t.spiral.saddle = int(value)
	case "spiral.hold":
		t.spiral.hold = uint64(value)
	case "jink.span":
		t.jink.span = value
	case "jink.base":
		t.jink.base = uint64(value)
	case "jink.spread":
		t.jink.spread = uint64(value)
	case "high.closure":
		t.high.closure = value
	case "high.span":
		t.high.span = value
	case "high.tail":
		t.high.tail = value
	case "high.hold":
		t.high.hold = uint64(value)
	case "low.near":
		t.low.near = value
	case "low.far":
		t.low.far = value
	case "low.tail":
		t.low.tail = value
	case "low.rise":
		t.low.rise = value
	case "plan.deficit":
		t.plan.deficit = value
	case "lead.closure":
		t.lead.closure = value
	case "lead.floor":
		t.lead.floor = value
	case "lead.angle":
		t.lead.angle = value
	case "missile.tail":
		t.missile.tail = value
	case "missile.span":
		t.missile.span = value
	case "missile.margin":
		t.missile.margin = value
	case "missile.step":
		t.missile.step = value
	case "missile.base":
		t.missile.base = value
	case "missile.slope":
		t.missile.slope = value
	case "missile.floor":
		t.missile.floor = value
	case "missile.gain":
		t.missile.gain = value
	case "sandwich.span":
		t.sandwich.span = value
	case "sandwich.nose":
		t.sandwich.nose = value
	case "sandwich.weight":
		t.sandwich.weight = value
	case "support.span":
		t.support.span = value
	case "support.share":
		t.support.share = value
	case "support.engaged":
		t.support.engaged = value
	case "support.behind":
		t.support.behind = value
	case "support.above":
		t.support.above = value
	case "support.near":
		t.support.near = value
	case "support.out":
		t.support.out = value
	case "support.rise":
		t.support.rise = value
	case "support.limit":
		t.support.limit = value
	case "form.abeam":
		t.form.abeam = value
	case "form.blend":
		t.form.blend = value
	case "form.burner":
		t.form.burner = value
	case "press.span":
		t.press.span = value
	case "press.hold":
		t.press.hold = value
	case "press.loose":
		t.press.loose = value
	case "press.closure":
		t.press.closure = value
	case "press.gap":
		t.press.gap = value
	case "crowd.weight":
		t.crowd.weight = value
	case "sandwich.reach":
		t.sandwich.reach = value
	case "rejoin.span":
		t.rejoin.span = value
	case "rejoin.fight":
		t.rejoin.fight = value
	case "zoom.edge":
		t.zoom.edge = value
	case "zoom.roof":
		t.zoom.roof = value
	case "zoom.hold":
		t.zoom.hold = uint64(value)
	case "rope.edge":
		t.rope.edge = value
	case "rope.near":
		t.rope.near = value
	case "rope.far":
		t.rope.far = value
	case "rope.nose":
		t.rope.nose = value
	case "rope.hold":
		t.rope.hold = uint64(value)
	case "bracket.span":
		t.bracket.span = value
	case "bracket.angle":
		t.bracket.angle = value
	case "wounded.weight":
		t.wounded.weight = value
	default:
		return false
	}
	return true
}

// battery is one scenario: build an instance, fly it, report metrics.
type battery struct {
	name string
	fly  func(seed uint64, doc tactics) map[string]float64
}

// arena builds a teams instance with the given per-side bots and hands every
// fighting brain the amended doctrine.
func arena(name string, seed uint64, doc tactics, missiles bool, red, blue map[string]any) *instance {
	g := New()
	made, err := g.Create(game.Session{Identifier: name, Game: "air", Mode: "teams", Capacity: 16, Seed: seed,
		Parameters: map[string]any{"missiles": missiles, "bots": map[string]any{"red": red, "blue": blue}}})
	if err != nil {
		panic(err)
	}
	i := made.(*instance)
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a.brain != nil {
			a.brain.tactics = doc
		}
	}
	return i
}

// casualties sums one side's deaths.
func casualties(i *instance, team string) (deaths int) {
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a.team == team {
			deaths += a.deaths
		}
	}
	return
}

// batteries is the scenario table. Each metric is prefixed for the driver:
// metrics named down_* want to fall, up_* want to rise — the direction is in
// the name so tools/tactics.py never hardcodes a second table.
var batteries = []battery{
	{"section", func(seed uint64, doc tactics) map[string]float64 {
		// The #138 shape: a mixed-skill 2v4 with missiles. Teamwork's yield.
		i := arena("section", seed, doc, true,
			map[string]any{"veteran": 2.0}, map[string]any{"pilot": 4.0})
		for tick := uint64(0); tick < 60*300; tick++ {
			i.Step(tick, nil)
		}
		deaths := casualties(i, "red")
		return map[string]float64{"down_deaths": float64(deaths), "up_net": float64(i.score["red"] - deaths)}
	}},
	{"skirmish", func(seed uint64, doc tactics) map[string]float64 {
		// The same 2v4 guns-only: the missile envelope out of the picture.
		i := arena("skirmish", seed, doc, false,
			map[string]any{"veteran": 2.0}, map[string]any{"pilot": 4.0})
		for tick := uint64(0); tick < 60*300; tick++ {
			i.Step(tick, nil)
		}
		deaths := casualties(i, "red")
		return map[string]float64{"down_deaths": float64(deaths), "up_net": float64(i.score["red"] - deaths)}
	}},
	{"defense", func(seed uint64, doc tactics) map[string]float64 {
		// Defensive entry: a lone pilot with two missile-armed veterans
		// already saddled 1.4 km behind. Survival time once saddled is the
		// metric the drag, spiral, jink, evade, and flare numbers serve.
		// Missiles ON deliberately: guns-only kills between maneuvering bots
		// essentially never land (see merge/skirmish), so a guns-only window
		// ceilings whoever the defender is.
		i := arena("defense", seed, doc, true,
			map[string]any{"pilot": 1.0}, map[string]any{"veteran": 2.0})
		var lone *craft
		hunters := []*craft{}
		for _, slot := range i.slots() {
			if a := i.aircraft[slot]; a.team == "red" {
				lone = a
			} else {
				hunters = append(hunters, a)
			}
		}
		aloft(lone, flight.Vec3{X: 0, Y: 4000, Z: 0}, flight.Vec3{X: 220})
		for n, h := range hunters {
			aloft(h, flight.Vec3{X: -1400, Y: 4000, Z: float64(240*n - 120)}, flight.Vec3{X: 260})
		}
		limit := uint64(60 * 300) // kills are rare in this sim: a short window ceilings the metric
		for tick := uint64(0); tick < limit; tick++ {
			i.Step(tick, nil)
			if lone.deaths > 0 {
				return map[string]float64{"up_survival": float64(tick) / 60}
			}
		}
		return map[string]float64{"up_survival": float64(limit) / 60}
	}},
	{"merge", func(seed uint64, doc tactics) map[string]float64 {
		// Same-skill 2v2 merge, guns only: does the fight RESOLVE? The lead
		// turn, circle plan, and stalemate displacement own this one.
		i := arena("merge", seed, doc, false,
			map[string]any{"ace": 2.0}, map[string]any{"ace": 2.0})
		limit := uint64(60 * 300)
		for tick := uint64(0); tick < limit; tick++ {
			i.Step(tick, nil)
			if casualties(i, "red")+casualties(i, "blue") > 0 {
				return map[string]float64{"down_first": float64(tick) / 60, "down_stalemate": 0}
			}
		}
		return map[string]float64{"down_first": float64(limit) / 60, "down_stalemate": 1}
	}},
	{"gunnery", func(seed uint64, doc tactics) map[string]float64 {
		// Conversion efficiency: one ace, one weaving drone. Seconds and
		// rounds to the kill measure the saddle and closure discipline.
		i := arena("gunnery", seed, doc, false,
			map[string]any{"ace": 1.0}, map[string]any{"drone": 1.0})
		var shooter, drone *craft
		for _, slot := range i.slots() {
			if a := i.aircraft[slot]; a.brain != nil {
				shooter = a
			} else {
				drone = a
			}
		}
		aloft(shooter, flight.Vec3{X: -2000, Y: 3400, Z: 0}, flight.Vec3{X: 220}) // start the chase 2 km astern of the weave — the metric is the conversion, not the transit
		aloft(drone, flight.Vec3{X: 0, Y: 3400, Z: 0}, flight.Vec3{X: 200})
		limit := uint64(60 * 300)
		for tick := uint64(0); tick < limit; tick++ {
			i.Step(tick, nil)
			if drone.deaths > 0 {
				return map[string]float64{"down_seconds": float64(tick) / 60, "down_rounds": float64(rounds - shooter.ammunition)}
			}
		}
		return map[string]float64{"down_seconds": float64(limit) / 60, "down_rounds": float64(rounds - shooter.ammunition)}
	}},
}

// TestBattery flies every scenario across the seed set in parallel and
// reports per-scenario metric means on BATTERY lines.
func TestBattery(t *testing.T) {
	if os.Getenv("AIR_BATTERY") == "" {
		t.Skip("set AIR_BATTERY=1 (see the header comment) — minutes of simulation, not a CI test")
	}
	seeds := 8
	if n, err := strconv.Atoi(os.Getenv("AIR_SEEDS")); err == nil && n > 0 {
		seeds = n
	}
	doc := standard()
	if raw := os.Getenv("AIR_TACTICS"); raw != "" {
		overrides := map[string]float64{}
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			t.Fatalf("AIR_TACTICS: %v", err)
		}
		for name, value := range overrides {
			if !amend(&doc, name, value) {
				t.Fatalf("AIR_TACTICS: unknown constant %q", name)
			}
			t.Logf("override %s=%v", name, value)
		}
	}

	type job struct {
		scenario int
		seed     uint64
	}
	jobs := make(chan job)
	results := make([]map[string][]float64, len(batteries))
	for n := range results {
		results[n] = map[string][]float64{}
	}
	var lock sync.Mutex
	var crew sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		crew.Add(1)
		go func() {
			defer crew.Done()
			for j := range jobs {
				metrics := batteries[j.scenario].fly(j.seed, doc)
				lock.Lock()
				for name, value := range metrics {
					results[j.scenario][name] = append(results[j.scenario][name], value)
				}
				lock.Unlock()
			}
		}()
	}
	for n := range batteries {
		for seed := uint64(1); seed <= uint64(seeds); seed++ {
			jobs <- job{n, seed}
		}
	}
	close(jobs)
	crew.Wait()

	for n, b := range batteries {
		names := make([]string, 0, len(results[n]))
		for name := range results[n] {
			names = append(names, name)
		}
		sort.Strings(names)
		line := ""
		for _, name := range names {
			total := 0.0
			for _, v := range results[n][name] {
				total += v
			}
			line += fmt.Sprintf(" %s=%.3f", name, total/math.Max(float64(len(results[n][name])), 1))
		}
		t.Logf("BATTERY %s%s (%d seeds)", b.name, line, seeds)
	}
}

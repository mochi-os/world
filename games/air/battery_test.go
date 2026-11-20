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
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestMain lets the environment amend the package-wide doctrine before any
// test runs, so a sweep flies the whole battery without touching a line of it:
//
//	AIR_GRAVITY=1 make test-doctrine-quick
//	AIR_SPAN_HIGH=8 make test-doctrine-quick
func TestMain(m *testing.M) {
	setting.Frame, setting.Chart = scene, scene // every session a test makes is laid in the scene (scene_test.go)
	stage.Frame = scene
	for _, knob := range []string{"gravity", "span.high", "span.pitch", "span.climb", "span.beam"} {
		if value, err := strconv.ParseFloat(os.Getenv("AIR_"+strings.ToUpper(strings.ReplaceAll(knob, ".", "_"))), 64); err == nil {
			amend(&doctrine, knob, value)
		}
	}
	os.Exit(m.Run())
}

// knobs maps each tunable constant's dotted name to its field in t. Explicit
// rather than reflected: the fields are unexported and a typo'd override must
// fail loudly, not silently measure the baseline. One table serves both ways,
// so the overrides the battery takes and the defaults it reports cannot name
// different sets.
func knobs(t *tactics) map[string]any {
	return map[string]any{
		"aim.walk":        &t.aim.walk,
		"aim.settle":      &t.aim.settle,
		"aim.reach":       &t.aim.reach,
		"drag.pace":       &t.drag.pace,
		"drag.span":       &t.drag.span,
		"bag.reach":       &t.bag.reach,
		"bag.bend":        &t.bag.bend,
		"spiral.nose":     &t.spiral.nose,
		"spiral.span":     &t.spiral.span,
		"spiral.floor":    &t.spiral.floor,
		"spiral.saddle":   &t.spiral.saddle,
		"spiral.hold":     &t.spiral.hold,
		"jink.span":       &t.jink.span,
		"jink.base":       &t.jink.base,
		"jink.spread":     &t.jink.spread,
		"high.closure":    &t.high.closure,
		"high.span":       &t.high.span,
		"high.tail":       &t.high.tail,
		"high.hold":       &t.high.hold,
		"low.near":        &t.low.near,
		"low.far":         &t.low.far,
		"low.tail":        &t.low.tail,
		"low.rise":        &t.low.rise,
		"plan.deficit":    &t.plan.deficit,
		"lead.closure":    &t.lead.closure,
		"lead.floor":      &t.lead.floor,
		"lead.angle":      &t.lead.angle,
		"missile.tail":    &t.missile.tail,
		"missile.span":    &t.missile.span,
		"missile.margin":  &t.missile.margin,
		"missile.step":    &t.missile.step,
		"missile.base":    &t.missile.base,
		"missile.slope":   &t.missile.slope,
		"missile.floor":   &t.missile.floor,
		"missile.gain":    &t.missile.gain,
		"sandwich.span":   &t.sandwich.span,
		"sandwich.nose":   &t.sandwich.nose,
		"sandwich.weight": &t.sandwich.weight,
		"support.span":    &t.support.span,
		"support.share":   &t.support.share,
		"support.engaged": &t.support.engaged,
		"support.behind":  &t.support.behind,
		"support.above":   &t.support.above,
		"support.near":    &t.support.near,
		"support.out":     &t.support.out,
		"support.rise":    &t.support.rise,
		"support.limit":   &t.support.limit,
		"form.abeam":      &t.form.abeam,
		"form.blend":      &t.form.blend,
		"form.burner":     &t.form.burner,
		"press.span":      &t.press.span,
		"press.hold":      &t.press.hold,
		"press.loose":     &t.press.loose,
		"press.closure":   &t.press.closure,
		"press.gap":       &t.press.gap,
		"crowd.weight":    &t.crowd.weight,
		"sandwich.reach":  &t.sandwich.reach,
		"rejoin.span":     &t.rejoin.span,
		"rejoin.fight":    &t.rejoin.fight,
		"zoom.edge":       &t.zoom.edge,
		"zoom.roof":       &t.zoom.roof,
		"zoom.hold":       &t.zoom.hold,
		"rope.edge":       &t.rope.edge,
		"rope.near":       &t.rope.near,
		"rope.far":        &t.rope.far,
		"rope.nose":       &t.rope.nose,
		"rope.hold":       &t.rope.hold,
		"bracket.span":    &t.bracket.span,
		"bracket.angle":   &t.bracket.angle,
		"wounded.weight":  &t.wounded.weight,
		"gravity":         &t.gravity,
		"span.high":       &t.span.high,
		"span.pitch":      &t.span.pitch,
		"span.climb":      &t.span.climb,
		"span.beam":       &t.span.beam,
	}
}

// amend applies one dotted-name override to the doctrine.
func amend(t *tactics, name string, value float64) bool {
	switch field := knobs(t)[name].(type) {
	case *float64:
		*field = value
	case *int:
		*field = int(value)
	case *uint64:
		*field = uint64(value)
	case *bool:
		*field = value != 0
	default:
		return false
	}
	return true
}

// knob_values is every tunable constant's value in t, as tactics.py reads them.
func knob_values(t *tactics) map[string]float64 {
	values := map[string]float64{}
	for name, field := range knobs(t) {
		switch field := field.(type) {
		case *float64:
			values[name] = *field
		case *int:
			values[name] = float64(*field)
		case *uint64:
			values[name] = float64(*field)
		case *bool:
			if *field {
				values[name] = 1
			} else {
				values[name] = 0
			}
		}
	}
	return values
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
		Parameters: map[string]any{"missiles": missiles, "weapons": map[bool]string{false: "guns", true: "fox2"}[missiles], "bots": map[string]any{"red": red, "blue": blue}}})
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
			map[string]any{"expert": 2.0}, map[string]any{"intermediate": 4.0})
		defer i.Close() // return this scenario's share of the server-wide bot budget (#208)
		for tick := uint64(0); tick < 60*300; tick++ {
			i.Step(tick, nil)
		}
		deaths := casualties(i, "red")
		return map[string]float64{"down_deaths": float64(deaths), "up_net": float64(i.score["red"] - deaths)}
	}},
	{"skirmish", func(seed uint64, doc tactics) map[string]float64 {
		// The same 2v4 guns-only: the missile envelope out of the picture.
		i := arena("skirmish", seed, doc, false,
			map[string]any{"expert": 2.0}, map[string]any{"intermediate": 4.0})
		defer i.Close() // return this scenario's share of the server-wide bot budget (#208)
		for tick := uint64(0); tick < 60*300; tick++ {
			i.Step(tick, nil)
		}
		deaths := casualties(i, "red")
		return map[string]float64{"down_deaths": float64(deaths), "up_net": float64(i.score["red"] - deaths)}
	}},
	{"defense", func(seed uint64, doc tactics) map[string]float64 {
		// Defensive entry: a lone intermediate with two missile-armed experts saddled 1.4 km
		// behind; the metric is survival time. Missiles ON deliberately - guns-only
		// kills between maneuvering bots essentially never land.
		i := arena("defense", seed, doc, true,
			map[string]any{"intermediate": 1.0}, map[string]any{"expert": 2.0})
		defer i.Close() // return this scenario's share of the server-wide bot budget (#208)
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
			map[string]any{"expert": 2.0}, map[string]any{"expert": 2.0})
		defer i.Close() // return this scenario's share of the server-wide bot budget (#208)
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
		// Conversion efficiency: one expert, one weaving drone. Seconds and
		// rounds to the kill measure the saddle and closure discipline.
		i := arena("gunnery", seed, doc, false,
			map[string]any{"expert": 1.0}, map[string]any{"drone": 1.0})
		defer i.Close() // return this scenario's share of the server-wide bot budget (#208)
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
	// The budget is process-wide and finite (BOTS_MAXIMUM), and a scenario that
	// forgets to give its share back does not fail - it quietly starves every
	// later one until Create grants ZERO bots and the setup dereferences a nil
	// craft. That leak capped this battery at nine seeds and went unnoticed for
	// as long as it existed, while #46 was asking for 24-48 (#208). Measured,
	// not asserted by reading: the budget has to come back.
	opening := bots_live.Load()
	defer func() {
		if leaked := bots_live.Load() - opening; leaked != 0 {
			each := float64(leaked) / float64(max(seeds, 1))
			t.Errorf("leaked %d of the %d-bot budget, %.0f per seed: a scenario is not calling Close, and the battery starves past %.0f seeds",
				leaked, BOTS_MAXIMUM, each, math.Floor(float64(BOTS_MAXIMUM)/math.Max(each, 1)))
		}
	}()
	doc := standard()
	// The defaults this run measured against: tools/tactics.py centres its
	// candidates on these rather than on a copy of standard() of its own,
	// which had drifted from it.
	if defaults, err := json.Marshal(knob_values(&doc)); err == nil {
		t.Logf("BATTERY-DEFAULTS %s", defaults)
	}
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

// TestKnobsReportTheDoctrineFlown: tools/tactics.py centred its sweeps on a
// hand copy of standard(), and support.share had drifted - it swept 0.6-0.9
// around a 0.75 the bots stopped flying in August. The battery now reports the
// defaults it ran with, read through the same table its overrides go through,
// and every name in that table can be both read and set.
func TestKnobsReportTheDoctrineFlown(t *testing.T) {
	doc := standard()
	values := knob_values(&doc)
	if values["support.share"] != doc.support.share || values["drag.pace"] != doc.drag.pace || values["jink.base"] != float64(doc.jink.base) {
		t.Errorf("reported %v / %v / %v, flown %v / %v / %v", values["support.share"], values["drag.pace"], values["jink.base"], doc.support.share, doc.drag.pace, doc.jink.base)
	}
	if len(values) != len(knobs(&doc)) {
		t.Errorf("reported %d of the %d constants: a field type the report does not read", len(values), len(knobs(&doc)))
	}
	for name, value := range values {
		moved, next := doc, value+1
		if _, flag := knobs(&moved)[name].(*bool); flag {
			next = 1 - value
		}
		if !amend(&moved, name, next) || knob_values(&moved)[name] == value {
			t.Errorf("%s cannot be set through the table it is reported from", name)
		}
	}
}

// TestTacticsNamesTheBatteryKnobs: tools/tactics.py sweeps constants by name
// and centres them on the defaults the battery reports, so a name it carries
// that the battery's table does not would end its run at the baseline.
func TestTacticsNamesTheBatteryKnobs(t *testing.T) {
	source, err := os.ReadFile("../../tools/tactics.py")
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile(`(?m)^\s+"([a-z.]+)":\s+\((True|False),`).FindAllStringSubmatch(string(source), -1)
	if len(table) < 40 {
		t.Fatalf("read %d constants from tactics.py: its table has changed shape", len(table))
	}
	doc := standard()
	known := knobs(&doc)
	for _, row := range table {
		if known[row[1]] == nil {
			t.Errorf("tactics.py sweeps %q, which the battery cannot report or set", row[1])
		}
	}
}

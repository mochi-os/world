package air

import (
	"math"
	"testing"
	"world/game"
	"world/games/air/flight"
)

// TestGateRecord: the launch gate's RECORD is the gate (#212). trigger() writes
// down what it decided so an instrument can report the real rule instead of
// keeping a copy of it — the heater probe kept a copy, it drifted, and it went
// on reporting the pre-#48 rear-aspect heuristic for long after the bot stopped
// applying it, which read as "never in a launch envelope" for bots that were
// firing twenty rounds apiece.
//
// A record is worth nothing once it disagrees with the decision, so pin them
// together: every tick the gate is open the bot asks for a round, and every
// tick it is shut the bot does not.
func TestGateRecord(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": true, "weapons": "fox2",
		"bots": map[string]any{"ace": 1.0, "pilot": 1.0}}, 0)
	defer i.Close()
	checked := 0
	for tick := uint64(0); tick < 60*90; tick++ {
		i.Step(tick, nil)
		for _, slot := range i.slots() {
			c := i.aircraft[slot]
			if c == nil || c.brain == nil || !c.alive || c.model == nil {
				continue
			}
			b := c.brain
			// Re-run the gate on this tick's state and check the record it
			// leaves against the request it makes. Saved and restored so the
			// probe question does not become a shot the bot never took.
			// Zero the record with a sentinel first. Re-running trigger is NOT
			// idempotent once the live path has already fired this tick: think()
			// consumes b.loose and launches, the magazine drops, and the second
			// call hits the `b.missiles <= 0` early return WITHOUT touching
			// b.gate - leaving the live call's record (open, this tick) against
			// the reset b.loose, which reads as a disagreement that never
			// happened. The sentinel makes "my call did not evaluate" visible
			// instead of borrowing the previous answer. Found when a scorer
			// change made the bots fire more and exposed it.
			loose, keep := b.loose, b.gate
			b.loose, b.gate = false, gate{when: ^uint64(0)}
			i.trigger(slot, c, tick)
			if b.gate.when == tick && b.gate.open() != b.loose {
				t.Fatalf("tick %d slot %d: the gate records open=%v while the launch request is %v (%+v)",
					tick, slot, b.gate.open(), b.loose, b.gate)
			}
			if b.gate.when == tick {
				checked++
			}
			b.loose, b.gate = loose, keep
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d ticks reached the gate — this fight never armed, so the invariant was never exercised", checked)
	}
	t.Logf("gate record matched the launch request on %d evaluated ticks", checked)
}

// TestEvolveAgainstArc pins the rollout's opponent model to a real turn.
//
// evolve() is what every rehearsal believes the other jet will do, and it used
// to extrapolate at constant acceleration - a parabola. A turning jet flies an
// arc, so the error grew with dt^2 and reached 3,154 m at 5 g over a 12 s
// rollout, against 2,160 m actually travelled: the miss EXCEEDED the distance
// flown, and `high`, the one play rehearsed that far out, was the one scored
// against the worst phantom (#42).
//
// The bound is generous on purpose - this asserts the model is an ARC and not
// a parabola, which is a factor-of-thousands difference, not a tuning margin.
func TestEvolveAgainstArc(t *testing.T) {
	const speed = 180.0 // m/s, about 350 kt
	truth := func(accel, dt float64) flight.Vec3 {
		const step = 1.0 / 240
		p, v := flight.Vec3{}, flight.Vec3{X: speed}
		for s := 0.0; s < dt; s += step {
			v = v.Add(flight.Vec3{Y: 1}.Cross(v).Normalize().Scale(-accel * step)).Normalize().Scale(speed)
			p = p.Add(v.Scale(step))
		}
		return p
	}
	for _, g := range []float64{3, 5, 7} {
		accel := g * 9.80665
		contact := &track{velocity: flight.Vec3{X: speed}, swing: flight.Vec3{Z: accel}}
		for _, dt := range []float64{2, 4, 8, 12} {
			got, _ := evolve(contact, dt)
			miss := got.Subtract(truth(accel, dt)).Length()
			if miss > 25 {
				t.Errorf("%.0f g over %.0f s: the phantom is %.0f m from a real turn - evolve is extrapolating a parabola again", g, dt, miss)
			}
		}
	}
}

// TestEvolveSlowing pins stage 11's forecast of a slowing jet to the flight it
// describes: his turn rate held, his speed falling at the measured rate to his
// floor, then held. The closed form must agree with a fine numerical flight of
// that law to the metre, straight or turning, with and without reaching the
// floor. A jet that is not slowing, one whose slowing has not yet lasted
// `lasting`, one already at his floor, and a track not at stage 11 must all fly
// the speed-holding arc.
func TestEvolveSlowing(t *testing.T) {
	const floor = slowest
	flown := func(speed, pull, along, dt float64) flight.Vec3 {
		const step = 1.0 / 2400
		omega := pull / speed
		p := flight.Vec3{}
		for s := step / 2; s < dt; s += step {
			v := math.Max(floor, speed+along*s)
			p = p.Add(flight.Vec3{X: math.Cos(omega * s), Z: math.Sin(omega * s)}.Scale(v * step))
		}
		return p
	}
	for _, c := range []struct {
		name               string
		speed, pull, along float64
	}{
		{"straight, never reaching the floor", 200, 0, -10},
		{"straight, reaching the floor", 200, 0, -20},
		{"turning at 5 g, reaching the floor", 200, 5 * 9.80665, -20},
		{"turning at 3 g, slow and reaching the floor early", 90, 3 * 9.80665, -40},
		{"turning at 7 g, a gentle slowing", 250, 7 * 9.80665, -5},
	} {
		contact := &track{velocity: flight.Vec3{X: c.speed}, swing: flight.Vec3{X: c.along, Z: c.pull}, floor: floor, lasted: lasting}
		for _, dt := range []float64{1, 2, 4, 8, 12} {
			got, _ := evolve(contact, dt)
			if miss := got.Subtract(flown(c.speed, c.pull, c.along, dt)).Length(); miss > 1 {
				t.Errorf("%s, %.0f s: the forecast is %.1f m from the flight it describes", c.name, dt, miss)
			}
		}
	}
	for _, dt := range []float64{4, 12} {
		held, _ := evolve(&track{velocity: flight.Vec3{X: 200}, swing: flight.Vec3{Z: 40}}, dt)
		for name, contact := range map[string]*track{
			"a jet speeding up":             {velocity: flight.Vec3{X: 200}, swing: flight.Vec3{X: 10, Z: 40}, floor: floor, lasted: lasting},
			"a slowing that has not lasted": {velocity: flight.Vec3{X: 200}, swing: flight.Vec3{X: -20, Z: 40}, floor: floor, lasted: lasting / 2},
			"a jet already at his floor":    {velocity: flight.Vec3{X: 200}, swing: flight.Vec3{X: -20, Z: 40}, floor: 200, lasted: lasting},
			"a track not at stage 11":       {velocity: flight.Vec3{X: 200}, swing: flight.Vec3{X: -20, Z: 40}, lasted: lasting},
		} {
			if got, _ := evolve(contact, dt); got != held {
				t.Errorf("%.0f s: %s left the speed-holding arc: %v against %v", dt, name, got, held)
			}
		}
	}
}

// TestSlowingIsStageEleven: only a brain at stage 11 believes a slowing
// opponent, and flown on stage 6 alone (&stage=11&omit=1920) it still does;
// the brain as it stands, and stage 6, keep the speed-holding forecast.
func TestSlowingIsStageEleven(t *testing.T) {
	for _, c := range []struct {
		stage, omit int
		want        bool
	}{{0, 0, false}, {6, 0, false}, {11, 0, true}, {11, 1920, true}} {
		i := build(t, "furball", map[string]any{"missiles": false, "bots": map[string]any{"ace": 2.0}}, 0)
		for _, slot := range i.slots() {
			if a := i.aircraft[slot]; a != nil && a.brain != nil {
				a.brain.tactics.stage, a.brain.tactics.omit = c.stage, c.omit
			}
		}
		seen := 0
		for tick := uint64(1); tick <= 60*30 && seen == 0; tick++ {
			i.Step(tick, nil)
			for _, slot := range i.slots() {
				if a := i.aircraft[slot]; a != nil && a.brain != nil {
					for _, known := range a.brain.known {
						seen++
						if want := map[bool]float64{false: 0, true: slowest}[c.want]; known.floor != want {
							t.Fatalf("stage %d omitting %d: a track floors his slowing at %v, want %v", c.stage, c.omit, known.floor, want)
						}
					}
				}
			}
		}
		i.Close()
		if seen == 0 {
			t.Fatalf("stage %d: no brain saw the other in 30 s, so nothing was checked", c.stage)
		}
	}
}

// TestTariffChargesTheDeficit: stage 12 prices a rehearsed line for the energy
// deficit against his forecast it takes the jet into. Dumping energy with bleed
// below parity with a jet holding his speed is charged; the same dump against a
// jet the track believes is slowing is free, because his forecast loses energy
// too; spending a surplus that stays above parity is free, because a surplus is
// there to be spent for angles; and a zoom that trades speed for height is free.
func TestTariffChargesTheDeficit(t *testing.T) {
	i, ace, _ := duellist(t, "drone")
	b := ace.brain
	find := func(name string) play {
		for _, p := range plays {
			if p.name == name {
				return p
			}
		}
		t.Fatalf("no play %q", name)
		return play{}
	}
	ahead := flight.Vec3{X: 1500, Y: 4000}
	holding := &track{when: 60, position: ahead, velocity: flight.Vec3{X: 180}, floor: slowest}
	slowing := &track{when: 60, position: ahead, velocity: flight.Vec3{X: 180}, swing: flight.Vec3{X: -20}, floor: slowest, lasted: lasting}
	charge := func(name string, prey *track, speed float64) float64 {
		score := func(stage int) float64 {
			ace.model.State = flight.Level(ace.model, flight.Vec3{Y: 4000}, flight.Vec3{X: 1}, speed, 3000)
			b.tactics.stage, b.tactics.omit = stage, 1<<7|1<<8|1<<10
			sim := flight.New(ace.model.Airframe, ace.model.Environment, ace.model.World)
			p := find(name)
			value, _ := i.rehearse(ace, b, sim, p, prey, 60, b.horizon(p), 0)
			return value
		}
		return score(11) - score(12)
	}
	fast, slow, zoom := charge("bleed", holding, 200), charge("bleed", slowing, 200), charge("climb", holding, 200)
	spare := charge("bleed", holding, 300)
	t.Logf("charged: bleed against a jet holding his speed %.3f, against a slowing jet %.3f, from a surplus %.3f; climb against the holding jet %.3f", fast, slow, spare, zoom)
	if fast < 0.1 {
		t.Errorf("bleed against a jet holding his speed was charged only %.3f", fast)
	}
	if slow > fast/3 {
		t.Errorf("bleed against a slowing jet was charged %.3f, not far below the %.3f against one holding his speed", slow, fast)
	}
	if zoom > fast/3 {
		t.Errorf("a climb, which trades speed for height, was charged %.3f against bleed's %.3f", zoom, fast)
	}
	if spare > fast/3 {
		t.Errorf("bleed from 300 m/s, spending a surplus it keeps, was charged %.3f against the %.3f of a dump below parity", spare, fast)
	}
}

// TestSlowingMustLast: the bot's own looks decide when a slowing is believed. A
// jet 1.5 km ahead of a stage-11 ace holds his speed, then pulls the throttle to
// idle with the boards out, then goes to full burner: the ace's track of him
// must count the slowing only while it happens, pass `lasting` inside the
// three seconds of it, and drop to nothing once he is speeding up again.
func TestSlowingMustLast(t *testing.T) {
	i := build(t, "furball", map[string]any{"missiles": false, "bots": map[string]any{"ace": 1.0}}, 1)
	var bot *craft
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a != nil && a.brain != nil {
			bot = a
		}
	}
	bot.brain.tactics.stage, bot.brain.tactics.omit = 11, 1920
	me := i.aircraft[0]
	me.model.State = flight.Level(me.model, flight.Vec3{X: 1500, Y: 4000}, flight.Vec3{X: 1}, 200, 3000)
	bot.model.State = flight.Level(bot.model, flight.Vec3{Y: 4000}, flight.Vec3{X: 1}, 200, 3000)
	fly := func(from, to uint64, inputs map[string]any) (most float64) {
		for tick := from; tick < to; tick++ {
			i.Step(tick, map[int][]game.Input{0: {{Sequence: uint32(tick + 1), Data: inputs}}})
			if known, found := bot.brain.known[0]; found && known.lasted > most {
				most = known.lasted
			}
		}
		return most
	}
	if held := fly(0, 120, map[string]any{"throttle": 0.8}); held > 0 {
		t.Fatalf("holding his speed, he was counted as slowing for %.2f s", held)
	}
	if slowed := fly(120, 300, map[string]any{"throttle": 0.0, "speedbrake": 1.0}); slowed < lasting {
		t.Fatalf("three seconds at idle with the boards out counted only %.2f s of slowing", slowed)
	}
	fly(300, 480, map[string]any{"throttle": 1.0, "reheat": 1.0})
	if known, found := bot.brain.known[0]; !found || known.lasted != 0 {
		t.Fatalf("after three seconds of burner his slowing still counts %v", known)
	}
}

// TestNoHeaterZoneWithoutMissiles: in a match nothing can be launched in, no
// brain builds the heater launch zone. Every brain carries six heaters whatever
// the match allows, and in a guns-only fight the zone's cache never held - the
// target's burner moves its plume every tick - so it was rebuilt on nearly
// every tick for a shot that could not be taken. The missiles arm is the
// control: the same fight does reach the zone when a launch is possible.
func TestNoHeaterZoneWithoutMissiles(t *testing.T) {
	for _, missiles := range []bool{true, false} {
		parameters := map[string]any{"missiles": missiles, "bots": map[string]any{"ace": 4.0}}
		if missiles {
			parameters["weapons"] = "fox2"
		}
		i := build(t, "furball", parameters, 0)
		built := 0
		for tick := uint64(1); tick <= 60*60; tick++ {
			i.Step(tick, nil)
			for _, slot := range i.slots() {
				if c := i.aircraft[slot]; c != nil && c.brain != nil && c.brain.heated == tick {
					built++
				}
			}
		}
		i.Close()
		switch {
		case missiles && built == 0:
			t.Fatalf("the missiles match never built a heater zone in 60 s: the fight never reached the gate, so the guns-only arm proves nothing")
		case !missiles && built > 0:
			t.Errorf("a guns-only match built the heater zone %d times in 60 s", built)
		}
		t.Logf("missiles %v: heater zone built %d times in 60 s", missiles, built)
	}
}

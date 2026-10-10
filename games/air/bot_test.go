package air

import (
	"testing"
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

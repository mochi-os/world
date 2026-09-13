package air

import (
	"testing"
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
			loose, keep := b.loose, b.gate
			b.loose = false
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

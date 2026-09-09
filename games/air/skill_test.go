// Mochi world: the skill table's own rules
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"
)

// TestAeroCapByTier: the aero cap is a skill, not a law of the airframe. One
// coefficient capped every tier at 85% of what the wing gives, so a novice -
// whose whole authenticity is getting slow without noticing - flew the same
// energy discipline as the ace and never mushed, and the slow fight was
// forbidden to everyone (#153). The wing's g at a speed is (speed/stall)^2.
func TestAeroCapByTier(t *testing.T) {
	const demand, stall = 7.5, 60.0 // a full pull, and a 117 kt stall
	wing := func(speed float64) float64 { return (speed / stall) * (speed / stall) }
	slow := 90.0 // 175 kt: the wing gives 2.25 g here

	novice, pilot, ace, machine := skills["novice"], skills["pilot"], skills["ace"], skills["superhuman"]

	if got := novice.capped(demand, slow, stall); got != demand {
		t.Errorf("novice commanded %.2f g of a %.1f g demand at %.0f m/s: the rookie has no cap, it pulls what it asks for", got, demand, slow)
	}
	if want := wing(slow); math.Abs(pilot.capped(demand, slow, stall)-want) > 1e-9 {
		t.Errorf("pilot commanded %.2f g at %.0f m/s, want exactly the wing's %.2f: no margin, no excess", pilot.capped(demand, slow, stall), slow, want)
	}
	if want := 0.85 * wing(slow); math.Abs(ace.capped(demand, slow, stall)-want) > 1e-9 {
		t.Errorf("ace commanded %.2f g at %.0f m/s, want %.2f: 85%% of the wing, the margin that keeps it out of the mush", ace.capped(demand, slow, stall), slow, want)
	}
	if ace.capped(demand, slow, stall) != machine.capped(demand, slow, stall) {
		t.Errorf("superhuman %.2f g v ace %.2f g: the superhuman is the ace with the human dials at zero, and the cap is not a human dial",
			machine.capped(demand, slow, stall), ace.capped(demand, slow, stall))
	}

	// The ladder is monotone at a slow speed: the less disciplined the tier,
	// the more it will pull past the wing.
	if !(novice.capped(demand, slow, stall) > pilot.capped(demand, slow, stall) && pilot.capped(demand, slow, stall) > ace.capped(demand, slow, stall)) {
		t.Errorf("cap ladder not monotone at %.0f m/s: novice %.2f, pilot %.2f, ace %.2f", slow,
			novice.capped(demand, slow, stall), pilot.capped(demand, slow, stall), ace.capped(demand, slow, stall))
	}

	// A capped tier is never taken below 1.1 g, however slow: level flight and
	// a little more is always available. Nor is a demand ever RAISED.
	if got := ace.capped(demand, 30, stall); got != 1.1 {
		t.Errorf("ace at 30 m/s commanded %.2f g, want the 1.1 g floor", got)
	}
	if got := ace.capped(1.0, slow, stall); got != 1.0 {
		t.Errorf("a 1.0 g demand became %.2f g: the cap only ever lowers", got)
	}
	// And at corner speed and above, the wing gives the placard and the cap
	// costs nothing anyone would command.
	if fast := stall * math.Sqrt(7.5) * 1.2; ace.capped(demand, fast, stall) != demand {
		t.Errorf("ace above corner commanded %.2f g of %.1f: the cap should be inert where the wing has the g", ace.capped(demand, fast, stall), demand)
	}
}

// Mochi world: Battle damage determination
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The battle package DECIDES damage — hits, fires, shedding, pilot fate —
// and writes the results into flight.DamageState for the flight core to
// apply. One implementation serves the multiplayer server natively and the
// single-player client through wasm, so damage is byte-identical wherever
// it is judged. Everything is deterministic: randomness comes only from the
// splitmix hash over (seed, slot, tick, counter); there is no clock and no
// math/rand anywhere in this package.

package battle

import (
	"math"

	"world/games/air/flight"
)

// Condition is the host-side damage state that the flight core never needs:
// fires, the pilot, and kill-credit bookkeeping. The zero value is healthy.
type Condition struct {
	Fire    [2]float64 // per-engine fire intensity 0..1 (0 = not burning)
	Burning bool       // fuel fire: unsuppressable, ends in an explosion
	Fuse    float64    // seconds of fuel fire remaining until the explosion
	Killed  bool       // the pilot is dead
	Damager int        // slot of the last player to damage this aircraft, -1 none
	Damaged float64    // seconds since that damage
}

// Body binds an airframe's part geometry to the damage stores it strikes
// into. For a real aircraft Damage points into the model's State; for a
// single-player hulk (the AI bandit, neutral traffic) it stands alone.
type Body struct {
	Airframe  *flight.Airframe
	Parts     []Part
	Damage    *flight.DamageState
	Condition *Condition
}

// Event reports a notable outcome for presentation and scoring. Kind is one
// of: hit, fire, leak, pilot, explode, shed, jam.
type Event struct {
	Kind    string
	Engine  int // burning/damaged engine, -1 when not engine-related
	Surface int // shed/hit surface index, -1 when not surface-related
	Count   int // hits in this burst (hit events)
}

// Advance runs one tick of the damage cascade at rate ticks per second:
// fire growth and the idle-throttle extinguish drill, burn-through, the
// fuel-fire fuse, and the over-g structural shed. Both hosts call it every
// tick for every living aircraft.
func Advance(body *Body, model *flight.Model, throttle float64, rate float64, seed uint64, slot uint64, tick uint64) []Event {
	var events []Event
	condition := body.Condition
	damage := body.Damage
	step := 1 / rate
	condition.Damaged += step

	// Engine fires: feed on throttle, starve at idle — pulling the engines
	// back IS the fire drill (one throttle serves both engines for now).
	for i := range condition.Fire {
		if condition.Fire[i] <= 0 {
			continue
		}
		if throttle > 0.1 {
			condition.Fire[i] += 0.05 * step
		} else {
			condition.Fire[i] -= 0.08 * step
		}
		if condition.Fire[i] <= 0 {
			condition.Fire[i] = 0 // extinguished
			continue
		}
		damage.Engine[i] = math.Min(1, damage.Engine[i]+0.04*step)
		if condition.Fire[i] >= 1 {
			condition.Fire[i] = 1
			// Burn-through: a fully developed engine fire reaches the fuel.
			if !condition.Burning && roll(seed, slot, tick, 11)*rate < 0.10 {
				ignite(body, seed, slot, tick)
				events = append(events, Event{Kind: "fire", Engine: -1, Surface: -1})
			}
		}
	}

	// Fuel fire: the fuse runs to the explosion; ejection is the out.
	if condition.Burning {
		condition.Fuse -= step
		if condition.Fuse <= 0 {
			events = append(events, Event{Kind: "explode", Engine: -1, Surface: -1})
		}
	}

	// Structural failure: ultimate load carries the classic 1.5 safety
	// factor over the limiter, weakened by accumulated overstress exposure
	// and by root damage. Exceeding it sheds the weaker wing's outboard
	// half — usually unrecoverable, and that is the point.
	if model != nil {
		ultimate := 1.5 * body.Airframe.Limit.Positive
		normal := math.Abs(model.State.Fcs.Normal)
		weaker, health := weakest(body)
		strength := clamp(1-0.05*damage.Stress-0.6*(1-health), 0.35, 1)
		if normal > ultimate*strength && weaker >= 0 {
			shed(body, weaker)
			events = append(events, Event{Kind: "shed", Engine: -1, Surface: weaker})
		}
	}
	return events
}

// ignite starts the unsuppressable fuel fire with a deterministic fuse.
func ignite(body *Body, seed uint64, slot uint64, tick uint64) {
	if body.Condition.Burning {
		return
	}
	body.Condition.Burning = true
	body.Condition.Fuse = 10 + 20*roll(seed, slot, tick, 12)
	body.Damage.Leak += 1
}

// weakest finds the wing whose inboard elements carry the most damage,
// returning its surface index and the worst inboard health (1 = pristine).
func weakest(body *Body) (int, float64) {
	surface, health := -1, 1.0
	base := 0
	for si := range body.Airframe.Surfaces {
		s := &body.Airframe.Surfaces[si]
		if s.Kind == flight.Wing {
			inboard := 1.0
			for ei := 0; ei < len(s.Elements) && ei < 3; ei++ {
				loss := 0.0
				if body.Damage.Element != nil && base+ei < len(body.Damage.Element) {
					loss = body.Damage.Element[base+ei]
				}
				if 1-loss < inboard {
					inboard = 1 - loss
				}
			}
			if surface < 0 || inboard < health {
				surface, health = si, inboard
			}
		}
		base += len(s.Elements)
	}
	return surface, health
}

// shed tears off the outboard half of a wing: the elements go, drag rises,
// the CG walks toward the surviving wing, and the mass leaves.
func shed(body *Body, surface int) {
	s := &body.Airframe.Surfaces[surface]
	base := 0
	for si := 0; si < surface; si++ {
		base += len(body.Airframe.Surfaces[si].Elements)
	}
	if body.Damage.Element == nil {
		body.Damage.Element = make([]float64, flight.Elements)
	}
	for ei := len(s.Elements) / 2; ei < len(s.Elements); ei++ {
		body.Damage.Element[base+ei] = 1
	}
	body.Damage.Drag += 0.15
	body.Damage.Shift.Z += -s.Side * 0.08
	body.Damage.Loss += 300
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Min(math.Max(v, low), high)
}

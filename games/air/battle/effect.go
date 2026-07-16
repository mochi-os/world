// Mochi world: Battle strike table
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// One table converts a hit on a part into system damage; guns and missile
// fragments both come through here. Every number in this file is a tuning
// knob for combat balance — keep them together.

package battle

import (
	"math"

	"world/games/air/flight"
)

// Strike damage per 20 mm HEI-class hit; missile fragments apply severity 2.
const (
	structure = 0.5   // element loss per hit: two rounds kill an element
	litter    = 0.03  // drag area per structure hit, m²
	turbine   = 0.35  // engine thrust loss per hit
	seep      = 0.5   // tank leak per hit, kg/s
	weep      = 0.25  // wet-wing leak per hit, kg/s
	clutter   = 0.02  // drag area per fuselage hit, m²
	flaperon  = 0.25  // chance a flapped-element hit jams its flaperon
	actuator  = 0.30  // chance a stabilator-root hit freezes that stabilator
	linkage   = 0.25  // chance a fin-root hit restricts the rudder
	kindle    = 0.30  // chance a hit on an already-damaged engine starts a fire
	torch     = 0.08  // chance a tank hit lights the fuel
	detonate  = 0.03  // chance a tank hit simply blows the jet up (#144): HEI in vapour space — the historical flamer, and the variance that makes some kills three rounds and some forty
	flash     = 0.05  // chance a wet-wing hit lights the fuel
	mortal    = 0.40  // chance a cockpit hit kills the pilot
	plumbing  = 0.12  // chance a fuselage hit cuts a hydraulic run
	wheel     = 0.45  // gear-leg damage per hit: one hit blows the tyre, two fold the leg (#78)
)

// strike applies one hit to a part. The three hash words identify the hit
// uniquely (shooter/tick/round) so every conditional roll is deterministic.
func strike(body *Body, part *Part, severity float64, seed uint64, slot uint64, tick uint64, round uint64) []Event {
	var events []Event
	damage := body.Damage
	chance := func(p float64, salt uint64) bool {
		return roll(seed, slot, tick, round, salt) < p*severity
	}
	switch part.Kind {
	case Structure:
		if damage.Element == nil {
			damage.Element = make([]float64, flight.Elements)
		}
		damage.Element[part.Index] = math.Min(1, damage.Element[part.Index]+structure*severity)
		damage.Drag += litter * severity
		surface := &body.Airframe.Surfaces[part.Surface]
		if part.Flapped && surface.Kind == flight.Wing && chance(flaperon, 1) {
			jam(damage, side(surface, flight.ChannelFlaperonLeft, flight.ChannelFlaperonRight), 0.6)
			events = append(events, Event{Kind: "jam", Engine: -1, Surface: part.Surface})
		}
		if part.Root && surface.Kind == flight.Stabilator && chance(actuator, 2) {
			jam(damage, side(surface, flight.ChannelStabilatorLeft, flight.ChannelStabilatorRight), 1)
			events = append(events, Event{Kind: "jam", Engine: -1, Surface: part.Surface})
		}
		if part.Root && surface.Kind == flight.Fin && chance(linkage, 3) {
			jam(damage, flight.ChannelRudder, 0.5)
			events = append(events, Event{Kind: "jam", Engine: -1, Surface: part.Surface})
		}
		if part.Wet {
			damage.Leak += weep * severity
			if chance(flash, 4) {
				ignite(body, seed, slot, tick)
				events = append(events, Event{Kind: "fire", Engine: -1, Surface: -1})
			}
		}
	case Turbine:
		already := damage.Engine[part.Index] >= turbine
		damage.Engine[part.Index] = math.Min(1, damage.Engine[part.Index]+turbine*severity)
		if already && body.Condition.Fire[part.Index%2] <= 0 && chance(kindle, 5) {
			body.Condition.Fire[part.Index%2] = 0.05
			events = append(events, Event{Kind: "fire", Engine: part.Index, Surface: -1})
		}
	case Tank:
		damage.Leak += seep * severity
		if chance(detonate, 10) {
			ignite(body, seed, slot, tick)
			body.Condition.Fuse = math.Min(body.Condition.Fuse, 0.3) // HEI in the vapour space: the fire IS the explosion
			events = append(events, Event{Kind: "fire", Engine: -1, Surface: -1})
		} else if chance(torch, 6) {
			ignite(body, seed, slot, tick)
			events = append(events, Event{Kind: "fire", Engine: -1, Surface: -1})
		}
	case Cockpit:
		if chance(mortal, 7) && !body.Condition.Killed {
			body.Condition.Killed = true
			events = append(events, Event{Kind: "pilot", Engine: -1, Surface: -1})
		}
	case Gear:
		damage.Gear[part.Index] = math.Min(1, damage.Gear[part.Index]+wheel*severity)
		events = append(events, Event{Kind: "gear", Engine: -1, Surface: -1})
	case Fuselage:
		damage.Drag += clutter * severity
		if chance(plumbing, 8) {
			channel := int(roll(seed, slot, tick, round, 9) * float64(flight.ChannelSpeedbrake+1))
			jam(damage, channel, 0.5)
			events = append(events, Event{Kind: "jam", Engine: -1, Surface: -1})
		}
	}
	return events
}

// jam adds restriction to an actuator channel.
func jam(damage *flight.DamageState, channel int, restriction float64) {
	if damage.Jam == nil {
		damage.Jam = make([]float64, flight.Channels)
	}
	damage.Jam[channel] = math.Min(1, damage.Jam[channel]+restriction)
}

// side picks the left or right channel for a surface.
func side(surface *flight.Surface, left int, right int) int {
	if surface.Side < 0 {
		return left
	}
	return right
}

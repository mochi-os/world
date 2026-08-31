// Mochi world: Bot radar and AMRAAM employment (BVR behaviours 1 and 2).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A bot's radar drives the same craft fields a human's client reports - emitter
// and lock - so the pose wire, RWR escalation, fox3() and the datalink work
// against bots unchanged. Everything here is gated on the match's weapons
// class, so the fox2-pinned dogfight batteries never enter this file.

package air

import (
	"math"

	"world/games/air/flight"
	"world/games/air/round"
)

// press is the per-level BVR employment character. Depth positions the shot
// between the no-escape rung (0) and the maximum rung (1). Look is the
// shoot-look-shoot spacing in ticks. Withhold holds SEARCH and denies the STT
// until commit range; it never means radiating nothing.
type press struct {
	depth    float64
	look     uint64
	withhold bool
}

func pressing(library int, machine bool) press {
	switch {
	case machine:
		return press{depth: 0.15, look: 180, withhold: true}
	case library <= 1:
		return press{depth: 1.0, look: 120, withhold: false}
	case library == 2:
		return press{depth: 0.7, look: 180, withhold: false}
	default:
		return press{depth: 0.35, look: 180, withhold: true}
	}
}

// The bot radar's own geometry: detection reaches further than any shot it
// can take (the DLZ is the limiting factor, as it should be), and the scan
// cone matches the seeker's gimbal constant.
const (
	radar_reach  = 80000.0 // m
	radar_commit = 1.25    // the withholding tiers light the STT inside this multiple of the maximum rung
	radar_seam   = 10000.0 // m: inside this the tuned WVR arbiter owns the flight path and the crank yields
)

// soar points a horizontal direction with the climb that walks the jet back to
// the BVR block. Altitude is the DLZ's biggest lever, and a level() aim under
// low g lets the jet sag until every rung collapses.
func soar(direction flight.Vec3, altitude float64) flight.Vec3 {
	d := level(direction)
	d.Y = clamp((bvraltitude-altitude)/4000, -0.1, 0.35)
	return d.Normalize()
}

// painted reports whether a bot's own radar holds this contact: radiating,
// inside the scan cone and reach, and outside the notch. Order dependency,
// deliberate: perception runs inside decide() at the skill's cadence and hunt()
// every tick after it, so last tick's emitter admits this tick's contacts.
func (i *instance) painted(a, c *craft) bool {
	if a.brain == nil || i.weapons != "open" || a.emitter < 1 {
		return false
	}
	if !c.alive || c.model == nil || !hostile(a, c) {
		return false
	}
	line, span := i.bearing(a.model.State.Position, c.model.State.Position)
	if span > radar_reach {
		return false
	}
	if a.model.State.Attitude.Rotate(flight.Vec3{X: 1}).Dot(line) < round.Gimbal {
		return false
	}
	radial := math.Abs(c.model.State.Velocity.Subtract(a.model.State.Velocity).Dot(line))
	return radial > round.Notch
}

// counterfire: a withholding tier forced defensive takes the one shot it has
// before committing to the notch, since its commit range arrives later than an
// aggressor's first credible shot. Full rack only - once anything has left the
// rails, normal doctrine owns the fight.
func (i *instance) counterfire(slot int, a *craft, tick uint64) {
	b := a.brain
	if i.weapons != "open" || a.amraams == 0 || !i.free() {
		return
	}
	if a.amraams != len(stores_amraams(a.loadout)) {
		return
	}
	if !pressing(b.skill.library, b.skill.machine).withhold {
		return
	}
	if b.countered >= b.alerted || (b.launched > 0 && tick-b.launched <= 60) {
		return
	}
	// The credibility gate: answer only a round that would still arrive
	// supersonic, or a max-range spray buys the one licensed round for free.
	credible := false
	for _, m := range i.flying {
		if m.radar == nil || m.target != slot || m.radar.Phase < round.Active {
			continue
		}
		if round.Lethal(*m.radar, round.Target{Position: a.model.State.Position,
			Velocity: a.model.State.Velocity}, 0.2) {
			credible = true
			break
		}
	}
	if !credible {
		return
	}
	target := b.target
	if target < 0 {
		return
	}
	prey := i.aircraft[target]
	if prey == nil || !prey.alive || prey.model == nil || !hostile(a, prey) {
		return
	}
	line, span := i.bearing(a.model.State.Position, prey.model.State.Position)
	nose := a.model.State.Attitude.Rotate(flight.Vec3{X: 1})
	radial := math.Abs(prey.model.State.Velocity.Subtract(a.model.State.Velocity).Dot(line))
	if nose.Dot(line) < round.Gimbal || span > radar_reach || radial <= round.Notch {
		return
	}
	zone := round.Ladder(
		round.Target{Position: a.model.State.Position, Velocity: a.model.State.Velocity},
		round.Target{Position: prey.model.State.Position, Velocity: prey.model.State.Velocity},
		i.environment.Wrap)
	if span > zone.Max || span < zone.Minimum {
		return
	}
	a.emitter, a.lock = 2, target
	if i.fox3(slot, a) {
		if !i.cheat.ammunition {
			a.amraams--
			a.model.Stores(a.attach())
		}
		a.release = 0
		b.supported = tick
		b.countered = tick
	}
}

// hunt is a bot's whole BVR offence, run every tick from think() between decide
// and steer: hold the emitter state, take the DLZ-judged shot through fox3(),
// and crank while an own round wants datalink. The only range floor is
// Zone.Minimum.
func (i *instance) hunt(slot int, a *craft, tick uint64) {
	b := a.brain
	if i.defend(slot, a, tick) {
		// Defence owns the flight path; the radar drops to search (silence with an
		// empty rack) - a beaming defender cannot hold an STT anyway.
		i.counterfire(slot, a, tick)
		if a.amraams > 0 && i.missiles {
			a.emitter, a.lock = 1, -1
		} else if a.emitter != 0 {
			a.emitter, a.lock = 0, -1
		}
		return
	}
	if i.weapons != "open" {
		// A fox2 or guns match never radiates. The gate is the EQUIPMENT, not the
		// rounds remaining: a Winchester bot keeps its radar and its picture, or the
		// pair goes blind and the fight never ends.
		if a.emitter != 0 {
			a.emitter, a.lock = 0, -1
		}
		return
	}

	target := b.target
	prey := (*craft)(nil)
	if target >= 0 {
		prey = i.aircraft[target] // a map: absent slots read nil, no bounds to check
	}
	if prey == nil || !prey.alive || prey.model == nil || !hostile(a, prey) {
		a.emitter, a.lock = 1, -1 // armed and wanting: search — this is what lets painted() bootstrap the first contact
		if b.contacted > 0 && tick-b.contacted < 10800 {
			// Investigate the last contact: a mutual defence staleness both pictures at
			// once, and the course a defence ends on points at the beam, not the fight.
			// Three minutes of memory lets the pair re-commit.
			to := b.contact.Subtract(a.model.State.Position)
			if to.Length() > 1 {
				b.aim = soar(to.Normalize(), a.model.State.Position.Y)
				b.g = 2
			}
		} else if b.mode == "cruise" {
			// Hold the lane: with nothing known, cruise walks every bot onto the same
			// patrol track and the joust becomes a tail-chase that never closes. An
			// armed searcher flies the course it is on.
			direction := a.model.State.Velocity
			if direction.Length() > 1 {
				b.aim = soar(direction.Normalize(), a.model.State.Position.Y)
				b.g = 1.4
			}
		}
		return
	}

	b.contact, b.contacted = prey.model.State.Position, tick
	line, span := i.bearing(a.model.State.Position, prey.model.State.Position)
	nose := a.model.State.Attitude.Rotate(flight.Vec3{X: 1})
	cone := nose.Dot(line) >= round.Gimbal
	radial := math.Abs(prey.model.State.Velocity.Subtract(a.model.State.Velocity).Dot(line))
	trackable := cone && span <= radar_reach && radial > round.Notch

	// The DLZ, refreshed at most once a second: the same arithmetic the
	// human HUD shows, so bot and human judge every shot by one truth.
	if tick-b.assessed >= 60 || b.assessed == 0 {
		b.assessed = tick
		b.zone = round.Ladder(
			round.Target{Position: a.model.State.Position, Velocity: a.model.State.Velocity},
			round.Target{Position: prey.model.State.Position, Velocity: prey.model.State.Velocity},
			i.environment.Wrap)
	}

	d := pressing(b.skill.library, b.skill.machine)
	committed := span <= b.zone.Max*radar_commit
	if trackable && (committed || !d.withhold) {
		a.emitter, a.lock = 2, target
	} else {
		// Search: the target is in the notch, outside the cone, or the
		// disciplined tiers are denying the hard-lock warning until
		// commit. Losing a track mid-support is fly_radar's business —
		// the round coasts on memory and goes active on schedule.
		a.emitter, a.lock = 1, -1
	}

	// The shot: STT up, inside the depth-positioned rung, above the
	// physics floor, spaced from the last launch of either magazine.
	// Everything else — fratricide, capacity, the lock's validity — is
	// fox3()'s to judge, the same as a human squeeze.
	if b.futiled != target {
		b.futiled, b.futile = target, 0
	}
	spaced := a.release > float64(d.look)/60 && (b.launched == 0 || tick-b.launched > 60)
	if b.skill.library >= 3 && b.futile >= 2 && span > radar_seam {
		// The LEARN half of the look: two rounds have already died against this
		// target's defence and the ladder cannot see why, so hold the rest of the
		// rack for the merge instead of feeding the same defence.
		spaced = false
	}
	if b.skill.library >= 2 {
		// Shoot-LOOK-shoot: the disciplined tiers watch a round to its pitbull before
		// committing the next, or every tier ripples its rack in the opening
		// exchange. The novice keeps the ripple.
		for _, m := range i.flying {
			if m.radar != nil && m.shooter == slot && m.radar.Phase < round.Pitbull {
				spaced = false
				break
			}
		}
	}
	if a.amraams > 0 && a.emitter == 2 && a.lock == target && i.free() && spaced {
		want := b.zone.Escape + d.depth*math.Max(b.zone.Max-b.zone.Escape, 0)
		if span <= want && span >= b.zone.Minimum && i.fox3(slot, a) {
			if !i.cheat.ammunition {
				a.amraams--
				a.model.Stores(a.attach())
			}
			a.release = 0
			b.supported = tick
		}
	}

	// The crank: while an own round is in midcourse beyond the merge seam, offset
	// the nose toward the gimbal edge - closure drops, the lock survives, the
	// datalink holds. It serves spike awareness too. Inside the seam the WVR
	// arbiter owns the flight path and this yields; the trigger stays live.
	if span > radar_seam && b.skill.library >= 2 {
		// The novice does not crank: it keeps pressing straight after launch, which
		// keeps its own geometry honest as well as its toolkit incomplete.
		overlay := false
		for _, m := range i.flying {
			if m.radar != nil && m.shooter == slot && m.target == target && m.radar.Phase < round.Active {
				overlay = true
				break
			}
		}
		if !overlay && b.skill.library >= 3 {
			for other, c := range i.aircraft {
				if other != slot && c.alive && hostile(a, c) && c.emitter == 2 && c.lock == slot {
					overlay = true // spiked on the approach: fly the offset, deny him the clean geometry
					break
				}
			}
		}
		if overlay {
			side := 1.0
			if b.turning < 0 {
				side = -1
			}
			crank := soar(flight.Vec3{
				X: line.X*math.Cos(side*0.7) - line.Z*math.Sin(side*0.7),
				Y: 0,
				Z: line.X*math.Sin(side*0.7) + line.Z*math.Cos(side*0.7),
			}, a.model.State.Position.Y)
			b.aim = crank
			b.g = 3
			b.cranked = tick
		}
	}
}

// poise is the per-level defensive execution character. The syllabus is
// knowledge-uniform from the pilot up - beam, dispense, re-square - and only
// execution separates the levels. The novice never beams.
type poise struct {
	tolerance float64 // beam error bound, radians: sampled once per engagement
	cadence   uint64  // ticks between re-squares; the beam drifts in between
	lapse     float64 // chance this engagement's defence reverts to drag
}

func poising(library int) poise {
	switch {
	case library == 2:
		return poise{tolerance: 0.5, cadence: 300, lapse: 0.25}
	case library >= 3:
		return poise{tolerance: 0.17, cadence: 150, lapse: 0.08}
	default:
		return poise{} // the novice never beams; the zero value is unused
	}
}

// guard_quiet is the terminal call: inside this range the machine drops its
// jammer, because a loud target hands an inbound round bearing-only
// home-on-jam that overrides every other defence.
const guard_quiet = 6000.0

// chance is a deterministic per-engagement roll, seeded like the flare
// rolls: same instance, slot, round and salt — same outcome, every replay.
func chance(seed uint64, slot int, number uint64, salt uint64) float64 {
	x := seed ^ uint64(slot)*0x9E3779B97F4A7C15 ^ number*0xBF58476D1CE4E5B9 ^ salt*0x94D049BB133111EB
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return float64(x>>11) / float64(1<<53)
}

// defend is behaviour 3: the reaction to an inbound radar round, from the
// moment a human's RWR calls MISSILE (the round past Active). Returns true
// while the defence owns the flight path. Beam then dispense, degraded by the
// poise dials; the machine also arms its jammer until the terminal call.
func (i *instance) defend(slot int, a *craft, tick uint64) bool {
	b := a.brain
	var threat *missile
	span := math.MaxFloat64
	inbound := 0 // radar rounds active against me: the ones behind the nearest each want their share of the chaff (#43)
	for _, m := range i.flying {
		if m.radar == nil || m.target != slot || m.radar.Phase < round.Active {
			continue
		}
		inbound++
		if _, d := i.bearing(a.model.State.Position, m.position); d < span {
			span, threat = d, m
		}
	}
	if threat == nil {
		b.alerted, b.guarding, b.jam = 0, 0, false
		return false
	}
	if b.alerted == 0 {
		b.alerted = tick
	}
	if float64(tick-b.alerted)/60 < b.skill.react {
		return false // not yet noticed: the reaction-delay dial is the skill's own reaction time
	}
	// Inside the merge the beam is no defence (#101): a shooter at two
	// kilometres arrives with his round, and the standoff notch hands him a
	// cold, predictable stern — under the #95 energy model eleven of fifteen
	// wide-BVR losses died mid-notch at ~2 km with rounds still aboard,
	// perfect perception keeping the machine on permanent defence while the
	// half-blind ace fought on. From the pilot up, once any hostile is inside
	// the seam the WVR arbiter owns the flight path: fight the shooter, and
	// let the terminal evade break the round.
	if b.skill.library >= 2 {
		for other, c := range i.aircraft {
			if other == slot || c == nil || !c.alive || c.model == nil || !hostile(a, c) {
				continue
			}
			if _, d := i.bearing(a.model.State.Position, c.model.State.Position); d < 3000 {
				b.guarding, b.jam = 0, false
				return false
			}
		}
	}
	if b.guarding != threat.number {
		// A fresh engagement: roll its character once, deterministically.
		b.guarding = threat.number
		p := poising(b.skill.library)
		b.lapse = chance(i.environment.Seed, slot, threat.number, 1) < p.lapse
		b.askew = (chance(i.environment.Seed, slot, threat.number, 2)*2 - 1) * p.tolerance
		b.squared = 0
	}

	line, _ := i.bearing(a.model.State.Position, threat.position)
	if b.skill.library <= 1 || b.lapse {
		// The drag: turn tail and run, dispensing in panic. Against a
		// radar round this is the losing defence — the stern chase stays
		// marginal only by the round's own physics.
		b.aim = level(line.Scale(-1))
		b.g = clamp(b.skill.pull*0.9, 2, 7.5)
		b.throttle, b.reheat = 1, 1
		b.mode = "drag"
		if a.clouded > 0.8 && a.chaff > 5*(inbound-1) {
			b.bloom = true // chaff, against a radar round: the split programme (#43) no longer spends flares here
		}
		return true
	}

	// The beam, re-squared at the cadence dial and drifting in between —
	// the LOS rotates as the round closes, so a held heading decays out of
	// the notch and gets reacquired: bought time, not a binary win.
	p := poising(b.skill.library)
	if b.squared == 0 || tick-b.squared >= p.cadence {
		b.squared = tick
		velocity := a.model.State.Velocity
		side := 1.0
		if velocity.Cross(line).Y < 0 {
			side = -1 // the perpendicular nearer the current course: no reversal mid-defence
		}
		angle := side * (math.Pi/2 + b.askew)
		sin, cos := math.Sin(angle), math.Cos(angle)
		b.beam = level(flight.Vec3{
			X: line.X*cos - line.Z*sin,
			Y: 0,
			Z: line.X*sin + line.Z*cos,
		})
	}
	b.aim = b.beam
	b.g = 4.5
	b.throttle, b.reheat = 1, 0
	b.mode = "notch"
	// A bloom is worth dispensing only outside the seeker's Resolve range, while
	// the jet is in the notch, and never into the five cartridges each other
	// inbound round will want. The 1.4 s cadence is measured: waiting for the
	// seeker's Hold to expire leaves half-second gaps with no fresh cloud.
	radial := math.Abs(a.model.State.Velocity.Dot(line))
	if a.clouded > 1.4 && span > round.Resolve && radial < round.Notch && a.chaff > 5*(inbound-1) {
		b.bloom = true
	}
	if b.skill.machine {
		b.jam = span > guard_quiet // armed while spoofable, quiet at the terminal call
	}
	return true
}

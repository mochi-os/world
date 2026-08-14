// Mochi world: Bot radar and AMRAAM employment (BVR behaviours 1 and 2).
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A bot's radar drives the same craft fields a human's client reports —
// emitter and lock — so the pose wire, the RWR escalation, fox3()'s launch
// gate, and the datalink in fly_radar all work against bots unchanged; and
// it feeds the same track table vision feeds (painted, called from the
// perception scan), because at BVR ranges the radar IS the bot's
// perception: the canopy ends at twelve kilometres. Everything here is
// gated on AMRAAMs aboard, and bots arm to the match's weapons class
// exactly as humans are clamped — so the fox2-pinned dogfight batteries
// never enter this file, and the WVR calibration holds by configuration,
// enforced by the silence sentinel in hunt_test.go.
//
// The design record lives in claude/plans/air-amraam.md, "Bot BVR
// decisions" (settled 2026-08-13).

package air

import (
	"math"

	"world/games/air/flight"
	"world/games/air/round"
)

// press is the per-level BVR employment character: tuning dials for the BVR
// battery, deliberately separate from the flight-model and WVR constants.
// Depth positions the shot between the no-escape rung (0: the target cannot
// kinematically defeat the round) and the maximum rung (1: it arrives only
// if he flies on as now) — the novice sprays at maximum, the disciplined
// tiers wait. Look is the shoot-look-shoot spacing in ticks. Withhold is
// the emission discipline: hold SEARCH and deny the STT until the commit
// range, so the defender's RWR reads paint rather than a hard lock until
// the shot is near. It cannot mean radiating nothing — the radar is how the
// bot finds the fight at all, and a fighter with its set off is blind.
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

// soar points a horizontal direction with the climb that walks the jet back
// to the BVR block. Altitude is the DLZ's biggest lever — the round's level
// range nearly triples between the deck and the block — and a `level()` aim
// under low g lets the jet sag: measured, a pair sank from 6,100 m to 2,500
// over one approach and the ladder's every rung collapsed to zero.
func soar(direction flight.Vec3, altitude float64) flight.Vec3 {
	d := level(direction)
	d.Y = clamp((bvraltitude-altitude)/4000, -0.1, 0.35)
	return d.Normalize()
}

// painted reports whether a bot's own radar holds this contact: radiating,
// inside the scan cone and reach, and outside the notch — a target holding
// its radial speed inside the clutter cannot be tracked, which is what
// makes the beam defeat the whole kill chain rather than just the round.
// Called from the perception scan as a second way of seeing.
//
// Order dependency, deliberate: perception runs inside decide() at the
// skill's cadence, hunt() runs every tick after it, so the emitter hunt()
// set last tick is what admits contacts this tick. hunt() defaults to
// SEARCH whenever the bot is armed and targetless, so acquisition
// bootstraps rather than deadlocking.
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

// counterfire is ruling 9 (settled 2026-08-14): a withholding tier being
// forced defensive takes the one shot it currently has before committing
// to the notch. The measured alternative was dying with a full rack — the
// deep tiers' commit range arrives later than an aggressor's first
// credible shot, and a defending bot never fires (the machine lost the
// twenty-seed mid-ladder to the ace 7-11 by exactly this, and every
// opening-depth dial was measured and refuted). The shot is DLZ-judged at
// the full working envelope — the round arrives if he flies on as now —
// floored only by the physics, and leaves through the same fox3() as any
// other; the momentary STT collapses back to search immediately and the
// round coasts to its own pitbull, exactly as an abandoned track does.
// Full rack only: the counter-shot exists to stop a bot dying without
// ever firing, and that is the whole licence — once anything has left the
// rails, normal doctrine owns the fight. The first form latched once per
// threat EPISODE, and every fresh inbound round is a fresh episode:
// sustained duels ping-ponged counter-shots, every pairing spent 45-48 of
// 48 (the battery's dump gate tripped), the ladder's end pair collapsed
// 5-1 to 2-2, and the attacker in the defence instrument converted
// nothing at all — measured 2026-08-14, same day as the ruling.
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
	// supersonic — the ladder's own kill bar, asked of the threat
	// mid-flight. Without it the novice's max-range spray bought the
	// machine's one licensed round for free: the end pairing's draws
	// tripled while every sprayed husk died six kilometres out anyway.
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

// hunt is the whole of a bot's BVR offence, run every tick from think()
// between decide and steer: hold the emitter state everyone else reads,
// take the DLZ-judged shot through the same fox3() a human trigger
// reaches, and bend the flight path into the crank while an own round
// wants datalink. There is no arbitrary range rule on the trigger
// (settled 2026-08-13): release is judged by the ladder at any range, and
// the only hard floor is Zone.Minimum, which is physics.
func (i *instance) hunt(slot int, a *craft, tick uint64) {
	b := a.brain
	if i.defend(slot, a, tick) {
		// Defence owns the flight path; the radar drops to search (or to
		// silence with an empty rack) — a beaming defender cannot hold an
		// STT anyway, and fly_radar lets an abandoned round coast on
		// memory. Behaviour 3 outranks every offensive overlay, except the
		// one counter-shot ruling 9 lets leave first.
		i.counterfire(slot, a, tick)
		if a.amraams > 0 && i.missiles {
			a.emitter, a.lock = 1, -1
		} else if a.emitter != 0 {
			a.emitter, a.lock = 0, -1
		}
		return
	}
	if i.weapons != "open" {
		// Out of context: a fox2 or guns match never radiates (today's
		// exact behaviour, and the configuration every dogfight battery
		// pins). The gate is the EQUIPMENT — the weapons class — not the
		// rounds remaining: a Winchester bot keeps its radar, its picture,
		// and its closing doctrine, or the pair goes blind at nineteen
		// kilometres and the fight never ends (measured in the seam test).
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
			// Investigate the last contact. A mutual defence drops both
			// radars into each other's notch at once — both pictures go
			// stale together — and the course a defence ends on points at
			// the beam, not the fight: holding it, the pair opened from 19
			// to 48 km and never re-committed (measured). Three minutes of
			// memory turns shot-defend-recommit into the cycle it should be.
			to := b.contact.Subtract(a.model.State.Position)
			if to.Length() > 1 {
				b.aim = soar(to.Normalize(), a.model.State.Position.Y)
				b.g = 2
			}
		} else if b.mode == "cruise" {
			// Hold the lane. With nothing known at all, cruise walks every
			// bot onto the same patrol track — measured in the BVR joust
			// as a 105 km tail-chase that never closed inside radar reach.
			// An armed searcher flies the course it is on (the joust
			// spawns it pointed at the enemy; an open room's wrap brings
			// contacts to the scan) instead of wandering.
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
		// The LEARN half of the look: two rounds have already died against
		// this target's defence, and the ladder cannot see why (its rungs
		// measure arrival speed, not a 15 m fringe pass against a dragging
		// tail-chase — the stern marginality the physics deliberately
		// keeps). Hold what remains for the merge instead of feeding the
		// same defence the rest of the rack: measured, the machine spent
		// all four on a dragging novice, fringe-damaged it, and drew.
		spaced = false
	}
	if b.skill.library >= 2 {
		// Shoot-LOOK-shoot: the disciplined tiers watch a round to its
		// pitbull before committing the next — the look is the whole
		// point of the spacing. Without it every tier rippled its rack in
		// the opening exchange (measured: 48 of 48 spent in every battery
		// pairing) and the fights were decided by whoever went Winchester
		// more usefully. The novice keeps the ripple: that spray is the
		// authentic incomplete toolkit.
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

	// The crank: while an own round is in midcourse against a target still
	// beyond the merge seam, offset the nose toward the gimbal edge —
	// closure drops, the lock survives, and the round keeps its datalink.
	// The same geometry serves behaviour 4, spike awareness: a disciplined
	// bot approaching under a hostile STT flies the offset before any
	// round is in the air, spoiling the shooter's radial while keeping its
	// own scan on him. Inside the seam the tuned WVR arbiter owns the
	// flight path and this override yields (a which-system-flies boundary,
	// not a weapons rule: the trigger above remains live at any range).
	if span > radar_seam && b.skill.library >= 2 {
		// The crank arrives with the pilot's syllabus — the novice keeps
		// pressing straight after launch, which is both the authentic
		// incomplete toolkit and what keeps its own geometry honest: a
		// novice that (wrongly) cranked between its ripple shots crossed
		// for minutes on end, and the disciplined opponent — correctly
		// refusing the crossing shot — never fired at all (measured).
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

// poise is the per-level defensive execution character: the four dials of
// the skill model (settled 2026-08-13). The syllabus is knowledge-uniform
// from the pilot up — beam, dispense, re-square — and execution separates
// the levels: how late the notch entry comes (the reaction delay rides
// skill.react), how far off square the beam sits, how often it is
// re-squared as the line of sight rotates, and how often the whole defence
// simply fails and reverts to a drag (the seeded lapse). The novice keeps
// today's incomplete toolkit — drag, stare, panic-dispense — as the
// baseline.
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
// same moment a human's RWR calls MISSILE (the round past its Active call).
// Returns true while the defence owns the flight path. The pilot-and-up
// syllabus is beam-then-dispense — radial velocity into the seeker's
// clutter notch, chaff blooming while inside it — degraded by the four
// dials; a lapsed engagement, and every novice one, is the drag that loses
// to the physics. The machine adds the emission trade: jammer armed while
// the round is far (home-on-jam is survivable when the geometry can still
// be spoiled), quiet at the terminal call.
func (i *instance) defend(slot int, a *craft, tick uint64) bool {
	b := a.brain
	var threat *missile
	span := math.MaxFloat64
	for _, m := range i.flying {
		if m.radar == nil || m.target != slot || m.radar.Phase < round.Active {
			continue
		}
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
		if a.flared > 0.8 {
			b.drop = true
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
	if a.flared > 1.4 {
		b.drop = true // the steady program: a fresh bloom inside the notch window
	}
	if b.skill.machine {
		b.jam = span > guard_quiet // armed while spoofable, quiet at the terminal call
	}
	return true
}

// Mochi world: Bot intelligence — one brain, degraded by skill
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Every skill level flies the SAME brain through the same airframe and FCS as
// a player — levels differ only in perception latency, decision cadence, the
// maneuver library unlocked, and execution precision. No stat cheats: guns
// are nose-traced by the host, so a sharper bot simply points better.

package air

import (
	"math"

	"world/games/air/battle"
	"world/games/air/flight"
)

// skill is one row of the ladder.
type skill struct {
	delay      float64 // perception latency, s: a contact's track refreshes no faster than this
	cadence    uint64  // ticks between decisions
	wander     float64 // aim noise, radians, refreshed each decision
	pull       float64 // max commanded g
	library    int     // maneuver tier unlocked (1..4)
	discipline float64 // missile-launch patience, 0..1
	react      float64 // reaction delay to an inbound missile, s
	open       float64 // gun opening range, m
}

// wander is the whole-flying imprecision, not just gunnery: a rookie flies
// 5-6° off the optimal line and cannot hold smooth g (see the wobble in
// decide) — that, not the maneuver library, is most of what a ladder feels like.
var skills = map[string]skill{
	"rookie":  {delay: 1.0, cadence: 30, wander: 0.10, pull: 5.5, library: 1, discipline: 0.2, react: 2.0, open: 900},
	"pilot":   {delay: 0.6, cadence: 20, wander: 0.045, pull: 6.5, library: 2, discipline: 0.5, react: 1.2, open: 700},
	"veteran": {delay: 0.35, cadence: 12, wander: 0.018, pull: 7.5, library: 3, discipline: 0.8, react: 0.7, open: 600},
	"ace":     {delay: 0.15, cadence: 8, wander: 0.007, pull: 7.5, library: 4, discipline: 1.0, react: 0.4, open: 550},
}

// layer is the server's view of a client cloud preset. base/top/cover MUST
// track the CLOUDS table in apps/air/web/src/game/engine.ts (which points
// back here) — drift means bots see through decks players think are solid.
type layer struct{ base, top, cover float64 }

var layers = map[string]layer{
	"cumulus":      {base: 600, top: 2400, cover: 0.42},
	"high_stratus": {base: 1829, top: 2134, cover: 0.78},
	"low_stratus":  {base: 152, top: 460, cover: 0.85},
}

// glare is the sun direction, fixed for merge fairness — mirrors TOD in
// engine.ts (the moon takes the same slot at night, but glare only blinds by day).
var glare = flight.Vec3{Y: 0.866, Z: 0.5}

// track is the last-seen picture of one contact. Perception latency lives
// here: the track refreshes no faster than the skill's delay, so a rookie
// fights a picture up to a second old.
type track struct {
	when     uint64 // tick last refreshed
	position flight.Vec3
	velocity flight.Vec3
}

// brain is the per-bot fight state. The decision layer runs at the skill's
// cadence and writes the command set; steer() turns it into Inputs every tick.
type brain struct {
	skill    skill
	mode     string // cruise, form, offense, defense, neutral, evade, and the named maneuvers below
	target   int    // slot, -1 none
	decided  uint64 // last decision tick
	known    map[int]*track
	prey     *track  // the target's track at decision time (steer aims/fires against it)
	distance float64 // to the target at decision time
	aim      flight.Vec3
	g        float64
	throttle float64
	reheat   float64
	brake    float64
	shoot    bool       // guns solution may be attempted this period
	loose    bool       // one-shot missile request, consumed by think()
	drop     bool       // one-shot flare request
	offset   [2]float64 // this period's aim wander components
	jink     uint64     // tick to re-roll the jink direction
	phase    float64    // current jink roll phase
	missiles int
	alert    uint64  // tick an inbound missile was first noticed (react delay runs from here)
	noticed  map[uint64]bool // inbound rounds already sighted (launch flash or the corner of the eye)
	judged   map[uint64]bool // rounds whose one launch-sighting roll has been taken
	plan     string  // the circle game plan chosen at the merge: "one" or "two" (held ~12 s; re-deciding every cadence is no plan at all)
	planned  uint64  // tick the plan was chosen
	side     float64 // which side the current threat/target sits (sign of the lateral LOS) — a flip while defensive is the reversal cue
	rolling  uint64  // rolling-scissors phase start; 0 = not rolling
	sense    float64 // committed roll direction while the aim is beyond ±140° (atan2 flips sides chaotically there)
	hold     uint64  // maneuver commitment: decisions re-evaluate but keep the aim until this tick (a yo-yo that flickers per decision is no yo-yo)
	stuck    int     // consecutive decisions of neutral non-progress (stalemate detector)
	tangle   int     // consecutive decisions locked in close combat (scissors detector)
	saddle   int     // consecutive defensive decisions with the attacker established behind (spiral gate: transients must not trigger a committed spiral)
	solo     bool    // section tactics OFF: fly pure individual BFM even with a team (the #138 pair-versus-pair control group)
	mate     int     // assigned section partner's slot (#140), -1 unpaired — set once at roster creation, stable across respawns
	spoke    uint64  // tick of the last brevity call (#139): one voice, one call at a time, never a chat storm
	told     int     // target already announced with ENGAGED (#139), -1 none — the call is an edge, not a repeat
	rolled   float64 // last roll input: the command is slew-limited so the executor cannot flap the stick
}

// mind builds a brain for a fighting level, or nil for drone/unknown.
func mind(level string) *brain {
	s, found := skills[level]
	if !found {
		return nil
	}
	return &brain{skill: s, mode: "cruise", target: -1, mate: -1, told: -1, known: map[int]*track{}, missiles: 2}
}

// reborn resets the per-life state after a respawn.
func (b *brain) reborn() {
	b.mode, b.target, b.missiles, b.alert = "cruise", -1, 2, 0
	b.saddle = 0
	b.told = -1
	b.prey = nil
	b.known = map[int]*track{}
}

// visible reports whether me can see other right now: visual range (halved at
// night), the canopy blind wedge, up-sun glare, and cloud-layer occlusion.
func (i *instance) visible(me, other *craft, tick uint64) bool {
	s, o := &me.model.State, &other.model.State
	direction, distance := i.bearing(s.Position, o.Position)
	reach := 12000.0
	if i.night {
		reach = 6000
	}
	if distance > reach {
		return false
	}
	body := s.Attitude.Unrotate(direction)
	if body.X < -0.35 && body.Y < 0.25 {
		return false // the blind wedge behind and below the canopy
	}
	if !i.night && o.Position.Y > s.Position.Y && direction.Dot(glare) > 0.996 {
		return false // up-sun, within ~5° of the disc
	}
	if l, found := layers[i.sky]; found {
		low, high := math.Min(s.Position.Y, o.Position.Y), math.Max(s.Position.Y, o.Position.Y)
		if over := math.Min(high, l.top) - math.Max(low, l.base); over > 0 {
			depth := over / math.Max(high-low, 1) * distance // LOS length inside the layer
			block := l.cover * clamp(depth/300, 0, 1)
			pair := uint64(me.player.Slot)*131 + uint64(other.player.Slot)
			if battle.Roll(i.environment.Seed, pair, tick/120) < block {
				return false // stable per 2 s bucket: contacts stay lost, not strobing
			}
		}
	}
	return true
}

// corner approximates the airframe's corner speed at the current weight and
// altitude: the 1 g stall (the same CLmax≈1.55 the carrier maths uses) scaled
// by √n. ISA troposphere density inline — flight's air() is package-private.
func corner(m *flight.Model) float64 {
	mass := m.Airframe.Mass.Empty + m.State.Fuel
	density := 1.225 * math.Pow(math.Max(1-2.2558e-5*m.State.Position.Y, 0.3), 4.2559)
	stall := math.Sqrt(2 * mass * 9.81 / (density * 1.55 * m.Airframe.Reference.Area))
	return stall * math.Sqrt(m.Airframe.Limit.Positive)
}

// think runs one bot for one tick: decide at the skill's cadence, steer every
// tick, and hand the one-shot weapon requests to the instance.
func (i *instance) think(slot int, a *craft, tick uint64) {
	b := a.brain
	if b.decided == 0 || tick-b.decided >= b.skill.cadence {
		b.decided = tick
		i.decide(slot, a, tick)
	}
	a.latest = b.steer(a.model, tick)
	// The fire drill (#130, deferred from #78): engine fires feed on throttle
	// and starve at idle (battle.Advance) — chopping the power IS the drill,
	// and it overrides every plan except a live missile evade (twenty seconds
	// of fire loses to three seconds of missile).
	if (a.condition.Fire[0] > 0 || a.condition.Fire[1] > 0) && b.mode != "evade" {
		a.latest.Throttle, a.latest.Reheat = 0, 0
	}
	// Shot discipline (teams, #130): never fire through a teammate's line —
	// hold whenever a friendly sits inside the stream's corridor, nearer than
	// the target. The trigger comes back by itself as the geometry clears.
	if a.latest.Fire && a.team != "" {
		s := &a.model.State
		bore := s.Attitude.Rotate(flight.Vec3{X: 1})
		for _, other := range i.slots() {
			mate := i.aircraft[other]
			if other == slot || mate == nil || !mate.alive || mate.model == nil || mate.team != a.team {
				continue
			}
			direction, span := i.bearing(s.Position, mate.model.State.Position)
			if span > b.distance+300 {
				continue // beyond the target: the burst is spent before it reaches him
			}
			if miss := math.Acos(clamp(bore.Dot(direction), -1, 1)) * span; miss < 60 {
				a.latest.Fire = false
				break
			}
		}
	}
	if b.loose {
		b.loose = false
		if i.missiles && i.free() && b.missiles > 0 {
			// Missile shot discipline (#141): the seeker head has no IFF — it
			// locks the best heat source in the cone whoever owns it. Checked
			// at the moment of launch, not request (a decision-old request may
			// be stale): decline while the seeker would acquire a teammate,
			// and the request comes back by itself once the geometry clears.
			if locked := i.acquire(slot, a); locked >= 0 && hostile(a, i.aircraft[locked]) {
				before := len(i.flying)
				i.launch(slot, a)
				if len(i.flying) > before && !i.cheat.ammunition {
					b.missiles--
				}
			}
		}
	}
	if b.drop {
		b.drop = false
		a.flared = 0
		i.events = append(i.events, map[string]any{"kind": "flare", "slot": slot})
	}
}

// decide refreshes the picture and picks the maneuver. Runs at the skill's cadence.
func (i *instance) decide(slot int, a *craft, tick uint64) {
	b := a.brain
	me := &a.model.State

	// Refresh tracks — no faster than the skill's perception delay. Being hit
	// reveals the shooter; close-aboard tracers reveal a firing attacker.
	stale := uint64(b.skill.delay * 60)
	for _, other := range i.slots() {
		c := i.aircraft[other]
		if other == slot || c == nil || !c.alive || c.model == nil {
			continue
		}
		if !hostile(a, c) {
			continue // teammates are never tracks: their picture arrives by radio (read fresh from the instance where the section tactics need it)
		}
		if t, found := b.known[other]; found && tick-t.when < stale {
			continue
		}
		seen := i.visible(a, c, tick)
		if !seen {
			if a.condition.Damager == other && a.condition.Damaged < 2 {
				seen = true // his rounds are the introduction
			} else if c.latest.Fire {
				direction, distance := i.bearing(c.model.State.Position, me.Position)
				if distance < 1500 && direction.Dot(c.model.State.Attitude.Rotate(flight.Vec3{X: 1})) > 0.985 {
					seen = true // tracers flashing past
				}
			}
		}
		if seen {
			b.known[other] = &track{when: tick, position: c.model.State.Position, velocity: c.model.State.Velocity}
		}
	}
	// The radio (#140): an engaged pair partner calls his target — the wing
	// fights from the section's picture, not just his own eyes, so the pair
	// enters every fight together. Pair-scoped: a human lead has no track
	// table to share.
	if b.mate >= 0 && !b.solo {
		if mate := i.aircraft[b.mate]; mate != nil && mate.alive && mate.brain != nil && mate.brain.target >= 0 {
			if called, found := mate.brain.known[mate.brain.target]; found {
				if mine, exists := b.known[mate.brain.target]; !exists || called.when > mine.when {
					heard := *called
					b.known[mate.brain.target] = &heard
				}
			}
		}
	}
	for s, t := range b.known { // forget the dead, the departed, and the long-lost
		if c := i.aircraft[s]; c == nil || !c.alive || tick-t.when > 15*60 {
			delete(b.known, s)
			if b.target == s {
				b.target = -1
			}
		}
	}

	// Target selection: nearest seen contact, weighted against dogpiles,
	// with 30% hysteresis before switching. In a team fight the dogpile
	// count is MY side's (sorting targets across the section), and an enemy
	// established on a teammate outranks everything nearer — the sandwich:
	// the threatened wingman's problem is the section's problem, and a human
	// teammate is defended exactly like a bot one.
	attackers := map[int]int{}
	for _, other := range i.slots() {
		if c := i.aircraft[other]; c != nil && c.bot && c.brain != nil && c.brain.target >= 0 {
			if a.team == "" || b.solo || c.team == a.team {
				attackers[c.brain.target]++
			}
		}
	}
	menacing := map[int]int{} // attacker slot -> the teammate he is running on
	danger, closest := -1, math.MaxFloat64
	if a.team != "" && !b.solo {
		for s, t := range b.known {
			for _, other := range i.slots() {
				mate := i.aircraft[other]
				if other == slot || mate == nil || !mate.alive || mate.model == nil || mate.team != a.team {
					continue
				}
				to, span := i.bearing(t.position, mate.model.State.Position)
				if span < 2200 && t.velocity.Normalize().Dot(to) > 0.92 {
					menacing[s] = other // nose committed on my teammate, in range: he is running an attack (a loose 0.8 cone flagged anyone merely flying this way); slots() order puts humans first, so a human victim wins the record
					// The BREAK call (#139): a human teammate with an attacker
					// established close behind his 3/9 — where his own eyes are
					// weakest — gets warned by name. Nearest attacker only, so
					// a two-bandit picture is one call, not a shouting match.
					if !mate.bot && span < 1800 && span < closest {
						if body := mate.model.State.Attitude.Unrotate(to.Scale(-1)); body.X < -0.2 {
							danger, closest = s, span
						}
					}
					break
				}
			}
		}
	}
	if danger >= 0 && (b.spoke == 0 || tick-b.spoke > 300) {
		victim := i.aircraft[menacing[danger]]
		if i.warned == nil {
			i.warned = map[int]uint64{}
		}
		if last, nagged := i.warned[menacing[danger]]; !nagged || tick-last > 300 { // one BREAK per victim per five seconds, whoever calls it
			side := "left"
			if at, _ := i.bearing(victim.model.State.Position, b.known[danger].position); victim.model.State.Attitude.Unrotate(at).Z > 0 {
				side = "right" // break INTO the attack: toward the side he is coming from
			}
			b.spoke = tick
			i.warned[menacing[danger]] = tick
			i.events = append(i.events, map[string]any{"kind": "call", "slot": slot, "call": "break", "direction": side, "target": menacing[danger]})
		}
	}
	best, cost := -1, math.MaxFloat64
	for s, t := range b.known {
		_, distance := i.bearing(me.Position, t.position)
		weight := distance * float64(1+attackers[s])
		if _, found := menacing[s]; found {
			weight *= 0.3
		}
		if s == b.target {
			weight *= 0.7 // hysteresis: the current target holds unless beaten by 30%
		}
		if weight < cost {
			best, cost = s, weight
		}
	}
	b.target = best
	// The ENGAGED call (#139): committing onto an enemy who is attacking a
	// human teammate — the rescue the sandwich weighting just ordered is
	// invisible from the victim's cockpit unless someone says so.
	if victim, found := menacing[best]; found && best != b.told && (b.spoke == 0 || tick-b.spoke > 300) {
		if mate := i.aircraft[victim]; mate != nil && !mate.bot {
			b.spoke, b.told = tick, best
			i.events = append(i.events, map[string]any{"kind": "call", "slot": slot, "call": "engaged"})
		}
	}

	speed := me.Velocity.Length()
	pace := corner(a.model)
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})
	b.g, b.throttle, b.reheat, b.brake, b.shoot = b.skill.pull, 0.85, 0, 0, false

	// Wounded flying (#130, deferred from #78): the brain reads its own jet.
	// Shed structure caps the commanded g — the pilot can see pieces of the
	// wing missing, and the ultimate-load margin they carried is gone with them.
	if me.Damage.Loss > 0 {
		b.g = math.Min(b.g, 4.5)
	}

	// Inbound missile: the AIM-9 is passive — no warning tone, only eyes. The
	// launch plume is the visible moment: one aspect-weighted sighting roll
	// per round, with the blind wedge behind the canopy covered only by
	// check-six discipline. A round unseen at launch is nearly smokeless in
	// the coast and is only caught late, at a discipline-scaled slant. Once
	// sighted, the skill's reaction delay runs as before: flares, cold
	// engines, and an orthogonal break. Trumps everything but the floor.
	inbound := flight.Vec3{}
	threatened := false
	if len(b.judged) > 64 { // rounds despawn and numbers only grow: reset rather than leak (a live round re-rolls once, harmlessly)
		b.noticed, b.judged = nil, nil
	}
	for _, m := range i.flying {
		if m.target != slot {
			continue
		}
		direction, distance := i.bearing(me.Position, m.position)
		if b.judged == nil {
			b.noticed, b.judged = map[uint64]bool{}, map[uint64]bool{}
		}
		if !b.judged[m.number] {
			b.judged[m.number] = true
			body := me.Attitude.Unrotate(direction)
			sight := 0.6 + 0.4*b.skill.discipline
			if body.X < -0.35 && body.Y < 0.25 {
				sight = 0.7 * b.skill.discipline // launched from the blind wedge: only lookout discipline catches the flash
			}
			if battle.Roll(i.environment.Seed, uint64(slot), m.number, 51) < sight {
				b.noticed[m.number] = true
			}
		}
		if !b.noticed[m.number] && distance < 500+1000*b.skill.discipline {
			b.noticed[m.number] = true // the corner of the eye, late
		}
		if b.noticed[m.number] && distance < 4500 {
			threatened, inbound = true, direction
			break
		}
	}
	if threatened {
		if b.alert == 0 {
			b.alert = tick
		}
		if float64(tick-b.alert) >= b.skill.react*60 {
			b.mode = "evade"
			side := me.Attitude.Rotate(flight.Vec3{Z: 1})
			if inbound.Dot(side) > 0 {
				side = side.Scale(-1)
			}
			b.aim = side.Subtract(inbound.Scale(0.4)).Normalize() // break across the seeker, away from it
			b.reheat = 0                                          // burner doubles the decoy's job
			b.throttle = 1
			b.drop = a.flared > 0.9 // re-flare through the evade
			i.guard(b, me, corner(a.model))
			return
		}
	} else {
		b.alert = 0
	}

	// Doctrine flares: an enemy assessed on my six inside the 9M envelope and
	// I cannot watch him — keep decoy coverage up so the launch I cannot see
	// meets a fresh flare. The cadence is the flare's own coverage window (a
	// lapse is exactly the gap an unseen round flies through); whether the
	// pilot actually flies the doctrine at each lapse is his lookout
	// discipline, so an ace keeps near-continuous coverage and a rookie
	// almost never thinks of it.
	if !threatened && a.flared > flare_window {
		blind := uint64(b.skill.delay*60) * 2
		for s, t := range b.known {
			direction, distance := i.bearing(me.Position, t.position)
			if distance > 3000 {
				continue
			}
			body := me.Attitude.Unrotate(direction)
			if body.X > -0.2 {
				continue // ahead of my 3/9 line: I can watch him and flare on the flash instead
			}
			if tick-t.when < blind {
				continue // still fresh eyes on him (a high six is visible over the shoulder)
			}
			if battle.Roll(i.environment.Seed, uint64(slot), uint64(s), tick/uint64(flare_window*60), 52) < b.skill.discipline {
				b.drop = true
			}
			break
		}
	}

	// Crippled (#130, deferred from #78): most of the thrust gone, or a fuel
	// fire on its fuse — the fight is over. Extend LOW AND FAST away from the
	// nearest known threat: altitude is a bank the engines can no longer
	// refill, so it gets spent as speed (the guard keeps the floor). A
	// straight-line cripple is a free kill, so a lazy jink rides under the
	// extension while anyone is close. Overrides any held maneuver.
	if thrust := 1 - (me.Damage.Engine[0]+me.Damage.Engine[1])/2; thrust < 0.35 || a.condition.Burning {
		b.mode = "limp"
		b.prey = nil
		b.shoot, b.loose = false, false
		away, gap := level(me.Velocity.Normalize()), math.MaxFloat64
		for _, t := range b.known {
			if d, span := i.bearing(me.Position, t.position); span < gap {
				away, gap = d.Scale(-1), span
			}
		}
		if mate := i.nearest_mate(slot, a); mate != nil && !b.solo {
			toward, span := i.bearing(me.Position, mate.model.State.Position)
			if span < 15000 && toward.Dot(away) > -0.3 {
				away = away.Add(toward.Scale(0.6)).Normalize() // limp toward friends: home is where the section is
			}
		}
		b.aim = flight.Vec3{X: away.X, Y: clamp(away.Y, -0.25, 0), Z: away.Z}.Normalize()
		b.g = 2
		b.throttle, b.reheat = 1, 1 // whatever thrust remains (think()'s fire drill takes it back while an engine burns)
		if gap < 1200 {
			if tick >= b.jink {
				b.phase = battle.Roll(i.environment.Seed, uint64(slot), tick) * 2 * math.Pi
				b.jink = tick + 50 + uint64(battle.Roll(i.environment.Seed, uint64(slot)+7, tick)*40)
			}
			up := me.Attitude.Rotate(flight.Vec3{Y: 1})
			side := me.Attitude.Rotate(flight.Vec3{Z: 1})
			b.aim = b.aim.Scale(1.2).Add(up.Scale(0.5 * math.Cos(b.phase))).Add(side.Scale(0.5 * math.Sin(b.phase))).Normalize()
			b.g = 4
		}
		i.guard(b, me, pace)
		return
	}

	// Maneuver commitment: the aim stands until the hold expires — only the
	// fire solution keeps updating underneath it.
	if tick < b.hold && b.target >= 0 {
		if prey, found := b.known[b.target]; found {
			b.prey = prey
			_, b.distance = i.bearing(me.Position, prey.position)
		}
		return
	}

	// Too slow to fight: unload, nose down a little, burner, rebuild to
	// corner — a stalled zoom otherwise floats for tens of seconds.
	if speed < 0.55*pace {
		b.mode = "rebuild"
		b.prey = nil
		flat := flight.Vec3{X: me.Velocity.X, Z: me.Velocity.Z}.Normalize()
		b.aim = flat.Subtract(flight.Vec3{Y: 0.25}).Normalize()
		b.g, b.throttle, b.reheat = 1.4, 1, 1
		if speed < 70 {
			// Nearly stopped (tail-sit or flat fall): PUSH — commanding positive
			// g just holds the stalled alpha. Idle until the nose falls through
			// and the airflow comes back, then light everything.
			b.g, b.throttle, b.reheat = -2, 0.2, 0 // full forward stick: g=0 maps to a homeopathic push that never breaks the stall
			b.aim = flight.Vec3{X: flat.X * 0.4, Y: -0.9, Z: flat.Z * 0.4}.Normalize()
		}
		return
	}

	// No target: cruise. A wingman holds combat spread off his lead (#140) —
	// line abreast, 1.5 km — instead of the solo weave, so the section arrives
	// at every fight together with the engaged/support split already standing,
	// and re-forms on the lead after each kill (a crippled lead limping home
	// collects an escort the same way). Leads, solo-flagged bots, and single
	// bots with no human to follow keep the free weave.
	if b.target < 0 {
		b.prey = nil
		if lead := i.leader(slot, a); lead != nil {
			b.mode = "form"
			him := &lead.model.State
			ahead := him.Velocity.Normalize()
			if him.Velocity.Length() < 1 {
				ahead = him.Attitude.Rotate(flight.Vec3{X: 1})
			}
			abeam := ahead.Cross(flight.Vec3{Y: 1})
			if abeam.Length() < 0.1 {
				abeam = him.Attitude.Rotate(flight.Vec3{Z: 1}) // lead pointed straight up or down: his wings still define a side
			}
			abeam = abeam.Normalize()
			if out, _ := i.bearing(him.Position, me.Position); out.Dot(abeam) < 0 {
				abeam = abeam.Scale(-1) // hold my own side: no cross-unders
			}
			station := him.Position.Add(abeam.Scale(1500))
			direction, span := i.bearing(me.Position, station)
			near := clamp(span/1200, 0, 1) // far: fly at the station; close: fly the lead's heading and let the station drift in
			mixed := ahead.Scale(1 - near).Add(direction.Scale(near))
			if mixed.Length() < 0.1 {
				mixed = ahead // overran the station dead ahead: the blend cancels — hold heading and let the throttle law drop me back
			}
			b.aim = mixed.Normalize()
			b.g = 3 // station keeping is not a limiter business
			want := him.Velocity.Length() + clamp((span-150)*0.05, -40, 80)
			b.throttle = clamp(0.55+(want-speed)*0.01, 0.25, 1)
			b.reheat = 0
			if span > 3000 {
				b.reheat = 1 // rejoin: cut the corner in burner
			}
			i.guard(b, me, pace)
			return
		}
		b.mode = "cruise"
		weave(slot, a, tick)
		return
	}

	prey := b.known[b.target]
	b.prey = prey
	age := float64(tick-prey.when) / 60
	spot := prey.position
	if b.skill.library >= 2 {
		spot = spot.Add(prey.velocity.Scale(age)) // lead the stale track; the rookie chases where he WAS
	}
	direction, distance := i.bearing(me.Position, spot)
	b.distance = distance
	chase := prey.velocity.Normalize()
	tail := direction.Dot(chase) // 1 = square behind him, -1 = head-on
	closure := me.Velocity.Subtract(prey.velocity).Dot(direction)
	mine := me.Position.Y + speed*speed/19.62
	theirs := prey.position.Y + prey.velocity.Length()*prey.velocity.Length()/19.62

	// Defensive check: a known contact behind my 3/9 inside 2 km, nose on me.
	menace, gap := -1, 2000.0
	for s, t := range b.known {
		to, span := i.bearing(t.position, me.Position)
		if span < gap && me.Attitude.Unrotate(to.Scale(-1)).X < -0.2 && t.velocity.Normalize().Dot(to) > 0.6 {
			menace, gap = s, span
		}
	}

	// Section flying (teams, #130): the engaged/supporting split. When a
	// CLOSER teammate is already fighting my target and the target is busy
	// with him — not menacing anyone, not nose-on me — piling in just fouls
	// the teammate's fight (and his line of fire). Fly SUPPORT instead: an
	// energy perch above and behind the fight, ready to convert the instant
	// the picture changes (the target threatening my teammate is the
	// sandwich, which the selection weighting turns into an immediate attack;
	// the target coming nose-on me makes it my fight through `tail`).
	_, sandwich := menacing[b.target]
	if a.team != "" && !b.solo && menace < 0 && b.target >= 0 && !sandwich && tail > -0.2 {
		engaged := false
		for _, other := range i.slots() {
			mate := i.aircraft[other]
			if other == slot || mate == nil || !mate.alive || mate.model == nil || mate.team != a.team {
				continue
			}
			if _, span := i.bearing(mate.model.State.Position, spot); span < math.Min(0.75*distance, 2200) {
				engaged = true
				break
			}
		}
		if engaged && distance < 6000 {
			b.mode = "support"
			b.shoot = false
			perch := spot.Subtract(chase.Scale(1100)).Add(flight.Vec3{Y: 500})
			if distance < 1300 {
				out, _ := i.bearing(spot, me.Position)
				perch = me.Position.Add(out.Scale(600)).Add(flight.Vec3{Y: 300}) // too close: open out, never through the fight
			}
			b.aim, _ = i.bearing(me.Position, perch)
			b.g = math.Min(b.g, 4)
			b.throttle, b.reheat = 1, boost(speed, pace, 60) // the perch is an energy bank
			i.guard(b, me, pace)
			return
		}
	}

	switch {
	case menace >= 0 && (b.target != menace || tail < 0.35): // he's the problem: fight him
		b.mode = "defense"
		foe := b.known[menace]
		at, span := i.bearing(me.Position, foe.position)
		b.throttle, b.reheat = 1, boost(speed, pace, -80)
		// A defensive fight is still a guns duel: the break and the scissors
		// cross his nose through yours — take the snapshot when it appears.
		b.shoot = true
		b.prey = foe
		b.distance = span
		// Cloud escape (tier 3+): a reachable layer is a blindfold the pursuer
		// cannot see through — dive or climb into it, then turn hard inside.
		// The visibility model does the rest: his track of us goes stale.
		if l, found := layers[i.sky]; found && b.skill.library >= 3 && span > 450 {
			mid := (l.base + l.top) / 2
			if math.Abs(me.Position.Y-mid) < 1300 && mid > 700 {
				b.mode = "shroud"
				away := at.Scale(-1)
				b.aim = flight.Vec3{X: away.X, Y: clamp((mid-me.Position.Y)*0.002, -0.5, 0.5), Z: away.Z}.Normalize()
				b.throttle, b.reheat = 1, boost(speed, pace, -40)
				if me.Position.Y > l.base && me.Position.Y < l.top {
					// Inside: hard turn while he's blind — come out somewhere else.
					side := me.Attitude.Rotate(flight.Vec3{Z: 1})
					b.aim = flight.Vec3{X: side.X, Y: 0.05, Z: side.Z}.Normalize()
					b.hold = tick + 150
				}
				i.guard(b, me, pace)
				return
			}
		}
		// The reversal cue (tier 3+): the attacker's lateral side FLIPPING
		// while he's close means he crossed my flight path — reverse the turn
		// into him NOW (the scissors entry), don't keep the old break.
		flank := math.Copysign(1, me.Velocity.Normalize().Cross(at).Y)
		if b.skill.library >= 3 {
			if b.side != 0 && flank != b.side && span < 700 && tick-b.rolling > 240 && tick > b.hold+240 { // (tangle never accumulates while defensive — the defense case returns before that counter)
				// Reverse once per genuine overshoot — a scissors flips sides
				// every weave, and reversing each flip churns the energy away.
				b.mode = "reverse"
				b.aim = level(at)
				b.brake = clamp((speed-0.9*pace)/80, 0, 1)
				b.hold = tick + 90
				b.side = flank
				return
			}
			b.side = flank
		}
		// The ROLLING scissors (tier 4): a flat scissors that will not resolve
		// (locked close, no closure) goes three-dimensional — barrel around
		// his flight path, boards out, force him out front.
		if b.skill.library >= 4 && span < 500 && speed < 0.85*pace && b.tangle > int(900/b.skill.cadence) {
			// (the rolling scissors belongs to an already-slow lock — rolling
			// while fast just hands the angles over)
			if b.rolling == 0 {
				b.rolling = tick
			}
			phase := float64(tick-b.rolling) / 60 * 1.6 // one barrel every ~4 s
			up := me.Attitude.Rotate(flight.Vec3{Y: 1})
			out := up.Scale(math.Cos(phase)).Add(me.Attitude.Rotate(flight.Vec3{Z: 1}).Scale(math.Sin(phase)))
			b.mode = "rolling"
			b.aim = at.Scale(0.55).Add(out.Scale(0.85)).Normalize()
			b.g = math.Min(b.g, 4.5)
			b.throttle, b.reheat, b.brake = 0.5, 0, clamp((speed-0.8*pace)/60, 0, 1)
			b.hold = tick + uint64(b.skill.cadence) // re-evaluate each cadence: the phase must keep turning
			return
		}
		b.rolling = 0
		// Drag when spent (tier 2+, #130): a break at mush speed is a tracking
		// gift — the turn neither defeats his solution nor keeps the corner.
		// Unload away from him, burner, slightly downhill, and rebuild to
		// fighting speed before offering another angle. Only with real
		// separation: inside 900 m an extension hands him the saddle and the
		// break stays mandatory however slow it is.
		if b.skill.library >= 2 && speed < 0.68*pace && span > 900 {
			b.mode = "drag"
			away := at.Scale(-1)
			// Drag-AND-BAG (teams): the extension bends toward the nearest
			// living teammate — the pursuer gets dragged across a friendly
			// nose instead of into empty sky.
			if mate := i.nearest_mate(slot, a); mate != nil && !b.solo {
				toward, span := i.bearing(me.Position, mate.model.State.Position)
				if span < 10000 && toward.Dot(away) > -0.3 { // never a reversal INTO the pursuer just to reach a friend
					away = away.Add(toward.Scale(0.8)).Normalize()
				}
			}
			b.aim = flight.Vec3{X: away.X, Y: -0.08, Z: away.Z}.Normalize()
			b.throttle, b.reheat = 1, 1
			b.shoot = false
			b.hold = tick + 120
			i.guard(b, me, pace)
			return
		}
		// His solution proximity, read from the track: the velocity vector is
		// his nose for this purpose. POINTED means a gun solution is imminent;
		// ESTABLISHED means he is saddled behind but not yet on — and it must
		// PERSIST (the saddle counter) before it justifies a committed spiral:
		// a one-decision transient during an overshoot is the reversal's
		// moment, and a spiral hold taken there masks the flank flip.
		on := foe.velocity.Normalize().Dot(at.Scale(-1))
		pointed := on > 0.985 && span < 1100
		if on > 0.90 {
			b.saddle++
		} else {
			b.saddle = 0
		}
		switch {
		case b.skill.library >= 4 && span < 700 && foe.velocity.Length() > speed+60:
			b.mode = "scissors" // he's overshooting hot: brakes out, reverse into him
			b.brake = 1
			b.aim = at
		case b.skill.library >= 3 && span < 900 && (pointed || b.skill.library < 4):
			// Guns jink: irregular out-of-plane rolls off the break, re-rolled
			// on a deterministic clock so it can't be learned. Tier 4 TIMES the
			// break off his gun solution — jinking while his nose is off just
			// spends the energy the fight will be decided by (tier 3 jinks
			// whenever he's close, wasteful and authentic).
			if tick >= b.jink {
				b.phase = battle.Roll(i.environment.Seed, uint64(slot), tick) * 2 * math.Pi
				b.jink = tick + 40 + uint64(battle.Roll(i.environment.Seed, uint64(slot)+7, tick)*35)
			}
			up := me.Attitude.Rotate(flight.Vec3{Y: 1})
			side := me.Attitude.Rotate(flight.Vec3{Z: 1})
			b.aim = me.Velocity.Normalize().Scale(0.4).Add(up.Scale(math.Cos(b.phase))).Add(side.Scale(math.Sin(b.phase))).Normalize()
		case b.skill.library >= 3 && on > 0.90 && b.saddle > 2 && span < 1400 && me.Position.Y > 2300:
			// DEFENSIVE SPIRAL (#130): saddled but not yet shot at, with altitude
			// in the bank — nose-low maximum-rate descending turn. Gravity pays
			// for the rate his level pursuit cannot match without overshooting,
			// and the guard flattens it before the floor.
			b.mode = "spiral"
			b.aim = flight.Vec3{X: at.X, Y: -0.5, Z: at.Z}.Normalize()
			b.throttle, b.reheat = 0.8, 0
			b.hold = tick + 150
		default:
			b.aim = level(at) // break INTO him at corner speed
		}
		i.guard(b, me, pace)
		return
	case tail > 0.35: // offensive: behind his 3/9
		b.mode = "offense"
		b.saddle = 0
		b.shoot = true
		// PURE pursuit: with a hitscan gun, pipper-on-target is both the chase
		// and the terminal solution. The intercept-lead variant predated the
		// hitscan discovery and chased phantom points off weaving targets.
		b.aim = direction
		// Sun exploitation (tier 4, day): while still approaching, drift the
		// run-in toward the up-sun station — his own glare model blinds him
		// to anything within ~5° of the disc, and we built that model.
		if b.skill.library >= 4 && !i.night && distance > 1500 {
			station := spot.Add(glare.Scale(distance * 0.2))
			toward, _ := i.bearing(me.Position, station)
			if toward.Dot(me.Velocity.Normalize()) > 0.75 { // nearly free, never a detour — the first cut drifted every approach into a delay
				b.aim = toward
			}
		}
		b.throttle, b.reheat = 1, boost(speed, pace, 40)
		if distance < 1500 {
			// Closure discipline into the control zone: arrive with 40-ish
			// overtake, not a hundred — a blown pass wastes the whole conversion.
			b.throttle = clamp(1-(closure-40)/200, 0.35, 1)
			b.reheat = boost(speed, pace, -60)
		}
		if nose.Dot(direction) < 0.3 && distance < 1500 {
			// He's far off the nose IN CLOSE: a turnaround, not a chase — fly
			// it at corner, boards out when hot, or the circle balloons for
			// miles. At range, keep the knots: the chase needs them.
			b.throttle, b.reheat = 0.5, 0
			if b.skill.library >= 3 && speed > pace*1.15 {
				b.brake = 1
			}
		}
		switch {
		case b.skill.library >= 4 && closure > 120 && distance < 400:
			// QUARTER PLANE: about to blow through — pull up and across into
			// the vertical behind him; the overshoot becomes a perch, not a
			// role swap.
			b.mode = "quarter"
			b.aim = direction.Scale(0.4).Add(flight.Vec3{Y: 1}).Normalize()
			b.g *= 0.9
			b.throttle, b.reheat = 0.55, 0
			b.brake = 1
			b.hold = tick + 120
		case b.skill.library >= 4 && closure > 70 && distance > 400 && distance < 1100 && tail > 0.2 && tail < 0.75:
			// LAG DISPLACEMENT ROLL: angles-hot on a crossing target — roll
			// out-of-plane around his turn, arrive back in lag with the
			// closure spent as geometry instead of an overshoot.
			if b.rolling == 0 || tick-b.rolling > 200 {
				b.rolling = tick
			}
			phase := float64(tick-b.rolling) / 60 * 1.3
			up := me.Attitude.Rotate(flight.Vec3{Y: 1})
			out := up.Scale(math.Cos(phase)).Add(me.Attitude.Rotate(flight.Vec3{Z: 1}).Scale(math.Sin(phase)))
			b.mode = "roll"
			b.aim = direction.Scale(0.5).Add(out.Scale(0.85)).Normalize()
			b.g = math.Min(b.g, 4.5)
			b.throttle, b.reheat = 0.55, 0
			b.hold = tick + uint64(b.skill.cadence)
		case b.skill.library >= 3 && closure > 90 && distance < 1200 && tail < 0.85:
			// (dead-astern overtake is the closure discipline's job — boards,
			// not vertical excursions that blow the approach every pass)
			// High yo-yo: pull up out of plane, spend closure as height —
			// committed for two seconds or it is just a twitch.
			b.aim = direction.Add(flight.Vec3{Y: 0.5}).Normalize()
			b.g *= 0.8
			b.throttle, b.reheat = 0.6, 0
			b.hold = tick + 120
			if b.skill.library >= 4 {
				b.brake = clamp((closure-150)/150, 0, 1)
			}
		case b.skill.library >= 2 && closure < -30 && closure > -140 && tail < 0.85 && direction.Y < 0.2:
			// Low yo-yo from trail: cut inside and below his TURNING circle —
			// the cut needs a crossing target. Against a straight runner
			// (dead-astern, big opening) it just lags the chase into the dirt;
			// and never against one climbing away above.
			b.aim = direction.Subtract(flight.Vec3{Y: 0.35}).Add(chase.Scale(-0.2)).Normalize()
		case b.skill.library >= 2 && distance > 700 && tail < 0.85 && closure > -40:
			b.aim = direction.Add(chase.Scale(-0.25)).Normalize() // lag: hold the control zone on a CROSSING target — lagging a runner points behind him forever
		}
	case tail < -0.35: // neutral: converging head-on
		b.mode = "neutral"
		b.saddle = 0
		// The face shot: rookies spray it (authentic), the disciplined decline
		// it and fight the turn instead — training doctrine, and it keeps the
		// merge a fight rather than a coin toss.
		b.shoot = distance < b.skill.open && (tail > -0.7 || b.skill.discipline < 0.6)
		b.throttle, b.reheat = 1, 1
		switch {
		case b.skill.library >= 4 && mine-theirs > 500 && me.Position.Y < 7000:
			b.aim = direction.Add(flight.Vec3{Y: 1}).Normalize() // take it vertical on an energy edge
		case b.skill.library >= 3 && b.plan == "one" && tick-b.planned < 720:
			// Flying the one-circle plan: tight, lift vector ON him — the
			// radius fight converts at the second pass, not by rate. Tight
			// means just under corner, never mushing.
			b.aim = level(direction)
			b.throttle, b.reheat = clamp(0.6+(0.88*pace-speed)*0.01, 0.5, 1), 0
		case b.skill.library >= 3 && mine < theirs-300:
			b.aim = level(direction) // energy-poor without a plan: fight radius anyway
			b.throttle, b.reheat = 0.8, 0
		default:
			b.aim = level(direction) // two-circle rate fight at corner
			b.reheat = boost(speed, pace, -30)
		}
		// The LEAD TURN: begin the pull ~2 s before the pass so the post-merge
		// angle is a quarter-circle, not a 12-second, 4 km turnaround — without
		// it every merge is one-pass-haul-ass forever and nobody ever guns.
		if closure > 0 && distance < math.Max(600, closure*2.0) {
			pass := math.Copysign(1, me.Velocity.Normalize().Cross(direction).Y) // his passing side
			// The game plan (tier 3+): TWO-circle (rate fight — turn toward his
			// side, fight at corner in burner) with the energy to rate; ONE-
			// circle (radius fight — turn across the pass, slow and tight,
			// denying his nose) when slower or poorer. Held, not re-rolled.
			turn := pass
			if b.skill.library >= 3 {
				if mine < theirs-400 {
					b.plan, turn = "one", -pass // a REAL energy deficit: deny his rate game
				} else {
					b.plan = "two"
				}
				b.planned = tick
			}
			sin, cos := math.Sin(turn*1.3), math.Cos(turn*1.3)
			b.aim = flight.Vec3{X: direction.X*cos - direction.Z*sin, Y: 0.05, Z: direction.X*sin + direction.Z*cos}.Normalize()
			b.throttle, b.reheat = 0.7, 0 // corner the pull, don't rocket past it
			if b.plan == "one" {
				b.throttle = 0.75 // the radius fight is tighter, not powerless — half throttle at a merge just donates the energy
			}
		}
	default: // flanking: lead-turn into his future
		b.mode = "offense"
		b.saddle = 0
		b.shoot = true
		b.aim = direction.Add(prey.velocity.Scale(2.0 / math.Max(distance, 200))).Normalize()
		b.throttle, b.reheat = 1, boost(speed, pace, 0)
		// BARREL ROLL ATTACK (tier 4): a fast beam crossing converts over the
		// top — roll up and behind his line instead of honouring the flat
		// lead turn's closure problem.
		if b.skill.library >= 4 && closure > 50 && distance > 600 && distance < 1500 && speed > pace && mine > theirs+200 {
			// (the roll over the top is paid for in energy — only with an edge)
			perch := spot.Subtract(chase.Scale(distance * 0.3)).Add(flight.Vec3{Y: distance * 0.4})
			b.mode = "barrel"
			b.aim, _ = i.bearing(me.Position, perch)
			b.g = math.Min(b.g, 5.5)
			b.throttle, b.reheat = 0.8, 0
			b.hold = tick + 140
		}
	}

	// The scissors lock: thirty seconds of close combat without a kill means
	// neither can convert — disengage, rebuild energy, force a fresh merge
	// where the conversion edges actually express (tier 3+).
	if distance < 900 {
		b.tangle++
	} else if distance > 2500 {
		b.tangle = 0
	}
	if b.skill.library >= 3 && b.tangle > int(1800/b.skill.cadence) {
		b.mode = "reset"
		b.aim = level(direction.Scale(-1))
		b.throttle, b.reheat = 1, 1
		b.shoot = false
		b.hold = tick + 600 // ten committed seconds of extension
		b.tangle = 0
		return
	}

	// Stalemate displacement (tier 3+): a mutual circle between equal jets
	// never resolves by rate — after ~8 s without progress, cut ACROSS the
	// circle on a lag line toward his six, committed for three seconds.
	if (b.mode == "neutral" || b.mode == "offense") && distance > 800 && distance < 3000 && math.Abs(closure) < 60 {
		b.stuck++
	} else {
		b.stuck = 0
	}
	if b.skill.library >= 3 && b.stuck > int(480/b.skill.cadence) {
		b.mode = "displace"
		lag := spot.Subtract(prey.velocity.Normalize().Scale(distance * 0.5)).Subtract(flight.Vec3{Y: distance * 0.2})
		b.aim, _ = i.bearing(me.Position, lag)
		b.throttle, b.reheat = 0.8, 0
		b.hold = tick + 200
		b.stuck = 0
	}

	// Energy bookkeeping (tier 4): neutral-ish and clearly poorer — extend,
	// rebuild, come back with the advantage.
	if b.skill.library >= 4 && b.mode == "neutral" && theirs-mine > 800 && distance > 1500 {
		b.mode = "extend"
		b.aim = level(direction.Scale(-1))
		b.throttle, b.reheat = 1, 1
		b.shoot = false
	}

	// Inside gun range the aim is the LEAD POINT: rounds fly real time of
	// flight now, so the bore belongs where the target WILL be — his velocity
	// carries him across the flight, my own velocity rides on every round,
	// and gravity pulls the round the whole way. This mirrors battle.Burst's
	// solution exactly (a bot that aims at the man himself misses every
	// crosser, which is precisely the deflection game). In the control zone,
	// SADDLE: kill the closure and hold the track.
	if b.shoot && b.prey != nil && distance < b.skill.open*1.4 {
		time := distance / math.Max(battle.Muzzle+closure, 200)
		lead := spot.Add(prey.velocity.Scale(time)).
			Subtract(me.Velocity.Scale(time)).
			Add(flight.Vec3{Y: 4.9 * time * time})
		b.aim, _ = i.bearing(me.Position, lead)
		if direction.Dot(nose) > 0.94 && tail > 0.2 {
			b.mode = "saddle"
			b.g = math.Min(b.g, 4) // tracking is a 2 g business: staying far off the g-limiter keeps the demand out of the boundary-trim regime (#131), whose faster integration rattles fine corrections
			b.throttle = clamp(0.7-closure*0.006, 0.2, 1) // match his speed, sit in the zone
			b.reheat = 0
			if closure > 90 && b.skill.library >= 3 {
				b.brake = 1
			}
			b.hold = tick + 45 // stay on the track: churn is what breaks gun solutions
		}
	}

	// Corner discipline (tier 3+): pulling the full limiter while slow just
	// bleeds the jet — scale the commanded g by the speed margin. Rookies
	// keep yanking; that bleed is authentic.
	if b.skill.library >= 3 {
		// At corner you pull the LIMIT — that's what corner speed is for. The
		// discipline only eases the stick once genuinely slow; the first cut
		// made the ace out-turned by the rookie's artless yank.
		b.g = 1 + (b.g-1)*clamp((speed/pace-0.35)/0.4, 0.6, 1)
	} else {
		// The low tiers cannot hold smooth g: the pull wobbles on a slow
		// deterministic rhythm — bursts of yank, moments of mush.
		b.g *= 0.55 + 0.45*battle.Roll(i.environment.Seed, uint64(slot)+41, tick/90)
	}
	// The aero cap, every tier: never command far past what the wing gives at
	// this speed — beyond it the demand rides the alpha limiter, thrust feeds
	// induced drag, and the jet mushes at 130 m/s in full burner forever.
	stall := pace / math.Sqrt(a.model.Airframe.Limit.Positive)
	b.g = math.Min(b.g, math.Max(0.85*(speed/stall)*(speed/stall), 1.1))

	// Missile request: the launch gates with discipline-scaled margin. The
	// disciplined SAVE their missiles for rear-aspect close shots — the ones
	// the victim's flare reaction cannot beat — instead of feeding flares at
	// the merge like everyone's first sortie.
	if b.missiles > 0 && b.shoot && (b.skill.discipline < 0.7 || (tail > 0.3 && distance < 2600)) {
		margin := 0.87 + 0.06*b.skill.discipline
		limit := missile_range * (0.4 + 0.6*math.Max(0, tail)) * (0.45 + 0.4*b.skill.discipline)
		if distance < limit && nose.Dot(direction) > margin {
			b.loose = true
		}
	}

	i.guard(b, me, pace)

	// Fuel discipline (tier 2+): at BINGO (3,000 lb) the burner is rationed;
	// at FUEL LO (1,600 lb) the fight is over — fly home level and economical.
	// Rookies never look down: they run the tanks dry and become gliders.
	if b.skill.library >= 2 {
		if a.model.State.Fuel < 726 {
			b.mode = "bingo"
			b.aim = level(me.Velocity.Normalize())
			b.throttle, b.reheat = 0.5, 0
			b.shoot, b.loose = false, false
		} else if a.model.State.Fuel < 1361 {
			b.reheat = 0
		}
	}

	// The aim wander: where this pilot's nose actually points. Re-rolled on a
	// slow clock, NOT per decision — sloppiness is a consistent bias, which is
	// exactly why sloppy pilots are easy to track and gun, while per-decision
	// noise had made even the rookie untrackable to an ace.
	b.offset[0] = (battle.Roll(i.environment.Seed, uint64(slot)+13, tick/150) - 0.5) * 2 * b.skill.wander
	b.offset[1] = (battle.Roll(i.environment.Seed, uint64(slot)+29, tick/150) - 0.5) * 2 * b.skill.wander
}

// guard applies the terrain safety clamps to the decided aim: flat fighting
// near the deck, and the climb-angle budget against the falling leaf. Every
// decide() exit passes through it — the missile evade once returned early and
// aimed breaks into the sea.
func (i *instance) guard(b *brain, me *flight.State, pace float64) {
	speed := me.Velocity.Length()
	if me.Position.Y < 1500 && b.aim.Y < 0.12 {
		b.aim = flight.Vec3{X: b.aim.X, Y: 0.12, Z: b.aim.Z}.Normalize() // PN missiles chase bots into harder low breaks: keep the deck fights gently climbing
	}
	if lid := clamp(speed/pace-0.6, 0.12, 1.0); b.aim.Y > lid {
		b.aim = flight.Vec3{X: b.aim.X, Y: lid, Z: b.aim.Z}.Normalize()
	}
}

// level flattens a direction toward the horizon — break turns live in the
// horizontal plane unless doctrine says otherwise.
func level(direction flight.Vec3) flight.Vec3 {
	return flight.Vec3{X: direction.X, Y: clamp(direction.Y, -0.15, 0.25), Z: direction.Z}.Normalize()
}

// boost decides reheat for a target speed around corner: light the burner
// below (corner + offset), hold it off above.
func boost(speed, pace, offset float64) float64 {
	if speed < pace+offset {
		return 1
	}
	return 0
}

// steer converts the brain's command set into FCS inputs. Runs every tick.
func (b *brain) steer(m *flight.Model, tick uint64) flight.Inputs {
	s := &m.State
	speed := math.Max(s.Velocity.Length(), 1)
	aim, want := b.aim, b.g

	// The floor overrides everything: wings level, maximum pull, burner.
	// Recovery height for the current dive at ~6.5 g, plus a hard 800 m gate.
	sink := -s.Velocity.Y / speed
	loss := 0.0
	if sink > 0 {
		radius := speed * speed / (6.5 * 9.81)
		loss = radius * (1 - math.Sqrt(math.Max(0, 1-sink*sink)))
		if s.Attitude.Rotate(flight.Vec3{Y: 1}).Y < 0.2 {
			loss *= 1.8 // rolled past the horizon: the recovery must roll upright before the pull exists
		}
	}
	if (s.Position.Y < 900 && s.Velocity.Y < 0) || s.Position.Y-loss*3.0 < 400 { // 3.0×: the unloaded roll to upright eats altitude before the ideal-g pull exists
		flat := flight.Vec3{X: s.Velocity.X, Z: s.Velocity.Z}.Normalize()
		aim = flat.Add(flight.Vec3{Y: 0.3}).Normalize()
		want = m.Airframe.Limit.Positive
		if speed < 80 {
			want = -2 // stalled: pulling deepens it; push hard through and fly out
		}
		b.fireHold()
		return b.compose(m, aim, want, 1, 1, 0, false, tick)
	}

	// Aim wander: the skill's imprecision, as a pointing error.
	side := aim.Cross(flight.Vec3{Y: 1}).Normalize()
	rise := side.Cross(aim).Normalize()
	aim = aim.Add(side.Scale(b.offset[0])).Add(rise.Scale(b.offset[1])).Normalize()

	fire := false
	if b.shoot && b.prey != nil {
		fire = b.solution(m, tick)
	}
	return b.compose(m, aim, want, b.throttle, b.reheat, b.brake, fire, tick)
}

func (b *brain) fireHold() { b.shoot = false }

// solution decides the trigger: the nose within tolerance of the target's
// CURRENT position (extrapolated from the possibly-stale track) — the gun is
// a hitscan, so there is no bullet time to lead.
func (b *brain) solution(m *flight.Model, tick uint64) bool {
	s := &m.State
	if b.distance > b.skill.open {
		return false
	}
	age := float64(tick-b.prey.when) / 60
	spot := b.prey.position.Add(b.prey.velocity.Scale(age))
	direction := spot.Subtract(s.Position).Normalize()
	nose := s.Attitude.Rotate(flight.Vec3{X: 1})
	miss := math.Acos(clamp(nose.Dot(direction), -1, 1)) * math.Max(b.distance, 50)
	return miss < 22+b.skill.wander*b.distance*1.5 // snapshot tolerance: the burst is a stream, not a bullet; sloppier noses spray more and hit less
}

// compose turns an aim direction and a g demand into stick, through the same
// UA law a player flies: stick = (g − level)/(ceiling − level), rolled so the
// lift vector carries the pull toward the aim.
func (b *brain) compose(m *flight.Model, aim flight.Vec3, want, throttle, reheat, brake float64, fire bool, tick uint64) flight.Inputs {
	s := &m.State
	speed := math.Max(s.Velocity.Length(), 1)
	// Roll error in the VELOCITY frame: current lift vector vs the pull
	// direction the aim demands, both perpendicular to the flight path. The
	// body-frame solution wobbled with every nose bobble and never settled.
	vhat := s.Velocity.Normalize()
	perp := aim.Subtract(vhat.Scale(aim.Dot(vhat)))
	if perp.Length() < 0.05 {
		perp = flight.Vec3{Y: 1}.Subtract(vhat.Scale(vhat.Y)) // aligned: pull toward up
	}
	perp = perp.Normalize()
	up := s.Attitude.Rotate(flight.Vec3{Y: 1})
	lift := up.Subtract(vhat.Scale(up.Dot(vhat))).Normalize()
	roll := math.Atan2(lift.Cross(perp).Dot(vhat), lift.Dot(perp)) // + = roll right (verify by trace: sign errors are the house specialty)
	if math.Abs(roll) > 2.45 {
		if b.sense == 0 {
			b.sense = math.Copysign(1, roll)
		}
		roll = b.sense * math.Abs(roll) // near-opposite: either way works — commit or the sign flaps
	} else if math.Abs(roll) < 1.5 {
		b.sense = 0
	} else if b.sense != 0 {
		roll = b.sense * math.Abs(roll)
	}
	// Pull persists off-plane (the vector still bends toward the aim);
	// starving it entirely just flies the jet into the sea stick-centred.
	plane := clamp(0.35+0.65*math.Cos(roll), 0, 1)
	if math.Abs(roll) > 2.2 {
		plane = math.Min(plane, 0.1) // roll first when nearly inverted to the solution
	}
	plane *= clamp(1-math.Abs(s.Omega.X)/3.5, 0.3, 1) // ease the pull under carried roll rate — that coupling departs the jet
	body := s.Attitude.Unrotate(aim)
	ahead := math.Acos(clamp(body.X, -1, 1))
	if ahead < 0.03 {
		// Settled: soften, but NEVER fade to zero — the old damper parked the
		// nose in a 3-6° standoff orbit around the aim, permanently outside
		// the gun gate, and the ace never fired a round.
		plane *= math.Max(ahead/0.03, 0.35)
		roll *= math.Max(ahead/0.03, 0.35)
	}
	level := clamp(math.Hypot(s.Velocity.X, s.Velocity.Z)/speed, 0, 1) // cos γ, the 1 g trim the law interpolates from
	ceiling := m.Airframe.Limit.Positive
	floor := -want // scale symmetric: forward stick interpolates level→Limit.Negative in the law
	pitch := clamp((want*plane-level)/math.Max(ceiling-level, 0.5), -1, 1)
	if want < 0.5 {
		pitch = clamp((want-level)/3.5, -1, 0) // pushes bypass the lift-plane gate: recovery, not pursuit
	}
	_ = floor
	b.rolled += clamp(clamp(roll*1.4, -1, 1)-b.rolled, -0.12, 0.12) // slew: full deflection over ~8 ticks, never a flap
	return flight.Inputs{
		Pitch:      pitch,
		Roll:       b.rolled,
		Throttle:   throttle,
		Reheat:     reheat,
		Speedbrake: brake,
		Fire:       fire,
	}
}

// weave is the drone: the original closed-loop wander — bank tracks a slow
// slot-staggered rhythm, pitch holds a per-slot altitude, throttle holds speed.
func weave(slot int, a *craft, tick uint64) {
	s := &a.model.State
	up := s.Attitude.Rotate(flight.Vec3{Y: 1})
	right := s.Attitude.Rotate(flight.Vec3{Z: 1})
	bank := math.Atan2(right.Y, up.Y)
	t := float64(tick) / 60
	phase := float64(slot) * 2.399
	lean := 0.35 * math.Sin(t*0.03+phase)
	height := 3200 + float64(slot%40)*60
	speed := s.Velocity.Length()
	a.latest = flight.Inputs{
		Throttle: clamp(0.55+(200-speed)*0.01, 0.3, 1),
		Roll:     clamp((bank-lean)*1.5, -0.5, 0.5), // positive stick rolls right = NEGATIVE bank in the atan2(right.Y, up.Y) convention
		Pitch:    clamp((height-s.Position.Y)*4e-4-s.Velocity.Y*4e-3+math.Abs(bank)*0.15, -0.3, 0.5),
	}
}

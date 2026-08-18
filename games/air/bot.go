// Mochi world: Bot intelligence — one brain, degraded by skill
// Copyright © 2026 Mochisoft OÜ
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
	"sort"

	"world/games/air/battle"
	"world/games/air/flight"
	"world/games/air/round"
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
	trigger    float64 // the shot's PRICE (#235): the minimum walk-through chance this pilot fires a burst at. Low is loose — the novice hoses at 2%, which is authentic; the pilot is disciplined but under-confident at 12%; the top tiers deliberately take the calculated snapshot (6% / 3%) because against a manoeuvring target the converged solution never comes and expected damage beats expected nothing. (Kept from #215: this axis is DECOUPLED from wander — precision and willingness were once one number and lethality ran backwards up the ladder.)
	commit     float64 // MINIMUM seconds a defensive or energy manoeuvre runs before another may replace it (#206)
	floor      float64 // speed below which energy recovery outranks the fight, m/s; 0 = never worries about it (#206)
	energy     float64 // how fully this pilot prices energy RELATIVE to the opponent, 0..1 (#248): the novice chases the nose and ignores it (authentic), the pilot half-understands, the instructor tiers fight the differential — which is what makes zooming off a floater score as winning
	geometry   float64 // how fully this pilot reads the opponent's turn circle, 0..1 (#248): being 30 degrees off-nose from INSIDE his circle is winning, the same angles outside are losing; angles-and-range scoring cannot tell the two apart
	machine    bool    // no human factors at all (the superhuman tier): every flight-model limit stays, every perception/reaction/discipline limit goes
}

// wander is the whole-flying imprecision, not just gunnery: a novice flies
// 5-6° off the optimal line and cannot hold smooth g (see the wobble in
// decide) — that, not the maneuver library, is most of what a ladder feels like.
// commit and floor invert what cadence accidentally encoded (#206). A human
// beat the ace from a couple of turns in, and the recording showed why: under
// pressure the ace re-decided every 1.6 s, alternating break-turn with
// energy-rebuild — two goals that each need seconds to pay off — so it
// achieved neither and never generated separation. Deciding FAST is a skill
// (noticing, tracking, shooting: that is cadence, unchanged). Abandoning a
// manoeuvre fast is the opposite of one, so commitment now RISES with tier,
// and the better pilots refuse to be slow at all. A novice still flails and
// gets slow — that is the novice's flaw, and it should stay authentic.
//
// Retuned 2026-07-30 (#215), measured against TestDroneKill (12 seeds, can the
// tier finish a compliant target) with the firing gate decoupled from aim:
//   - trigger is the new axis; wander no longer forbids the shot it aims.
//   - ace cadence 8 -> 12: re-deciding every 0.13 s suppressed conversion (4/12
//     kills; at 12 it matches the veteran's 7 with a faster kill). Deliberate
//     decisions beat twitchy ones — the same lesson as the commitment floor.
//   - ace open 550 -> 600: the shortest engagement range on the ladder belonged
//     to the tier meant to convert most.
//   - pilot wander 0.045 -> 0.035, open 700 -> 600: the old pilot killed NOTHING
//     (0/12) — it fired from ranges its own aim noise could not serve. 4/12 now.
//   - pull UN-inverted: the rookie now commands the limiter (5.5 -> 7.5) like a
//     real novice — the joystick baseline showed a self-described novice at
//     full aft stick 43% of a fight, never unloading. What tiers is what
//     always did: the low tiers' pull WOBBLES and never eases when slow, so
//     the rookie yanks itself into the mush (the aero cap bounds it) while the
//     high tiers hold corner. Skill is energy discipline, not stick authority.
//   - library 4 KEPT for the ace although dropping it measured 8/12: tier 4 is
//     the rolling scissors, the sun, the bag and the rope — anti-fighter tools a
//     drone cannot exercise — and deleting doctrine to win one seed of a drone
//     metric is tuning the instrument. The ladder reads 1/4/7/7 by kills, the
//     ace faster by time-to-kill.
//
// The ladder (2026-08-04 restructure): novice / pilot / ace / superhuman.
// Novice carries the old rookie tuning verbatim; pilot moved about a third of
// the way toward the removed veteran (sharper eyes, quicker decisions, a
// tighter trigger, better energy sense) while keeping the tier-2 repertoire —
// a good line pilot, not an instructor; ace is unchanged; superhuman is the
// machine: every flight-model limit, zero human factors — its row's delay,
// wander, and react are genuinely zero, and the machine flag switches off the
// perception, discipline, and trigger-cadence humanities the code applies
// beyond this table.
var skills = map[string]skill{
	"novice":     {delay: 1.0, cadence: 30, wander: 0.10, pull: 7.5, library: 1, discipline: 0.2, react: 2.0, open: 900, trigger: 0.02, commit: 1.0, floor: 0},
	"pilot":      {delay: 0.5, cadence: 16, wander: 0.030, pull: 7.2, library: 2, discipline: 0.6, react: 1.0, open: 600, trigger: 0.12, commit: 2.3, floor: 105, energy: 0.5},
	"ace":        {delay: 0.15, cadence: 12, wander: 0.007, pull: 7.5, library: 4, discipline: 1.0, react: 0.4, open: 600, trigger: 0.06, commit: 4.0, floor: 154, energy: 1, geometry: 1},
	"superhuman": {delay: 0, cadence: 1, wander: 0, pull: 7.5, library: 4, discipline: 1.0, react: 0, open: 700, trigger: 0.03, commit: 4.0, floor: 154, energy: 1, geometry: 1, machine: true}, // commit stays ace-grade: strategy re-judged at 1.6 s like the ace — the machine edge is reflex and precision, and a half-second commit just flipped between near-tied lines and finished none of them
}

// commitment is the manoeuvre set that must be flown through rather than
// re-decided: each of these needs seconds to pay off, and abandoning one
// halfway is worse than never starting it.
var commitment = map[string]bool{"defense": true, "rebuild": true, "reverse": true, "drag": true,
	"spiral": true, "rolling": true, "scissors": true, "zoom": true, "rope": true, "extend": true}

// settle commits the manoeuvre just chosen for this pilot's commitment time.
func (b *brain) settle(tick uint64) { b.settled = tick + uint64(b.skill.commit*60) }

// surveyed returns the known contact slots in ascending order. Every decision
// loop walks b.known through this, never by ranging the map: Go randomises map
// order per range, so a min-pick with a strict < broke ties by whichever
// contact happened to come first — and the symmetric spawn ring makes exact
// ties real. Two identical runs of the section sweep disagreed on who died
// (#225), which meant every band in the doctrine battery was fitted against
// noise the size of its own effect.
func (b *brain) surveyed() []int {
	order := make([]int, 0, len(b.known))
	for s := range b.known {
		order = append(order, s)
	}
	sort.Ints(order)
	return order
}

// elapsed reports whether `since` ticks have passed since an event stamped at
// `when`. Zero means it never happened: these stamps are anti-churn debounces,
// and a bot is born with nothing to debounce. Read as plain arithmetic, birth
// counted as "reversed at tick 0" and left a several-second dead zone at spawn
// in which no overshoot could be answered at all.
func elapsed(tick, when, since uint64) bool { return when == 0 || tick-when > since }

// tactics is the shared tactical doctrine: the hand-picked constants the
// maneuver decisions gate on, extracted (#143) so the tuning battery can fly
// same-brain-one-number-different arms. Each brain carries a copy — a harness
// may amend one bot's doctrine without touching the roster. The skill ladder
// (library, cadence, wander, pull, discipline) is deliberately NOT here:
// tiers are a constraint the tuning respects, never a variable it moves.
type tactics struct {
	drag struct {
		pace float64 // drag-when-spent below this fraction of corner speed
		span float64 // ... and only beyond this separation, m (closer, the break is mandatory)
	}
	bag struct {
		reach float64 // drag-and-bag: bend toward a mate within this range, m
		bend  float64 // how hard the extension bends toward him
	}
	spiral struct {
		nose   float64 // attacker established: his velocity dotted on my line
		span   float64 // saddle range, m
		floor  float64 // minimum altitude to trade, m
		saddle int     // consecutive established decisions before committing
		hold   uint64  // committed ticks
	}
	jink struct {
		span   float64 // guns-defense range, m
		base   uint64  // ticks between re-rolls...
		spread uint64  // ...plus up to this many more, per the deterministic roll
	}
	high struct {
		closure float64 // high yo-yo: overtake past this, m/s
		span    float64 // inside this range, m
		tail    float64 // and not dead astern (tail below this)
		hold    uint64  // committed ticks
	}
	low struct {
		near float64 // low yo-yo: opening slower than this, m/s (negative)
		far  float64 // but not opening faster than this, m/s (negative)
		tail float64 // crossing target only (tail below this)
		rise float64 // never against one climbing away (LOS Y above this)
	}
	plan struct {
		deficit float64 // one-circle: energy height below his by this, m
	}
	lead struct {
		closure float64 // lead-turn range as a multiple of closure (seconds of arrival)
		floor   float64 // but never later than this range, m
		angle   float64 // the cut across his side, radians
		hold    uint64  // ticks the committed turn is flown through the pass
	}
	missile struct {
		tail   float64 // disciplined shooters demand at least this aspect
		span   float64 // ... inside this range, m
		margin float64 // nose-on-target gate at zero discipline...
		step   float64 // ...tightened by this per unit discipline
		base   float64 // envelope: head-on fraction of missile_range
		slope  float64 // ...growing by this at square rear aspect
		floor  float64 // envelope: fraction granted at zero discipline
		gain   float64 // ...growing by this at full discipline
	}
	sandwich struct {
		span   float64 // an enemy this close to a teammate, m
		nose   float64 // with his nose committed on him (velocity dot)
		weight float64 // outranks nearer targets by this factor
		reach  float64 // ... but only within this range of ME (#144): a rescue 20 km out is a lonely transit to a stale fight, not a rescue
	}
	support struct {
		span    float64 // fights farther than this are not mine to crowd, m
		share   float64 // engaged = a mate closer to my target than this share of my range
		engaged float64 // ... capped at this absolute range, m
		behind  float64 // perch: behind the fight along his track, m
		above   float64 // perch: above the fight, m
		near    float64 // too close to the fight: open out inside this, m
		out     float64 // ... by this much laterally, m
		rise    float64 // ... and this much up, m
		limit   float64 // g cap on the perch (an energy bank, not a fight)
	}
	form struct {
		abeam  float64 // combat spread: line abreast off the lead, m
		blend  float64 // fly-at-the-station beyond this off-station distance, m
		burner float64 // rejoin in reheat beyond this, m
	}
	press struct {
		span    float64 // advantage only counts inside this range, m
		hold    float64 // ticks of held advantage before pressing
		loose   float64 // guns tolerance multiplier while pressing (measured 1.0: wider only sprays — rounds trace the airframe, not the gate)
		closure float64 // overtake ceiling while pressing, m/s, scheduled down to zero at the finishing range
		gap     float64 // the finishing range the press parks at, m — half the range is four times the hit density
	}
	crowd struct {
		weight float64 // target-selection distance penalty per friendly already attacking a contact: the knob that spreads a section across a target-rich picture instead of perching behind one fight
	}
	rejoin struct {
		span  float64 // pair separation beyond which a distant fight waits, m
		fight float64 // a target inside this range is my fight NOW, rejoined or not, m
	}
	zoom struct {
		edge float64 // energy-height advantage that takes the merge upstairs, m
		roof float64 // no vertical takes above this altitude, m
		hold uint64  // commitment: an unheld zoom was a one-decision twitch
	}
	rope struct {
		edge float64 // rope-a-dope: my energy height over his by this, m
		near float64 // never inside this range (the break owns close defense), m
		far  float64 // entry ceiling, m
		nose float64 // only while he is NOT established (his velocity dot below this)
		hold uint64  // the climb he cannot follow, committed
	}
	bracket struct {
		span  float64 // two pairs running the same distant contact split beyond this range, m
		angle float64 // the junior pair's offset off the direct line, radians
	}
	wounded struct {
		weight float64 // target-selection discount for a visibly hurt contact (#144): the smoking, burning, wing-shy bird pulls the eye
	}
}

// standard is the doctrine every brain flies today: the defaults the tuning
// battery measures candidates against. Every value here was hand-picked with
// a reason a pilot would recognise; a candidate that beats one without such
// a reason is a bot-metagame artifact, not doctrine (#143).
func standard() tactics {
	var t tactics
	// drag.span 900 -> 720 (#145 sweep): the range inside which an extension
	// hands him the saddle, so the break stays mandatory. Measured at 200 seeds
	// the tighter threshold improves the section's survival edge over solo.
	t.drag.pace, t.drag.span = 0.68, 720
	t.bag.reach, t.bag.bend = 10000, 0.8
	t.spiral.nose, t.spiral.span, t.spiral.floor, t.spiral.saddle, t.spiral.hold = 0.90, 1400, 2300, 2, 150
	t.jink.span, t.jink.base, t.jink.spread = 900, 40, 35
	t.high.closure, t.high.span, t.high.tail, t.high.hold = 90, 1200, 0.85, 120
	t.low.near, t.low.far, t.low.tail, t.low.rise = -30, -140, 0.85, 0.2
	t.plan.deficit = 400
	t.lead.closure, t.lead.floor, t.lead.angle, t.lead.hold = 2.0, 600, 1.3, 100
	// step 0.06 -> 0 (#37, measured 2026-08-16). The nose gate was TIGHTENED by
	// discipline: 0.93 for the instructor tiers against 0.87 for a rookie —
	// 21.6 degrees where the seeker's own acquisition cone is 30
	// (missile_cone). So the better the pilot, the more shots it refused
	// INSIDE the cone its weapon can actually take, and the machine starved:
	// 0.86 s per fight in a legal launch envelope against the ace's 2.83, with
	// the nose gate the lone blocker for 17 s of every fight and range never
	// binding once. Discipline still earns its keep on the clause above it —
	// rear aspect, closer range — which is where shot quality lives, because
	// the flare roll is graded on ASPECT and not on how precisely the shooter
	// was pointed at release.
	t.missile.tail, t.missile.span, t.missile.margin, t.missile.step = 0.3, 2600, 0.87, 0
	t.missile.base, t.missile.slope, t.missile.floor, t.missile.gain = 0.4, 0.6, 0.45, 0.4
	t.sandwich.span, t.sandwich.nose, t.sandwich.weight, t.sandwich.reach = 2200, 0.92, 0.3, 10000
	t.support.span, t.support.share, t.support.engaged = 6000, 0.9, 2200 // share 0.75 -> 0.9 (2026-08-10 sweep): defer to a mate a little sooner, so the pair stops both-committing to the same target and trading two jets for one — section deaths fell 11% at flat kills across 80 seeds
	t.support.behind, t.support.above, t.support.near, t.support.out, t.support.rise, t.support.limit = 1100, 500, 1300, 600, 300, 4
	t.form.abeam, t.form.blend, t.form.burner = 1500, 1200, 3000
	// press.closure stays 45: raising it to 54 scored well on the battery
	// (~10 s and ~4 rounds off a gun kill) but FAILED TestBotSection - pressing
	// harder individually erased the pair's survival edge over two solos, and
	// paired with drag.span=720 it still scored below drag.span alone (#145).
	t.press.span, t.press.hold, t.press.loose, t.press.closure, t.press.gap = 1500, 300, 1.0, 45, 250
	// crowd.weight 1 -> 2 (2026-07-25, the trimmed-spawn recalibration): fixing
	// the inverted Level spawn attitude let gunnery connect (~3x the hit
	// events), and in that world a pair doubling up on one contact leaves free
	// bandits shooting at twice the old rate. Doubling the dogpile penalty
	// spreads the section across a target-rich picture instead. Measured:
	// veterans-v-pilots at 40 seeds goes 16 deaths/net +3 (weight 1) to 13/+6,
	// and the equal-tier A/B recovers its survival edge at every sweep tried
	// (11v15 / 27v28 / 52v55 red deaths at 12/24/48 seeds). Weight 3
	// over-rotates - the pair scatters and both edges invert (24 seeds:
	// net -24/28 deaths against solo's -17/25).
	// 2 -> 2.5 (2026-07-27, the pitch-washout recalibration): the tracking
	// damper sharpened everyone's gunnery again and the section's defensive
	// edge inverted (12 deaths v solo's 7 at 14 seeds); 2.5 restores it.
	// 2.75 already over-rotates on the equal tier (12 v 9). The equal-tier
	// A/B itself was red before the law change (13 v 12 after it, one death
	// from passing at 12 seeds) - that residual lives with the #206 doctrine
	// pass, not this constant.
	t.crowd.weight = 2.75 // 2.5 -> 2.75 (2026-08-04, the tier restructure): the mid-tier sweeps now fly the NUDGED pilot, whose tighter wander sharpened everyone's gunnery again — the same direction as both earlier steps; at 2.5 the equal-tier section lost its edge (deaths 50 v 47)
	t.rejoin.span, t.rejoin.fight = 4000, 10000
	t.zoom.edge, t.zoom.roof, t.zoom.hold = 500, 7000, 120
	t.rope.edge, t.rope.near, t.rope.far, t.rope.nose, t.rope.hold = 600, 700, 2000, 0.9, 180
	t.bracket.span, t.bracket.angle = 4500, 0.6
	t.wounded.weight = 0.6
	return t
}

// doctrine is the package default every mind() copies.
var doctrine = standard()

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
	swing    flight.Vec3 // velocity change per second between refreshes (#144): the turn the good shooters lead — a straight-line prediction lands every round behind a 6 g break
	nose     flight.Vec3 // his bore at the last sighting (#251): the planform cue — you can SEE where a close jet is pointing, and a bore swinging into lead on you is the shot forming
	wobble   float64     // jerk estimate, m/s^3: how fast his acceleration has been CHANGING. This is the target's unpredictability — a steady circle carries a small constant jerk (the acceleration vector rotates with the turn), a reversal spikes it an order of magnitude. The trigger prices its shot against it: a predictable target rewards waiting for a converged solution, an erratic one never converges and the snapshot is the only shot there is (#235).
	heard    bool        // the picture came over the radio (#146), not my own eyes — replaced by a real sighting, which is the TALLY moment
}

// orbit is the opponent's estimated turning circle — the object BFM is flown
// against. Estimated from two spaced track samples: the velocity swing gives
// turn rate and plane, |v|/omega the radius, and the centre sits perpendicular
// to his velocity in the turn plane. A near-straight target reports invalid
// and callers fall back to flight-path lag.
type orbit struct {
	centre flight.Vec3
	radius float64
	normal flight.Vec3 // unit angular-velocity direction: travel runs along normal x radial
	omega  float64     // rad/s
	valid  bool
}

// circle estimates the orbit from two samples of the same track.
func circle(p1, v1 flight.Vec3, t1 uint64, p2, v2 flight.Vec3, t2 uint64) orbit {
	dt := float64(t2-t1) / 60
	if dt < 0.15 || dt > 2.5 {
		return orbit{}
	}
	speed := v2.Length()
	if speed < 40 {
		return orbit{}
	}
	u1, u2 := v1.Normalize(), v2.Normalize()
	swing := math.Acos(clamp(u1.Dot(u2), -1, 1))
	omega := swing / dt
	if omega < 0.06 { // under ~3.5 deg/s: straight enough that lag beats geometry
		return orbit{}
	}
	normal := u1.Cross(u2)
	if normal.Length() < 1e-6 {
		return orbit{}
	}
	normal = normal.Normalize()
	radius := speed / omega
	inward := normal.Cross(u2).Scale(-1) // centripetal: toward the centre
	return orbit{
		centre: p2.Add(inward.Scale(radius)),
		radius: radius,
		normal: normal,
		omega:  omega,
		valid:  true,
	}
}

// behind returns the point ON the orbit a given arc behind the target's
// current position, pulled slightly inside the circle — the lag control point
// an attacker flies to, which MOVES with the target's turn (that motion is
// what makes flying at it a closed-loop pursuit of geometry, not of the jet).
func (o orbit) behind(target flight.Vec3, arc float64) flight.Vec3 {
	radial := target.Subtract(o.centre)
	planar := radial.Subtract(o.normal.Scale(radial.Dot(o.normal)))
	if planar.Length() < 1 {
		return target
	}
	u := planar.Normalize()
	w := o.normal.Cross(u) // travel direction at the target's station
	// Rotate the radial BACKWARD along travel by arc, and stand 10% inside.
	back := u.Scale(math.Cos(arc)).Subtract(w.Scale(math.Sin(arc)))
	return o.centre.Add(back.Scale(o.radius * 0.9))
}

// predict projects a track horizon seconds past its refresh. The curved form
// (tier 3+) bends the prediction along the observed turn — the difference
// between gunning a runner and gunning a fighter in a 6 g break.
func predict(t *track, horizon float64, curved bool) flight.Vec3 {
	spot := t.position.Add(t.velocity.Scale(horizon))
	if curved {
		spot = spot.Add(t.swing.Scale(0.5 * horizon * horizon))
	}
	return spot
}

// brain is the per-bot fight state. The decision layer runs at the skill's
// cadence and writes the command set; steer() turns it into Inputs every tick.
type brain struct {
	skill     skill
	tactics   tactics // per-brain copy of the doctrine (#143): the battery amends one bot's numbers without touching the roster
	mode      string  // cruise, form, offense, defense, neutral, evade, and the named maneuvers below
	target    int     // slot, -1 none
	decided   uint64  // last decision tick
	known     map[int]*track
	prey      *track  // the target's track at decision time (steer aims/fires against it)
	distance  float64 // to the target at decision time
	aim       flight.Vec3
	g         float64
	throttle  float64
	reheat    float64
	brake     float64
	shoot     bool       // guns solution may be attempted this period
	loose     bool       // one-shot missile request, consumed by think()
	drop      bool       // one-shot flare request
	bloom     bool       // one-shot chaff request (#43): the split programme spends the chaff magazine against a radar round and the flare magazine against a heater
	dispensed int        // countermeasures requested against the current threat episode: the programme's own count, reset when the threat clears
	offset    [2]float64 // this period's aim wander components
	bursting  uint64     // consecutive ticks of trigger: the burst governor (#206)
	walked    float64    // aim error at the last trigger judgement, m: a burst keeps firing while this is SHRINKING — the stream is being walked on (#235)
	walkedAt  uint64     // tick of that judgement: consecutive judgements give the sweep rate the crossing shot is priced on
	tracking  bool       // the pipper owns the stick this tick
	menace    int        // the attacker slot decide() last judged the problem, -1 none (#251): steer's evasion reads its track
	glimpsed  uint64     // tick a forming shot was first seen (#251): the tier's reaction time runs from here
	flanked   float64    // his bearing against my flight path last tick (#251): the sign flip through my 3/9 line IS the overshoot
	launched  uint64     // tick of the last missile away (#253): shoot-shoot-look — the quick pair is doctrine, the volley is not
	paired    int        // rounds already away in the CURRENT pair; the look resets it. Without a count, `paired` below re-armed off every launch and the pair became the whole rack
	dodge     uint64     // tick the commanded evasion ends; steer overrides the aim out-of-plane until then
	magazine  int        // rounds remaining, mirrored from the craft each tick: a pilot reads the counter
	sampled   uint64     // tick of the stored orbit sample
	sampleP   flight.Vec3
	sampleV   flight.Vec3
	sampleW   flight.Vec3 // swing at the anchor: a sign flip against it is the reversal that invalidates the arc
	intent    string      // fight-level posture (#236): convert / deny / reset / finish; "" = neutral
	minded    uint64      // tick the posture was set: the intent commitment
	chanced   uint64      // last tick the trigger had a shot worth its price — the stalemate detector's clock
	struck    uint64      // last tick a round of mine hit anyone — the signal that a conversion is actually wounding
	finished  uint64      // tick a finish was evicted for not wounding — its re-entry cooldown
	promise   float64     // the winning rehearsal's mean OFFENCE at the last re-plan (0..1): how much of the best line available is a gun solution
	countered uint64      // tick of the last counter-fire shot: one round per threat episode
	nearing   float64     // closure EMA, m/s: is the pursuit actually gaining
	spanned   float64     // last range, for the closure trend
	gauged    uint64      // tick of that range
	ring      orbit       // the prey's estimated turning circle (#206 planner)
	quiet     uint64      // tick the mandatory pause ends
	safed     string      // which doctrine last safed the gun — the offence instrument (TestOffence) prints it for every wasted firing window, which is how the preamble default was caught disarming the saddle (#206)
	turning   float64     // committed lead-turn direction, +1/-1; 0 = not in a pass
	zone      round.Zone  // cached AMRAAM DLZ against the current target (hunt.go; refreshed at most once a second)
	assessed  uint64      // tick the zone was computed
	supported uint64      // tick of this bot's last AMRAAM launch: the crank window opens here
	cranked   uint64      // tick the BVR overlay last overrode the aim (observability: the seam test asserts it never fires inside the merge)
	alerted   uint64      // tick an inbound radar round first crossed the Active call; 0 = clear sky
	guarding  uint64      // number of the round being defended: a new round re-rolls the engagement character
	squared   uint64      // tick the beam was last re-squared (the re-square cadence dial)
	beam      flight.Vec3 // the held beam heading between re-squares: it DRIFTS as the LOS rotates, which is the characterised pilot failure
	askew     float64     // this engagement's beam error sample, radians (the geometry-tolerance dial)
	lapse     bool        // this engagement reverts to drag (the seeded lapse: some defences simply fail)
	jam       bool        // brain-driven jammer arming (the machine's emission trade); applied to latest.Jammer after steer
	contact   flight.Vec3 // last place a BVR contact was held (hunt.go): the fight's address when the picture goes dark
	contacted uint64      // tick of that memory; a defence drops both radars into each other's notch at once, and without this the pair politely flies apart forever
	futile    int         // own AMRAAMs that have DIED against the current target without a kill (hunt.go): the look half of shoot-look-shoot
	futiled   int         // the target that futility was counted against; a new target resets the lesson
	turned    uint64      // tick the lead turn was committed
	aimed     float64     // last tick's pointing error, sin of the angle off the aim
	closing   float64     // smoothed rate that error is shrinking, rad/s: the anticipation that stops the turn overshooting
	jink      uint64      // tick to re-roll the jink direction
	phase     float64     // current jink roll phase
	missiles  int
	alert     uint64          // tick an inbound missile was first noticed (react delay runs from here)
	noticed   map[uint64]bool // inbound rounds already sighted (launch flash or the corner of the eye)
	judged    map[uint64]bool // rounds whose one launch-sighting roll has been taken
	plan      string          // the circle game plan chosen at the merge: "one" or "two" (held ~12 s; re-deciding every cadence is no plan at all)
	planned   uint64          // tick the plan was chosen
	side      float64         // which side the current threat/target sits (sign of the lateral LOS) — a flip while defensive is the reversal cue
	rolling   uint64          // rolling-scissors phase start; 0 = not rolling
	sense     float64         // committed roll direction while the aim is beyond ±140° (atan2 flips sides chaotically there)
	hold      uint64          // maneuver commitment: decisions re-evaluate but keep the aim until this tick (a yo-yo that flickers per decision is no yo-yo)
	stuck     int             // consecutive decisions of neutral non-progress (stalemate detector)
	tangle    int             // consecutive decisions locked in close combat (scissors detector)
	saddle    int             // consecutive defensive decisions with the attacker established behind (spiral gate: transients must not trigger a committed spiral)
	press     uint64          // tick+1 the offensive advantage was first held in range (#144), 0 = none: patience becomes the finish once it has lasted (a tick clock, not a decision count — maneuver holds throttle decisions)
	solo      bool            // section tactics OFF: fly pure individual BFM even with a team (the #138 pair-versus-pair control group)
	mate      int             // assigned section partner's slot (#140), -1 unpaired — set once at roster creation, stable across respawns
	spoke     uint64          // tick of the last brevity call (#139): one voice, one call at a time, never a chat storm
	told      int             // target already announced with ENGAGED (#139), -1 none — the call is an edge, not a repeat
	tallied   int             // contact already confirmed with TALLY (#146), -1 none — one call per bandit per life
	rolled    float64         // last roll input: the command is slew-limited so the executor cannot flap the stick
	ahead     float64         // last tick's boresight error, rad — the executor's tracking damper predicts from its closure
	reversed  uint64          // last reversal commitment tick: the anti-churn cooldown belongs to REVERSALS, not to whatever hold happens to be live
	settled   uint64          // tick the current committed manoeuvre may be replaced (#206): commitment is a SKILL, and it rises with tier
	starving  bool            // below the skill's energy floor: recovery outranks the fight until well clear of it (#206)
	play      string          // the duel arbiter's committed manoeuvre (duel.go)
	until     uint64          // tick that manoeuvre is re-judged
	picked    uint64          // tick the manoeuvre was chosen: the abort clause may re-judge early, but never within a quarter second of the last rehearsal (at machine cadence the abort re-planned EVERY TICK of a knife fight — thousands of rollout steps per tick)
}

// mind builds a brain for a fighting level, or nil for drone/unknown.
func mind(level string) *brain {
	s, found := skills[level]
	if !found {
		return nil
	}
	return &brain{skill: s, tactics: doctrine, mode: "cruise", target: -1, mate: -1, told: -1, tallied: -1, known: map[int]*track{}, missiles: 6}
}

// reborn resets the per-life state after a respawn.
func (b *brain) reborn() {
	b.mode, b.target, b.missiles, b.alert = "cruise", -1, 6, 0
	b.saddle, b.press = 0, 0
	b.aimed, b.closing = 0, 0
	b.turning, b.turned = 0, 0
	b.told, b.tallied = -1, -1
	b.play, b.until, b.promise = "", 0, 0
	b.prey = nil
	b.known = map[int]*track{}
}

// pressing reports whether the advantage has been held long enough to finish
// the fight (#144): the deliberate acceptance of deflection risk that makes a
// guns fight resolve — without it the saddle tracks patiently forever and the
// jink defeats it indefinitely (the battery's skirmish and merge scenarios
// recorded zero guns kills at every seed). Tier 3+: rookies already spray.
func (b *brain) pressing(tick uint64) bool {
	return b.skill.library >= 3 && b.press != 0 && float64(tick+1-b.press) > b.tactics.press.hold
}

// visible reports whether me can see other right now: visual range (halved at
// night), the canopy blind wedge, up-sun glare, and cloud-layer occlusion.
func (i *instance) visible(me, other *craft, tick uint64) bool {
	s, o := &me.model.State, &other.model.State
	direction, distance := i.bearing(s.Position, o.Position)
	if me.brain != nil && me.brain.skill.machine {
		return distance <= 12000 // the machine: full sphere, night, sun, and cloud alike — eyes are a human factor; only sensor reach remains
	}
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
	// The TRUE flown mass (#253): the stores work hung up to ~900 kg of
	// missiles and racks on armed jets, and a brain that referenced the
	// clean jet's corner speed flew every pace-gated behaviour — saddle
	// bias, climb gates, energy floor, corner throttle — against a speed
	// the heavy jet does not have.
	mass := m.Mass()
	if mass <= 0 {
		mass = m.Airframe.Mass.Empty + m.State.Fuel
	}
	density := 1.225 * math.Pow(math.Max(1-2.2558e-5*m.State.Position.Y, 0.3), 4.2559)
	stall := math.Sqrt(2 * mass * 9.81 / (density * 1.55 * m.Airframe.Reference.Area))
	return stall * math.Sqrt(m.Airframe.Limit.Positive)
}

// think runs one bot for one tick: decide at the skill's cadence, steer every
// tick, and hand the one-shot weapon requests to the instance.
func (i *instance) think(slot int, a *craft, tick uint64) {
	b := a.brain
	b.magazine = a.ammunition
	if i.cheat.ammunition {
		b.magazine = rounds
	}
	if b.decided == 0 || tick-b.decided >= b.skill.cadence {
		b.decided = tick
		i.decide(slot, a, tick)
	}
	i.hunt(slot, a, tick) // BVR radar, DLZ shot, crank, and defence (hunt.go): inert without AMRAAMs aboard or radar rounds inbound
	a.latest = b.steer(a.model, tick)
	a.latest.Jammer = b.jam // the armed level, brain-driven for bots the way a client reports it for humans (same equipment, same trade-offs)
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
		// Shoot-shoot-look (#253): the second round of a pair may follow
		// inside a second — doubling the Pk against one flare programme is
		// doctrine — but the third waits three seconds for the look. The
		// six-round magazine made this load-bearing: without it the
		// free-aspect tiers rippled the whole rack in the opening merge,
		// and six missiles in one stream saturate any flare defence — the
		// ace lost the missiles ladder 0-6 to the pilot, dead at t=10 with
		// four still flying.
		// A PAIR is two rounds, and the count is what makes it so. This read
		// `tick-b.launched < 60` alone, and every launch reset b.launched —
		// so one second after the second round the gate re-armed, and again
		// after the third, for as long as the brain kept asking. Measured
		// 2026-08-15, the moment single-player heaters became visible: an ace
		// put its whole rack of six into one second at 2.5 km and missed with
		// all of them. The look below is what resets the count, which is the
		// doctrine this comment always described.
		looked := elapsed(tick, b.launched, 180)
		if looked {
			b.paired = 0
		}
		paired := b.launched != 0 && tick-b.launched < 60 && b.paired < 2
		if i.missiles && i.free() && b.missiles > 0 && (paired || looked) {
			// Missile shot discipline (#141): the seeker head has no IFF — it
			// locks the best heat source in the cone whoever owns it. Checked
			// at the moment of launch, not request (a decision-old request may
			// be stale): decline while the seeker would acquire a teammate,
			// and the request comes back by itself once the geometry clears.
			if locked := i.acquire(slot, a); locked >= 0 && hostile(a, i.aircraft[locked]) && !i.committed(slot, a, locked) {
				if i.launch(slot, a) && !i.cheat.ammunition {
					b.missiles--
					b.paired++
					b.launched = tick
					a.model.Stores(armed(b.missiles))
				}
			}
		}
	}
	if b.drop {
		b.drop = false
		if a.flares > 0 || i.cheat.ammunition {
			if !i.cheat.ammunition {
				a.flares--
			}
			a.flared = 0
			i.events = append(i.events, map[string]any{"kind": "flare", "slot": slot})
		}
	}
	if b.bloom {
		b.bloom = false
		if a.chaff > 0 || i.cheat.ammunition {
			if !i.cheat.ammunition {
				a.chaff--
			}
			a.cloud = a.model.State.Position
			a.clouded = 0
			i.events = append(i.events, map[string]any{"kind": "chaff", "slot": slot})
		}
	}
}

// rationed decides whether a jet past its burner bingo must still go dry.
//
// It yields to a real chance: a slow opponent close aboard is what the reserve
// was being saved FOR, and a jet that will not spend it there flies home with
// fuel and no kill. The test is the same "slow and close" FINISH uses, so the
// bot may accelerate into exactly the shot its posture is waiting for.
//
// Measured against a human 2026-08-17 (recording 01a010a1...): the ace crossed
// the threshold at t=250 s and never got burner back, because fuel only falls.
// It spent 100+ seconds at 266-367 kt while the player cruised at 430-465, sat
// at 6.4-7.2 km and never closed — read by the pilot, correctly, as making no
// attempt. At t=330 the player WAS slow (232 kt) and close (1,396 m), the shot
// the whole posture waits for, and the ace could not take it: at 262 kt it was
// below its own energy floor, so FINISH refused to commit a slow jet, which is
// right. The ration is what had made it slow.
//
// NOTE the doctrine batteries cannot exercise this: a bandit burns about
// 4.2 kg/s, so the threshold is 327 s away, and their fights run 98-178 s.
func rationed(prey *track, distance float64) bool {
	return prey == nil || prey.velocity.Length() >= 170 || distance >= 2200
}

// beaten reports a round this pilot can see has already lost: seduced onto a
// flare, or gone ballistic with its lock broken. Its fuse stays live, so it is
// not harmless — but it is no longer STEERING, and abandoning the fight for it
// costs more than it saves.
//
// Measured 2026-08-16 (#37). The machine treats every round inside 4,500 m as
// a reason to break, and the ace throws forty of them a fight at a 10 percent
// arrival rate. That bought the ace something better than kills: the machine
// spent 5.0 s of every fight defending against rounds that were never going to
// reach it, and 0.86 s in a launch envelope of its own — then lost the gun
// fight it had been kept out of position for, 6-1. Volume was suppressing
// judgement.
//
// Only the instructor tiers get this. Watching a seeker go for the flare and
// reading that you are safe is a trained skill; the rookie sees a missile and
// breaks, which is authentic and is most of why the low tiers get gunned.
func beaten(b *brain, m *missile) bool {
	return b.skill.library >= 3 && (m.loose || m.blind > 0)
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
		if !seen && i.painted(a, c) {
			// The radar picture (hunt.go): a BVR-armed bot's search radar
			// extends its perception far past the canopy's twelve
			// kilometres, feeding the same track table vision feeds — so
			// swing, wobble, staleness, and the section radio all work on
			// radar contacts unchanged. Gated on AMRAAMs aboard and the
			// emitter being on: a heater bot's picture, and every dogfight
			// battery's, is byte-identical.
			seen = true
		}
		// Lost tally (#144): under load the head is pinned — the eyes hold
		// the current target and the forward view, and everyone else goes
		// unseen until the g comes off. Discipline raises the strain a pilot
		// keeps the scan under. This is where the unseen saddles that finish
		// real fights come from: bots that never lose sight never blunder.
		if seen && other != b.target && !b.skill.machine {
			if load := math.Abs(a.model.State.Fcs.Normal); load > 4+3*b.skill.discipline {
				if direction, _ := i.bearing(me.Position, c.model.State.Position); me.Attitude.Unrotate(direction).X < 0.3 {
					seen = false // padlocked through the break: off-nose contacts drop out of the scan (rounds landing and tracers below still announce themselves)
				}
			}
		}
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
			fresh := &track{when: tick, position: c.model.State.Position, velocity: c.model.State.Velocity, wobble: 45,
				nose: c.model.State.Attitude.Rotate(flight.Vec3{X: 1})}
			if t, found := b.known[other]; found {
				fresh.wobble = t.wobble // carried between looks; a fresh contact starts at 45 — treat the unknown as manoeuvring until watched
				least := 0.05
				if b.skill.machine {
					least = 0 // perfect perception includes the acceleration a human infers from spaced looks
				}
				if gap := float64(tick-t.when) / 60; gap > least && gap < 1.5 {
					fresh.swing = fresh.velocity.Subtract(t.velocity).Scale(1 / gap)
					if fresh.swing.Length() > 80 {
						fresh.swing = fresh.swing.Normalize().Scale(80) // cap at ~8 g: track noise is not a maneuver
					}
					// The jerk update rides the same pair of looks, on a
					// roughly half-second memory whatever the refresh rate.
					jerk := fresh.swing.Subtract(t.swing).Length() / gap
					fresh.wobble = t.wobble + clamp(gap*3, 0.05, 1)*(jerk-t.wobble)
				}
				// TALLY (#146): my own eyes replace the radio picture — tell
				// the lead his call was picked up. Once per bandit per life.
				if t.heard && other == b.target && b.tallied != other && i.audible(a) && (b.spoke == 0 || tick-b.spoke > 300) {
					b.tallied = other
					b.spoke = tick
					i.events = append(i.events, map[string]any{"kind": "call", "slot": slot, "call": "tally"})
				}
			}
			b.known[other] = fresh
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
					word := *called
					word.heard = true // the radio's picture, not my eyes — a real sighting replacing it is the TALLY moment (#146)
					b.known[mate.brain.target] = &word
				}
			}
		}
	}
	for _, s := range b.surveyed() { // forget the dead, the departed, and the long-lost
		t := b.known[s]
		// 45 s of track memory, up from 15 (2026-07-30): a duelling ace forgot
		// its ONLY opponent while rebuilding nose-away and fell to a blind
		// cruise weave at 1.4 km — a pilot who just fought someone does not
		// forget they exist because they spent fifteen seconds in the blind
		// cone. A stale track means flying to where he WAS, which is searching;
		// cruise is giving up. The cloud escape still works — the escapee turns
		// inside the layer, so the remembered point is wrong on purpose.
		// Track memory is 15 s in company and 45 s for the LAST man in a duel
		// (2026-07-30): a duelling ace forgot its only opponent while
		// rebuilding nose-away and fell to a blind cruise weave at 1.4 km — a
		// pilot who just fought someone does not forget they exist because of
		// fifteen seconds in the blind cone. But long memory for EVERY contact
		// sent section bots chasing stale ghosts away from their pair (six
		// extra deaths across 40 seeds), so the extension is duel-scoped: the
		// current target, with nobody else on the board. The cloud escape
		// still works — the escapee turns inside the layer, so the remembered
		// point is wrong on purpose.
		memory := uint64(15 * 60)
		if s == b.target && len(b.known) == 1 {
			memory = 45 * 60
		}
		if c := i.aircraft[s]; c == nil || !c.alive || tick-t.when > memory {
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
		if other == slot {
			continue // my own engagement is not a dogpile: self-counting made every bot penalize staying on its own target (#144)
		}
		if c := i.aircraft[other]; c != nil && c.bot && c.brain != nil && c.brain.target >= 0 {
			if a.team == "" || b.solo || c.team == a.team {
				attackers[c.brain.target]++
			}
		}
	}
	menacing := map[int]int{} // attacker slot -> the teammate he is running on
	danger, closest := -1, math.MaxFloat64
	if a.team != "" && !b.solo {
		for _, s := range b.surveyed() {
			t := b.known[s]
			for _, other := range i.slots() {
				mate := i.aircraft[other]
				if other == slot || mate == nil || !mate.alive || mate.model == nil || mate.team != a.team {
					continue
				}
				to, span := i.bearing(t.position, mate.model.State.Position)
				if span < b.tactics.sandwich.span && t.velocity.Normalize().Dot(to) > b.tactics.sandwich.nose {
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
	for _, s := range b.surveyed() {
		t := b.known[s]
		_, distance := i.bearing(me.Position, t.position)
		weight := distance * (1 + b.tactics.crowd.weight*float64(attackers[s]))
		if _, found := menacing[s]; found && distance < b.tactics.sandwich.reach {
			weight *= b.tactics.sandwich.weight // the radio warns at any range; the RESCUE priority stops where a lonely transit to a stale fight begins (#144)
		}
		if c := i.aircraft[s]; c != nil && c.alive && c.model != nil {
			// The wounded bird (#144): a contact trailing fire, fuel, or
			// pieces pulls the eye — finish what is already dying. Smoke and
			// vapor read at any range the contact is visible at.
			if c.condition.Burning || c.condition.Fire[0] > 0 || c.condition.Fire[1] > 0 ||
				c.model.State.Damage.Loss > 0 || c.model.State.Damage.Leak > 0.5 {
				weight *= b.tactics.wounded.weight
			}
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
	b.safed = "preamble"

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
	threatened, radar := false, false // and whether the round that threatens is a radar missile: the split programme (#43) spends chaff on it and flares on a heater
	if len(b.judged) > 64 { // rounds despawn and numbers only grow: reset rather than leak (a live round re-rolls once, harmlessly)
		b.noticed, b.judged = nil, nil
	}
	for _, m := range i.flying {
		direction, distance := i.bearing(me.Position, m.position)
		if b.judged == nil {
			b.noticed, b.judged = map[uint64]bool{}, map[uint64]bool{}
		}
		if m.target != slot {
			// Section missile defense (#146): a launch at a TEAMMATE that I
			// sight gets called — the call is the victim's sighting (a
			// padlocked or blind-wedged victim often never sees the plume at
			// all), though his own reaction delay still runs. One call per
			// round, ever; the most life-saving words on the radio.
			if a.team == "" || b.solo || m.called {
				continue
			}
			victim := i.aircraft[m.target]
			if victim == nil || !victim.alive || victim.team != a.team {
				continue
			}
			if !b.judged[m.number] {
				b.judged[m.number] = true
				body := me.Attitude.Unrotate(direction)
				sight := 0.6 + 0.4*b.skill.discipline
				if body.X < -0.35 && body.Y < 0.25 && !b.skill.machine {
					sight = 0.7 * b.skill.discipline
				}
				if battle.Roll(i.environment.Seed, uint64(slot), m.number, 51) < sight {
					b.noticed[m.number] = true
				}
			}
			if b.noticed[m.number] && distance < 6000 {
				m.called = true
				b.spoke = tick
				if victim.bot && victim.brain != nil {
					if victim.brain.judged == nil {
						victim.brain.noticed, victim.brain.judged = map[uint64]bool{}, map[uint64]bool{}
					}
					victim.brain.noticed[m.number] = true
					victim.brain.judged[m.number] = true
				} else {
					i.events = append(i.events, map[string]any{"kind": "call", "slot": slot, "call": "missile", "target": m.target})
				}
			}
			continue
		}
		if !b.judged[m.number] {
			b.judged[m.number] = true
			body := me.Attitude.Unrotate(direction)
			sight := 0.6 + 0.4*b.skill.discipline
			if body.X < -0.35 && body.Y < 0.25 && !b.skill.machine {
				sight = 0.7 * b.skill.discipline // launched from the blind wedge: only lookout discipline catches the flash
			}
			if battle.Roll(i.environment.Seed, uint64(slot), m.number, 51) < sight {
				b.noticed[m.number] = true
			}
		}
		if !b.noticed[m.number] && distance < 500+1000*b.skill.discipline {
			b.noticed[m.number] = true // the corner of the eye, late
		}
		if b.noticed[m.number] && distance < 4500 && !beaten(b, m) {
			threatened, inbound, radar = true, direction, m.radar != nil
			break
		}
	}
	if threatened {
		if b.alert == 0 {
			b.alert = tick
		}
		if float64(tick-b.alert) >= b.skill.react*60 {
			b.mode = "evade"
			b.press = 0
			side := me.Attitude.Rotate(flight.Vec3{Z: 1})
			if inbound.Dot(side) > 0 {
				side = side.Scale(-1)
			}
			b.aim = side.Subtract(inbound.Scale(0.4)).Normalize() // break across the seeker, away from it
			b.reheat = 0                                          // burner doubles the decoy's job
			b.throttle = 1
			// A PROGRAMME per defended round (#43), not a stream. This used to
			// re-flare every 0.9 s for as long as the threat lasted — nine to
			// twenty flares per missile, fifty-seven in one fight — which was
			// only affordable because the dispenser was bottomless, and was
			// worth little past the fourth: a seeker that has seen through a
			// flare halves its odds of taking the next (air.go pursue), so the
			// value is in the first few, dispensed close together while the
			// round is still deciding. Four at half-second spacing on
			// sighting, two more if it is still coming four seconds on, and
			// the rest of the magazine is kept for the next one. Flares only:
			// dispensing chaff with every flare, and flares with every chaff,
			// was the coupling that let a flare magazine reach the BVR ladder.
			// A radar round's countermeasures belong to hunt.go's defence, which
			// knows the seeker's phase and blooms only where a cloud can take
			// it; the flares here would be spent on a round they cannot touch.
			if !radar && a.flared > 0.5 && (b.dispensed < 4 || (float64(tick-b.alert) > 240 && b.dispensed < 6)) {
				b.drop = true
				b.dispensed++
			}
			i.guard(b, me, corner(a.model))
			return
		}
	} else {
		b.alert = 0
		b.dispensed = 0
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
		for _, s := range b.surveyed() {
			t := b.known[s]
			direction, distance := i.bearing(me.Position, t.position)
			if distance > 2600 || a.flares <= flare_load/3 {
				continue // outside the rear-aspect heater's reach, or into the last third of the magazine, which is kept for rounds actually seen (#43)
			}
			body := me.Attitude.Unrotate(direction)
			if body.X > -0.2 {
				continue // ahead of my 3/9 line: I can watch him and flare on the flash instead
			}
			if tick-t.when < blind {
				continue // still fresh eyes on him (a high six is visible over the shoulder)
			}
			// With missiles up, coverage is a DISCIPLINE: the ace keeps a fresh
			// flare out against the shot he cannot see, the rookie rarely thinks
			// of it. In a guns-only fight the same insurance buys nothing — and
			// knowing that is itself a skill, so the ladder inverts. The rookie
			// alone still pops them, having not worked out that nothing up there
			// homes on heat; everyone better saves the stores and their position
			// (#211).
			// A DUTY CYCLE, not permanent cover. The roll fires once per 0.8 s
			// coverage window, so an ace at discipline 1.0 kept a fresh flare
			// up continuously and was effectively missile-proof — a human
			// emptied a magazine of 9Ms into one and it soaked every shot.
			// Real coverage is periodic: this leaves roughly two thirds of the
			// windows uncovered for the best pilot, and the ladder still
			// orders (ace 0.35 .. rookie 0.07). Stores are finite too, so
			// permanent cover was never affordable in the first place.
			chance := b.skill.discipline * 0.35
			if b.skill.machine {
				chance = 1 // doctrine without lapses: every uncovered window gets its flare
			}
			if !i.missiles {
				chance = 0
				if b.skill.discipline < 0.5 {
					chance = 0.6 - b.skill.discipline // the rookie's mistake, and his alone
				}
			}
			if battle.Roll(i.environment.Seed, uint64(slot), uint64(s), tick/uint64(flare_window*60), 52) < chance {
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
		b.press = 0
		b.prey = nil
		b.shoot, b.loose = false, false
		b.safed = "evade"
		away, gap := level(me.Velocity.Normalize()), math.MaxFloat64
		for _, s := range b.surveyed() {
			if d, span := i.bearing(me.Position, b.known[s].position); span < gap {
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
	// fire solution keeps updating underneath it. EXCEPT on the gun track:
	// the saddle's hold pins the MODE, never the pipper — a lead point frozen
	// for the hold's 0.75 s was the difference between a pinned tracker's 42%
	// hit rate and a flown one's 4% (#144: the drone kill took fifteen
	// minutes of match time before this).
	if tick < b.hold && b.target >= 0 {
		if prey, found := b.known[b.target]; found {
			b.prey = prey
			_, b.distance = i.bearing(me.Position, prey.position)
			if (b.mode == "saddle" || b.mode == "press") && b.distance < b.skill.open*1.4 {
				age := float64(tick-prey.when) / 60
				horizon := 0.0
				if b.skill.library >= 2 {
					horizon = age
				}
				spot := predict(prey, horizon, b.skill.library >= 3)
				direction, span := i.bearing(me.Position, spot)
				closure := me.Velocity.Subtract(prey.velocity).Dot(direction)
				time := span / math.Max(battle.Average(span, me.Position.Y)+closure, 200)
				lead := predict(prey, horizon+time, b.skill.library >= 3).
					Subtract(me.Velocity.Scale(time)).
					Add(flight.Vec3{Y: 4.9 * time * time})
				b.aim, _ = i.bearing(me.Position, lead)
				// The gun is LIVE for the whole held track (#206): the decide
				// preamble defaults shoot to false, and this early return kept
				// that default — so one cadence into every saddle or press,
				// the bot flew its perfectly-led tracking aim with a DEAD
				// TRIGGER for the rest of the hold. The offence instrument
				// measured the majority of every tier's real firing windows
				// dying exactly here: gate open, mode saddle, gun safed by
				// the preamble. The led solution gate still decides each
				// round; this only stops the doctrine disarming its own shot.
				b.shoot = true
			}
		}
		return
	}

	// Too slow to fight: unload, nose down a little, burner, rebuild to
	// corner — a stalled zoom otherwise floats for tens of seconds.
	if speed < 0.55*pace {
		b.mode = "rebuild"
		b.settle(tick)
		b.press = 0
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
		b.press = 0
		if lead := i.leader(slot, a); lead != nil {
			if b.mode != "form" && b.mode != "cruise" && b.mode != "rejoin" && i.audible(a) && (b.spoke == 0 || tick-b.spoke > 300) {
				b.spoke = tick // returning to the spread off a fight: say so (#146)
				i.events = append(i.events, map[string]any{"kind": "call", "slot": slot, "call": "rejoin"})
			}
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
			station := him.Position.Add(abeam.Scale(b.tactics.form.abeam))
			direction, span := i.bearing(me.Position, station)
			near := clamp(span/b.tactics.form.blend, 0, 1) // far: fly at the station; close: fly the lead's heading and let the station drift in
			mixed := ahead.Scale(1 - near).Add(direction.Scale(near))
			if mixed.Length() < 0.1 {
				mixed = ahead // overran the station dead ahead: the blend cancels — hold heading and let the throttle law drop me back
			}
			b.aim = mixed.Normalize()
			b.g = 3 // station keeping is not a limiter business
			want := him.Velocity.Length() + clamp((span-150)*0.05, -40, 80)
			b.throttle = clamp(0.55+(want-speed)*0.01, 0.25, 1)
			b.reheat = 0
			// Rejoin in burner beyond the far gate — or any time the throttle
			// is already pegged and still short of the speed the rejoin needs.
			// The distance gate alone left a dead band just inside it: off
			// station at 2.5-3 km, full dry thrust, a few m/s slower than the
			// lead, drifting further back every minute and never reaching the
			// threshold that would have lit the burner.
			if span > b.tactics.form.burner || (span > b.tactics.form.abeam && b.throttle >= 1 && speed < want-10) {
				b.reheat = 1
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
	// Refresh the orbit estimate whenever the track has a NEW observation: two
	// spaced samples give the circle BFM is flown against (#206 planner).
	// The pair must be SPACED: two looks a sixtieth of a second apart carry
	// no measurable turn and circle() rejects them, so an anchor that
	// advanced on every refresh left the zero-delay tier — alone on the
	// ladder — with no orbit estimate at all, and its rehearsals evolving
	// the opponent as a straight-liner (#235). The anchor now holds until
	// the pair is wide enough. A reversal drops the arc instead of fitting
	// it: a circle bridged across a direction change confidently predicts
	// a turn the target has already abandoned, which measured WORSE than no
	// estimate when this was first tried without the guard.
	if prey.when != b.sampled {
		if b.sampled == 0 {
			b.sampled, b.sampleP, b.sampleV, b.sampleW = prey.when, prey.position, prey.velocity, prey.swing
		} else if prey.swing.Dot(b.sampleW) < 0 && prey.swing.Length() > 25 && b.sampleW.Length() > 25 {
			b.ring = orbit{}
			b.sampled, b.sampleP, b.sampleV, b.sampleW = prey.when, prey.position, prey.velocity, prey.swing
		} else if float64(prey.when-b.sampled)/60 >= 0.25 {
			b.ring = circle(b.sampleP, b.sampleV, b.sampled, prey.position, prey.velocity, prey.when)
			b.sampled, b.sampleP, b.sampleV, b.sampleW = prey.when, prey.position, prey.velocity, prey.swing
		}
	}
	age := float64(tick-prey.when) / 60
	horizon := 0.0
	if b.skill.library >= 2 {
		horizon = age // lead the stale track; the rookie chases where he WAS
	}
	spot := predict(prey, horizon, b.skill.library >= 3)
	direction, distance := i.bearing(me.Position, spot)
	b.distance = distance
	chase := prey.velocity.Normalize()
	tail := direction.Dot(chase) // 1 = square behind him, -1 = head-on
	closure := me.Velocity.Subtract(prey.velocity).Dot(direction)
	mine := me.Position.Y + speed*speed/19.62
	theirs := prey.position.Y + prey.velocity.Length()*prey.velocity.Length()/19.62

	// Defensive check: a known contact behind my 3/9 inside 2 km, nose on me.
	menace, gap := -1, 2000.0
	for _, s := range b.surveyed() {
		t := b.known[s]
		to, span := i.bearing(t.position, me.Position)
		if span < gap && me.Attitude.Unrotate(to.Scale(-1)).X < -0.2 && t.velocity.Normalize().Dot(to) > 0.6 {
			menace, gap = s, span
		}
	}
	i.flinch(slot, a, tick, menace, gap)

	// Rejoin discipline (#144): never cross into a distant fight single-ship.
	// The equal-tier probe found section bots dying 8-30 km from their pair —
	// endless respawns scatter a section across the map, and a bot that
	// transits alone arrives alone. With the fight still far and the pair
	// split, fly to the pair first; both partners converge, so the section
	// arrives together or not at all. A close fight is mine regardless.
	if a.team != "" && !b.solo && b.mate >= 0 && menace < 0 && distance > b.tactics.rejoin.fight {
		if mate := i.aircraft[b.mate]; mate != nil && mate.alive && mate.model != nil {
			if toward, span := i.bearing(me.Position, mate.model.State.Position); span > b.tactics.rejoin.span {
				b.mode = "rejoin"
				b.press = 0
				b.safed = "rejoin"
				b.shoot = false
				b.aim = flight.Vec3{X: toward.X, Y: clamp(toward.Y, -0.1, 0.15), Z: toward.Z}.Normalize()
				b.g = 3
				b.throttle, b.reheat = 1, boost(speed, pace, 40)
				i.guard(b, me, pace)
				return
			}
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
			_, span := i.bearing(mate.model.State.Position, spot)
			if span > b.tactics.support.engaged {
				continue // he is not fighting this target, whatever else he is doing
			}
			// He is the engaged fighter if he is meaningfully closer — or if we
			// are COMPARABLY placed and he wins the deterministic tiebreak. The
			// comparable band is the case the old rule missed entirely: a pair
			// arriving together is near-equidistant, so neither was ever
			// "closer times share", neither deferred, both committed, and the
			// section traded two jets for one (the 2026-08-10 sweep's verdict:
			// no constant closes the gate, the pair must SEQUENCE). Both bots
			// evaluate the same rule with the same inputs, so the split is
			// consistent without a radio call: the lower slot takes the fight,
			// the other banks energy on the perch — and the roles swap
			// naturally as the geometry moves them out of the band, or the
			// instant the picture changes (the sandwich weighting and the
			// nose-on `tail` check both bypass this whole block).
			if span < b.tactics.support.share*distance ||
				(span < distance/b.tactics.support.share && other < slot) {
				engaged = true
				break
			}
		}
		if engaged && distance < b.tactics.support.span {
			b.mode = "support"
			b.press = 0
			b.safed = "support"
			b.shoot = false
			perch := spot.Subtract(chase.Scale(b.tactics.support.behind)).Add(flight.Vec3{Y: b.tactics.support.above})
			if distance < b.tactics.support.near {
				out, _ := i.bearing(spot, me.Position)
				perch = me.Position.Add(out.Scale(b.tactics.support.out)).Add(flight.Vec3{Y: b.tactics.support.rise}) // too close: open out, never through the fight
			}
			b.aim, _ = i.bearing(me.Position, perch)
			b.g = math.Min(b.g, b.tactics.support.limit)
			b.throttle, b.reheat = 1, boost(speed, pace, 60) // the perch is an energy bank
			i.guard(b, me, pace)
			return
		}
	}

	// The duel arbiter (#206): a teamless bot picks its manoeuvre by forward
	// simulation (duel.go), not the ladder below — offence and defence are one
	// decision there, so an attacker's overshoot flips the fight without a
	// hand-written bridge between modes. The ladder remains the section doctrine.
	if a.team == "" {
		i.duel(slot, a, tick, prey, direction, distance, tail, menace, gap)
		i.polish(slot, a, tick, speed, pace, nose, direction, distance, tail)
		return
	}

	// Commitment gate (#206). A manoeuvre that keeps being replaced accomplishes
	// nothing: the recorded human fight showed the ace alternating defense and
	// rebuild every ~1.6 s in the endgame, generating no separation while a
	// keyboard pilot sat on its six for two minutes. Once a manoeuvre from the
	// committed set is running it owns the next skill.commit seconds — returning
	// here keeps the command set already chosen, which steer() keeps flying. The
	// exceptions are what a real pilot genuinely abandons a plan for: a new
	// threat behind, or an inbound missile.
	if tick < b.settled && commitment[b.mode] && menace < 0 {
		if _, urgent := menacing[b.target]; !urgent {
			return
		}
	}

	// Energy floor (#206). Below it, recovering energy outranks the fight — a
	// committed unload-and-accelerate, not the two-second gesture the trace
	// showed. Hysteresis (recovery at 1.3x the floor) stops it flickering back
	// into the fight the moment it gains a knot. A rookie has no floor: getting
	// slow and dying is exactly the rookie's flaw, and it stays authentic.
	threatRange := 1e9
	if menace >= 0 {
		if foe, found := b.known[menace]; found {
			_, threatRange = i.bearing(me.Position, foe.position)
		}
	}
	// ...and the floor also yields to a close PREY, not just a close threat. It
	// only checked the range to whoever was attacking ME, so against a target
	// that was not shooting back there was no menace, and the ace starved out
	// of a PRESS 380 m behind a compliant target — the player handed it a free
	// kill and watched it unload away (2026-07-30 recording). Rebuilding is FOR
	// the fight; abandoning gun parameters to rebuild is throwing away the
	// fight it was rebuilding for. A slow gun kill in the saddle is still a
	// kill — the aero cap keeps the nose honest at low speed.
	// The yield is for the SADDLE, not for any nearby enemy: gated on being
	// behind him (tail geometry), because "someone is close" also describes a
	// grinding scissors, where refusing to rebuild is how both jets stall into
	// the sea — measured as six extra section deaths across 40 seeds when the
	// yield keyed on range alone.
	saddled := false
	if b.target >= 0 {
		if quarry, found := b.known[b.target]; found {
			direction, span := i.bearing(me.Position, quarry.position)
			if span < 900 && quarry.velocity.Length() > 1 && direction.Dot(quarry.velocity.Normalize()) > 0.35 {
				saddled = true
			}
		}
	}
	if b.skill.floor > 0 && threatRange > 1200 && !saddled && b.plan != "" { // ...and never before the merge plan is chosen: energy management belongs inside the fight // a slow jet with the attacker still outside gun range unloads and accelerates: that is the energy defence. Inside 1200 m it keeps fighting - unloading in front of a gun is how you die tidily
		if speed < b.skill.floor {
			b.starving = true
		} else if speed > b.skill.floor*1.3 {
			b.starving = false
		}
		if b.starving {
			b.mode = "rebuild"
			b.settle(tick)
			b.press = 0
			b.safed = "rebuild"
			b.shoot = false
			b.throttle, b.reheat = 1, 1
			nose := me.Attitude.Rotate(flight.Vec3{X: 1})
			nose.Y = -0.15 // unload and accelerate; the descent is the cheapest energy there is
			if me.Position.Y < 900 {
				nose.Y = 0.05 // ...but never dive into the sea
			}
			b.aim = nose.Normalize()
			b.g = 1
			b.settle(tick)
			return
		}
	}

	// Carry the committed lead turn THROUGH the pass. Without this the turn
	// ends the instant closure goes negative, the mode leaves neutral, and the
	// pursuit law starts chasing a bandit who is now behind the wing - so the
	// jet snapped from a clean pull into a scramble at exactly the moment the
	// player is trying to read which way it went. A real pilot commits to the
	// lead turn and looks afterwards. A live missile still overrides.
	if b.turning != 0 {
		if tick-b.turned < b.tactics.lead.hold && menace < 0 {
			b.aim = b.commit(me, b.turning)
			// The gun stays LIVE through the pass (#206): the committed turn
			// sweeps the boresight through the crossing solution, and that
			// sweep IS the snapshot — the offence instrument measured nearly
			// every firing solution a defending target ever offers landing
			// inside this hold, with the trigger wired shut ("nothing to
			// shoot at across a merge" was instant-gunnery reasoning; real
			// time of flight makes the crossing shot honest, and the led
			// solution gate decides whether the rounds are worth it).
			b.shoot = true
			i.guard(b, me, pace)
			return
		}
		if tick-b.turned >= b.tactics.lead.hold {
			b.turning = 0
		}
	}

	switch {
	case menace >= 0 && (b.target != menace || tail < 0.35): // he's the problem: fight him
		b.mode = "defense"
		b.settle(tick)
		b.press = 0
		foe := b.known[menace]
		at, span := i.bearing(me.Position, foe.position)
		b.throttle, b.reheat = 1, boost(speed, pace, -80)
		if a.team == "" && span < 700 && !elapsed(tick, b.reversed, 300) {
			// The scissors is a STATE, not an instant (lone doctrine): while
			// the weave is live — reversed within 5 s, attacker inside 700 m —
			// the break between reversals stays slow too. Letting defense
			// shove the throttle back to corner between reversals re-armed the
			// attacker's tracking with the very speed the trap had just spent.
			b.throttle, b.reheat = 0.25, 0
		}
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
		// The ROPE-A-DOPE (#144 vertical literacy, tier 4): an attacker at
		// range without an established saddle, and I hold the energy — climb
		// at a rate he cannot sustain. He follows or he loses the angles; if
		// he follows he arrives below, slow, and mushing, and the conversion
		// is ordinary offense once the hold expires and his nose falls off.
		if b.skill.library >= 4 && span > b.tactics.rope.near && span < b.tactics.rope.far {
			foes := foe.position.Y + foe.velocity.Length()*foe.velocity.Length()/19.62
			on := foe.velocity.Normalize().Dot(at.Scale(-1))
			if mine > foes+b.tactics.rope.edge && on < b.tactics.rope.nose {
				b.mode = "rope"
				b.settle(tick)
				away := at.Scale(-1)
				b.aim = flight.Vec3{X: away.X * 0.5, Y: 0.9, Z: away.Z * 0.5}.Normalize()
				b.g = math.Min(b.g, 4)
				b.throttle, b.reheat = 1, 1
				b.hold = tick + b.tactics.rope.hold
				i.guard(b, me, pace)
				return
			}
		}
		weave := uint64(300)
		if a.team == "" {
			weave = 150
		}
		// The reversal cue (tier 3+): the attacker's lateral side FLIPPING
		// while he's close means he crossed my flight path — reverse the turn
		// into him NOW (the scissors entry), don't keep the old break.
		// Which side he sits on across MY WINGS. Measured against world vertical
		// this went unreliable exactly when it matters: in a defensive break the
		// jet is banked past 90°, where "left of my velocity in the horizontal
		// plane" stops corresponding to "left across my flight path", and the
		// cue both missed real overshoots and fired on the bank alone.
		flank := math.Copysign(1, at.Dot(me.Attitude.Rotate(flight.Vec3{Z: 1})))
		if b.skill.library >= 3 {
			closing := foe.velocity.Subtract(me.Velocity).Dot(at.Scale(-1))                                                                                           // his speed along the line toward me
			if b.side != 0 && flank != b.side && span < 700 && (closing > -30 || a.team != "") && elapsed(tick, b.rolling, 240) && elapsed(tick, b.reversed, weave) { // the crossing must CARRY CLOSURE to be an overshoot for a LONE fighter — reversing under an attacker already lagging and slow hands him the turn; teams keep the pure geometric cue their sweeps were calibrated with (the qualifier cost the section six deaths). The cue is geometric either way: his side flipping across my wings IS the flight-path crossing; the interval is only an anti-churn floor // lone fighters weave at the scissors rhythm (2.5 s); wingmen keep the old 5 s — cycling reversals with a second attacker around cost the section eight deaths // (tangle never accumulates while defensive — the defense case returns before that counter)
				// Reverse once per genuine overshoot — a scissors flips sides
				// every weave, and reversing each flip churns the energy away.
				// The cooldown is against the LAST REVERSAL, not the live hold:
				// coupled to b.hold, a spiral committed moments before the
				// overshoot blocked the reversal until 4 s past the spiral's
				// end — but the attacker crossing the flight path is exactly
				// the event that invalidates the spiral's premise.
				b.mode = "reverse"
				b.settle(tick)
				b.aim = level(at)
				// The reversal is an OVERSHOOT TRAP (2026-08-01): chop the
				// throttle and board out. It used to brake toward 0.9 corner
				// with the throttle up — a 300 kt reversal in wide arcs that a
				// human tracked with ease. A scissors converts by getting
				// SLOWER than the attacker until his flight path carries him
				// past; speed is the thing being spent, on purpose. The floor
				// never blocked this (it only arms past 1200 m) — the branch's
				// own energy targets did.
				b.throttle, b.reheat = 0.1, 0
				b.brake = 1
				if a.team != "" { // the trap is LONE doctrine: slow with a second attacker around is how wingmen die — teams keep the fast reversal
					b.throttle, b.reheat = 1, 0
					b.brake = clamp((speed-0.9*pace)/80, 0, 1)
				}
				b.hold = tick + 90
				b.reversed = tick
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
			b.settle(tick)
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
		if b.skill.library >= 2 && speed < b.tactics.drag.pace*pace && span > b.tactics.drag.span {
			b.mode = "drag"
			b.settle(tick)
			away := at.Scale(-1)
			// Drag-AND-BAG (teams): the extension bends toward the nearest
			// living teammate — the pursuer gets dragged across a friendly
			// nose instead of into empty sky.
			if mate := i.nearest_mate(slot, a); mate != nil && !b.solo {
				toward, span := i.bearing(me.Position, mate.model.State.Position)
				if span < b.tactics.bag.reach && toward.Dot(away) > -0.3 { // never a reversal INTO the pursuer just to reach a friend
					away = away.Add(toward.Scale(b.tactics.bag.bend)).Normalize()
				}
			}
			b.aim = flight.Vec3{X: away.X, Y: -0.08, Z: away.Z}.Normalize()
			b.throttle, b.reheat = 1, 1
			b.safed = "drag"
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
		if on > b.tactics.spiral.nose {
			b.saddle++
		} else {
			b.saddle = 0
		}
		switch {
		case b.skill.library >= 4 && span < 700 && foe.velocity.Length() > speed+60:
			b.mode = "scissors"
			b.settle(tick) // he's overshooting hot: brakes out, reverse into him
			b.brake = 1
			b.aim = at
		case b.skill.library >= 3 && span < b.tactics.jink.span && (pointed || b.skill.library < 4):
			// Guns jink: irregular out-of-plane rolls off the break, re-rolled
			// on a deterministic clock so it can't be learned. Tier 4 TIMES the
			// break off his gun solution — jinking while his nose is off just
			// spends the energy the fight will be decided by (tier 3 jinks
			// whenever he's close, wasteful and authentic).
			if tick >= b.jink {
				b.phase = battle.Roll(i.environment.Seed, uint64(slot), tick) * 2 * math.Pi
				b.jink = tick + b.tactics.jink.base + uint64(battle.Roll(i.environment.Seed, uint64(slot)+7, tick)*float64(b.tactics.jink.spread))
			}
			up := me.Attitude.Rotate(flight.Vec3{Y: 1})
			side := me.Attitude.Rotate(flight.Vec3{Z: 1})
			b.aim = me.Velocity.Normalize().Scale(0.4).Add(up.Scale(math.Cos(b.phase))).Add(side.Scale(math.Sin(b.phase))).Normalize()
		case b.skill.library >= 3 && on > b.tactics.spiral.nose && b.saddle > b.tactics.spiral.saddle && span < b.tactics.spiral.span && me.Position.Y > b.tactics.spiral.floor:
			// DEFENSIVE SPIRAL (#130): saddled but not yet shot at, with altitude
			// in the bank — nose-low maximum-rate descending turn. Gravity pays
			// for the rate his level pursuit cannot match without overshooting,
			// and the guard flattens it before the floor.
			b.mode = "spiral"
			b.settle(tick)
			b.aim = flight.Vec3{X: at.X, Y: -0.5, Z: at.Z}.Normalize()
			b.throttle, b.reheat = 0.8, 0
			b.hold = tick + b.tactics.spiral.hold
		default:
			b.aim = level(at) // break INTO him at corner speed
		}
		i.guard(b, me, pace)
		return
	case tail > 0.35: // offensive: behind his 3/9
		b.mode = "offense"
		b.saddle = 0
		if distance < b.tactics.press.span {
			if b.press == 0 {
				b.press = tick + 1 // the advantage clock starts (+1: tick zero is a real start, zero means none)
			}
		} else {
			b.press = 0
		}
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
		// The BRACKET (#144 division tactics): two pairs running the same
		// distant contact split the approach — the junior pair offsets off the
		// direct line, away from the senior, so the merge arrives from two
		// bearings and someone always has the flank. Inside bracket.span the
		// approaches straighten and it is an ordinary converging attack.
		if a.team != "" && !b.solo && b.mate >= 0 && distance > b.tactics.bracket.span {
			ours := b.mate
			if slot < ours {
				ours = slot
			}
			for _, other := range i.slots() {
				c := i.aircraft[other]
				if other == slot || other == b.mate || c == nil || !c.alive || c.model == nil || c.brain == nil || c.team != a.team {
					continue
				}
				if c.brain.mate < 0 || c.brain.target != b.target {
					continue
				}
				peers := c.brain.mate
				if other < peers {
					peers = other
				}
				if ours >= peers {
					continue // the senior pair (created first, higher slots) flies the direct line
				}
				toward, _ := i.bearing(me.Position, c.model.State.Position)
				side := -math.Copysign(1, b.aim.Cross(toward).Y) // offset AWAY from the other pair
				sin, cos := math.Sin(side*b.tactics.bracket.angle), math.Cos(side*b.tactics.bracket.angle)
				b.aim = flight.Vec3{X: b.aim.X*cos - b.aim.Z*sin, Y: b.aim.Y, Z: b.aim.X*sin + b.aim.Z*cos}.Normalize()
				break
			}
		}
		b.throttle, b.reheat = 1, boost(speed, pace, 40)
		if closure < 20 && distance > 800 {
			b.reheat = 1 // a stern chase that is not closing is no chase at all — the corner-speed boost cap parked pursuits behind a running target forever (#144: the battery's gunnery scenario sat beyond 1200 m for most of a 300 s drone chase)
		}
		if distance < 1500 {
			// Closure discipline into the control zone: arrive with 40-ish
			// overtake, not a hundred — a blown pass wastes the whole conversion.
			goal := 40.0
			if b.pressing(tick) {
				goal += b.tactics.press.closure // finishing: arrive hot, not polite (#144)
			}
			b.throttle = clamp(1-(closure-goal)/200, 0.35, 1)
			b.reheat = boost(speed, pace, -60)
			if b.pressing(tick) {
				b.reheat = boost(speed, pace, 40)
			}
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
		case b.skill.library >= 3 && closure > b.tactics.high.closure && distance < b.tactics.high.span && tail < b.tactics.high.tail:
			// (dead-astern overtake is the closure discipline's job — boards,
			// not vertical excursions that blow the approach every pass)
			// High yo-yo: pull up out of plane, spend closure as height —
			// committed for two seconds or it is just a twitch.
			b.aim = direction.Add(flight.Vec3{Y: 0.5}).Normalize()
			b.g *= 0.8
			b.throttle, b.reheat = 0.6, 0
			b.hold = tick + b.tactics.high.hold
			if b.skill.library >= 4 {
				b.brake = clamp((closure-150)/150, 0, 1)
			}
		case b.skill.library >= 2 && closure < b.tactics.low.near && closure > b.tactics.low.far && tail < b.tactics.low.tail && direction.Y < b.tactics.low.rise:
			// Low yo-yo from trail: cut inside and below his TURNING circle —
			// the cut needs a crossing target. Against a straight runner
			// (dead-astern, big opening) it just lags the chase into the dirt;
			// and never against one climbing away above.
			b.aim = direction.Subtract(flight.Vec3{Y: 0.35}).Add(chase.Scale(-0.2)).Normalize()
		case b.skill.library >= 2 && a.team == "" && distance > 500 && tail < 0.85 && closure > -40:
			// (teamless doctrine only for now: a stalking wingman chases
			// geometry away from his pair — both section sweeps regressed past
			// their slack when the stalk flew in teams, and gating on b.solo
			// instead poisoned the A/B by giving only the solo arm a planner.
			// Section-aware planning is the plan-spine work, measured on its
			// own terms.)
			// THE STALK (#206 planner): fly to a control point ON HIS CIRCLE —
			// half a radian of arc behind him, standing slightly inside — not
			// at the jet itself. Pointing at a turning target is a matched-
			// radius tail chase whose angles never improve (measured: no tier
			// bent the angle-off-tail trace downward, ever); the control point
			// moves with his turn, so flying at it lags the circle, builds
			// closure across the chord, and arrives BEHIND him. Straight
			// targets keep the classic flight-path lag.
			b.mode = "stalk"
			if b.ring.valid {
				point := b.ring.behind(predict(prey, age, b.skill.library >= 3), 0.5)
				b.aim, _ = i.bearing(me.Position, point)
				b.throttle, b.reheat = 1, boost(speed, pace, -20)
				if closure > 140 {
					b.brake = clamp((closure-140)/120, 0, 1) // arriving too hot overshoots the control zone the stalk is buying
				}
			} else {
				b.aim = direction.Add(chase.Scale(-0.25)).Normalize() // classic lag on a straight runner
			}
		case b.skill.library >= 2 && distance > 700 && tail < 0.85 && closure > -40:
			b.aim = direction.Add(chase.Scale(-0.25)).Normalize() // teams keep the classic crossing lag the stalk replaced for lone fighters — removing it cost the section six deaths
		}
	case tail < -0.35: // neutral: converging head-on
		b.mode = "neutral"
		b.saddle, b.press = 0, 0
		// The face shot: rookies spray it (authentic), the disciplined decline
		// it and fight the turn instead — training doctrine, and it keeps the
		// merge a fight rather than a coin toss.
		b.shoot = distance < b.skill.open && (tail > -0.7 || b.skill.discipline < 0.6)
		if !b.shoot {
			b.safed = "face-decline"
		}
		b.throttle, b.reheat = 1, 1
		switch {
		// The ZOOM MERGE (#144 vertical literacy) was REMOVED here, 2026-07-29,
		// on measurement. It selected on an energy edge and then spent that edge
		// climbing, so it cancelled itself halfway: the ace rode a zoom to within
		// 1.7 s of the merge, lost the condition, dropped to an ordinary lead turn
		// and rolled again for offense — three roll events in three seconds, and a
		// player could not tell which way it was turning. Neither latching on the
		// hold (entered ~3.7 s out, the 2 s hold lapses first) nor hysteresis on
		// the entry (flies the zoom whole, still two reversals) fixed that.
		//
		// Removing it measured better on BOTH axes at once: merge reversals 2.0 ->
		// 0.0, drone kills 2/6 -> 4/6, time-to-kill 173 s -> 134 s. That agrees
		// with #219, which found the whole tier-4 library to be a net negative
		// (library 3 gives the ace the best positional advantage of the sweep).
		// TestBotZoom went with it. The zoom tactics constants stay for now; #215
		// owns whether vertical literacy returns in a form that does not cancel
		// itself.
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
		if closure > 0 && distance < math.Max(b.tactics.lead.floor, closure*b.tactics.lead.closure) {
			pass := math.Copysign(1, me.Velocity.Normalize().Cross(direction).Y) // his passing side
			// The game plan (tier 3+): TWO-circle (rate fight — turn toward his
			// side, fight at corner in burner) with the energy to rate; ONE-
			// circle (radius fight — turn across the pass, slow and tight,
			// denying his nose) when slower or poorer. Held, not re-rolled.
			turn := pass
			if b.skill.library >= 3 {
				if mine < theirs-b.tactics.plan.deficit {
					b.plan, turn = "one", -pass // a REAL energy deficit: deny his rate game
				} else {
					b.plan = "two"
				}
				b.planned = tick
			}
			// Commit the turn, and fly it off MY OWN nose rather than off the
			// bearing to him. Rotating `direction` re-derives the aim from a
			// bearing that sweeps through 180 degrees as he goes past, and the
			// passing side flips with it, so the jet rolled madly through every
			// merge - measured at a 130 deg/s p90 in the two seconds around the
			// pass, which is exactly where a pilot has to read which way the
			// bandit is turning. Referenced to my own flight path it is a
			// steady committed pull, which is what a lead turn IS.
			if b.turning == 0 {
				b.turning, b.turned = turn, tick
			}
			b.aim = b.commit(me, b.turning)
			b.throttle, b.reheat = 0.7, 0 // corner the pull, don't rocket past it
			if b.plan == "one" {
				b.throttle = 0.75 // the radius fight is tighter, not powerless — half throttle at a merge just donates the energy
			}
		}
	default: // flanking: lead-turn into his future
		b.mode = "offense"
		b.saddle, b.press = 0, 0 // angles not yet held: the press clock runs only behind his 3/9
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
	if b.skill.library >= 3 && b.tangle > int(1800/b.skill.cadence) && !b.pressing(tick) {
		// (never disengage while PRESSING — the reset exists for fights that
		// cannot convert, and a held advantage is the conversion happening)
		b.mode = "reset"
		b.press = 0
		b.aim = level(direction.Scale(-1))
		b.throttle, b.reheat = 1, 1
		b.safed = "extend"
		b.shoot = false
		b.hold = tick + 600 // ten committed seconds of extension
		b.tangle = 0
		return
	}

	// Stalemate displacement (tier 3+): a mutual circle between equal jets
	// never resolves by rate — after ~8 s without progress, cut ACROSS the
	// circle on a lag line toward his six, committed for three seconds.
	if (b.mode == "neutral" || b.mode == "offense") && distance > 800 && distance < 3000 && math.Abs(closure) < 60 && tail < 0.85 {
		// (crossing targets only: a dead-astern runner is a CHASE — the lag
		// cut that breaks a mutual circle just trails a runner at constant
		// range forever, which is how the battery caught an ace parked 1.5 km
		// behind a drone for five straight minutes)
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
		b.settle(tick)
		b.aim = level(direction.Scale(-1))
		b.throttle, b.reheat = 1, 1
		b.safed = "bookkeeping"
		b.shoot = false
	}

	// Inside gun range the aim is the LEAD POINT: rounds fly real time of
	// flight now, so the bore belongs where the target WILL be — his velocity
	// carries him across the flight, my own velocity rides on every round,
	// and gravity pulls the round the whole way. This mirrors the gunnery's
	// real flight exactly (a bot that aims at the man himself misses every
	// crosser, which is precisely the deflection game). In the control zone,
	// SADDLE: kill the closure and hold the track.
	if b.shoot && b.prey != nil && distance < b.skill.open*1.4 {
		time := distance / math.Max(battle.Average(distance, me.Position.Y)+closure, 200)
		lead := predict(prey, horizon+time, b.skill.library >= 3).
			Subtract(me.Velocity.Scale(time)).
			Add(flight.Vec3{Y: 4.9 * time * time})
		b.aim, _ = i.bearing(me.Position, lead)
		if direction.Dot(nose) > 0.94 && tail > 0.2 {
			b.mode = "saddle"
			b.g = math.Min(b.g, 4)                        // tracking is a 2 g business: staying far off the g-limiter keeps the demand out of the boundary-trim regime (#131), whose faster integration rattles fine corrections
			b.throttle = clamp(0.7-closure*0.006, 0.2, 1) // match his speed, sit in the zone
			b.reheat = 0
			if closure > 90 && b.skill.library >= 3 {
				b.brake = 1
			}
			if b.pressing(tick) {
				// The PRESS (#144): the advantage has been held long enough —
				// stop parking at the polite range and finish. Close to the
				// finishing gap and park THERE: the gun's dispersion is
				// angular, so half the range is four times the hit density,
				// and it is the sustained short-range track that kills (an
				// overtake through the zone measured worse than the patience
				// it replaced — churn breaks the very track that finishes).
				b.mode = "press"
				goal := clamp((distance-b.tactics.press.gap)*0.15, 0, b.tactics.press.closure)
				b.throttle = clamp(0.7-(closure-goal)*0.006, 0.2, 1)
				b.brake = 0
				if closure > goal+60 && b.skill.library >= 3 {
					b.brake = 1
				}
			}
			b.hold = tick + 45 // stay on the track: churn is what breaks gun solutions
		}
	}

	i.polish(slot, a, tick, speed, pace, nose, direction, distance, tail)
}

// polish is the shared tail of every fight decision — the g the airframe can
// actually give, the missile request, the terrain guard, fuel discipline, and
// the aim wander. Both the section ladder and the duel arbiter end here.
// flinch is the shot-cue jink's detection (#251): is the attacker LINING UP?
// The tier ladder is the design: a novice never sees it coming; a pilot reacts
// only to tracers already past; an ace reads the planform cue — the bore
// swinging into lead inside gun range, which is exactly what "his underside
// is appearing" means geometrically; the machine mirrors the attacker's whole
// firing solution and also reacts to the muzzle flash itself, dodging during
// the rounds' time of flight — which physics defeats only at close range,
// forcing the shooter to fly better rather than the defender being tougher.
// The battle model has priced this defence since real time of flight landed
// ("a jink after the trigger is a real defence") and no bot ever used it.
func (i *instance) flinch(slot int, a *craft, tick uint64, menace int, gap float64) {
	b := a.brain
	b.menace = menace
	if b.skill.library < 2 || menace < 0 || tick < b.dodge {
		return
	}
	c := i.aircraft[menace]
	if c == nil || c.model == nil || !c.alive || gap > 1200 {
		b.glimpsed = 0
		return
	}
	me := &a.model.State
	his := &c.model.State
	bore := his.Attitude.Rotate(flight.Vec3{X: 1})
	line := me.Position.Subtract(his.Position)
	span := math.Max(line.Length(), 1)
	// His aim error ON ME, the mirror of pipper(): bore against my led
	// future position with his rounds' real average speed.
	closure := his.Velocity.Subtract(me.Velocity).Dot(line.Scale(1 / span))
	transit := span / math.Max(battle.Average(span, his.Position.Y)+closure, 200)
	future := me.Position.Add(me.Velocity.Scale(transit)).
		Subtract(his.Velocity.Scale(transit)).
		Add(flight.Vec3{Y: 4.9 * transit * transit})
	aim := future.Subtract(his.Position)
	forming := math.Acos(clamp(bore.Dot(aim.Normalize()), -1, 1)) * span
	cued := false
	switch {
	case b.skill.machine:
		// The solution mirror, and the muzzle flash: his rounds fly for
		// transit seconds, and an out-of-plane pull started NOW beats any
		// tracking shot fired from stand-off range.
		cued = forming < 70 || (c.latest.Fire && forming < 220)
	case b.skill.library >= 3:
		// The planform cue, at gun range — plus the tracers, which this tier
		// was alone in not having (#38, measured 2026-08-16). The rule below
		// gives a PILOT a dodge whenever he is fired on from inside 160, and
		// the machine one from inside 220, but the ace's cue read the aim
		// solution only: it waited for 45 m, about four degrees at gun range,
		// and ignored `Fire` entirely. So the best human tier was the least
		// responsive to being shot at, which is backwards. Pinned at its own
		// six by a scripted attacker the ace died in all six seeds — three of
		// them inside 2.2 s, on fire by 1.9 — where the machine survived half
		// of them and never let the attacker inside 380 m.
		cued = (span < 900 && forming < 45) || (c.latest.Fire && forming < 160)
	default:
		cued = c.latest.Fire && forming < 160 // the pilot needs tracers already past
	}
	if !cued {
		b.glimpsed = 0
		return
	}
	if b.glimpsed == 0 {
		b.glimpsed = tick
	}
	react := uint64(b.skill.react * 60)
	if tick-b.glimpsed < react {
		return
	}
	// Intent gates the flinch (#251): a bot whose OWN kill is forming holds
	// the track and accepts the risk — two perfect deniers never resolve.
	if b.intent == "finish" && !c.latest.Fire {
		return
	}
	if b.walked > 0 && b.walked < 30 && !b.skill.machine {
		return // my pipper is nearly on: the exchange favours me
	}
	b.dodge = tick + uint64(45+((tick/30)%3)*15) // 0.75-1.25 s of evasion, varied so a shooter cannot time it
	b.glimpsed = 0
}

func (i *instance) polish(slot int, a *craft, tick uint64, speed, pace float64, nose, direction flight.Vec3, distance, tail float64) {
	b := a.brain
	me := &a.model.State

	// Wounded flying, re-asserted for BOTH paths: the duel arbiter writes b.g
	// from its chosen play, which discarded the preamble's shed-structure cap.
	if me.Damage.Loss > 0 {
		b.g = math.Min(b.g, 4.5)
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
	// Run-in burner discipline (#255): with missiles in play the afterburner
	// is a beacon — the plume-conditioned seeker locks a lit nose out to half
	// the envelope and a cold one only close aboard. The instructor tiers run
	// the approach at MIL when pointed in and fast enough to afford it; the
	// low tiers keep advertising, which is authentic and is how they die.
	if b.skill.library >= 3 && i.missiles && b.prey != nil && distance < 3500 &&
		nose.Dot(direction) > 0.2 && speed > pace*0.85 {
		b.reheat = 0
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
	if b.missiles > 0 && b.shoot && (b.skill.discipline < 0.7 || (tail > b.tactics.missile.tail && distance < b.tactics.missile.span)) {
		margin := b.tactics.missile.margin + b.tactics.missile.step*b.skill.discipline
		limit := missile_range * (b.tactics.missile.base + b.tactics.missile.slope*math.Max(0, tail)) * (b.tactics.missile.floor + b.tactics.missile.gain*b.skill.discipline)
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
			b.safed = "hold-missile"
		} else if a.model.State.Fuel < 1361 {
			if rationed(b.prey, b.distance) {
				b.reheat = 0
			}
		}
	}

	// The aim wander: where this pilot's nose actually points. Re-rolled on a
	// slow clock, NOT per decision — sloppiness is a consistent bias, which is
	// exactly why sloppy pilots are easy to track and gun, while per-decision
	// noise had made even the rookie untrackable to an ace.
	b.offset[0] = (battle.Roll(i.environment.Seed, uint64(slot)+13, tick/150) - 0.5) * 2 * b.skill.wander
	b.offset[1] = (battle.Roll(i.environment.Seed, uint64(slot)+29, tick/150) - 0.5) * 2 * b.skill.wander
	// The machine's defensive JINK (#236): deliberate, not noise. Perfectly
	// smooth flight is perfectly predictable flight — the wander tiers get
	// an accidental jink for free, and the tier without it was measured
	// losing the top of the ladder to burn-downs from crossing shots that
	// only connect against a clean line. Superseded by the shot-cue flinch
	// (#251), which jinks every tier on the cue its skill can read instead
	// of weaving the machine on a posture.
}

// committed reports whether a teammate's missile is already chasing the
// target (#144 deconfliction): a second SHOOTER on one bandit wastes half
// the section's warshots while another bandit roams free. A bot's own
// follow-up shot stays legal (shoot-shoot-look is doctrine), and a missile
// gone ballistic no longer blocks — nothing is chasing anymore.
func (i *instance) committed(slot int, a *craft, target int) bool {
	if a.team == "" || a.brain == nil || a.brain.solo {
		return false
	}
	for _, m := range i.flying {
		if m.target != target || m.loose || m.shooter == slot {
			continue
		}
		if shooter := i.aircraft[m.shooter]; shooter != nil && shooter.team == a.team {
			return true
		}
	}
	return false
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

// commit builds the lead-turn aim: my own flight path rotated by the cut angle
// in the committed direction. Independent of where he is, so it does not spin
// when he passes.
func (b *brain) commit(me *flight.State, turn float64) flight.Vec3 {
	flat := flight.Vec3{X: me.Velocity.X, Z: me.Velocity.Z}
	if flat.Length() < 1 {
		flat = me.Attitude.Rotate(flight.Vec3{X: 1})
		flat = flight.Vec3{X: flat.X, Z: flat.Z}
	}
	flat = flat.Normalize()
	sin, cos := math.Sin(turn*b.tactics.lead.angle), math.Cos(turn*b.tactics.lead.angle)
	return flight.Vec3{X: flat.X*cos - flat.Z*sin, Y: 0.05, Z: flat.X*sin + flat.Z*cos}.Normalize()
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
	b.tracking = false // re-earned every tick: a latched track would settle the wings for the whole fight
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
		// fire=false for THIS tick only: the old fireHold() here flipped the
		// persistent doctrine flag, so one brush with the recovery margin —
		// routine in a hard descending pass, exactly where crossing shots
		// live — silenced the gun until a future decide happened to re-arm
		// it. The offence instrument found the majority of every tier's
		// gate-open moments spent in "saddle" with the gun safed by this.
		return b.compose(m, aim, want, 1, 1, 0, false, tick)
	}

	// The pipper takes the stick (#235): inside the gun envelope, with the
	// play already carrying the nose near the solution, steer the LED point
	// rather than the play's control point. The plays are pursuit GEOMETRY —
	// lag points, yo-yo apexes — and nothing in them ever commanded the bore
	// onto the firing solution, so the nose crossed it only by accident:
	// measured, the machine held a solution for 0.4% of its in-range time
	// while flying textbook offence. The cone keeps the takeover honest —
	// far off the solution the play's geometry still owns the stick, so lag
	// pursuit is not collapsed into pure pursuit and the overshoot it
	// prevents stays prevented. Wander applies AFTER: a novice's pipper
	// still wanders.
	// The commanded evasion (#251): while the dodge stands, the stick flies
	// an out-of-plane weave — aperiodic (two incommensurate frequencies) so
	// a patient shooter cannot time it — and the gun track yields. This is
	// the manoeuvre the round-flight model has priced since real time of
	// flight landed: rounds resolve on arrival, and a defender who moves
	// during their flight is not where they were aimed.
	if tick < b.dodge {
		side := aim.Cross(flight.Vec3{Y: 1}).Normalize()
		rise := side.Cross(aim).Normalize()
		phase := float64(tick)
		aim = aim.Add(side.Scale(0.5 * math.Sin(phase/8))).Add(rise.Scale(0.45 * math.Cos(phase/5.3))).Normalize()
		want = math.Max(want, b.skill.pull*0.9)
	}
	if b.shoot && b.prey != nil && tick >= b.quiet && b.magazine > 0 && tick >= b.dodge {
		if direction, _, span, _ := b.pipper(m, tick); span < b.skill.open*1.15 && direction.Dot(aim) > 0.94 {
			b.tracking = true
			// Aim PAST the solution by twice the residual: the pursuit law
			// is proportional-only, so against a drifting lead direction it
			// settles with a standing error — measured 0.7 degrees, which is
			// seven metres at gun range, which is a miss astern where the
			// airframe is edge-on. Tripling the apparent error divides the
			// standing error by three; the cone above bounds the input, so
			// the boost cannot wrench the jet.
			nose := s.Attitude.Rotate(flight.Vec3{X: 1})
			aim = direction.Scale(3).Subtract(nose.Scale(2)).Normalize()
			// The walk needs authority: compose sizes the turn by the
			// pointing error against this ceiling, and the approach plays
			// fly low ones — under which a one-degree residual closes at a
			// tenth of a degree per second and the whole magazine leaves
			// before the pipper arrives (measured: 16 m of believed miss
			// decaying 1.5 m/s while 578 rounds passed 9-17 m off a
			// straight-and-level target). The ceiling is the skill's full
			// pull, aero-capped: a hard-turning target slews the lead
			// direction at twenty degrees a second, and a four-g tracking
			// allowance tops out at six — measured as a machine whose best
			// aim all fight was 26 m while flying textbook pursuit. The
			// error-proportional sizing still keeps the demand gentle once
			// converged; the ceiling only matters while the pipper is being
			// DRIVEN to him.
			pace := corner(m)
			stall := pace / math.Sqrt(m.Airframe.Limit.Positive)
			cap := math.Max(0.85*(speed/stall)*(speed/stall), 1.1)
			want = math.Max(want, math.Min(b.skill.pull, cap))
		}
	}

	// Aim wander: the skill's imprecision, as a pointing error.
	side := aim.Cross(flight.Vec3{Y: 1}).Normalize()
	rise := side.Cross(aim).Normalize()
	aim = aim.Add(side.Scale(b.offset[0])).Add(rise.Scale(b.offset[1])).Normalize()

	// The burst governor (#206): the magazine is 578 rounds and the fight is
	// minutes long, so fire is a deliberate SQUEEZE — up to ~0.75 s, then a
	// mandatory half-second pause. When the gun-live fix landed without this,
	// every tier hosed its ammunition at marginal windows and went Winchester
	// before the kill geometry arrived: the drone ladder read 0/4/4/1.
	fire := false
	if b.shoot && b.prey != nil && tick >= b.quiet && b.magazine > 0 {
		fire = b.solution(m, tick)
	}
	if fire { // the squeeze-and-pause paces every tier now that the trigger is a wager: the price buys ONE burst, and the pause re-judges the next — without it the machine turned a three-percent shot into a six-second magazine dump (#235)
		span, rest := uint64(45), uint64(30)
		if b.intent == "finish" && b.skill.discipline >= 1 {
			// The kill is banked (#252): a disciplined pilot walks the long
			// burst in rather than politely pausing over a beaten opponent.
			span, rest = 100, 12
		}
		b.bursting++
		if b.bursting > span {
			b.bursting = 0
			b.quiet = tick + rest
			fire = false
		}
	} else if !fire {
		b.bursting = 0
	}
	return b.compose(m, aim, want, b.throttle, b.reheat, b.brake, fire, tick)
}

// pipper computes the led gun solution: the direction the bore must point
// for the stream to arrive where the target will be (#real-TOF — rounds fly
// real time of flight, inherit the shooter's velocity, and drop), how far
// the nose sits from it in metres at the target, the live range, and the
// total prediction horizon (track age + bullet transit).
func (b *brain) pipper(m *flight.Model, tick uint64) (direction flight.Vec3, miss, span, horizon float64) {
	s := &m.State
	age := float64(tick-b.prey.when) / 60
	curved := b.skill.library >= 3
	current := predict(b.prey, age, curved)
	line := current.Subtract(s.Position)
	span = math.Max(line.Length(), 1)
	closure := s.Velocity.Subtract(b.prey.velocity).Dot(line.Scale(1 / span))
	transit := span / math.Max(battle.Average(span, s.Position.Y)+closure, 200)
	spot := predict(b.prey, age+transit, curved).
		Subtract(s.Velocity.Scale(transit)).
		Add(flight.Vec3{Y: 4.9 * transit * transit})
	direction = spot.Subtract(s.Position).Normalize()
	nose := s.Attitude.Rotate(flight.Vec3{X: 1})
	miss = math.Acos(clamp(nose.Dot(direction), -1, 1)) * math.Max(span, 50)
	return direction, miss, span, age + transit
}

// solution decides the trigger. It is a WAGER, not a verdict (#235): the old
// gate demanded the nose inside a fixed metre band of the led point, which a
// PLANNING opponent essentially never grants — measured across sixteen
// bot-against-bot fights, the band was met on 0.0-0.4% of in-range ticks,
// nobody fired, and no fight ever resolved, while every single-sided
// instrument (drones, scripted turners) read healthy. The gate now estimates
// the chance a burst taken NOW walks through the target and fires when that
// chance clears the tier's price. Two terms set the chance: the aim error,
// against the width of the corridor the stream actually kills through; and
// the target's measured unpredictability (track.wobble), which widens where
// he might be by bullet arrival. The consequence the old gate could not
// express: against an erratic target the converged solution never comes, so
// waiting for it has zero value and the snapshot is worth its low odds —
// which is exactly how a human treats the same picture.
func (b *brain) solution(m *flight.Model, tick uint64) bool {
	if b.distance > b.skill.open {
		return false
	}
	_, miss, span, horizon := b.pipper(m, tick)
	// The chance the stream centre passes within the airframe's reach of
	// him: P(|N(miss, sigma)| < reach). The widths must be the REAL ones —
	// the first version of this gate used the kill corridor as the sigma,
	// which called a 20 m miss at 700 m a one-in-three shot; every tier
	// opened far out on those odds, dumped the magazine on the approach,
	// and arrived at the kill range Winchester (TestConvert went 0/12 on a
	// straight-and-level offerer). Sigma is what actually scatters the
	// stream centre relative to him: the gun's dispersion cone, the
	// prediction drift his measured unpredictability buys over the bullet's
	// flight (wobble*h^3/6 — a steady turn's is negligible at gun ranges, a
	// reversal's is not), this pilot's own aim wander over the burst, and a
	// floor for the FCS's fine-tracking jitter.
	// Reach is the vulnerable half-width the stream must land inside. Five
	// metres, not the airframe's paper envelope: from dead astern — where
	// most gun kills live — the wings are edge-on and eight metres of
	// claimed reach called a certain miss a 79 percent shot, which the
	// machine then spent its whole belt on.
	reach := 5.0
	scatter := 0.004 * span
	drift := b.prey.wobble * horizon * horizon * horizon / 6
	waver := b.skill.wander * span
	sigma := math.Sqrt(scatter*scatter + drift*drift + waver*waver)
	// A burst being WALKED is finished, not re-judged: while the error is
	// shrinking the stream is converging on him and the next rounds are the
	// best-priced of the fight. This is the correction loop of real gunnery
	// — the first rounds find the line, the walk kills.
	if b.bursting > 0 && miss < b.walked-0.3 {
		b.walked, b.walkedAt = miss, tick
		b.chanced = tick
		return true
	}
	// The crossing shot (#235): against mutual manoeuvre nobody TRACKS the
	// lead point — it slews at up to forty degrees a second at close range
	// and the airframe answers with fifteen. Kills come from the bore
	// SWEEPING through the solution, and a trigger that prices frozen
	// instants refuses exactly that shot: by the time the instant is good
	// the sweep has passed it. Price the burst on where the sweep will
	// bottom over the next four tenths — rounds fired now arrive as the
	// bore crosses. Measured before this: the machine's best aim across
	// whole four-minute fights was 26 m, one never-fired standoff, while
	// the ace's aim WANDER accidentally produced the crossings that won it
	// fights up the ladder.
	sweep := 0.0
	if tick == b.walkedAt+1 && b.walked > miss {
		sweep = (b.walked - miss) * 60
	}
	b.walked, b.walkedAt = miss, tick
	// Only a FAST sweep is a crossing: eighty metres a second separates a
	// bore slicing through the solution (hundreds) from a pursuit steadily
	// converging on it (tens). Discounting the slow kind re-opened the
	// approach-phase magazine dump this gate exists to prevent — measured
	// straight back to seven of twelve on the conversion referendum.
	judged := miss
	if sweep > 80 {
		judged = math.Max(0, miss-0.4*(sweep-80))
	}
	root := sigma * math.Sqrt2
	chance := 0.5 * (math.Erf((reach-judged)/root) - math.Erf((-reach-judged)/root))
	// Conservation: the magazine is ~8 seconds of fire for a fight minutes
	// long, so the last rounds demand better odds than the first.
	// No pressing discount: the old tolerance needed one because it could
	// not see range; the chance already prices range, drift, and this
	// pilot's own scatter, and halving the price just opened fire at 700 m
	// on two-percent shots — measured dumping the whole belt on the
	// approach to a straight-and-level offerer.
	price := b.skill.trigger * (1.6 - 0.6*clamp(float64(b.magazine)/float64(rounds), 0, 1))
	if chance > price {
		b.chanced = tick
		return true
	}
	return false
}

// compose turns an aim direction and a g demand into stick, through the same
// UA law a player flies: stick = (g − level)/(ceiling − level), rolled so the
// lift vector carries the pull toward the aim.
func (b *brain) compose(m *flight.Model, aim flight.Vec3, want, throttle, reheat, brake float64, fire bool, tick uint64) flight.Inputs {
	s := &m.State
	speed := math.Max(s.Velocity.Length(), 1)
	// Roll error in the VELOCITY frame: current lift vector vs the one the aim
	// demands, both perpendicular to the flight path. The body-frame solution
	// wobbled with every nose bobble and never settled.
	//
	// The demand is a LIFT vector, not the pointing error: gravity support
	// (skyward, in that same plane) plus the turning component the error calls
	// for, so bank falls out of the ratio between the two. Steering on the
	// error direction alone made bank bang-bang — on a level run-in the
	// correction is purely horizontal, so a 1° heading error demanded a lift
	// vector 90° from vertical, hence full roll stick, and nothing in the law
	// said stop at any particular bank. Measured: the ace rolled through
	// ±180° with a ~3.5 s period for the whole run-in to the merge.
	vhat := s.Velocity.Normalize()
	skyward := flight.Vec3{Y: 1}.Subtract(vhat.Scale(vhat.Y))
	if skyward.Length() < 0.05 { // straight up or down: no horizon to reference, so any perpendicular serves
		skyward = s.Attitude.Rotate(flight.Vec3{Y: 1})
		skyward = skyward.Subtract(vhat.Scale(skyward.Dot(vhat)))
	}
	perp := skyward.Normalize()
	// `want` is the doctrine's CEILING, not its demand: the turn is sized by
	// the pointing error, and reaches the ceiling only once the error is big
	// enough to need all of it — a dozen-odd degrees. Pinning the pull at the
	// skill's structural g regardless of whether any turn was needed made the
	// ace haul 7 g down a straight-line intercept, which threw its own nose
	// off the line — and the roll law then answered that self-inflicted error
	// with full bank. Both halves of the limit cycle came from this one line.
	// Size it the way the geometry does: the turn rate that nulls the error in
	// `settling` seconds is off/settling, and g = v·ω/9.81. So the demand is
	// speed-aware (a fast jet needs more g for the same correction) and
	// saturates at doctrine's ceiling once the error is big enough to want
	// everything the airframe has — which is every real turning fight.
	const settling = 0.7 // seconds to null a pointing error
	turn := 0.0
	lateral := aim.Subtract(vhat.Scale(aim.Dot(vhat)))
	off := lateral.Length()                  // sin of the pointing error
	adrift := math.Atan2(off, aim.Dot(vhat)) // the error ITSELF, 0..pi
	toward := perp                           // which way to pull; skyward until the aim says otherwise
	if off > 1e-3 {
		toward = lateral.Scale(1 / off)
	}
	// Anticipate: size the turn on where the error will be in a third of a
	// second, not where it is. Proportional-only, the nose arrives at the aim
	// still carrying turn rate and sails past — which no single time constant
	// fixes, because tight enough to hold a gun track hunted around a
	// formation station and loose enough to sit on the station lagged the
	// track. The rate is clamped and smoothed because `aim` steps at the
	// decision cadence, not every tick, and a step would read as huge rate.
	rate := clamp((adrift-b.aimed)*60, -6, 6)
	b.aimed = adrift
	b.closing += (rate - b.closing) * 0.2
	if adrift > 1e-4 {
		// math.Max(want, 1): a PUSH still banks off the same 1 g reference —
		// scaling by a negative g demand would point the demand away from the
		// target and roll the wrong way.
		predicted := math.Max(adrift+b.closing*0.35, 0)
		turn = clamp(speed*predicted/(9.81*settling), 0, math.Max(want, 1))
		perp = perp.Add(toward.Scale(turn)).Normalize()
	}
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
	closure := (b.ahead - ahead) * 60 // rad/s toward the aim, from last tick
	b.ahead = ahead
	// Settled: soften PREDICTIVELY — on where the nose will be in ~0.25 s,
	// not where it is. The old proportional cone (soften inside 1.7°) was
	// tuned when the sluggish C* law was itself the rate filter; against the
	// fixed law the same shaping oscillated the nose ±3° through the pipper
	// and the ace's gunnery collapsed (193 -> 57 hits across the gate's
	// seeds). Never fade to zero — a dead damper parked the nose in a 3-6°
	// standoff orbit around the aim, permanently outside the gun gate.
	if future := ahead - closure*0.25; future < 0.05 {
		soften := math.Max(future/0.05, 0.35)
		plane *= soften
		roll *= soften
	}
	level := clamp(math.Hypot(s.Velocity.X, s.Velocity.Z)/speed, 0, 1) // cos γ, the 1 g trim the law interpolates from
	// The stick is normalised against the ceiling the FCS will actually
	// enforce THIS tick, rolling reduction included (fcs.go, NATOPS 11.1.7:
	// 20% of the limiter gone from a quarter of lateral travel — keep the two
	// schedules in sync). Before this, pitch was scaled against the untaxed
	// structural limit, so during every rolling correction the delivered g
	// undershot the demanded lift by up to a fifth and the nose lagged the
	// solution it was chasing — the deficit behind the 70 m aim floor against
	// manoeuvring targets (#215 cause 2, bisected to the law landing in
	// 64a2ad6). This is the OPPOSITE of the rejected roll damper above: roll
	// keeps its full authority, and the pull is simply honest about what
	// rolling costs. The slewed roll stick is computed first for exactly this
	// reason.
	rolled := clamp(clamp(roll*1.4-s.Omega.X*0.45, -1, 1)-b.rolled, -0.12, 0.12) + b.rolled
	// The compensation only applies where the jet is G-LIMITED — above corner
	// speed, where extra stick genuinely buys back the taxed lift. Below
	// corner the limiter is ALPHA, and pressing into the tax just camps the
	// alpha limiter: the roll rate dies with the wing and tracking collapses.
	// Measured both ways on the first cut, which compensated everywhere: the
	// fast fights improved sharply (guns superhuman-v-ace 3-3 to 7-4, drone
	// pilot/ace +2 kills each) while every slow fight collapsed (the flounder
	// went from hunted to UNTOUCHED — zero rounds — and the jinker shed 90%
	// of the rounds previously landed on it). Slow fights keep the original
	// normalisation and the plane gate's roll-first sequencing instead.
	limited := clamp((speed-0.9*corner(m))/math.Max(0.25*corner(m), 1), 0, 1)
	ceiling := m.Airframe.Limit.Positive * (1 - 0.2*limited*clamp((math.Abs(rolled)-0.25)/0.75, 0, 1))
	floor := -want // scale symmetric: forward stick interpolates level→Limit.Negative in the law
	// The load the demanded lift vector actually represents: gravity support
	// and the turn, at right angles. Aligned and settled it falls to `level` —
	// stick centred, wings level, no self-inflicted nose excursion to chase.
	pitch := clamp((math.Hypot(level, turn)*plane-level)/math.Max(ceiling-level, 0.5), -1, 1)
	if want < 0.5 {
		pitch = clamp((want-level)/3.5, -1, 0) // pushes bypass the lift-plane gate: recovery, not pursuit
	}
	_ = floor
	// Roll rate feeds back, so the jet ARRIVES at the demanded bank instead of
	// accelerating through it: a pure error term reached the target at full
	// roll rate every time and flew straight past, and past 140° the sense
	// lock above then committed it to carrying on round.
	// TRIED AND REJECTED (#215): damping the roll while the pipper tracks, to
	// spare the g the rolling reduction taxes. It bought the ace's firing
	// solutions back (3.46% -> 4.81% of in-range time) and cost far more
	// elsewhere — the machine tracks almost continuously, so damping its
	// roll took its manoeuvring with it: the guns ladder inverted 5-1 to 2-9
	// against the ace, and TestConvert fell from 12/12 to 9/12. A bot that
	// aims better and loses is not better.
	b.rolled = rolled // slewed above: full deflection over ~8 ticks, never a flap
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

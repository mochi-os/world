// Mochi world: The duel arbiter — manoeuvre selection by forward simulation
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A teamless bot REHEARSES rather than picking from a ladder of gates: each
// candidate is flown forward through the real flight model, scored, and
// committed until its horizon expires. Offence and defence are one decision.

package air

import (
	"math"

	"world/games/air/battle"
	"world/games/air/flight"
)

// moment is the geometry one candidate law reads: my live (or simulated)
// state against the opponent's predicted position and velocity.
type moment struct {
	me        *flight.State
	prey      flight.Vec3 // his predicted position
	velocity  flight.Vec3 // his predicted velocity
	ring      orbit       // his estimated turning circle
	pace      float64     // my corner speed
	pull      float64     // the skill's g ceiling
	direction flight.Vec3 // unit, me toward him
	distance  float64
	closure   float64
	grain     float64 // committed merge side, +1/-1; 0 = uncommitted (#251): a lead turn that re-picks its side as the geometry drifts reads as dithering and IS dithering
}

// derive fills the dependent fields from the endpoints.
func (m *moment) derive() {
	los := m.prey.Subtract(m.me.Position)
	m.distance = math.Max(los.Length(), 1)
	m.direction = los.Scale(1 / m.distance)
	m.closure = m.me.Velocity.Subtract(m.velocity).Dot(m.direction)
}

// flat returns my normalized horizontal flight path.
func (m *moment) flat() flight.Vec3 {
	f := flight.Vec3{X: m.me.Velocity.X, Z: m.me.Velocity.Z}
	if f.Length() < 1 {
		n := m.me.Attitude.Rotate(flight.Vec3{X: 1})
		f = flight.Vec3{X: n.X, Z: n.Z}
	}
	return f.Normalize()
}

// swing rotates my horizontal flight path by an angle (radians, + toward his
// side when signed by the caller) and lifts it just off the horizon.
func (m *moment) swing(angle float64) flight.Vec3 {
	f := m.flat()
	sin, cos := math.Sin(angle), math.Cos(angle)
	return flight.Vec3{X: f.X*cos - f.Z*sin, Y: 0.05, Z: f.X*sin + f.Z*cos}.Normalize()
}

// lead is the ballistic intercept point the gunnery flies: his position at
// bullet arrival, in the shooter's accelerating frame, with gravity drop.
func (m *moment) lead() flight.Vec3 {
	transit := m.distance / math.Max(battle.Average(m.distance, m.me.Position.Y)+m.closure, 200)
	return m.prey.Add(m.velocity.Scale(transit)).
		Subtract(m.me.Velocity.Scale(transit)).
		Add(flight.Vec3{Y: 4.9 * transit * transit})
}

// lag is the control point behind him on his own circle — flight-path lag
// when the circle is not readable.
func (m *moment) lag() flight.Vec3 {
	if m.ring.valid {
		return m.ring.behind(m.prey, 0.85)
	}
	back := m.velocity
	if back.Length() > 1 {
		back = back.Normalize()
	}
	return m.prey.Subtract(back.Scale(math.Max(250, m.distance*0.3)))
}

// side reports which side of my flight path he sits: +1 right, -1 left.
func (m *moment) side() float64 {
	v := m.me.Velocity
	if v.Length() < 1 {
		v = m.me.Attitude.Rotate(flight.Vec3{X: 1})
	}
	cross := v.Cross(m.direction)
	if cross.Y >= 0 {
		return -1
	}
	return 1
}

// aloft aims from my position toward a world point.
func (m *moment) aloft(point flight.Vec3) flight.Vec3 {
	to := point.Subtract(m.me.Position)
	if to.Length() < 1 {
		return m.direction
	}
	return to.Normalize()
}

// order is one candidate command set.
type order struct {
	aim      flight.Vec3
	g        float64
	throttle float64
	reheat   float64
	brake    float64
}

// play is one candidate manoeuvre: a closed-loop control law, recomputed from
// fresh geometry at every decision while committed — the rollout flies the
// same law it judges.
type play struct {
	name string
	tier int     // library tier that unlocks it
	span float64 // rehearsal horizon, seconds; 0 = the tier default (#248): flat pursuit is honestly judged in four seconds, a vertical manoeuvre's payoff lies past eight — one shared horizon systematically biased the arbiter toward flat orbiting (measured: under 4% vertical against a floater begging for it)
	law  func(m *moment) order
}

// plays is the candidate library, in fixed order (map iteration would
// re-litigate #225). Tiers: a novice owns pursuit, the breaks, and running
// away; the circle work, the vertical, and the reversal arrive with skill.
var plays = []play{
	{"press", 1, 0, func(m *moment) order {
		o := order{aim: m.direction, g: m.pull, throttle: 1, reheat: 1}
		speed := m.me.Velocity.Length()
		if m.distance < 1200 {
			o.aim = m.aloft(m.lead()) // in range: fly the gun solution, not the jet
		}
		if m.distance < 900 {
			// Terminal closure discipline: park at the finishing gap with the
			// overtake spent — arriving hot blows through the saddle and turns
			// the kill into another run-in from a mile out.
			goal := clamp((m.distance-250)*0.15, 0, 45)
			o.throttle = clamp(0.7-(m.closure-goal)*0.006, 0.2, 1)
			o.reheat = 0
			if m.closure > goal+50 {
				o.brake = 1
			}
			// ...and BURN for the gap when he is running (#49): MIL alone held
			// this play 620-640 m behind a target in full burner, opening at
			// 3-10 m/s, for the four seconds a 4-7 degree track lasted. Behind
			// him the plume is hidden from the seeker that matters.
			if speed < m.velocity.Length()+goal-30 {
				o.reheat = 1
			}
		} else if m.distance < 1500 {
			o.throttle = clamp(1-(m.closure-40)/200, 0.35, 1)
			o.reheat = 0
			if speed < m.pace-60 {
				o.reheat = 1
			}
		}
		return o
	}},
	{"left", 1, 0, func(m *moment) order {
		return order{aim: m.swing(-1.6), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"right", 1, 0, func(m *moment) order {
		return order{aim: m.swing(1.6), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"extend", 1, 0, func(m *moment) order {
		away := level(m.direction.Scale(-1))
		return order{aim: away, g: 2.5, throttle: 1, reheat: 1}
	}},
	{"lag", 2, 0, func(m *moment) order {
		o := order{aim: m.aloft(m.lag()), g: m.pull * 0.92, throttle: 1}
		if m.me.Velocity.Length() < m.pace {
			o.reheat = 1
		}
		return o
	}},
	{"low", 2, 6, func(m *moment) order {
		point := m.prey // the gun-frame lead point is a phantom beyond gun range: it swings with my own velocity and the jet chases it in circles
		if m.distance < 1400 {
			point = m.lead()
		}
		return order{aim: m.aloft(point.Subtract(flight.Vec3{Y: 300})), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"high", 3, 7, func(m *moment) order {
		o := order{aim: m.aloft(m.lag().Add(flight.Vec3{Y: 450})), g: m.pull * 0.85, throttle: 0.9}
		if m.closure > 90 {
			o.brake = 1
		}
		return o
	}},
	{"reverse", 3, 0, func(m *moment) order {
		side := m.grain
		if side == 0 {
			side = m.side()
		}
		o := order{aim: m.swing(side * 1.9), g: m.pull, throttle: 0.35}
		if m.me.Velocity.Length() > m.pace {
			o.brake = 1
		}
		return o
	}},
	{"saddle", 2, 0, func(m *moment) order {
		// The slow saddle (#250): park astern at HIS speed - a 280 m perch with a
		// speed bias that closes gently from behind and never blows through.
		o := order{aim: m.aloft(m.lead()), g: m.pull * 0.85, throttle: 0.6}
		his := m.velocity.Length()
		mine := m.me.Velocity.Length()
		want := his + clamp((m.distance-280)*0.12, -25, 60)
		o.throttle = clamp(0.55+(want-mine)*0.012, 0, 1)
		if mine > want+25 {
			o.brake = 1
		}
		if mine < want-50 {
			o.reheat = 1
		}
		return o
	}},
	{"screw", 3, 0, func(m *moment) order {
		// The displacement (#250): when arriving with overtake to spare,
		// climb off-axis above his six in proportion to the excess — the
		// path lengthens, the closure dies, and the position holds instead
		// of blowing through. Throttle and boards carry the rest.
		excess := m.closure - 25
		point := m.lead()
		if excess > 0 && m.distance < 800 {
			point = point.Add(flight.Vec3{Y: clamp(excess*4, 0, 350)})
		}
		o := order{aim: m.aloft(point), g: m.pull * 0.9, throttle: clamp(0.7-excess*0.008, 0.2, 1)}
		if excess > 60 {
			o.brake = 1
		}
		return o
	}},
	{"pitch", 3, 9, func(m *moment) order {
		// The pitch-back (#250): bank altitude off an energy-dead opponent, then pull
		// through onto him. Staged on STATE, not time, so one law carries both phases
		// and the rollout can fly it whole - hence the long horizon.
		rise := m.me.Position.Y - m.prey.Y
		speed := m.me.Velocity.Length()
		if rise < 600 && speed > m.pace*0.75 {
			f := m.flat()
			return order{aim: flight.Vec3{X: f.X * 0.5, Y: 0.85, Z: f.Z * 0.5}.Normalize(), g: 4.5, throttle: 1, reheat: 1}
		}
		o := order{aim: m.aloft(m.lead()), g: m.pull, throttle: 0.75}
		if m.closure > 60 {
			o.brake = 1
		}
		return o
	}},
	{"cross", 3, 0, func(m *moment) order {
		// The merge lead-turn (#250): closing fast and nose-on, turn EARLY
		// across his path so the pass ends angles-on instead of neutral;
		// once past, pull hard into him. The arbiter's merges were straight
		// — every fight restarted from scratch after the pass.
		vhat := m.me.Velocity
		if vhat.Length() > 1 {
			vhat = vhat.Normalize()
		}
		side := m.grain
		if side == 0 {
			side = m.side()
		}
		if m.closure > 250 && m.distance < 1100 && m.direction.Dot(vhat) > 0.5 {
			// Turn inside the last second of the closure, not before: an early lead turn
			// serves seconds of crossing target to an opponent holding nose-on, and
			// every tier now owns the crossing shot.
			return order{aim: m.swing(side * 0.55), g: m.pull, throttle: 1}
		}
		// Past the pass: CONTINUE the committed turn onto him - one readable
		// direction through the whole manoeuvre. Aiming at the lead point instead
		// lets the lift-vector law pick either roll direction per tick.
		return order{aim: m.swing(side * 2.0), g: m.pull, throttle: 1, reheat: 1}
	}},
	{"split", 2, 7, func(m *moment) order {
		// The split-S (#251): when there is sky to spend, roll through and
		// dive out — separation the pursuer must pay the same altitude to
		// follow. Below the sky it needs, it degrades to the flat run so
		// the rollout judges an honest law everywhere.
		if m.me.Position.Y > 2200 {
			f := m.flat()
			return order{aim: flight.Vec3{X: -f.X * 0.25, Y: -0.92, Z: -f.Z * 0.25}.Normalize(), g: m.pull, throttle: 1}
		}
		away := level(m.direction.Scale(-1))
		return order{aim: away, g: 3, throttle: 1, reheat: 1}
	}},
	{"trap", 3, 6, func(m *moment) order {
		// The completed overshoot trap (#251): while he is behind, break across to
		// spend his closure; the moment he crosses my 3/9 line, reverse INTO him and
		// convert.
		vhat := m.me.Velocity
		if vhat.Length() > 1 {
			vhat = vhat.Normalize()
		}
		if m.direction.Dot(vhat) < -0.1 {
			side := m.grain
			if side == 0 {
				side = m.side()
			}
			o := order{aim: m.swing(side * 1.7), g: m.pull, throttle: 0.45}
			if m.me.Velocity.Length() > m.pace {
				o.brake = 1
			}
			return o
		}
		if m.distance < 600 {
			// He has JUST crossed — the overshoot the break bought: reverse
			// into him and convert while his nose is off me.
			return order{aim: m.aloft(m.lead()), g: m.pull, throttle: 1, reheat: 1}
		}
		// Nobody is trapped: he is ahead and FAR, which is a merge, and converting
		// there is a head-on charge into his guns. Hold lag and keep the energy until
		// there is something to spring.
		return order{aim: m.aloft(m.lag()), g: m.pull * 0.9, throttle: 1}
	}},
	{"climb", 4, 8, func(m *moment) order {
		f := m.flat()
		return order{aim: flight.Vec3{X: f.X, Y: 0.6, Z: f.Z}.Normalize(), g: 3, throttle: 1, reheat: 1}
	}},
	{"ride", 3, 6, func(m *moment) order {
		// The limiter ride (#63): square the nose onto the gun solution and
		// command double the doctrine ceiling — the FCS alpha limiter, never
		// the paddle, is the boundary, exactly the transient the player wins
		// angle fights with. polish()'s aero cap and guard()'s climb lid step
		// aside for this play alone; the rollout prices the speed the point
		// donates, so the arbiter only buys it when the shot pays for it, and
		// choose() offers it only inside a claimed FINISH with a clean six.
		return order{aim: m.aloft(m.lead()), g: m.pull * 2, throttle: 1}
	}},
}

// evolve advances the opponent's track by dt along the LOCAL CURVE: velocity
// plus observed swing, the same model the gunnery's lead uses. Speed is
// preserved rather than integrated. The circle estimate is deliberately not
// used here - it judges standing on his turn and picks the lag point.
func evolve(t *track, dt float64) (flight.Vec3, flight.Vec3) {
	position := t.position.Add(t.velocity.Scale(dt)).Add(t.swing.Scale(0.5 * dt * dt))
	velocity := t.velocity.Add(t.swing.Scale(dt))
	if speed := t.velocity.Length(); speed > 1 && velocity.Length() > 1 {
		velocity = velocity.Normalize().Scale(speed)
	}
	return position, velocity
}

// posture is the fight-level intent (#236) expressed as weights on the one
// scorer — never a second planner: the layers cannot fight each other when
// the upper one only re-prices what the lower one already measures.
type posture struct {
	offence float64
	threat  float64
	energy  float64
	closing float64
}

// stances: CONVERT presses for a firing solution; DENY spoils his and buys
// separation; RESET rebuilds deliberately when converting has provably
// stalled; FINISH spends everything on a beaten opponent. Neutral is the
// novice's whole strategic life — fighting moment to moment is authentic.
var stances = map[string]posture{
	"":        {1, 1, 1, 1},
	"convert": {1.5, 0.8, 0.8, 1.3},
	"deny":    {0.5, 1.8, 1.3, 0.6},
	"reset":   {0.3, 1.2, 2.2, -0.4},
	"finish":  {2.2, 0.4, 0.4, 1.6},
}

// husband grades how hard a jet must nurse its energy: 1 while the engines are
// healthy, falling to 0 at the limp threshold. Only a fight against a PLAYER
// reaches the graded middle - a damaged bot dies within moments.
func husband(me *flight.State) float64 {
	thrust := 1 - (me.Damage.Engine[0]+me.Damage.Engine[1])/2
	return clamp((thrust-0.35)/0.55, 0, 1) // 0 at the limp threshold, 1 by 0.90
}

// judge re-decides the posture on TREND, not instantaneous geometry: whether
// the pursuit gains, how long since a shot was worth taking, and his energy -
// the four-second arbiter cannot represent a plan that pays at twenty. The
// posture is COMMITTED between re-judgements; DENY and RESET carry exits.
func (b *brain) judge(me *flight.State, prey *track, tick uint64, distance float64, menace int, gap float64) {
	if b.skill.library < 2 {
		return // the novice holds no fight-level intent
	}
	commit := uint64(600) // 10 s; the pilot switches slower
	if b.skill.library < 3 {
		commit = 900
	}
	threatened := menace >= 0 && gap < 1600
	if b.intent != "" && tick-b.minded < commit {
		// Commitment guards PATIENCE - riding out a reset, not abandoning a
		// conversion the moment it feels slow. It must not hold against the two
		// signals that cannot wait: an attacker arriving, and an opponent beaten.
		urgent := (threatened && b.intent != "deny") ||
			(b.skill.library >= 3 && b.intent != "finish" && (prey.velocity.Length() < 170 && distance < 2200 || b.promise > 0.6))
		if !urgent {
			return
		}
	}
	// FINISH needs the advantage actually in hand: his energy collapsed,
	// mine intact, and enough sky under me to spend. Without the guard the
	// posture chose itself off the opponent's state alone — including while
	// low, slow, and pointing away, where "spend everything" is suicide.
	speed := me.Velocity.Length()
	mine := speed*speed/2 + 9.81*me.Position.Y
	him := prey.velocity.Length()
	his := him*him/2 + 9.81*prey.position.Y
	slow := him < 170 && distance < 2200 && mine > his*1.2 && me.Position.Y > 600
	// FINISH on OPPORTUNITY: a line whose rehearsal spends most of its instants ON
	// the gun solution is a saddle held, not hoped for. It claims on PARITY rather
	// than the `slow` claim's fifth of energy - a held solution is spent in
	// seconds - but never below the skill's speed floor, and never below the deck.
	opportune := b.skill.library >= 3 && b.promise > 0.6 && distance < 1500 && mine >= his && speed > b.skill.floor && me.Position.Y > 600
	slow = slow || opportune
	starving := tick-b.chanced > 2700 && b.nearing < 12 // 45 s without a shot worth its price, and the range is not coming down
	next := "convert"
	// FINISH must be WOUNDING to keep its claim: its entry condition renews itself
	// every commitment window, so the starvation escape never notices a stalled
	// one. Younger than thirty seconds, or wounding inside thirty, is working;
	// otherwise it yields to convert with a minute's cooldown.
	finishing := b.intent == "finish"
	wounding := tick-b.minded < 1800 || (b.struck > 0 && tick-b.struck < 1800)
	switch {
	case slow && b.skill.library >= 3 && finishing && !wounding:
		b.finished = tick
		next = "convert"
	case slow && b.skill.library >= 3 && husband(me) < 0.5:
		next = "convert" // hurt: a beaten opponent is still worth converting, but FINISH spends everything and the engines cannot refill it
	case slow && b.skill.library >= 3 && (finishing || b.finished == 0 || tick-b.finished > 3600):
		next = "finish"
	case threatened:
		next = "deny"
	case b.intent == "reset" && tick-b.minded < 900 && distance < 4000:
		// A reset runs its full spell, bounded by RANGE as well as time: two evenly
		// matched bots both reset, and the negative closing weight pays each to open.
		// Past four kilometres it is not rebuilding, it is leaving.
		next = "reset"
	case starving && b.intent != "reset":
		next = "reset"
	}
	if next != b.intent {
		if next == "convert" || next == "reset" {
			b.chanced = tick // the conversion clock restarts with the attempt
		}
		b.intent, b.minded = next, tick
	}
}

// appraise scores one instant of a rehearsed future. Positive is a fight
// being won: his tail toward my pointed nose inside the gun band. Negative is
// a fight being lost: his nose behind my tail, or the sea arriving.
func appraise(s *flight.State, hisP, hisV flight.Vec3, pace float64, w posture, sk *skill, ring orbit) (float64, float64) {
	los := hisP.Subtract(s.Position)
	r := math.Max(los.Length(), 1)
	lhat := los.Scale(1 / r)
	nose := s.Attitude.Rotate(flight.Vec3{X: 1})
	speed := math.Max(s.Velocity.Length(), 1)
	vhat := s.Velocity.Scale(1 / speed)
	hv := hisV
	if hv.Length() > 1 {
		hv = hv.Normalize()
	}
	rear := hv.Dot(lhat)    // 1: I look up his tailpipes
	point := nose.Dot(lhat) // 1: my nose is on him
	ahead := lhat.Dot(vhat) // 1: he is ahead of my path, -1: behind me
	band := clamp((r-60)/190, 0, 1) * clamp((1500-r)/800, 0, 1)
	near := clamp((2500-r)/1500, 0, 1)
	offence := clamp(rear, 0, 1) * clamp(point, 0, 1) * clamp(point, 0, 1) * band
	threat := clamp(-rear, 0, 1) * clamp(-ahead, 0, 1) * near
	// The head-on trade (#45): `threat` prices him BEHIND me, so a mutual nose-on
	// pass scored zero danger and the arbiter learned that jousting at the merge
	// wins coin flips. Lead-turning off the line has to outscore riding it in.
	joust := clamp(-rear, 0, 1) * clamp(ahead, 0, 1) * clamp((1400-r)/900, 0, 1)
	// Energy is a CONSTRAINT, not a currency (#45): rewarding speed and altitude
	// out-votes any play that slows, so there is no energy reward here - only a
	// penalty when I am below fighting speed AND he holds the edge to punish it.
	// No pursuit term either: every remedy tried broke BVR, where standing off IS
	// the discipline.
	hungry := clamp((0.85*pace-speed)/(0.35*pace), 0, 1)
	// The chase gradient: outside the gun band the geometry terms flatten to zero,
	// so without this the choice at 3 km is decided by selection noise. It fades
	// approaching the band, where arriving hot is the blown pass.
	closing := s.Velocity.Subtract(hisV).Dot(lhat)
	// The RELATIVE energy truth (#248): the fight's currency is the difference,
	// not the balance - this is what makes zooming off a floater score as winning.
	// Its weight is a skill: the novice chases the nose and ignores it.
	mine := speed*speed/2 + 9.81*s.Position.Y
	his := hisV.Length()*hisV.Length()/2 + 9.81*hisP.Y
	edge := clamp((mine-his)/(pace*pace/2), -1, 1)
	// The circle truth (#248): where I sit relative to HIS turn circle.
	// Inside it, modest angles convert; outside it, the same angles are an
	// overshoot being prepared. Instructor tiers only — this is the piece of
	// BFM that arrives at the weapons school.
	standing := 0.0
	if ring.valid && r < 2500 {
		radial := s.Position.Subtract(ring.centre)
		planar := radial.Subtract(ring.normal.Scale(radial.Dot(ring.normal)))
		standing = clamp((ring.radius-planar.Length())/math.Max(ring.radius, 200), -1, 1)
	}
	// The range term is deliberately shallow: every geometry term dies by 2.5 km,
	// so beyond that this gradient is nearly the whole scorer and steepening it
	// reweights the merge and gun bands too. Pulling a distant bot back wants a
	// term that is zero inside weapons range, not a bigger multiplier here.
	score := w.offence*offence - 1.3*w.threat*threat - 0.8*w.threat*joust - w.energy*hungry*clamp(edge*-1, 0, 1) - r/12000 +
		sk.geometry*0.25*standing*clamp((2500-r)/1500, 0, 1) +
		w.closing*0.35*clamp(closing/400, -1, 1)*clamp((r-500)/1200, 0, 1) +
		w.offence*0.15*point - // nose toward him is progress at any range: a reversal's value shows as swing long before it shows as a gun band
		0.5*clamp((closing-70)/150, 0, 1)*clamp((900-r)/600, 0, 1) // the blown pass, priced: arriving hot inside the merge cannot be stopped by any law (stopping distance alone exceeds the range), and without this the incumbent full-burner play tied the disciplined one and zero-noise argmax never escaped it — the machine overshot every pass it flew
	// The deck is NON-NEGOTIABLE, whatever the posture: these penalties sit
	// outside the weights because FINISH's discounted threat and energy let
	// otherwise-marginal low lines score positive.
	if s.Position.Y < 400 {
		score -= 6
	}
	if s.Velocity.Y < 0 {
		score -= 1.5 * clamp((700-s.Position.Y)/300, 0, 1)
	}
	if speed < 80 {
		score -= 1
	}
	// Midair avoidance (#215), a penalty and NOT a play: 14 m kills both jets for
	// no credit, so a line threading within collision range is scored down across
	// EVERY play. Costs no offence - the gun band is already zero inside 60 m.
	if sk.library >= 3 && r < 70 {
		score -= 8 * clamp((70-r)/56, 0, 1) // 0 at 70 m, -8 at the 14 m kill radius
	}
	return score, offence
}

// rehearse flies one candidate law forward through the real flight model - the
// same airframe, FCS, and executor imperfections the live bot flies - and
// returns the mean score and, beside it, the mean raw offence (brain.promise).
func (i *instance) rehearse(a *craft, b *brain, sim *flight.Model, chosen play, prey *track, tick uint64, horizon int) (float64, float64) {
	sim.State = a.model.State
	sim.State.Damage = sim.State.Damage.Copy() // the struct copy shares Element/Jam with the LIVE jet; a damage-writing Step would corrupt it mid-fight
	shadow := *b                               // the executor's scalar state rides along; maps are untouched
	shadow.shoot = false
	pace := corner(a.model)
	age := float64(tick-prey.when) / 60
	score, offence, samples := 0.0, 0.0, 0.0
	for k := 1; k <= horizon; k++ {
		t := float64(k) / 60
		hisP, hisV := evolve(prey, age+t)
		m := moment{me: &sim.State, prey: hisP, velocity: hisV, ring: b.ring, pace: pace, pull: b.skill.pull, grain: b.turning}
		m.derive()
		o := chosen.law(&m)
		if rehearsal.reduced() {
			// The point-mass surrogate flies the ORDER directly: no stick, no
			// FCS, no blade element (#256).
			for sub := 0; sub < rehearsal.substeps(); sub++ {
				glide(sim, o, rehearsal.span())
			}
		} else {
			shadow.aim, shadow.g, shadow.throttle, shadow.reheat, shadow.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
			in := shadow.steer(sim, tick+uint64(k))
			for sub := 0; sub < rehearsal.substeps(); sub++ {
				sim.Step(in) // the flight core steps at 240 Hz: four substeps per 60 Hz rollout tick, exactly like the live path. One Step per tick ran the WHOLE rehearsal at quarter time — the phantom moved four times faster than the jet, every candidate scored against that fiction, and the rollouts barely discriminated (the press-versus-extend trace that exposed it flew identical paths). `coarse` reaches the same sim-time honestly, by stepping once at four times the span
			}
		}
		if sim.State.Position.Y < 120 {
			return -100, 0 // flew it into the sea: veto, whatever else it bought
		}
		if k%30 == 0 || k == horizon {
			// Every sampled instant counts alike: rounds land during the trajectory, so
			// time on the solution is itself the payoff and a terminal weighting
			// discounts the tracking the fight is won by. Nursed weights bend the same
			// stance rather than adding a second doctrine.
			stance := stances[b.intent]
			if nurse := husband(&a.model.State); nurse < 1 {
				stance.energy *= 1 + 2.0*(1-nurse)
				stance.offence *= 0.4 + 0.6*nurse
			}
			one, guns := appraise(&sim.State, hisP, hisV, pace, stance, &b.skill, b.ring)
			score += one
			offence += guns
			samples++
		}
	}
	return score / samples, offence / samples
}

// allowance is how many bots may rehearse in one tick. Two is comfortably
// above the natural rate — a bot re-plans about every 1.5 s, so even 99 of
// them want only ~1.1 re-plans per tick — and it exists for the COINCIDENCE,
// which is what turned a healthy median into multi-second stalls.
const allowance = 2

// choose runs the candidate rehearsal and returns the winning play and its mean
// offence - the raw gun-solution share of the line about to be flown, which
// judge() reads as an opportunity (brain.promise).
func (i *instance) choose(slot int, a *craft, b *brain, sim *flight.Model, prey *track, tick uint64, distance float64, scores map[string]float64) (string, float64) {
	// The horizon must outlive the manoeuvres it judges — in REAL seconds,
	// now that the rollout clock is honest: 2.5 s for the novice up to 4 s
	// for the top tiers, enough for a reversal's payoff to show through the
	// point-progress term without quadrupling the rehearsal budget.
	base := 60*2 + 30*b.skill.library
	best, top, promise, n := b.play, math.Inf(-1), 0.0, 0
	for _, p := range plays {
		if p.tier > b.skill.library {
			continue
		}
		if p.name == "extend" && distance > 2500 {
			continue // already separated: rehearsing more separation buys nothing
		}
		if p.name == "ride" && (b.intent != "finish" || prey.velocity.Length() < 170 || !i.serene(slot)) {
			continue // the point donates the jet's energy: only inside a claimed FINISH, with nothing hunting me, and against a target still fast enough to contest the angles — a crawling one is the saddle's kill (#49)
		}
		horizon := base
		if p.span > 0 && int(p.span*60) > horizon {
			horizon = int(p.span * 60)
		}
		score, guns := i.rehearse(a, b, sim, p, prey, tick, horizon)
		// Selection noise is the skill's wander: the ace nearly argmaxes,
		// the novice sometimes picks the second-best line and flies it well.
		score += (battle.Roll(i.environment.Seed, uint64(slot)+57, tick, uint64(n)) - 0.5) * b.skill.wander * 2
		// Personality (#252): a per-MISSION bias on the repertoire - no tick in the
		// hash, so it holds for the whole fight and the next mission draws a
		// different opponent. Sized to tie-break only.
		score += (battle.Roll(i.environment.Seed, uint64(slot)+97, 0, uint64(n)) - 0.5) * 2 * math.Max(0.004, b.skill.wander*0.7)
		if p.name == b.play {
			score += 0.02 // ties keep the committed line: churn is its own cost (a larger incumbency rode broken lines past the moment press should take over — measured 6/6 kills falling to 3/6 on the conversion referendum)
		}
		if scores != nil {
			scores[p.name] = score
		}
		if score > top {
			best, top, promise = p.name, score, guns
		}
		n++
	}
	return best, promise
}

// duel is the teamless fight brain: rehearse, commit, fly. Replaces the mode
// ladder (which remains the section doctrine) from the moment a target is
// held. The caller runs the shared tail (aero cap, missile request, guard,
// fuel, wander) after it.
func (i *instance) duel(slot int, a *craft, tick uint64, prey *track, direction flight.Vec3, distance, tail float64, menace int, gap float64) {
	b := a.brain
	me := &a.model.State
	nose := me.Attitude.Rotate(flight.Vec3{X: 1})

	// The closure trend, on a ~1.5 s memory: the intent layer judges the
	// pursuit by whether it GAINS, not by where it points.
	if b.gauged != 0 && tick > b.gauged {
		dt := float64(tick-b.gauged) / 60
		rate := (b.spanned - distance) / dt
		b.nearing += clamp(dt/1.5, 0.05, 1) * (rate - b.nearing)
	}
	b.spanned, b.gauged = distance, tick
	if b.chanced == 0 {
		b.chanced = tick // the conversion clock starts when the fight does
	}
	b.judge(me, prey, tick, distance, menace, gap)

	// The press clock (the finisher's deliberate looseness, #144): held
	// advantage in range starts it, losing the range stops it.
	if distance < b.tactics.press.span && tail > 0.35 && nose.Dot(direction) > 0.5 {
		if b.press == 0 {
			b.press = tick + 1
		}
	} else if distance > b.tactics.press.span || tail < 0 {
		b.press = 0
	}

	// Re-judge when the committed line expires, or when the picture breaks it: an
	// attacker arriving close behind invalidates any offensive line, and HIS
	// overshoot (#251) opens a window the normal cadence sleeps through.
	vhat := me.Velocity
	if vhat.Length() > 1 {
		vhat = vhat.Normalize()
	}
	bearing := direction.Dot(vhat)
	overshot := b.flanked < -0.15 && bearing > 0.15 && distance < 700 && b.skill.library >= 3
	b.flanked = bearing
	offensive := b.play == "press" || b.play == "lag" || b.play == "low" || b.play == "high" || b.play == "climb"
	if b.play == "" || tick >= b.until || (offensive && menace >= 0 && gap < 700 && tick-b.picked >= 15) || (overshot && tick-b.picked >= 10) {
		// The tick's rehearsal allowance (#256): cost is bounded by CONSTRUCTION - a
		// roster of any size spends only so many re-plans per tick, and the rest
		// wait. Deterministic by slot order, never wall clock, as the gates need.
		if i.rehearsals >= allowance {
			b.until = tick + 4 // come back for the allowance in a few ticks, keeping the committed line meanwhile
		} else {
			i.rehearsals++
			sim := flight.New(a.model.Airframe, a.model.Environment, a.model.World)
			b.play, b.promise = i.choose(slot, a, b, sim, prey, tick, distance, nil)
			b.picked = tick
			b.until = tick + uint64(math.Max(54, b.skill.commit*24)) // the commitment: ~0.9 s floor, 1.6 s at the top — the machine included, whose edge is reflex and precision, not strategy churn
		}
	}

	// Fly the committed law against the LIVE geometry — commitment holds the
	// line, never a stale aim point.
	pace := corner(a.model)
	m := moment{me: me, ring: b.ring, pace: pace, pull: b.skill.pull}
	age := float64(tick-prey.when) / 60
	// The LIVE aim predicts with the track's measured swing, not the fitted
	// circle: gunnery needs local precision and a weaving target breaks any circle
	// fit. The circle keeps its place in the rollouts' multi-second arcs.
	m.prey = predict(prey, age, b.skill.library >= 3)
	m.velocity = prey.velocity.Add(prey.swing.Scale(age))
	m.derive()
	// The merge side is committed ONCE per pass (#251): re-picking it per tick as
	// the geometry drifts reads as roll dithering. b.turning holds the side in the
	// classic path, and the arbiter keeps it too.
	if bearing > 0.5 && distance < 2500 {
		if b.turning == 0 {
			b.turning = m.side()
		}
	} else if (bearing < -0.2 && distance > 900) || distance > 3000 {
		b.turning = 0 // released only once the pass is genuinely spent: dropping the commitment AT the crossing un-committed the very turn the gate reads
	}
	m.grain = b.turning
	for _, p := range plays {
		if p.name == b.play {
			o := p.law(&m)
			b.aim, b.g, b.throttle, b.reheat, b.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
			break
		}
	}
	b.mode = b.play
	// The trigger is ALWAYS live in a duel: the led solution gate and the
	// burst governor decide each round. Doctrine safing the gun was the #206
	// offensive deficit, found one wired-shut window at a time.
	b.shoot = true
}

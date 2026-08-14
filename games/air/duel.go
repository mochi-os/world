// Mochi world: The duel arbiter — manoeuvre selection by forward simulation
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A teamless bot does not pick its manoeuvre from a ladder of hand-tuned
// gates. It REHEARSES: each candidate manoeuvre is flown forward through the
// real flight model against the opponent's estimated turn circle, the outcome
// geometry is scored, and the winner is committed until its horizon expires.
// Offence and defence are the same decision — a break turn and a reversal
// into attack are just candidates, so the moment an attacker overshoots, the
// attacking line starts outscoring the defensive one with no hand-written
// bridge between "modes". Skill shapes the machinery, not the outcome list:
// the library gates which candidates exist, wander adds selection noise, and
// commitment sets how long a chosen line is flown before re-judging.

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
		// The slow saddle (#250): park astern at HIS speed. The anti-floater
		// staple the library lacked — measured orbiting at 135-233 m/s
		// against a 90 m/s target for minutes, every approach an overshoot.
		// Station keeps a 280 m perch with a speed bias that closes gently
		// from behind and never blows through.
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
		// The pitch-back (#250): bank altitude off an energy-dead opponent,
		// then pull through the top onto him. Staged on STATE, not time, so
		// one law carries both phases and the rollout can fly the whole
		// manoeuvre: while the advantage is unbanked and the jet has climb
		// speed, zoom; once banked, attack. Needs the relative-energy truth
		// to score and the long horizon to be judged fairly.
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
			// Turn inside the last second of the closure, not before: an
			// early lead turn is three seconds of crossing target served to
			// an opponent who holds nose-on — and every tier now owns the
			// crossing shot, so the bet went from cheap to fatal (measured:
			// the ace lead-turning from 2 km lost the bottom ladder rung to
			// the pilot's face shots).
			return order{aim: m.swing(side * 0.55), g: m.pull, throttle: 1}
		}
		// Past the pass: CONTINUE the committed turn around onto him — one
		// readable direction through the whole manoeuvre. Aiming at the lead
		// point from 200 m behind the crossing left the lift-vector law free
		// to pick either roll direction per tick, and TestMergeRoll read it
		// as the dithering it was.
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
		// The completed overshoot trap (#251): while he is behind, break
		// across to spend his closure; the moment he crosses my 3/9 line,
		// reverse INTO him and convert. The old flat reverse ended at the
		// first phase — a forced overshoot bought nothing.
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
		// Nobody is trapped: he is ahead and FAR, which is a merge, and the
		// convert stage flown there is a head-on charge into his guns — it
		// killed an ace at t=10.5 of a ladder fight, first pass, detonated.
		// A trap without a trapped opponent is patience: hold lag and keep
		// the energy until there is something to spring.
		return order{aim: m.aloft(m.lag()), g: m.pull * 0.9, throttle: 1}
	}},
	{"climb", 4, 8, func(m *moment) order {
		f := m.flat()
		return order{aim: flight.Vec3{X: f.X, Y: 0.6, Z: f.Z}.Normalize(), g: 3, throttle: 1, reheat: 1}
	}},
}

// evolve advances the opponent's track by dt seconds along the LOCAL CURVE:
// his velocity plus the swing (his observed acceleration), which is the same
// model the gunnery's own lead is built on (predict()). Speed is preserved
// rather than integrated, because a turn changes a jet's direction and not
// much else.
//
// This used to swing him around his estimated turn circle, capped at 2.6
// radians of arc. TestYouModel measured that against both scripted humans
// and it was not prediction, it was fantasy: wrong by 49 m at a quarter
// second, 192 m at one second, and 750 m at the four-second rollout horizon,
// where the local curve holds 2 m and 58 m — and where a DEAD STRAIGHT LINE
// scored 6.9 m and 100 m, beating the circle 28-fold. Every candidate play
// was being ranked against a phantom hundreds of metres from where the
// target actually went, which is the arbiter's whole discrimination problem
// in one number. The circle estimate stays where it earns its place: judging
// where I sit relative to his turn (appraise's standing term) and picking
// the lag point, both of which read the circle NOW rather than extrapolating
// a reversal into a future that never happens.
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

// judge re-decides the posture. Trend, not instantaneous geometry: the
// signals are whether the pursuit is actually gaining (nearing), how long
// since the trigger last had a shot worth taking (chanced), and his energy
// state — the four-second play arbiter below cannot represent "he is out of
// energy, leave, rebuild, come back", because that plan scores badly at four
// seconds and only pays at twenty. The posture is COMMITTED between
// re-judgements or the churn defect returns one level up; DENY and RESET
// carry their own exits so neither becomes the unbounded flee this layer
// exists to end — measured before it: a pilot-tier bot ran flat out for
// three and a half minutes from an attacker who never closed, and the
// fight could not resolve because nothing in either bot could say "this is
// settled, turn around".
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
		// Commitment guards PATIENCE — riding out a reset, not abandoning a
		// conversion the moment it feels slow. It must not hold a posture
		// against the two signals that cannot wait, the same exception the
		// play arbiter has always carried for a committed line: an attacker
		// arriving (measured: the machine died in convert, fifteen hits
		// taken at 300 m while its four-second-old posture was locked), and
		// an opponent suddenly beaten, whose recovery is bought back at
		// twenty metres a second.
		urgent := (threatened && b.intent != "deny") ||
			(b.skill.library >= 3 && b.intent != "finish" && prey.velocity.Length() < 170 && distance < 2200)
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
	starving := tick-b.chanced > 2700 && b.nearing < 12 // 45 s without a shot worth its price, and the range is not coming down
	// FINISH must also be WORKING. Deny and reset each carry an exit; this
	// one had none, and its entry condition is the OPPONENT's state, which
	// stays true for as long as he stays slow — so the posture renewed
	// itself every commitment window and the starvation check below, the
	// one signal that notices a conversion which is simply not happening,
	// could never be reached. Measured once the opponent model was made
	// honest (2026-08-13), which is what let the machine judge a target
	// beaten with confidence: against a compliant drone it entered finish
	// and flew pitch-backs for three minutes — 4237 ticks inside gun range
	// at a 412 m aim error — while the ace, which never entered finish,
	// killed the same drone six times from six. Spending everything on a
	// beaten opponent is only a plan while it is buying something.
	next := "convert"
	// FINISH must be WOUNDING to keep its claim. Its entry condition is the
	// OPPONENT's state, which holds for as long as he stays slow, so the
	// posture renews itself every commitment window and the starvation
	// escape below can never notice it (a stalled finish keeps taking
	// priced shots: firing and not killing). A thirty-second SPELL with a
	// re-entry guard was measured and rejected — slow-target conversion
	// legitimately runs past thirty seconds, and the time bound evicted the
	// ace from finishes it was winning (flounder 16 kills to 13, the ace
	// alone 7 to 4). The key is rounds landing: a finish younger than
	// thirty seconds, or one that has wounded him inside the last thirty,
	// is working; one doing neither is pitch-backs at a target it cannot
	// hit (measured: 4237 in-range ticks at a 412 m aim error while the
	// ace killed the same drone six from six), and it yields to convert
	// with a minute's cooldown before finish may claim again.
	finishing := b.intent == "finish"
	wounding := tick-b.minded < 1800 || (b.struck > 0 && tick-b.struck < 1800)
	switch {
	case slow && b.skill.library >= 3 && finishing && !wounding:
		b.finished = tick
		next = "convert"
	case slow && b.skill.library >= 3 && (finishing || b.finished == 0 || tick-b.finished > 3600):
		next = "finish"
	case threatened:
		next = "deny"
	case b.intent == "reset" && tick-b.minded < 900 && distance < 4000:
		// A reset runs its full spell — rebuilding half an energy reserve buys
		// nothing — but it is bounded by RANGE as well as time. Two evenly
		// matched bots both starve at the same moment, both reset, and the
		// posture's negative closing weight then pays each of them to open:
		// the separation rate doubles and fifteen seconds of mutual burner
		// ends the fight ten kilometres apart. Measured before this: a neutral
		// ace-vs-pilot duel reached EIGHTEEN kilometres, spent 94% of four
		// minutes outside gun range, fired nothing, and 16 of 16 seeds ended
		// no-result. Four kilometres is the separation a reset was ever for;
		// past it the extension is not rebuilding, it is leaving.
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
func appraise(s *flight.State, hisP, hisV flight.Vec3, pace float64, w posture, sk *skill, ring orbit) float64 {
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
	energy := 0.25*clamp((speed-0.8*pace)/pace, -1, 0.5) + 0.1*clamp((s.Position.Y-800)/2200, 0, 1)
	// The chase gradient: outside the gun band the geometry terms flatten to
	// zero and a short rehearsal barely moves the range, so without this the
	// choice at 3 km is decided by the selection noise while the target sails
	// away. Closure is what a pursuit buys; it fades out approaching the band,
	// where arriving hot is the classic blown pass.
	closing := s.Velocity.Subtract(hisV).Dot(lhat)
	// The RELATIVE energy truth (#248): the fight's currency is the
	// difference, not the balance. The absolute term above keeps a bot from
	// dying slow in an empty sky; this one is what makes zooming off a
	// floater, or refusing to match a slow fight, score as winning — and its
	// weight is a skill: the novice chases the nose and ignores it.
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
	// The range term is deliberately shallow. Every geometry term below dies
	// at 2.5 km (standing) or 1.7 km (closing), so beyond that this gradient is
	// nearly the whole scorer, and steepening it to pull a distant bot back
	// reweights the merge and gun bands too: at r/6000 the ladder inverted —
	// superhuman v ace fell 3-3 to 0-1 and missiles 5-7 to 1-4 — while the
	// rollout traces looked better than ever, ranges holding 1-4 km with press
	// chosen over climb. Judge a doctrine change on kills and rounds; a trace
	// that reads well is a hypothesis, not evidence. Pulling a bot back from
	// the far field wants a term that is zero inside weapons range, not a
	// bigger multiplier on one that applies everywhere.
	score := w.offence*offence - 1.3*w.threat*threat + w.energy*energy - r/12000 +
		sk.energy*w.energy*0.4*edge +
		sk.geometry*0.25*standing*clamp((2500-r)/1500, 0, 1) +
		w.closing*0.35*clamp(closing/400, -1, 1)*clamp((r-500)/1200, 0, 1) +
		w.offence*0.15*point - // nose toward him is progress at any range: a reversal's value shows as swing long before it shows as a gun band
		0.5*clamp((closing-70)/150, 0, 1)*clamp((900-r)/600, 0, 1) // the blown pass, priced: arriving hot inside the merge cannot be stopped by any law (stopping distance alone exceeds the range), and without this the incumbent full-burner play tied the disciplined one and zero-noise argmax never escaped it — the machine overshot every pass it flew
	// The deck is NON-NEGOTIABLE, whatever the posture: these penalties are
	// deliberately outside the weights, because FINISH's discounted threat
	// and energy let otherwise-marginal low lines score positive — measured
	// as the machine flying itself into the sea at three kilometres from
	// its target, twice in eight duels, and losing the ladder to the tier
	// below it on drownings.
	if s.Position.Y < 400 {
		score -= 6
	}
	if s.Velocity.Y < 0 {
		score -= 1.5 * clamp((700-s.Position.Y)/300, 0, 1)
	}
	if speed < 80 {
		score -= 1
	}
	// Midair avoidance (#215), a penalty and NOT a play: 14 m kills both jets
	// for no kill credited — strictly a loss — and the rollout already carries
	// his predicted position, so a line threading within collision range is
	// scored down at exactly the instants it is close, across EVERY play. It
	// composes with BFM instead of a committed avoidance manoeuvre reacting a
	// re-plan too late at merge closure. Costs no offence: the gun band factor
	// is already zero inside 60 m, so nothing legitimate lives here. Instructor
	// tiers only — a green pilot flying into you is authentic, and it keeps the
	// novice ram in the game.
	if sk.library >= 3 && r < 70 {
		score -= 8 * clamp((70-r)/56, 0, 1) // 0 at 70 m, -8 at the 14 m kill radius
	}
	return score
}

// rehearse flies one candidate law forward through the real flight model —
// the same airframe, FCS, and executor imperfections the live bot flies —
// against the opponent's evolved track, and returns the MEAN score over the
// sampled instants. Every instant counts alike: see the sampling comment
// below for why the old terminal weighting was measured out.
func (i *instance) rehearse(a *craft, b *brain, sim *flight.Model, chosen play, prey *track, tick uint64, horizon int) float64 {
	sim.State = a.model.State
	sim.State.Damage = sim.State.Damage.Copy() // the struct copy shares Element/Jam with the LIVE jet; a damage-writing Step would corrupt it mid-fight
	shadow := *b                               // the executor's scalar state rides along; maps are untouched
	shadow.shoot = false
	pace := corner(a.model)
	age := float64(tick-prey.when) / 60
	score, samples := 0.0, 0.0
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
			return -100 // flew it into the sea: veto, whatever else it bought
		}
		if k%30 == 0 || k == horizon {
			// Every sampled instant counts alike. The old curve rose from
			// 0.4 to 1.0 across the horizon on the reasoning that BFM is
			// judged by where it ends up — true of POSITION, false of
			// GUNNERY: rounds land during the trajectory, so time on the
			// solution is itself the payoff, and discounting the early
			// instants discounted the tracking the fight is won by.
			// Measured 2026-08-13 on two independent seed blocks (the kill
			// counts are too sparse to read alone, so this is judged on
			// rounds landed, which replicated 4 comparisons of 4): flat
			// lands 1.6-4x the rounds on both scripted humans — jinker 130
			// against 66 and 69 against 44, flounder 235 against 57 and 182
			// against 81 — with kills equal or better, and it agrees with
			// the FULL model more, not less: 69.1% against 66.0%, costly
			// picks 5.3% against 6.7%. A steeper curve (0.15 to 1.0) was
			// measured too and lands between the two, so the gradient is
			// real rather than seed luck. All gates hold, including the two
			// that reject over-eager tracking (the drone ladder's
			// monotonicity and TestConvert).
			score += appraise(&sim.State, hisP, hisV, pace, stances[b.intent], &b.skill, b.ring)
			samples++
		}
	}
	return score / samples
}

// allowance is how many bots may rehearse in one tick. Two is comfortably
// above the natural rate — a bot re-plans about every 1.5 s, so even 99 of
// them want only ~1.1 re-plans per tick — and it exists for the COINCIDENCE,
// which is what turned a healthy median into multi-second stalls.
const allowance = 2

// choose runs the candidate rehearsal and returns the winning play. Extracted
// from duel (#256) so the fidelity experiment can ask the SAME selector for an
// answer at each fidelity from one live state, rather than measuring a copy of
// the loop — and so the eventual per-tick rehearsal budget has one place to
// live.
func (i *instance) choose(slot int, a *craft, b *brain, sim *flight.Model, prey *track, tick uint64, distance float64, scores map[string]float64) string {
	// The horizon must outlive the manoeuvres it judges — in REAL seconds,
	// now that the rollout clock is honest: 2.5 s for the novice up to 4 s
	// for the top tiers, enough for a reversal's payoff to show through the
	// point-progress term without quadrupling the rehearsal budget.
	base := 60*2 + 30*b.skill.library
	best, top, n := b.play, math.Inf(-1), 0
	for _, p := range plays {
		if p.tier > b.skill.library {
			continue
		}
		if p.name == "extend" && distance > 2500 {
			continue // already separated: rehearsing more separation buys nothing
		}
		horizon := base
		if p.span > 0 && int(p.span*60) > horizon {
			horizon = int(p.span * 60)
		}
		score := i.rehearse(a, b, sim, p, prey, tick, horizon)
		// Selection noise is the skill's wander: the ace nearly argmaxes,
		// the novice sometimes picks the second-best line and flies it well.
		score += (battle.Roll(i.environment.Seed, uint64(slot)+57, tick, uint64(n)) - 0.5) * b.skill.wander * 2
		// Personality (#252): a per-MISSION bias on the repertoire — no
		// tick in the hash, so it holds for the whole fight and the next
		// mission draws a different opponent rather than a replay. Large
		// where humans are inconsistent, tie-breaks only at the top:
		// the machine's optimality is never diluted, its near-ties just
		// stop being decided the same way every rematch.
		score += (battle.Roll(i.environment.Seed, uint64(slot)+97, 0, uint64(n)) - 0.5) * 2 * math.Max(0.004, b.skill.wander*0.7)
		if p.name == b.play {
			score += 0.02 // ties keep the committed line: churn is its own cost (a larger incumbency rode broken lines past the moment press should take over — measured 6/6 kills falling to 3/6 on the conversion referendum)
		}
		if scores != nil {
			scores[p.name] = score
		}
		if score > top {
			best, top = p.name, score
		}
		n++
	}
	return best
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

	// Re-judge when the committed line expires — or the picture breaks it: an
	// attacker arriving close behind invalidates any offensive line now, and
	// HIS overshoot through my 3/9 line (#251) opens a two-second window the
	// normal cadence sleeps through — the offensive twin of the threat
	// bypass, stamped with the tier's reflexes via the commitment floor.
	vhat := me.Velocity
	if vhat.Length() > 1 {
		vhat = vhat.Normalize()
	}
	bearing := direction.Dot(vhat)
	overshot := b.flanked < -0.15 && bearing > 0.15 && distance < 700 && b.skill.library >= 3
	b.flanked = bearing
	offensive := b.play == "press" || b.play == "lag" || b.play == "low" || b.play == "high" || b.play == "climb"
	if b.play == "" || tick >= b.until || (offensive && menace >= 0 && gap < 700 && tick-b.picked >= 15) || (overshot && tick-b.picked >= 10) {
		// The tick's rehearsal allowance (#256). Cost is bounded by
		// CONSTRUCTION here, not by hoping the constant stays small: a
		// roster of any size can only spend so many re-plans per tick, and
		// the rest wait a few ticks. The commitment they are re-judging
		// runs about a second, so the deferral is imperceptible — and it
		// is deterministic (slot order, not wall clock), which the
		// determinism gates require. Without it, an unauthenticated
		// session with a 99-bot roster put every re-plan on the same tick,
		// stalled the session goroutine for seconds, and the liveness
		// sweep then evicted everyone in it as 15-seconds-silent.
		if i.rehearsals >= allowance {
			b.until = tick + 4 // come back for the allowance in a few ticks, keeping the committed line meanwhile
		} else {
			i.rehearsals++
			sim := flight.New(a.model.Airframe, a.model.Environment, a.model.World)
			b.play = i.choose(slot, a, b, sim, prey, tick, distance, nil)
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
	// circle: gunnery needs local precision, and a weaving target breaks any
	// circle fit — the lead point wandered and the trigger starved (drone
	// ladder 0/0/3/5). The circle keeps its place in the rollouts, where
	// multi-second arcs are the question and a parabola diverges.
	m.prey = predict(prey, age, b.skill.library >= 3)
	m.velocity = prey.velocity.Add(prey.swing.Scale(age))
	m.derive()
	// The merge side is committed ONCE per pass (#251): TestMergeRoll's
	// claim — a disciplined lead turn is readable — regressed to 1.2-1.4
	// roll reversals when cross re-picked its side per tick as the
	// geometry drifted. b.turning was built for exactly this hold in the
	// classic path; the arbiter now keeps it too.
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

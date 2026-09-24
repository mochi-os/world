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
	"sort"

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
	weight   float64 // the live jet's flown mass, kg (stage 6): the rollout's scratch model is never weighed - glide fell back to the empty airframe and its internal fuel, a clean jet some 470 kg lighter than a bandit carrying heaters
	weighed  bool    // rehearse against the g ceiling the flight control system enforces, scheduled with gross weight (6.7 g on a combat-loaded jet), instead of the bare 7.5 g placard (stage 6, rollout.go glide)
	gravity  bool    // rehearse with the turn geometry that pays gravity across the path (tactics.gravity, rollout.go glide): off, every rehearsed turn also climbs at about one g
	alpha    bool    // licensed past the lift peak (stage 9): exempt from the aero cap and corner discipline, and rehearsed with glide's post-stall branch. One play holds it - bleed
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
			//
			// REFUTED 2026-09-13, recorded here so it is not rebuilt (#42). The
			// standing suspicion was that this law spends the overtake on RANGE
			// alone - goal knows nothing about where the nose is - and so parks
			// the jet abeam, which would fit the field's negative median closure
			// inside 900 m. Measured in the bot seat over 124,000 ticks inside
			// the finishing gap, it does the OPPOSITE:
			//     nose ON  (<30 deg)  103,483 ticks, parked (|closure|<10) 56.1%
			//     nose OFF (>30 deg)   21,150 ticks, parked                 6.2%
			// It spends the overtake when it holds the solution and keeps it
			// when it does not. saddle behaves the same (52.2% / 4.6%). The
			// geometry does the work the law does not: the jet only ARRIVES
			// here with the nose on, so a range-only goal is not the defect it
			// looked like, and making it angle-aware would be tuning against a
			// measurement that says the behaviour is already right.
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
	{"high", 2, 12, func(m *moment) order {
		// The high yo-yo (#153): climb out of his plane to shed closure, then
		// come down INSIDE his circle with lead. Staged on state like the
		// pitch-back, so one law carries both phases and the rollout flies the
		// whole manoeuvre - hence the long horizon. The old law was the climb
		// alone, re-aimed 450 m above the lag line at every re-plan, so a
		// chosen yo-yo became a 7,000-ft zoom to 174 kt and thirty seconds of
		// recovery; the human who won with this flew nine of 500-1,700 ft and
		// came down every time. The stager is the vertical speed: once the
		// nose is down it stays down to the bottom whatever the closure does
		// on the way, and the next yo-yo starts only from the bottom, with
		// closure to shed again. Tier 2, not 3: it is the pilot's only way
		// up. With every other vertical play at tier 3 or 4 its catalogue
		// could go flat or DOWN, and against a sinking slow target "300 m
		// below him in burner" was a dive that never ended - 528 kt mean and
		// a 120-second fight nobody won.
		rise := m.me.Position.Y - m.prey.Y
		speed := m.me.Velocity.Length()
		climbing := m.me.Velocity.Y > 0 || rise <= 0
		// Stop pulling when the FLOAT will top out where the yo-yo wants it,
		// not when the jet has already got there: at 150 m/s of vertical speed
		// the apex is a kilometre above the point the pull stops, whatever is
		// commanded after, because the wing at 200 kt cannot arrest it. That
		// is the zoom the old law flew and a rise cap alone still flies.
		apex := rise
		if m.me.Velocity.Y > 0 {
			apex += m.me.Velocity.Y * m.me.Velocity.Y / (2 * 9.81)
		}
		// The pull starts on an overshoot threat: closing on a target more
		// than sixty degrees off its tail. A tail chase closing at forty is a
		// shot forming, not a threat, and closure alone would climb out of it.
		aspect := 0.0
		if his := m.velocity.Length(); his > 1 {
			aspect = m.velocity.Scale(1 / his).Dot(m.direction)
		}
		threat := m.closure > 40 && aspect < 0.5
		if climbing && threat && apex < 450 && speed > m.pace*0.6 {
			o := order{aim: m.aloft(m.lag().Add(flight.Vec3{Y: 450})), g: m.pull * 0.85, throttle: 0.9}
			if m.closure > 90 {
				o.brake = 1
			}
			return o
		}
		return order{aim: m.aloft(m.lead()), g: m.pull, throttle: 1, reheat: 1}
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
	{"bleed", 3, 6, func(m *moment) order {
		// The energy dump (stage 9): idle, boards out, and the whole stick, to
		// take the wing past its lift peak on purpose. Speed falls a hundred
		// knots in seconds, the radius with it, and the nose rides thirty
		// degrees inside the flight path - onto him. It is what a human did to
		// this bot (recording 01a0b090: 254 kt to 138 and a 1,667 m radius to
		// 735 in fourteen seconds, head-on to dead astern), and the one turn
		// the catalogue could neither fly nor rehearse: every other play is
		// held under the aero cap, and glide() stopped at the lift peak. The
		// only play that carries the `alpha` licence. LAST in the table with
		// regain, and skipped below its stage (staged), so no other play's
		// noise draw moves.
		//
		// MEASURED AND DECLINED BY ITS OWN RULE (2026-09-18). The plan said in
		// advance that an ace tracked over 20% of a fight while converting
		// nothing is the #153 signature, and that is what the dump buys:
		//
		//                         tracked   converted   v current ace, guns / heaters
		//   stage 0                 18%       0.0%
		//   truth + ends            18%       0.1%        11-4  /  23-22
		//   truth + ends + bleed    36%       0.1%         5-14 /  17-29
		//   the whole stack (6-9)   34%       0.2%         7-3  /  27-20
		//
		// with the missile top rung inverted (superhuman lost to the ace 11-5),
		// gunnery against the jinker inverted, and the spiral gate's donated
		// perch at 309,584 m.s against 143,883 - chosen for only 3-4% of the
		// fight. It does what it was built for where it was built: both recorded
		// scenes it targets reverse (the human's dump ends with him on its tail
		// for 0.6 s of the last 3, from 1.8-2.9; under his guns it flies no
		// offensive play at all, from 100%). A jet that spends its speed to
		// point wins the scene and is then slow in front of everyone else. The
		// play, the licence and glide's post-stall branch (TestBleedFidelity,
		// TestStallProbe) stay behind stage 9 so it can be flown against;
		// nothing about it argues for cutover.
		return order{aim: m.aloft(m.lead()), g: m.pull, throttle: 0, brake: 1, alpha: true}
	}},
	{"regain", 2, 12, func(m *moment) order {
		// The regain bailout's own law (duel(), #64) as a candidate (stage 10):
		// climb out from under him, away from him, in burner. At this stage the
		// bypass stands down and the arbiter weighs the climb against the rest
		// of the catalogue, over the longest window on offer because a sustained
		// climb pays late.
		//
		// MEASURED 2026-09-18 on stage 6 alone: it is chosen (the pounce gate's
		// entry count goes green again, and lingering stays at 21-25%), and it
		// holds the current ace 14-8 guns, 26-20 heaters, but the guns ladder
		// goes red - the ace lost to the pilot 4-2 and superhuman v ace left 11
		// of 16 undecided. Not accepted; stage 6's own red pounce gate is the
		// reason it was tried.
		out := m.me.Position.Subtract(m.prey)
		out.Y = 0
		if out.Length() > 1 {
			out = out.Normalize()
		} else {
			out = flight.Vec3{X: 1}
		}
		return order{aim: flight.Vec3{X: out.X, Y: 0.75, Z: out.Z}.Normalize(), g: 3, throttle: 1, reheat: 1}
	}},
	// TRIED AND DECLINED (2026-09-18): `rebuild` as a play. The "too slow to fight"
	// reflex in bot.go pre-empts this arbiter, so making its law a candidate here
	// (tier 2, a ten-second span like the other slow-paying plays, last in the
	// table so no other play's noise draw moved) and deleting the reflex for
	// teamless bots looked like the clean repair. Both synthetic twins stayed
	// green (TestDuelSaddleFinish, TestDuelThreatenedNeverUnloads). The recorded
	// scene did not: TestSceneThreatOnTheSix went from 8/8 back to 0/8, the
	// arbiter CHOOSING rebuild for 0.8 of the 4 seconds with a registered menace
	// 770 m behind it. Against one constant-arc phantom, flying straight away
	// from him opens the range in the rollout and scores as the threat going
	// away; a pursuer who adjusts simply shoots. So the reflex stays, repaired
	// (bot.go starved: it now yields to a threat and to the saddle), until the
	// rollout rehearses against an opponent who can follow. Revisit only then.
}

// evolve advances the opponent's track by dt along the LOCAL CURVE: velocity
// plus observed swing, the same model the gunnery's lead uses. Speed is
// preserved rather than integrated. The circle estimate is deliberately not
// used here - it judges standing on his turn and picks the lag point.
func evolve(t *track, dt float64) (flight.Vec3, flight.Vec3) {
	// A turning jet flies an ARC, not a parabola. This used to extrapolate at
	// constant acceleration - position += v*dt + swing*dt^2/2 - which is a
	// parabola, and the error grows with dt^2 while a real turn curves back on
	// itself. Measured against an integrated 240 Hz turn at 350 kt:
	//
	//            2 s     4 s     8 s      12 s
	//     3 g      6 m    51 m   397 m   1,289 m
	//     5 g     17 m   139 m  1,043 m  3,154 m
	//     7 g     34 m   268 m  1,879 m  5,132 m
	//
	// At 5 g over a 12 s rollout the phantom sat 3,154 m from the real jet
	// while it had travelled 2,160 m: the error EXCEEDED the distance flown.
	// The old model was sound to about two seconds and fiction at twelve.
	//
	// That is the whole span asymmetry #42 has been chasing, and it is a
	// defect rather than a design choice: `high` is rehearsed over 12 s and
	// every other play over ~4, so the longest-window play was the one scored
	// against the worst phantom - and a yo-yo's payoff is exactly the part
	// that needs the opponent to be where you predicted. It also explains both
	// declined repairs recorded in choose(): a common longest window helped
	// because it stopped judging anything at 12 s, and it cost the BVR rung
	// because a BVR target flies straight, where swing is near zero, the error
	// vanishes and a long look is genuinely informative.
	//
	// The arc below is closed-form and costs a sine and a cosine - this is the
	// hottest path in the bot (one call per rollout tick per candidate), so it
	// must not become a second simulation. Only the component of swing across
	// the velocity turns the jet; the along-track part would change speed, and
	// the old code discarded that too by renormalising.
	speed := t.velocity.Length()
	if speed <= 1 {
		return t.position.Add(t.velocity.Scale(dt)), t.velocity
	}
	ahead := t.velocity.Scale(1 / speed)
	across := t.swing.Subtract(ahead.Scale(t.swing.Dot(ahead)))
	if along := t.swing.Dot(ahead); t.floor > 0 && t.lasted >= lasting && along < 0 && speed > t.floor {
		return slowing(t.position, ahead, across, speed, along, t.floor, dt)
	}
	pull := across.Length()
	if pull < 0.01 { // straight enough that the arc and the line agree
		return t.position.Add(t.velocity.Scale(dt)), t.velocity
	}
	inward := across.Scale(1 / pull)
	turn := pull / speed * dt // radians swept
	radius := speed / (pull / speed)
	position := t.position.
		Add(ahead.Scale(radius * math.Sin(turn))).
		Add(inward.Scale(radius * (1 - math.Cos(turn))))
	velocity := ahead.Scale(speed * math.Cos(turn)).Add(inward.Scale(speed * math.Sin(turn)))
	return position, velocity
}

// slowest is the speed a slowing forecast bottoms out at, m/s (track.floor):
// the rollout's own floor (glide), below which a jet stops flying rather than
// slowing.
const slowest = 30.0

// lasting is how long a jet's speed must have kept falling before the forecast
// believes it, s (track.lasted). Replayed against the pilot's seven recorded
// jousts (forecast_test.go), the 12 s forecast in the 12 s after each merge:
//
//	                                    slowed-hard fights   fast fights
//	any slowing believed, whole window   -32% and -62%       +3% to +20%
//	any slowing believed for 3 s         -22% and -45%        0% to  +8%
//	slowing that has lasted 1 s          -20% and -63%        0%
//
// No deceleration threshold separated them: a pilot turning hard at speed bleeds
// as fast as one slowing on purpose, but for less than a second before he
// unloads.
//
// Stage 11 costs the superhuman its guns ladder against the ace, with or
// without this gate: over 64 seeds 13-11 with 40 undecided (12-15 with 37
// believing any slowing for 3 s) against 22-15 with 27 at stage 6. Its forecast
// of the ace is closer there too, 582 m against 711 m at 12 s where a slowing
// is believed, but it flies `high` 43% of the time against 35%, and fewer fights
// finish.
const lasting = 1.0

// slowing is evolve() for a jet that has been losing speed for `lasting` (stage
// 11): his turn RATE held, his speed falling at the rate measured along his
// path until it reaches floor, then held, so his circle shrinks as he slows.
// The speed-holding arc flew a pilot who had bled 400 kt in 11 s round a circle
// three times too big, and the forecast landed a kilometre from him at 12 s.
//
// Down to slowest it has him too slow at 12 s: by 115 and 60 kt in the
// pilot's two slowed-hard jousts, about 100 kt in bot fights. DECLINED
// 2026-09-23, a floor at his own 1 g stall where he was seen: his speed came
// right in those jousts (-14 and +45 kt), but the forecast gave back most of
// its gain, 01a0ca24 closer by 4% where this is 10-20%, 01a0ca2e by 36% where
// this is 57-63%, since both pilots, and the scripted mush, flew well below
// their stall. Against the mush the superhuman went 2 kills and 13 deaths of 16
// (this floor 7 and 5, stage 6 7 and 7), and the guns ladder did not move
// (14-12 of 64).
//
// Closed form, as the arc is: with v = v0 + a*s and heading w*s, the path is
// the integral of v e^(iws), which is
// [(v/w) sin ws + (a/w^2) cos ws, -(v/w) cos ws + (a/w^2) sin ws].
func slowing(position, ahead, across flight.Vec3, speed, along, floor, dt float64) (flight.Vec3, flight.Vec3) {
	until := math.Min(dt, (floor-speed)/along) // seconds of slowing before the floor
	final := speed + along*until
	pull := across.Length()
	if pull < 0.01 { // straight: the distance of a steady deceleration, then the floor
		distance := speed*until + along*until*until/2 + final*(dt-until) // a steady deceleration, then the speed it reached
		return position.Add(ahead.Scale(distance)), ahead.Scale(final)
	}
	inward := across.Scale(1 / pull)
	omega := pull / speed // rad/s, held as he slows
	w2 := omega * omega
	sweep := omega * until
	x := final*math.Sin(sweep)/omega + along*(math.Cos(sweep)-1)/w2
	y := (speed-final*math.Cos(sweep))/omega + along*math.Sin(sweep)/w2
	turn := omega * dt // then the rest of the window at the speed reached, on the same rate
	x += final * (math.Sin(turn) - math.Sin(sweep)) / omega
	y += final * (math.Cos(sweep) - math.Cos(turn)) / omega
	velocity := ahead.Scale(final * math.Cos(turn)).Add(inward.Scale(final * math.Sin(turn)))
	return position.Add(ahead.Scale(x)).Add(inward.Scale(y)), velocity
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
// keen is the exponent on the nose term in appraise's offence (#42). Two is
// the historical value and is nearly flat across the whole useful range; see
// the comment at its use. SWEEP THIS, do not guess it.
const keen = 6.0

func appraise(s *flight.State, hisP, hisV flight.Vec3, pace float64, w posture, sk *skill, ring orbit, t *tactics) (float64, float64) {
	stack, sharp, danger, chase, progress, press := 0.45, keen, 1.3, 0.35, 0.15, 1.0
	if t.on(6) {
		stack, sharp, danger, chase = t.truth.stack, t.truth.keen, t.truth.threat, t.truth.closing
		press = t.truth.offence
		// The extra nose reward is a SKILL: pressing the nose onto him at any range
		// is what the ace's geometry read buys, and the pilot keeps the old
		// weight. Handed to every tier alike, 0.30 sent the pilot diving at a
		// floundering target in burner (TestPilotEngagesTheMush: 552 kt against
		// a 450 kt bar, 385 at 0.15), and fading it with closure instead
		// (truth.overtake) gave back what it had bought: at 150 m/s the guns
		// head-to-head went 21-12 to 8-15 and the wide BVR rung inverted 56-78.
		progress += (t.truth.point - 0.15) * sk.geometry
	}
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
	// The nose term is sharpened past its old square (#42). Squared, it could
	// not tell a gun solution from "roughly pointed": 0 deg scores 1.00, 5 deg
	// (the firing tolerance) 0.99 and 20 deg 0.88, so a play holding 3 deg and
	// one holding 18 outscored each other by a tenth while one fires and the
	// other never does. That flatness did not matter while the tracking loop
	// left a standing error nothing could convert; now that the aim integrator
	// takes press to 37.9% inside tolerance at 400-600 m (was 1.8%), the scorer
	// is the thing that cannot see it, and the ace still spends 63% of a fight
	// in `high`.
	//
	// keen is the exponent, named so it can be swept rather than guessed - the
	// lesson of 283b565, where two constants shipped claiming a sweep that had
	// never been run. At 6 the half-value sits at 19 deg instead of 32.
	//
	// THE STATED REASON FOR THIS CHANGE WAS REFUTED BY ITS OWN MEASUREMENT, and
	// it is kept on the result rather than the argument. The expectation was
	// that rewarding conversion would move the ace out of `high` and into
	// `press`. The opposite happened - high went 63% -> 68% - and in hindsight
	// that is obvious: `high` is the REPOSITIONING play, the yo-yo exists to
	// put the nose on, so rewarding nose-on more sharply favours the manoeuvre
	// that best achieves it. Do not re-argue the mix from this term.
	//
	// What it did measure, all gates green:
	//   tier ladder, pilot arm (the trustworthy one - the hornet is a
	//     transient there, not the tumble of #214)   11 killed/4 died -> 15/0
	//   wide missiles superhuman v ace, the marginal top rung   23-21 -> 27-21
	//   wide guns ace v pilot, fights reaching a decision       9-2/37 no result
	//                                                        -> 12-5/31
	//   guns superhuman v ace                          6-4 -> 4-3, and mean
	//     time to kill 150 -> 101 s: the one arm that softened, inside noise at
	//     sixteen seeds and still positive and well clear of the #213 floor.
	offence := clamp(rear, 0, 1) * math.Pow(clamp(point, 0, 1), sharp) * band
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
	if t.on(6) && t.truth.overtake > 0 {
		progress *= clamp(1-(closing-t.truth.overtake)/(2*t.truth.overtake), 0.4, 1)
	}
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
	// The stack (#42): height OVER the opponent, priced on its own — edge
	// already counts altitude as energy, but in close combat height over a
	// co-energy opponent converts to position in a way speed cannot (the jet
	// beneath cannot bring its lift vector up without collapsing its rate),
	// and pricing them as fungible is what let the bot dive out of deadlocked
	// spirals and donate the perch every piloted fight was lost through (the
	// Nash-equilibrium defection, recording 01a0496dbd0a: co-altitude lag at
	// t=96, low at t=104, dead at t=157). ITS WEIGHT IS LOAD-BEARING, measured
	// 2026-09-17 (#42): halving it to 0.225 does what the guns stalemate wants -
	// the ace's stalemate speed rises 388 -> 414 kt, its solution share 0.02%
	// -> 0.64%, one fight of twelve converts - and fails two quick gates doing
	// it, the perch donated 256,989 m.s against the fixed band's ~126,000 and
	// the missile rung inverted, superhuman losing to the ace 10-5. The height
	// reward and the slow fight are in tension here exactly as the aero cap is
	// at capped(), and this number stays where the gates are green too.
	// Relative and zero-sum, dead beyond
	// 2.5 km, dwarfed by the offence terms in a genuine conversion — and
	// scaled by the energy-read skill: the novice authentically cannot price
	// it. Sits outside the posture weights, like the deck penalties — and
	// faded by threat: height is an asset a pilot husbands in a NEUTRAL
	// fight and spends without regret once someone is on the six, so taxing
	// the escape dive is wrong exactly where escaping matters (it inverted
	// the wide heater ladder before this gate).
	height := clamp((s.Position.Y-hisP.Y)/400, -1, 1) * near * clamp(1-2*threat, 0, 1)
	// The range term is deliberately shallow: every geometry term dies by 2.5 km,
	// so beyond that this gradient is nearly the whole scorer and steepening it
	// reweights the merge and gun bands too. Pulling a distant bot back wants a
	// term that is zero inside weapons range, not a bigger multiplier here.
	score := press*w.offence*offence - danger*w.threat*threat - 0.8*w.threat*joust - w.energy*hungry*clamp(edge*-1, 0, 1) + stack*sk.energy*height - r/12000 +
		sk.geometry*0.25*standing*clamp((2500-r)/1500, 0, 1) +
		w.closing*chase*clamp(closing/400, -1, 1)*clamp((r-500)/1200, 0, 1) +
		w.offence*progress*point - // nose toward him is progress at any range: a reversal's value shows as swing long before it shows as a gun band
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
func (i *instance) rehearse(a *craft, b *brain, sim *flight.Model, chosen play, prey *track, tick uint64, horizon, window int) (float64, float64) {
	if b.tactics.truthful(carried) {
		// The scratch model carries the live jet's stores, so a rollout through the
		// full model weighs and drags what the bot is actually carrying. Before the
		// state is copied: mounting a tank fills it, and the copy then sets the
		// fuel the jet really has.
		sim.Stores(a.model.Attached())
	}
	sim.State = a.model.State
	sim.State.Damage = sim.State.Damage.Copy() // the struct copy shares Element/Jam with the LIVE jet; a damage-writing Step would corrupt it mid-fight
	shadow := *b                               // the executor's scalar state rides along; maps are untouched
	shadow.shoot = false
	pace := corner(a.model)
	age := float64(tick-prey.when) / 60
	score, offence, samples := 0.0, 0.0, 0.0
	weighed, last, before := 0.0, 0.0, 0.0 // the ends scorer (stage 8): the confidence-weighted sum, and the final two samples
	// A PURSUIT future (stage 7, futures()) is closed-loop: he flies at ME, where
	// the rollout has put me, turning as hard as a fighter can. Every other future
	// is an arc he would fly whatever I did - and an arbiter rehearsed only
	// against those learns that a gun on its six simply goes away if it flies
	// somewhere else.
	chaseP, chaseV := evolve(prey, age)
	held := surplus(&sim.State, chaseP, chaseV) // stage 12: the energy lead over him this line starts from
	for k := 1; k <= horizon; k++ {
		t := float64(k) / 60
		hisP, hisV := evolve(prey, age+t)
		if prey.pursuit {
			if pace := chaseV.Length(); pace > 1 {
				if to := sim.State.Position.Subtract(chaseP); to.Length() > 1 {
					have, want := chaseV.Scale(1/pace), to.Normalize()
					if off := math.Acos(clamp(have.Dot(want), -1, 1)); off > 1e-6 {
						// He flies the same airframe under the same wing: below corner
						// he cannot pull six g any more than I can, and a phantom that
						// could out-turned every defence and made them all score alike.
						load := math.Max(1.5, math.Min(6, a.model.Airframe.Limit.Positive*(pace/corner(a.model))*(pace/corner(a.model))))
						swing := 9.81 * math.Sqrt(load*load-1) / pace / 60 // what that load turns him through in one rollout tick
						share := math.Min(1, swing/off)
						chaseV = have.Scale(1 - share).Add(want.Scale(share)).Normalize().Scale(pace)
					}
				}
			}
			chaseP = chaseP.Add(chaseV.Scale(1.0 / 60))
			hisP, hisV = chaseP, chaseV
		}
		m := moment{me: &sim.State, prey: hisP, velocity: hisV, ring: b.ring, pace: pace, pull: b.skill.pull, grain: b.turning}
		m.derive()
		o := chosen.law(&m)
		o.gravity = b.tactics.gravity || b.tactics.truthful(gravity)
		o.weighed = b.tactics.truthful(carried)
		if b.tactics.truthful(carried) {
			o.weight = a.model.Mass()
		}
		if b.tactics.truthful(capped) {
			// Truth (stage 6): rehearse the jet that will actually be flown. The
			// law's g used to go straight to the rollout while the LIVE command
			// went on through corner discipline and the aero cap in polish() -
			// so the ace rehearsed every sub-corner turn at 100% of the wing and
			// flew 85% of it, and chose its plays with an aeroplane that does
			// not exist. It also means every earlier cap experiment (1.2, 1.5)
			// moved the live jet and left the rehearsal alone: those results
			// were confounded. The low tiers' wobble is not rehearsed - a novice
			// cannot predict his own mush - but their cap is.
			//
			// MEASURED 2026-09-18, not yet accepted. The rollout's own forecast
			// error against the recorded human falls at every lookahead but the
			// last (27 -> 24 m at 2 s, 102 -> 91 at 4 s, 380 -> 351 at 8 s), and
			// against the current ace it wins 18-4 with guns and draws 23-23
			// with heaters (current against itself: 3-5). Four quick gates are
			// green; the fifth, TestPounceExposure, is red on its PROXY - the
			// regain bailout is never entered - with the outcomes it guards
			// unchanged (lost 0 of 24, lingering 21.9% against a 28% bar). With
			// `high` rehearsed over eight seconds instead of twelve all five are
			// green and the head-to-head is even (tactics.span).
			//
			// Then the full battery, and the wide BVR rung, which no quick gate
			// flies, inverts beyond any doubt: over 144 seeds the superhuman
			// loses to the ace 35-93 (12 s span) and 48-89 (8 s), where stage 0
			// reads 62-62 and HEAD 65-60. The 8 s variant also leaves the free
			// kill in TestConvert untaken twice in twelve (superhuman 10/12,
			// floor 11). Truth in the rehearsal helps the fight it was built for
			// and costs the missile fight at range more than it gains, so this
			// stage is not accepted in either form.
			flown := sim.State.Velocity.Length()
			if !o.alpha {
				if b.skill.library >= 3 {
					o.g = disciplined(o.g, flown, pace)
				}
				o.g = b.capped(o.g, flown, pace/math.Sqrt(a.model.Airframe.Limit.Positive))
			}
		}
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
		if b.journal != nil && b.journal.tracing {
			for _, span := range spans {
				if k == int(span*60) {
					b.journal.path = append(b.journal.path, sim.State.Position)
				}
			}
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
			one, guns := appraise(&sim.State, hisP, hisV, pace, stance, &b.skill, b.ring, &b.tactics)
			if b.tactics.on(7) {
				one -= b.tactics.peril * stance.threat * peril(&sim.State, hisP, hisV)
			}
			if b.tactics.on(12) {
				// The deficit this line takes me into, below parity or below where
				// I already stood, on the corner-speed scale appraise() judges
				// energy on: a surplus is there to be spent for angles.
				one -= b.tactics.tariff * stance.energy * clamp((math.Min(held, 0)-surplus(&sim.State, hisP, hisV))/(pace*pace/2), 0, 1)
			}
			if flown := sim.State.Velocity.Length(); b.tactics.on(9) && flown < 80 && flown >= 0.9*hisV.Length() {
				one++ // appraise fines any line below 80 m/s a full point, whoever it is flown against. Slow while he is slower still is not a fault, it is the fight being won where he chose to hold it - and with the fine absolute, the energy dump could never be chosen however it was priced
			}
			weighed += confidence(float64(k)/60) * one
			before, last = last, one
			score += one
			offence += guns
			samples++
		}
	}
	// MEASURED AND DECLINED (#153, 2026-09-10): pricing what a line SPENDS.
	// appraise scores energy as a LEVEL - hungry fires only once already slow,
	// edge only once already behind - so a rollout about to trade its whole
	// airspeed for a moment's angle scores as well as one that keeps it, and a
	// 2.5-4 s horizon never sees the bill arrive. The obvious remedy is a window
	// term here: specific energy at the line's end against its start, on the
	// corner-speed scale, discounted by what the line converts. Built, and
	// measured over the five quick gates at three aero caps:
	//
	//   cap 0.85/1.0 no term  5/5   (the committed tree)
	//   cap 0.85/1.0 + term   4/5   guns ladder inverted, superhuman lost 3-1
	//   cap 1.2      + term   3/5   and the ace shot down 2 of 24 - a real defence loss
	//   cap 1.5      no term  1/5
	//   cap 1.5      + term   3/5   best of the arms: ladder, jink and pounce bought back
	//
	// It cannot land. At the committed cap it costs a gate outright; at every
	// raised cap the ace is tracked 57-67% against the 20% bar while CONVERTING
	// 0.0% - the same figure as every point in history. The slow fight the cap
	// unlocks is trackable without being lethal, so there is nothing on the
	// other side of the trade. Fading the tariff by range moved which gates
	// broke (jink against pounce) and never the total. Do not rebuild it: the
	// counter-offence is #73's unbuilt capability, not a mispriced one.
	if window > 0 {
		return ends(weighed, last, before, samples, horizon, window), offence / samples
	}
	return score / samples, offence / samples
}

// confidence is how far a rehearsed instant t seconds ahead is believed (stage
// 8). Measured by the decision journal against a real human (recording
// 01a0b090), the phantom's median error grows as the square of the lookahead -
// 84 m at four seconds, 376 m at eight, 851 m at twelve, which is 5.9 t^2 to
// within a tenth - and 700 m is where a forecast has stopped describing a guns
// fight at all. Floored, because even a fictional twelfth second says which way
// the line was heading.
//
// It is ONE curve for every play, applied over ONE window, and that is the
// point. rehearse() returned each play's mean over its own span, so `high`,
// judged over twelve seconds, was scored on eight seconds of fiction the
// four-second plays were never charged for. Both repairs on record failed for
// reasons this shape avoids: a discount renormalised per play cancels itself
// (choose(), REFUTED 2026-09-13), and flying every play out to the longest span
// judges a four-second law against a phantom it was never meant to meet
// (declined twice, same place). Here no play is flown past its span; the
// remainder of the window is a static valuation of the state it ended in - a
// constraint on where a line leaves you, never a currency (#153) - and the
// divisor is common, so it is neither a sum nor a per-play mean.
//
// MEASURED 2026-09-18 and NOT ACCEPTED. On stage 6 alone it reverses the
// recorded under-guns scene (offensive share 100% -> 24%) and holds the
// current ace (11-4 guns, 23-22 heaters), but three quick gates go red: the
// superhuman is shot down 3 times in 24 by the crude tail-chase (allowance
// 2), its guns kills over the ace arrive in 64.6 s against a 75 s floor, and
// the regain bailout is never entered. On top of stage 7 the same three go
// red with the jinker gate added.
func confidence(t float64) float64 { return math.Max(0.1, 1-5.9*t*t/700) }

// ends is the stage 8 value of a rehearsed line: its own samples at the trust
// their lookahead has earned (`weighed`, summed by rehearse), the rest of the
// common window valued at where the line ended up - the mean of its last two
// samples - and every play divided by the same number. Samples fall every half
// second, as rehearse() takes them.
func ends(weighed, last, before, samples float64, horizon, window int) float64 {
	end := last
	if samples > 1 {
		end = (last + before) / 2
	}
	tail, total := 0.0, 0.0
	for k := 30; k <= window; k += 30 {
		trust := confidence(float64(k) / 60)
		total += trust
		if k > horizon {
			tail += trust
		}
	}
	return (weighed + end*tail) / total
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
	// point-progress term without quadrupling the rehearsal budget. One
	// window per candidate, each play on its own span: judging every rival
	// over the LONGEST span on offer (so a yo-yo and the pursuit it competes
	// with are compared alike, #169/#174) fixed the yo-yo's dominance - its
	// share of a slow fight fell from 57% to 18-36%, the ace's heater arm
	// against the hornet went 9/4 to 14/0, and the #107 anchor's pilot and
	// superhuman rows rose to 11/4 and 16/0 - but it cost the BVR rung the
	// gates exist to hold: the wide missile ladder read superhuman 19-29
	// against the ace where per-play windows read 18-21, and narrowing the
	// common window to 1,200 m passed the ladder while giving the anchor gain
	// back (9/6, 13/2). Measured three ways and declined; the asymmetry it
	// closes is real and wants a scorer that prices it, not a longer look.
	//
	// REFUTED 2026-09-13, so it is not rebuilt: discounting distant rollout
	// samples does NOT price the asymmetry. Each sample was weighted
	// d^(seconds) with `samples` accumulating the weights, which renormalises
	// every play over ITS OWN window - so the discount pulls each play's mean
	// toward its own early samples equally and never changes a 12 s window's
	// standing RELATIVE to a 4 s one. Self-cancelling by construction, and the
	// guns arm agreed: over d = 1.0/0.97/0.93/0.88 the yo-yo's share held at
	// 52-65% (it ROSE on the ace arm, 58 -> 65) and kills read 7/8/5/7, which
	// is noise. Pricing the span needs a shape that does not renormalise per
	// play - and a shape that does not is the sum-versus-mean problem again.
	//
	// RE-TESTED 2026-09-13 against the corrected evolve() and declined AGAIN,
	// which settles more than the window. The suspicion was that both halves of
	// the original result were artefacts of the parabola phantom: that it
	// helped only by refusing to judge anything at 12 s, where the phantom was
	// fiction, and hurt BVR only because a straight-flying target has no
	// prediction error to fix. With the phantom honest at EVERY horizon it
	// still breaks the dominance (top `high` 54-66% -> 18-31%, press and
	// saddle taking over) and the bots still get WORSE: heater ace 15 -> 11
	// kills, superhuman 16 -> 11 and now dying 3, guns total 5 -> 3, and the
	// fight degrades enough to drive the scripted hornet into a 69.5 s tumble
	// (3.70% of the time flown against a 1.00% limit).
	//
	// So `high`'s dominance is NOT what limits conversion. It is dominant
	// because it earns it, and taking the arbiter off it costs kills - which
	// refutes #174's premise from a second direction and closes the line of
	// enquiry that treated the play mix as the defect.
	best, top, promise, n := b.play, math.Inf(-1), 0.0, 0
	hedging := b.tactics.on(7) && distance < 2500 && b.tactics.futures > 1
	window := 0
	if b.tactics.on(8) {
		// The common window is the longest span this pilot owns: a property of
		// the tier, not of which plays survive today's gates, so a play's score
		// does not move because a rival was banned.
		for _, p := range plays {
			if p.tier <= b.skill.library && b.tactics.staged(p.name) {
				window = max(window, b.horizon(p))
			}
		}
	}
	var shortlist []candidate
	for _, p := range plays {
		if p.tier > b.skill.library || !b.tactics.staged(p.name) {
			continue
		}
		if p.name == "extend" && distance > 2500 {
			continue // already separated: rehearsing more separation buys nothing
		}
		if (p.name == "saddle" || p.name == "lag") && distance > 2000 && b.tail > 0.35 && b.nearing*120 < distance-800 {
			n++      // consume the play's noise index: every surviving play keeps the draw it had ungated, so fights diverge from the old arbitration ONLY where a park would actually have won
			continue // the parking laws (#69): saddle's speed-match and lag's corner-pace reheat both MIL-park a matched-speed stern chase, and beyond ~2.5 km the rehearsal horizon cannot tell a park from a pursuit — they win on selection noise, stick via incumbency, and bleed the closure press had built. Banned only where they cannot ARRIVE: a far-field stern chase whose closure trend is more than two minutes from gun range. A closing stalk, and any mutual fight, keeps the full repertoire.
		}
		horizon := b.horizon(p)
		score, guns := i.rehearse(a, b, sim, p, prey, tick, horizon, window)
		raw := score
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
		if hedging {
			shortlist = append(shortlist, candidate{play: p, horizon: horizon, score: score, raw: raw, guns: guns})
		}
		if score > top {
			best, top, promise = p.name, score, guns
		}
		n++
	}
	if hedging {
		best, promise = i.hedge(a, b, sim, prey, tick, shortlist, scores, window)
	}
	return best, promise
}

// horizon is how far one play is rehearsed, in rollout ticks: the tier's own
// window (2.5 s for the novice up to 4 s at the top), or the play's span when
// that is longer.
func (b *brain) horizon(p play) int {
	span := p.span
	switch {
	case p.name == "high" && b.tactics.span.high > 0:
		span = b.tactics.span.high
	case p.name == "pitch" && b.tactics.span.pitch > 0:
		span = b.tactics.span.pitch
	case p.name == "climb" && b.tactics.span.climb > 0:
		span = b.tactics.span.climb
	}
	return max(60*2+30*b.skill.library, int(span*60))
}

// staged reports whether a play is in the catalogue at this stage. The two
// plays the structural stages add sit LAST in the table and are skipped below
// their stage, so no other play's noise draw moves and stage 0 is today's bot.
func (t *tactics) staged(name string) bool {
	switch name {
	case "bleed":
		return t.on(9)
	case "regain":
		return t.on(10)
	}
	return true
}

// candidate is one rehearsed play on its way to the hypotheses stage: `raw` is
// what it scored against the opponent as he IS, `score` that plus the selection
// noise, personality and incumbency the arbiter has always added.
type candidate struct {
	play       play
	horizon    int
	score, raw float64
	guns       float64
}

// peril is HIS gun solution on ME: appraise()'s offence term seen from the other
// cockpit, with the same gun band and the same sharpened nose term. How much of
// my tail he sees, times how nearly his flight path is on me, inside gun range.
//
// appraise() already has `threat`, and it is blunt by design: he is behind my
// path, flying my way, inside 2.5 km. It cannot tell a break turn that takes my
// tailpipes out of his windscreen from a yo-yo that leaves them there - and once
// the rollout was given an opponent who PURSUES (stage 7), that showed: against
// him every play scored between -2.2 and -2.6 and `high` was still the least
// bad, which is the ace flying a yo-yo for 22.7 s with a human 450 m behind
// hitting it (recording 01a0b090). Angle-off is what defeats a tracking gun,
// and this is where angle-off is priced.
func peril(me *flight.State, hisP, hisV flight.Vec3) float64 {
	line := me.Position.Subtract(hisP) // him -> me
	r := math.Max(line.Length(), 1)
	line = line.Scale(1 / r)
	mine, his := me.Velocity, hisV
	if mine.Length() < 1 || his.Length() < 1 {
		return 0
	}
	tail := clamp(mine.Normalize().Dot(line), 0, 1) // 1: he looks up my tailpipes
	nosed := clamp(his.Normalize().Dot(line), 0, 1) // 1: his flight path is on me
	band := clamp((r-60)/190, 0, 1) * clamp((1500-r)/800, 0, 1)
	return tail * math.Pow(nosed, keen) * band
}

// TRIED AND DECLINED (2026-09-23, #31): HIS heater priced in every rehearsed
// instant. With a heater-armed pilot 500-1,100 m in its rear quarter, the bandit
// extended on `high` until his 9M arrived, in four recorded episodes (01a0ce44
// 41.6-52.5 s, 01a0cf7e 44.7-53.9 s, 01a0cfbf 46-60 s and 119-139 s). appraise()'s
// threat term and peril() both price his GUN, dead by 1.5-2.5 km, so a term was
// built as the zone heater.go draws for my own shots seen from his cockpit: the
// share of his seeker's reach at my aspect left before I am out of it (5 km up
// my tailpipe, the plume's floor on the beam), times his flight path on me,
// times whether his seeker could hold the line (0.35 rad/s), weighted 1.5 as
// stage 6 weighs the threat term. Put to the real arbiter at the recorded
// re-plans, it lowered every score and changed almost no choice: `high` still
// won 17 of 20 (12 of 13 with stage 7's peril instead). Every line stays deep
// inside a heater's reach at that range for its whole rehearsal, and no play
// in the catalogue is rehearsed as escaping it. What the re-plans do show is
// that `high` was chosen as a turn onto him - its law pulls the whole wing at
// his lead point - while the jet flown dived away: the winner's own rehearsed
// path landed 560-1,070 m from the flown one at 8 s and 950-1,700 m at 12 s in
// all three dives. The defect is that divergence, not the missing price.

// surplus is my specific energy over his, J/kg: what stage 12 charges a rehearsed
// line for giving up.
//
// appraise() prices energy as a constraint, never a currency (#45), and pricing
// what a line spends was measured and declined (#153, rehearse()): charged
// against my own energy, the slow fight could never pay, and the guns ladder
// inverted. That charge was absolute. This one is RELATIVE to his forecast, and
// stage 11 made the forecast honest about a slowing jet: when he is slowing, his
// phantom loses energy as fast as I do and matching him costs nothing, while
// dumping energy against a jet holding or gaining speed hands him the lead. The
// scripted hornet showed the price missing: at 9.6 s after a merge `bleed` beat
// `high` by 0.032 against a hornet accelerating through 315-326 kt, threw away
// an 1,860 ft lead in 1.5 s, and lost the fight that `high` won (#22).
//
// Only the DEFICIT is charged: how far below parity, or below where it already
// stood, a line takes the jet. A surplus is there to be spent for angles. The
// first form charged every foot of lead given up, and turned the best dump the
// bandit has flown against the pilot (01a0cf57 at 17.6 s: 353 kt against his
// 226, 1,000 ft above, nose 76 to 31 degrees off him in 3 s) into `high` at
// every weight. Swept 2026-09-23 on stages 8, 9 and 11 (omit 1152), scripted
// humans with heaters, 64 seeds, kills-deaths:
//
//	                          mush v ace  hornet v ace  mush v superhuman  hornet v superhuman
//	stage 11 alone (1920)       19-20        45-6           34-18              59-3
//	1152, no price              33-27        11-35          30-26               7-40
//	1152, lead given up 1       30-25        22-27          13-36              11-37
//	1152, deficit 0.5           33-27        19-25          32-28              17-23
//	1152, deficit 1             27-32         8-25          28-31              20-23
//	1152, deficit 2             26-30         7-43          16-41              21-27
//
// The deficit at 0.5 keeps the slow fight as it was and halves the fast-fight
// losses. It does not reach stage 11 alone against the hornet, because stage 8
// is half of that collapse on its own (without bleed, 1664: 5-34 and 4-28).
func surplus(me *flight.State, hisP, hisV flight.Vec3) float64 {
	speed, his := me.Velocity.Length(), hisV.Length()
	return speed*speed/2 + 9.81*me.Position.Y - his*his/2 - 9.81*hisP.Y
}

// futures derives what the opponent might do next from what he is doing now.
// evolve() is left alone - the heater ladder and the live aim both depend on it
// - so each future is simply a different TRACK for it to extrapolate, anchored
// at this tick:
//
//	continue  the track as it stands: the single phantom every candidate was
//	          always rehearsed against
//	pursue    he flies at ME, wherever the rollout puts me, at six g: the only
//	          closed-loop future, and the one that matters with him behind me
//	reverse   the cross-track swing negated: he turns the other way
//	tighten   slower and pulling harder, the energy dump: a human took the ace
//	          from head-on to dead astern in fourteen seconds doing exactly this
//	          (recording 01a0b090: 254 kt to 138, radius 1,667 m to 735), and a
//	          constant-SPEED arc cannot represent it at all
func futures(prey *track, tick uint64, count int) []track {
	age := float64(tick-prey.when) / 60
	position, velocity := evolve(prey, age)
	now := track{when: tick, position: position, velocity: velocity, swing: prey.swing, nose: prey.nose, wobble: prey.wobble, floor: prey.floor}
	list := []track{now}
	speed := velocity.Length()
	if count < 2 || speed < 1 {
		return list
	}
	ahead := velocity.Scale(1 / speed)
	along := ahead.Scale(prey.swing.Dot(ahead))
	across := prey.swing.Subtract(along)
	pursue, reverse, tighten := now, now, now
	pursue.pursuit = true
	reverse.swing = along.Subtract(across)
	tighten.velocity = velocity.Scale(0.85)
	tighten.swing = across.Scale(1.4)
	if across.Length() < 2 {
		// Nearly straight: there is no turn to reverse or tighten, so the
		// surprises are a break either way at a fighter's working load.
		side := ahead.Cross(flight.Vec3{Y: 1})
		if side.Length() < 1e-6 {
			side = flight.Vec3{Z: 1}
		}
		side = side.Normalize().Scale(4 * 9.81)
		reverse.swing, tighten.swing = side, side.Scale(-1)
	}
	for _, extra := range []track{pursue, reverse, tighten} {
		if len(list) < count {
			list = append(list, extra)
		}
	}
	return list
}

// hedge re-ranks the leading plays against an opponent who might not continue
// as he is (stage 7). The arbiter rehearsed every candidate against ONE
// constant-speed arc; measured against a real human that arc is 84 m out at
// four seconds and 851 m out at twelve - past gun range - and the play judged
// over twelve seconds, `high`, was half the fight: the ace flew it for 22.7 s
// with the human 450 m behind hitting it, because against that one phantom the
// yo-yo always pays inside its window.
//
// Only the top four and the incumbent meet the extra futures, inside 2,500 m,
// which holds the rollout cost near double rather than fourfold. A play's
// value is the weighted mean over the futures plus `tactics.hedge` times its
// WORST: not pure minimax, which hands kills to scripts that never reverse.
// The weights are what each future predicted last time against what he then
// did (brain.cast, brain.doubt), so a steady opponent earns a confident arc
// and an erratic one earns a hedge. With one future, no hedge and no peril
// this is the old argmax exactly: TestHedgeControl.
//
// MEASURED 2026-09-18 and NOT ACCEPTED: its keep rule asked for green gates
// and both of the dump and under-guns scenes flipped, and neither holds.
// Against the current ace it is stronger (13-5 guns, 33-12 heaters, first
// build; 12-8 and 30-14 with the early re-plan), but every variant reddens
// the guns ladder: superhuman v ace leaves 13-14 of 16 fights undecided
// against an allowance of 9. The hedge sweep does not repair it - at 0 the
// guns rung is also decided too fast (58.9 s against a 75 s floor) and the
// jinker is out-shot by the pilot; at 0.5 the rung inverts, 4-2 guns and
// 11-5 missiles. Neither scene moves: with a pursuing future in the set the
// yo-yo still scores best under guns, because it beats the breaks by about
// 1.7 against the continue and tighten futures and loses only against the
// pursuing one. Tick cost
// was not the obstacle (26.1 ms against 24.6 at stage 0, 16 aces; see
// TestBudget for why both are over).
func (i *instance) hedge(a *craft, b *brain, sim *flight.Model, prey *track, tick uint64, shortlist []candidate, scores map[string]float64, window int) (string, float64) {
	b.learn(prey, tick)
	cast := futures(prey, tick, b.tactics.futures)
	sort.SliceStable(shortlist, func(x, y int) bool { return shortlist[x].score > shortlist[y].score })
	keep := shortlist
	if len(keep) > 4 {
		keep = keep[:4]
		for _, c := range shortlist[4:] {
			if c.play.name == b.play {
				keep = append(keep, c)
			}
		}
	}
	weight := b.trust(len(cast))
	best, top, promise := b.play, math.Inf(-1), 0.0
	for _, c := range keep {
		mean, worst, guns := weight[0]*c.raw, c.raw, weight[0]*c.guns
		for n := 1; n < len(cast); n++ {
			raw, shots := i.rehearse(a, b, sim, c.play, &cast[n], tick, c.horizon, window)
			mean += weight[n] * raw
			guns += weight[n] * shots
			worst = math.Min(worst, raw)
		}
		value := mean + b.tactics.hedge*worst + (c.score - c.raw)
		if scores != nil {
			scores[c.play.name] = value
		}
		if value > top {
			best, top, promise = c.play.name, value, guns
		}
	}
	b.cast, b.casted = cast, tick
	return best, promise
}

// tactics.steady and tactics.startled are the two ends of the forecast's running
// miss, in metres per second of lookahead. Against the recorded human the single
// arc's median error was 84 m at four seconds, about 21 m/s; an opponent inside
// half of that is flying the arc, and one past twice it has left it. Either at
// zero switches its half off, which is how the two were told apart.

// surprised reports that the opponent has left the future the committed line
// was chosen against (stage 7): the `continue` arc cast at the last re-plan,
// extrapolated to now, against where his track says he is. It waits half a
// second, because every arc is right at first, and it never fires twice on one
// cast.
func (b *brain) surprised(prey *track, tick uint64) bool {
	if len(b.cast) == 0 || b.casted == 0 || b.jolted == b.casted || tick < b.casted+30 || tick-b.casted > 300 {
		return false
	}
	elapsed := float64(tick-b.casted) / 60
	guess, _ := evolve(&b.cast[0], elapsed)
	actual, _ := evolve(prey, float64(tick-prey.when)/60)
	if b.tactics.startled <= 0 || guess.Subtract(actual).Length()/elapsed < b.tactics.startled {
		return false
	}
	b.jolted = b.casted // spent. The cast itself stays: learn() reads it at the re-plan, and this miss is the one most worth learning from
	return true
}

// learn scores the futures cast at the last re-plan against where he is now.
func (b *brain) learn(prey *track, tick uint64) {
	if b.casted == 0 || tick <= b.casted || tick-b.casted > 300 {
		return // nothing cast, or so long ago that it says nothing about his habits now
	}
	elapsed := float64(tick-b.casted) / 60
	age := float64(tick-prey.when) / 60
	actual, _ := evolve(prey, age)
	for n := range b.cast {
		guess, _ := evolve(&b.cast[n], elapsed)
		miss := guess.Subtract(actual).Length() / math.Max(elapsed, 0.5) // metres per second of lookahead: a re-plan after 1 s and one after 3 s are judged alike
		if n < len(b.doubt) {
			b.doubt[n] += (miss - b.doubt[n]) * 0.4
		}
	}
}

// trust turns the running doubt in each future into weights that sum to one.
func (b *brain) trust(count int) []float64 {
	weight := make([]float64, count)
	total := 0.0
	for n := range weight {
		doubt := 0.0
		if n < len(b.doubt) {
			doubt = b.doubt[n]
		}
		weight[n] = 1 / (1 + doubt/25)
		total += weight[n]
	}
	for n := range weight {
		weight[n] /= total
	}
	return weight
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

	// The regain bailout (#64): the perch-and-pounce pattern that has beaten
	// the bot in every piloted fight hides from BOTH deciding layers. Each
	// dive flips the intent to deny and back, restarting the conversion
	// clock, so the starvation reset never fires; and the rehearsal horizon
	// ends seconds before a sustained climb pays (the refuted scorer term).
	// So the diagnosis is tracked directly — he owns the height and the
	// energy overhead, he is not committed on me right now, and the
	// arbiter's own promise admits it holds no solution — and once it has
	// held for five seconds the catalogue is set aside for the one law it
	// cannot rehearse: climb out from under him and TAKE the sky, held to
	// parity or his commitment. Every gate is counted in b.audit so an inert
	// landing is visible to the pounce probe, not silent (#64's history).
	if b.skill.library >= 2 {
		if b.audit == nil {
			b.audit = map[string]int{}
		}
		speed := me.Velocity.Length()
		above := prey.position.Y - me.Position.Y
		diving := -prey.velocity.Dot(direction) > 120 && distance < 2500
		if diving && above > 250 {
			b.dove = tick // he attacked from above: the perch is a THREAT, not a bystander
		}
		// The cede is POSITIONAL, not energetic: a perch orbits slowly, so its
		// TOTAL energy often sits below the jet beneath it — the first landing
		// of this bailout gated on the energy sum and never entered once in
		// 24 pounce fights (audit: ceded 0.5% of ticks). Height inside pounce
		// reach IS the advantage being measured.
		holds := above > 400 && distance < 4000
		if holds {
			b.audit["ceded"]++
			if b.ceded == 0 {
				b.ceded = tick
			}
		} else if above < 250 || distance > 4500 {
			b.ceded = 0 // clearly down or clearly gone; a dive's brief pass through the band does not clear the diagnosis
		}
		// Entry demands the PERCH signature, not mere height: he loiters slow
		// overhead (banking position to spend on dives) AND has recently
		// DIVED on me from above. Geometry alone is not enough — the low
		// play parks 300 m beneath its own target, so mere height-above
		// described the straight-and-level offerer TestConvert hands over,
		// and the first two landings sent the superhuman climbing away from
		// free kills (0/12) and inverted the BVR top rung. An opponent who
		// has never attacked from his perch is a target, not a threat.
		if !b.tactics.on(10) && b.regained == 0 && holds && !diving && menace < 0 && b.promise < 0.4 && b.intent != "finish" &&
			prey.velocity.Length() < 200 && b.dove != 0 && tick-b.dove < 2400 && me.Position.Y > 600 {
			b.audit["calm"]++
			if b.ceded != 0 && tick-b.ceded > 300 {
				b.audit["enter"]++
				b.regained = tick
			}
		}
		if b.regained != 0 {
			exit := ""
			switch {
			case menace >= 0:
				exit = "menaced"
			case diving:
				exit = "committed"
			case above < 150:
				exit = "parity"
			case distance > 4500:
				exit = "left" // past reach it is not regaining, it is leaving (the reset's own range bound)
			case speed < b.skill.floor*0.85:
				exit = "slow"
			}
			if exit != "" {
				b.audit[exit]++
				b.regained, b.ceded = 0, 0
				b.until = tick // the catalogue re-arbitrates immediately
			} else {
				b.audit["hold"]++
				out := me.Position.Subtract(prey.position)
				out.Y = 0
				if out.Length() > 1 {
					out = out.Normalize()
				} else {
					out = flight.Vec3{X: 1}
				}
				b.play = "regain"
				b.aim = flight.Vec3{X: out.X, Y: 0.75, Z: out.Z}.Normalize()
				b.g, b.throttle, b.reheat, b.brake = 3, 1, 1, 0
				b.mode = b.play
				return
			}
		}
	}

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
	if b.tactics.on(7) && b.surprised(prey, tick) {
		b.until = tick // the forecast the committed line was chosen against has failed: re-plan now, not when the commitment runs out
	}
	if b.play == "" || tick >= b.until || (offensive && menace >= 0 && gap < 700 && tick-b.picked >= 15) || (overshot && tick-b.picked >= 10) {
		// The tick's rehearsal allowance (#256): cost is bounded by CONSTRUCTION - a
		// roster of any size spends only so many re-plans per tick, and the rest
		// wait. Deterministic by slot order, never wall clock, as the gates need.
		if i.rehearsals >= allowance {
			b.until = tick + 4 // come back for the allowance in a few ticks, keeping the committed line meanwhile
		} else {
			i.rehearsals++
			sim := flight.New(a.model.Airframe, a.model.Environment, a.model.World)
			var scores map[string]float64
			if b.journal != nil {
				scores = map[string]float64{}
			}
			held := b.play
			b.play, b.promise = i.choose(slot, a, b, sim, prey, tick, distance, scores)
			if b.play == "regain" && held != "regain" && b.audit != nil {
				b.audit["enter"]++ // stage 10: the arbiter choosing the climb IS the bailout entering, so the pounce gate keeps its reading
			}
			if b.journal != nil {
				i.minute(a, b, sim, prey, tick, scores)
			}
			b.picked = tick
			b.until = tick + uint64(math.Max(54, b.skill.commit*24)) // the commitment: ~0.9 s floor, 1.6 s at the top — the machine included, whose edge is reflex and precision, not strategy churn
			if b.tactics.on(7) && len(b.cast) > 0 && b.casted == tick && b.doubt[0] < b.tactics.steady {
				// An opponent who has been doing what the arc said earns a longer
				// commitment: half as long again, which is the cost of a re-plan
				// saved where a re-plan would have told the ace nothing new.
				b.until = tick + (b.until-tick)*3/2
			}
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
			b.licensed = o.alpha
			break
		}
	}
	b.mode = b.play
	// The trigger is ALWAYS live in a duel: the led solution gate and the
	// burst governor decide each round. Doctrine safing the gun was the #206
	// offensive deficit, found one wired-shut window at a time.
	b.shoot = true
}

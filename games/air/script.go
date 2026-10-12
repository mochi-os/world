package air

import (
	"math"

	"world/games/air/battle"
	"world/games/air/flight"
)

// The scripted doctrine: single player's bandit flies one fixed fight in a guns
// or heater match instead of choosing among rehearsed plays; server bots, and
// the bandit in a BVR fight, fly the planner (duel.go). It is plan step 6 of the difficulty
// target (claude/plans/air-bot-arbiter.md): five changes inside the planner left
// the ace winning about half its fights against mimic, the scripted pilot of the
// user's own fights, where the target is nine in ten, and every one of the
// ace's deaths at stage 0 came while the planner flew `high` away from a
// heater-armed pilot. mimic itself beats the stage 0 novice and pilot and draws
// with the ace, so its fight is where this starts.
//
// The law is mimic's (pointing_probe_test.go, hornet.fly), copied rather than
// shared: mimic is the yardstick, and a yardstick that moved whenever the
// bandit was tuned would measure nothing. What differs is what a bandit has
// and mimic does not: the brain's own perception of him (the tier's delay and
// staleness), its sighting of inbound rounds, its heater trigger and pairing
// discipline, its flare programme, and its gun, aimed at the led solution and
// fired on the brain's wager.
type routine struct {
	regaining bool        // below the floor or past the ceiling: unloaded until the band is back
	beam      flight.Vec3 // the break being flown across an inbound round, latched
	until     uint64      // the tick the break may be re-picked
	incoming  flight.Vec3 // the round being defended, as decide() last sighted it; zero while none is
	side      flight.Vec3 // my side of him at the pass, world horizontal: chosen once per run-in
	course    flight.Vec3 // the line held on the way in, level
	turning   bool        // the lead turn has begun
	judged    bool        // the first look has said whether this is a head-on meeting at all
	passed    bool        // he has gone by: the run-in is over and the fight is on
	branch    string      // the law that set the stick this tick
	velocity  flight.Vec3 // mine a tick ago: the turn I am flying, for the pass
	away      flight.Vec3 // the way a pass inside the bubble is being cleared, latched until the range opens; zero while none is
	running   int         // ticks he has been running away from me: faster, not turning, the range opening beyond 600 m
	chasing   bool        // pursuing him, from three seconds of his running until he is inside gun range or turns
}

// drive flies the scripted doctrine for one tick, from what decide() last saw.
func (i *instance) drive(slot int, a *craft, tick uint64) flight.Inputs {
	b, m := a.brain, a.model
	me := &m.State
	s := &b.routine
	own := flight.Vec3{} // the acceleration I am flying: my own turn, for the pass
	if s.velocity.Length() > 1 {
		own = me.Velocity.Subtract(s.velocity).Scale(60)
	}
	s.velocity = me.Velocity
	if b.prey == nil || deck(me) {
		s.branch = "deck"
		b.play = s.branch       // the recording's Doctrine channel reads it
		return b.steer(m, tick) // the recovery owns the stick near the water, whatever the plan
	}
	age := float64(tick-b.prey.when) / 60
	horizon := 0.0
	if b.skill.library >= 2 {
		horizon = age
	}
	spot := predict(b.prey, horizon, b.skill.library >= 3)
	want, span := i.bearing(me.Position, spot)
	b.distance = span
	if span < 1 {
		return b.steer(m, tick)
	}
	n := &b.tactics.script
	axis := me.Attitude.Rotate(flight.Vec3{X: 1})
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	// AIRSPEED, not speed over the ground: the regain's floor and exit, the pull
	// limit and the roll fade are all the wing's numbers. In single player the
	// bandit flies the pilot's air, and with the ground speed the regain began at
	// 172 kt into a 40 kt headwind and held until 268 kt, 12.8 s unloaded with its
	// tail to him (01a10e19). The geometry below stays over the ground, where
	// both jets' tracks are.
	speed := me.Velocity.Subtract(m.Gust()).Length()

	// The run-in: a line 400 m beside him, then the lead turn toward his side at
	// mimic's 1.6 km, held until he has gone by. Flown through steer(), which
	// holds a line well; the fight's own law below is for the fight.
	// The run-in is a head-on meeting's: a pilot whose flight path at the first
	// look does not point within 60 degrees of me is a fight from that look. A
	// tail chase at matched speed never closes and never goes by, and flown as a
	// run-in the bandit held a line 400 m beside a target in its gun range and
	// never fired.
	if !s.judged {
		s.judged = true
		if his := b.prey.velocity; his.Length() < 1 || his.Normalize().Dot(want.Scale(-1)) < 0.5 {
			s.passed = true
		}
	}
	if !s.passed {
		closing := me.Velocity.Subtract(b.prey.velocity).Dot(want)
		if want.Dot(axis) < 0 || closing < 0 {
			s.passed = true
		} else {
			return i.approach(slot, a, spot, want, span, tick)
		}
	}
	b.shoot = true

	// THE BREAK: an inbound round goes on the 3/9 line, where its sight-line
	// rate is highest and its gimbal runs out, latched for a second and a half
	// so a bearing that swings as the round passes is not chased.
	breaking := false
	if s.incoming.Length() > 0.5 {
		if tick >= s.until || s.beam.Length() < 0.5 {
			path := me.Velocity.Normalize()
			beam := path.Subtract(s.incoming.Scale(path.Dot(s.incoming)))
			if beam.Length() <= 1e-3 {
				beam = right // dead on the nose or the tail: pick the wing line
			}
			s.beam, s.until = beam.Normalize(), tick+90
		}
		want, breaking = s.beam, true
	} else {
		s.beam = flight.Vec3{}
	}

	// The gun: inside its reach, the lift vector goes on the led solution
	// rather than on him, so the nose sweeps through the point the rounds must
	// go to and the brain's wager decides each burst.
	gunning := false
	if !breaking && b.magazine > 0 && span < b.skill.open*1.4 {
		if direction, _, _, _ := b.pipper(m, tick); direction.Dot(axis) > 0.5 {
			want, gunning = direction, true
		}
	}
	if !breaking {
		aside := want.Cross(flight.Vec3{Y: 1}).Normalize()
		rise := aside.Cross(want).Normalize()
		want = want.Add(aside.Scale(b.offset[0])).Add(rise.Scale(b.offset[1])).Normalize() // the tier's aim wander
	}

	// The bubble after the merge. Once the closest approach, both jets flown
	// on as turns at the rates they are flying (nearest), would come inside
	// 1.6 bubbles within five seconds, the lift vector goes the way escape
	// chooses and the gun waits, latched until the range opens. The pilot tier
	// and the novice fly it throughout the fight; the instructor tiers only on
	// the gun from behind him, an overtake, or it takes their turning fight.
	// Against mimic, 32 fights per tier, still air and wind, the fights with a
	// close approach inside 150 m after the merge went from 4 of 256 to 1, and
	// with the slow clear below to none; every circle fight and the ladder held.
	//
	// A slow wing needs longer. The last of those, the novice in wind at 145 m,
	// cleared at its fighting alpha at 160 kt, where that alpha makes one g,
	// and a bandit at 146-233 kt let a jet crossing it on a collision course,
	// found 5 to 8 s out, by inside the bubble in 298 of 576 crossings, the
	// nearest at 31 m. Below three g of wing the look goes on to eight seconds,
	// escape counts gravity, and a clear below the wings is pushed: found 7 s
	// or more out, 2 of 288 still go by inside it (118 and 131 m, a jet
	// climbing at it at 292 kt). Found 5 s out, a wing that slow cannot open
	// 150 m (94-120 m).
	//
	// Declined on the way, against mimic over the same fights: flown by every
	// tier through the whole fight it took the ace's fast one-circle from 21
	// won and 2 lost to 2 and 10 (the superhuman 19/3 to 8/9); on every gun pass
	// for the instructor tiers, the slow one-circle to 9/13 and the fast to 2/8.
	// Three seconds ahead and unlatched it fluttered, turning away until the
	// projection read clear and straight back in; on a collision course the
	// miss's own direction pointed back across his path; at 1.3 bubbles the
	// novice was already too close to open it. Judged on the present line with
	// his turn alone, or only as a pass of 100 m/s or more, it missed the
	// crossings and slow drifts together that most of them were. The look
	// stretched by corner speed over speed, up to 1.6 times, read 250 kt at
	// 14,000 ft as slow and cost the pilot tier 11 of 128 fights (72 won and
	// 54 lost to 61 and 62); the clear held over the regain's floor changed
	// nothing in 256 fights once the slow clear was pushed.
	clearing := false
	// Where he is, whatever the tier's reaction time: inside a kilometre he is
	// in plain sight, so the bubble is kept on where he really is, and further
	// out on the last look carried on. On its once-a-second look the novice's
	// projected miss moved 40 m at every look, and with the guard flying all
	// the way in it went by at 142 m.
	seen := *b.prey
	seen.position = predict(b.prey, age, true)
	if quarry := i.aircraft[b.target]; quarry != nil && quarry.alive && quarry.model != nil {
		if direction, gap := i.bearing(me.Position, quarry.model.State.Position); gap < 1000 {
			seen.position, seen.velocity = me.Position.Add(direction.Scale(gap)), quarry.model.State.Velocity
		}
	}
	now := seen.position
	astern := gunning && me.Position.Subtract(now).Dot(seen.velocity) < 0 // on the gun from behind him: an overtake
	// Fast in a guns match: closing faster than 60 m/s with no missiles in the
	// match. With no missiles the fight comes back round to a second merge,
	// and the instructor tiers met it on the gun at 98-139 m and crossed later
	// at 14-63 m: against mimic with the missiles off, 76 of 128 fights had a
	// pass inside the bubble after the merge, and 1 with this (in the deck
	// recovery, which outranks every branch near the water). At 100 m/s it
	// tripped late on a jet climbing nose on at the bandit and left 3; held to
	// head-on, both noses within 30 degrees of the other jet, it left 9.
	// Declined: in every match, on every gun pass closing faster than 100 m/s, it left 19
	// (15 with the five-second look) but took the circles, the ace's fast
	// one-circle from 21/1 to 7/3, the superhuman's to 7/11, the ace's slow
	// one-circle to 15/6; on every fast pass, gun or not, the superhuman's
	// heater jousts went from 29/2 and 32/0 to 16/12 and 20/11; head-on in a
	// heater match as well, the superhuman's in wind to 20/11. A floor of 1.5 s
	// before the pass on the trip, which the gun-tied form needed, read the
	// same 128 fights here.
	fast := false
	if gap := now.Subtract(me.Position); !i.missiles && gap.Length() > 1 {
		fast = me.Velocity.Subtract(seen.velocity).Dot(gap.Normalize()) > 60
	}
	if !breaking && (b.skill.library < 3 || astern || fast || s.away.Length() > 0.5) {
		if now.Subtract(me.Position).Dot(seen.velocity.Subtract(me.Velocity)) >= 0 {
			s.away = flight.Vec3{} // the range is opening: the pass is over
		}
		if s.away.Length() < 0.5 {
			wing := math.Min(b.skill.pull, m.Airframe.Limit.Positive*math.Pow(speed/corner(m), 2)) // the g the wing has at this speed
			// The five-second look on a fast pass as well: with the overtake's
			// three seconds, 5 of the 36 head-on passes went inside the bubble.
			ahead, room := 5.0, 1.6*bubble
			switch {
			case b.skill.library < 3:
				ahead += 3 * clamp((3-wing)/2, 0, 1)
			case !fast:
				ahead, room = 3, 1.2*bubble // the overtake alone: a gun held from 300 m astern of him is a gun attack, not a pass
			}
			if miss, when := nearest(&seen, me, now, own, math.Max(ahead, 5)); when > 0.1 && when < ahead && miss.Length() < room {
				// Chased: his nose within 30 degrees of me and his turn carrying
				// it toward me. A jet crossing on a straight line to the meeting
				// point has its nose on me too, and does not follow.
				from := me.Position.Subtract(now) // him to me
				chased := false
				if from.Length() > 1 && bending(&seen) > 10 {
					from = from.Normalize()
					path := seen.velocity.Normalize()
					toward := from.Subtract(path.Scale(from.Dot(path)))
					chased = b.prey.nose.Dot(from) > math.Cos(30*math.Pi/180) && seen.swing.Dot(toward) > 0
				}
				s.away = escape(miss, when, me, me.Position.Y < now.Y, chased, wing)
			}
		}
		if s.away.Length() > 0.5 {
			want, gunning, clearing = axis.Add(s.away.Scale(2)).Normalize(), false, true
		}
	} else {
		s.away = flight.Vec3{}
	}

	// THE CHASE: a jet faster than me, flying straight and opening the range
	// beyond 600 m cannot be caught by turning, and the fight law's full alpha
	// behind him bled the ace to the regain's floor and back for minutes (106 s
	// of four below 194 kt), handing a pilot who turns back a slow bandit. Once
	// he has run for three seconds the bandit pursues: the near-nose blend of
	// the gun, and a pull only as hard as the speed affords, the fighting
	// alpha scaled by the speed over corner squared, until he stops running.
	// Three seconds keeps it out of the turning fight, where his regain opens
	// the range for a second or two: the fights against mimic read as they did
	// without it. A cap of two g lost him (21 km behind at 650 kt), and a
	// floor on the push changed nothing. Held only while he ran, rather than
	// to gun range or his turn, a novice fight went by at 94 m in its deck
	// recovery: 2 of 256 with a close approach after the merge against 1.
	steady := !breaking && !gunning && !clearing && span > 600 && bending(&seen) <= 15
	if steady && seen.velocity.Length() > speed && now.Subtract(me.Position).Dot(seen.velocity.Subtract(me.Velocity)) > 0 {
		s.running++
	} else {
		s.running = 0
	}
	s.chasing = steady && (s.chasing || s.running >= 180) // held until he is inside gun range or turns
	pursuing := s.chasing
	// Conceded height. Under a jet perched 300 m or more above with the energy
	// to stay there, the fight law zooms at a climb it cannot finish and bleeds
	// to the regain's floor: in the perch probes (guns, 24 fights each) the
	// script lingered beneath him 77.6% and 44.2% of the fight, handing the
	// pouncer 2.1 s of gun windows a fight, and lost none. Declined: extending
	// level away while he held the height and was not pointing at it, beyond
	// 2 km or turning under a g, in matches without missiles, took that to
	// 10.9%, 27.8% and 0.2 s, but in guns jousts against mimic he came down on
	// its tail and the fights with a pass inside 150 m went from 5 to 18 of
	// 256 (closest 12 m); flown as a level turn in heater matches at every
	// range, the ace's heater jousts fell from 32/0 31/1 to 23/7 19/13.
	// A nose-low slice, the lift vector 20 or 35 degrees below him while he sat
	// more than 45 degrees off, read 8/8 and 9/7 over the joust's 16 against
	// 8/8, and in the replay of 01a10e335a moved the ace's first heater from
	// 19.6 s, a second before the pilot's, to 21.7 s. Declined.
	angle := math.Acos(clamp(want.Dot(axis), -1, 1)) * 180 / math.Pi
	riding := m.Alpha() * 180 / math.Pi
	// The lift vector onto him, wherever he sits round the nose, and the pull
	// sized by how far off he is; inside 1.5 km the fighting alpha is held
	// whatever the nose is doing, which is what sustains the turn.
	//
	// On the gun, near the nose, the direction of a small offset means nothing:
	// him 3 degrees low asked for the lift vector rolled under the canopy, and
	// behind a jet 300 m dead ahead the bandit barrel-rolled through 360
	// degrees at full roll stick, its nose 33 degrees off him, and never fired.
	// So inside `fine` of the nose on the gun the lift vector blends to wings
	// level against gravity and the pull to the alpha that holds a g, plus
	// twice the offset in the plane of the wings, signed: a push when he sits
	// below. It fires on 135 frames of 240 there, within 1.1 degrees (116 and
	// 1.9 at the offset's own gain).
	//
	// Flown away from the gun as well against a jet that is not turning, with
	// the pull eased below corner speed while he outran it beyond a kilometre,
	// it cut the chase of a jet flying straight away from 93 s of four minutes
	// below 194 kt to 54 s, but the fights with a close approach inside 150 m
	// after the merge went from 1 of 256 to 6 against mimic. Declined.
	const fine = 10.0
	w := 1.0
	if gunning || pursuing {
		w = clamp(angle/fine, 0, 1)
	}
	roll := 0.0
	lift := flight.Vec3{}
	if aside := want.Subtract(axis.Scale(want.Dot(axis))); aside.Length() > 1e-6 {
		lift = aside.Normalize().Scale(w)
	}
	if w < 1 {
		if sky := (flight.Vec3{Y: 1}).Subtract(axis.Scale(axis.Y)); sky.Length() > 1e-6 {
			lift = lift.Add(sky.Normalize().Scale(1 - w))
		}
	}
	if lift.Length() > 1e-6 {
		lift = lift.Normalize()
		roll = clamp(math.Atan2(lift.Dot(right), lift.Dot(up))*2.5, -1, 1)
	}
	limit := clamp((speed-60)/90, 0.15, 1)
	goal := clamp(angle/180*math.Pi*3, 0, 1) * b.skill.alpha
	if w < 1 {
		one := riding * math.Max(up.Y, 0) / math.Max(m.Nz(), 0.5) // the alpha that holds a g in the plane of the wings
		offset := math.Atan2(want.Dot(up), want.Dot(axis)) * 180 / math.Pi
		goal = goal*w + (one+2*offset)*(1-w)
	}
	if pursuing {
		goal = math.Min(goal, b.skill.alpha*clamp(math.Pow(speed/corner(m), 2), 0.3, 1)) // only as hard as the speed affords
	}
	// Inside 1.5 km the fighting alpha is held whatever the nose is doing, which
	// is what sustains a turning fight; against a jet that is not turning the
	// pull is sized by the angle as it is further out. Held at the full alpha,
	// the nose went straight through a target flying ahead at its own speed,
	// 300 m off, and the trigger never came; against a target flying straight
	// away the bandit spent 122 s of four minutes below 194 kt. Sizing it by the
	// angle on the gun's led solution as well let the slow one-circle fight go:
	// the ace's alpha fell from 32 to 17 degrees at 430 m with its heater pair a
	// tick from the rail, and it won 1 and lost 17 of 24 against 19 and 1 (the
	// superhuman 3 and 17 against 17 and 5).
	if span < 1500 && bending(b.prey) > 15 {
		goal = b.skill.alpha
	}
	// The regulator is proportional on alpha alone and the flight control law
	// takes the stick as g, so at no error the stick asks for one g: above
	// about 250 kt the pull droops and the bandit porpoises, alpha 28 to 2
	// degrees and back every 1.8 s with the stick at 0.9. Declined, the stick
	// that holds the present g fed forward, (nz - 1) / (limit - 1): the alpha
	// held steady and the circles strengthened (slow one-circle, ace 18/2 to
	// 23/0, superhuman 18/3 to 24/0), but the tighter fights brought passes
	// inside the bubble in 4 of 256 heater jousts against none and 12 of 256
	// guns jousts against 7, and the fast-pass guard in every match that would
	// hold them took the slow two-circle to 0/19. Fed forward only while
	// clearing it left 9 of 256 guns jousts.
	pitch := clamp((goal-riding)/8, -1, limit)
	// A clear below the wings is pushed, rolled so the push goes the way it
	// lies. Rolled inverted to pull, a slow wing spent the clear in the roll,
	// a quarter of the stick at 165 kt, pulling up the while: 64 m by the
	// pass where the push gave 118.
	if clearing && s.away.Dot(up) < -0.3 {
		if under := s.away.Scale(-1).Subtract(axis.Scale(-s.away.Dot(axis))); under.Length() > 1e-6 {
			roll = clamp(math.Atan2(under.Dot(right), under.Dot(up))*2.5, -1, 1)
		}
		pitch = -1
	}
	s.branch = "fight"
	if breaking {
		s.branch = "break"
	} else if pursuing {
		s.branch = "pursue"
	} else if clearing {
		s.branch = "clear"
	} else if gunning {
		s.branch = "guns"
	}
	// The regain: below the floor, or past the ceiling, the stick comes forward
	// whatever the geometry and the band is bought back in burner, with the
	// nose to the horizon or below and the more forward of that and the alpha
	// regulator's own command.
	if speed < n.floor || riding > n.ceiling {
		s.regaining = true
	} else if speed > n.band && riding < 20 {
		s.regaining = false
	}
	if s.regaining {
		pitch, roll = math.Min(clamp(-axis.Y*1.5, -0.4, 0.1), clamp((goal-riding)/8, -1, limit)), roll*0.3
		s.branch = "regain"
	}
	// Roll with the speed there is, and a nudge only at high alpha: a lift
	// vector flipping as he crosses the nose departs a jet at the limiter.
	roll *= clamp((speed-60)/100, 0.15, 1)
	roll *= clamp((36-riding)/8, 0.1, 1)
	if pitch > 0 {
		pitch *= clamp((44-riding)/10, 0.08, 1)
	}
	// No floor below 8,200 ft: mimic pulls at least a third of the stick along
	// its lift vector anywhere under 2,500 m, its one concession to the water,
	// and copied here it pinned the bandit's nose up and the gun off a jet
	// ahead of it (`sea` in 01a108b8 and 01a10e5e). The water belongs to the
	// deck recovery above, sized on the dive. The circle fights flown at 4,900
	// ft read the same with the floor and without it, crashes into the water
	// included.
	fire := gunning && b.squeeze(m, tick)
	b.play, b.rolled = s.branch, roll
	b.demand.Stick = pitch
	return flight.Inputs{Pitch: pitch, Roll: roll, Throttle: 1, Reheat: i.reheat(b, me, span, speed), Fire: fire}
}

// reheat is the fight's burner: COLD, military power, inside the heater span
// while his nose bears on me, and lit otherwise, for the tiers that know what
// a plume does (library 3 and up); stage 0's instructor tiers fly cold inside
// the span whatever his nose is doing. A lit plume stretches his heater's head-on reach from about 750 m to
// 2.5 km and halves every flare's pull, and the plume takes 1.5 s to cool past
// the seduction's threshold (stage_lag), so cutting it once a round is seen is
// too late. Cold everywhere inside the span starves the slow fight instead;
// below 130 m/s the cone narrows to where his seeker can see me now.
//
// Measured against mimic over 24 fights each (won/lost, ace), after the zone
// and the launch were put on the nose (launcher): fast one-circle (450 kt line
// abreast), slow one-circle and slow two-circle (210 kt), and the joust's
// 16 (mimic's deaths/the bandit's):
//   - lit throughout: 5/17, 22/2, 17/7, 9/7
//   - military only while breaking a seen round: 9/12, 22/2, 15/8, 10/6
//   - cold anywhere inside the span: 22/2, 9/10, 17/6, 11/4; with a 130 m/s
//     floor 16/8, 22/2, 17/7, 11/5; 160 m/s 19/5, 22/2, 17/7, 8/8
//   - cold while his nose is within 40 deg: 14/9, 22/1, 23/1, 11/5; 60 deg
//     23/1, 16/6, 20/3, 12/4; 90 deg 21/3, 11/9, 22/1, 11/4
//   - 40 deg, 60 above 130 m/s (this): 23/1, 22/1, 23/1, 13/2; 60 above
//     150 m/s 22/2, 22/1, 23/1, 13/2; 90 above 150 m/s 21/2, 22/1, 23/1, 11/4
//
// The pilot tier flies three-quarters of that cone (plume): against mimic, 32
// fights in still air and wind, it won 41 and lost 18 where it won 34 and lost
// 29 without the plume, the two in three the target asks for; with the whole
// cone 45 and 10, with half 37 and 23. Flown cold through a clear as well, a
// clear tripped and short of speed met him at 106 m, so a clear is lit.
func (i *instance) reheat(b *brain, me *flight.State, span, speed float64) float64 {
	n := &b.tactics.script.cold
	if b.skill.plume == 0 || !i.missiles || span >= b.tactics.missile.span || b.routine.regaining || b.routine.away.Length() > 0.5 {
		return 1 // and lit while clearing a pass: the speed is what opens it
	}
	cone := n.cone * b.skill.plume
	if speed > n.speed {
		cone = n.wide * b.skill.plume
	}
	if back, _ := i.bearing(b.prey.position, me.Position); b.prey.nose.Dot(back) > math.Cos(cone*math.Pi/180) {
		return 0
	}
	return 1
}

// bending is how hard he is turning as the track last saw him, m/s2: his
// acceleration across his own flight path. 15 is about a g and a half.
func bending(t *track) float64 {
	if t.velocity.Length() < 1 {
		return 0
	}
	path := t.velocity.Normalize()
	return t.swing.Subtract(path.Scale(t.swing.Dot(path))).Length()
}

// projected is where he goes by me, on my present line, WITH his measured turn
// carried on through the last few seconds: a pass projected as if he flew
// straight saw a pilot's lead turn onto my line only once it had closed the
// pass, and met him at 59-137 m. What is left along the line of closure is the
// range still to run, not the miss, so it is taken out: my position less the
// pass is the miss.
func projected(prey *track, me *flight.State, spot flight.Vec3) flight.Vec3 {
	relative := prey.velocity.Subtract(me.Velocity)
	at := 0.0
	if speed := relative.Dot(relative); speed > 1 {
		at = clamp(-spot.Subtract(me.Position).Dot(relative)/speed, 0, 60)
	}
	pass, least := spot.Add(relative.Scale(at)), math.Inf(1)
	for k := math.Max(0, at-3); k <= at+3; k += 0.05 {
		his := spot.Add(relative.Scale(k)).Add(prey.swing.Scale(0.5 * k * k))
		if gap := his.Subtract(me.Position).Length(); gap < least {
			pass, least = his, gap
		}
	}
	if closure := relative.Length(); closure > 1 {
		along := relative.Scale(1 / closure)
		pass = pass.Subtract(along.Scale(pass.Subtract(me.Position).Dot(along)))
	}
	return pass
}

// nearest flies both jets on as turns at the rates they are flying, his from
// his measured swing and mine from the acceleration I am flying, and returns
// the miss at the closest point inside the horizon (my position less his) and
// when it comes. Two jets in a hard turning fight each turn a quarter
// circle or more in that time, which a straight line, or a parabola, does not
// follow.
func nearest(prey *track, me *flight.State, spot, own flight.Vec3, horizon float64) (miss flight.Vec3, when float64) {
	const dt = 0.05
	rate := func(velocity, swing flight.Vec3) flight.Vec3 { // the turn's rate vector, rad/s
		speed := velocity.Dot(velocity)
		if speed < 1 {
			return flight.Vec3{}
		}
		return velocity.Cross(swing).Scale(1 / speed)
	}
	mine, his, fly, flown := me.Position, spot, me.Velocity, prey.velocity
	spin, turn := rate(fly, own), rate(flown, prey.swing)
	least := math.Inf(1)
	for k := 0.0; k <= horizon; k += dt {
		if gap := mine.Subtract(his); gap.Length() < least {
			least, miss, when = gap.Length(), gap, k
		}
		fly = fly.Add(spin.Cross(fly).Scale(dt)).Normalize().Scale(fly.Length())
		flown = flown.Add(turn.Cross(flown).Scale(dt)).Normalize().Scale(flown.Length())
		mine, his = mine.Add(fly.Scale(dt)), his.Add(flown.Scale(dt))
	}
	return miss, when
}

// overtake is the planner's reflex against flying into its target from
// behind: once the pass, both jets flown on as turns, would come inside 1.2
// bubbles within three seconds while it closes on him from astern faster than
// 100 m/s, the aim
// goes the way escape chooses at the full pull and the gun waits, latched
// until the range opens. The planner's rehearsal scores a line inside 70 m
// down, but against a constant arc: in TestJink the ace overtook a jinker
// reversing every second or two at 170 m/s through high, low, saddle, lag and
// screw and rammed it. With this nobody rams it and the rounds landed still
// climb the ladder (pilot 97, ace 184, superhuman 314). At any closure it broke
// off ordinary gun attacks on a drone flying straight, whose pass projects as
// a collision course: TestDroneKill's kills fell from 3, 12, 11 and 12 to 1,
// 9, 12 and 10, and the ladder inverted.
func (i *instance) overtake(a *craft, b *brain) {
	s := &b.routine
	me := &a.model.State
	if b.prey == nil || b.target < 0 {
		s.away = flight.Vec3{}
		return
	}
	quarry := i.aircraft[b.target]
	if quarry == nil || !quarry.alive || quarry.model == nil {
		s.away = flight.Vec3{}
		return
	}
	him := &quarry.model.State
	gap := him.Position.Subtract(me.Position)
	if gap.Length() > 1500 || gap.Dot(him.Velocity.Subtract(me.Velocity)) >= 0 {
		s.away = flight.Vec3{} // far, or the range is opening: the pass is over
		return
	}
	if s.away.Length() < 0.5 && me.Position.Subtract(him.Position).Dot(him.Velocity) < 0 && me.Velocity.Subtract(him.Velocity).Dot(gap.Normalize()) > 100 {
		seen := *b.prey
		seen.position, seen.velocity = him.Position, him.Velocity // in plain sight this close
		if miss, when := nearest(&seen, me, him.Position, flight.Vec3{}, 3); when > 0.1 && when < 3 && miss.Length() < 1.2*bubble {
			speed := me.Velocity.Subtract(a.model.Gust()).Length()
			wing := math.Min(b.skill.pull, a.model.Airframe.Limit.Positive*math.Pow(speed/corner(a.model), 2))
			s.away = escape(miss, when, me, me.Position.Y < him.Position.Y, false, wing)
		}
	}
	if s.away.Length() > 0.5 {
		b.aim = me.Velocity.Normalize().Add(s.away.Scale(2)).Normalize()
		b.g, b.shoot = b.skill.pull, false
	}
}

// escape is the way to clear a pass coming inside the bubble: the way the miss
// already lies, or over him, or under when below, when the miss is too small
// to have a direction, kept while the wing can open the pass to 1.6 bubbles by
// the time it comes. A slow wing that cannot takes whichever of that, over and
// under opens it most, gravity counted: over him the lift is spent holding the
// jet up, under him gravity adds to it. At 165 kt over him came to 71 m by a
// pass 5 s out. The water is the deck recovery's.
//
// Chased, his nose within 30 degrees of me, his path follows mine, and the way
// across his height meets him: the ace at 151 kt, 950 ft above mimic climbing
// at it nose on, took under for gravity and passed him at 66 m. So chased, the
// choice is the vertical away from him or the miss's own way kept level. Held to that
// against every jet, 28 of the 84 crossings in TestScriptClearsTheCrossing went
// inside the bubble: a jet crossing in a straight line from below carries on
// above the meeting point, and under is the way out.
func escape(miss flight.Vec3, when float64, me *flight.State, below, chased bool, wing float64) flight.Vec3 {
	away := miss.Normalize()
	if miss.Length() < bubble/2 {
		away = flight.Vec3{Y: 1}
		if below {
			away = flight.Vec3{Y: -1}
		}
	}
	path := me.Velocity.Normalize()
	reach := func(d flight.Vec3) (flight.Vec3, float64) { // the way, across my path, and the miss it makes by the pass
		d = d.Subtract(path.Scale(d.Dot(path)))
		if d.Length() < 0.1 {
			return d, -1
		}
		d = d.Normalize()
		return d, miss.Add(d.Scale(0.5 * 9.81 * math.Max(wing-d.Y, 0) * when * when)).Length()
	}
	if _, kept := reach(away); kept < 1.6*bubble {
		best := kept
		ways := []flight.Vec3{{Y: 1}, {Y: -1}, miss}
		if chased { // over him or under when below, or the miss's own way kept level
			ways = []flight.Vec3{{Y: 1}, {X: miss.X, Z: miss.Z}}
			if below {
				ways[0] = flight.Vec3{Y: -1}
			}
		}
		for _, c := range ways {
			if d, far := reach(c); far > best {
				best, away = far, d
			}
		}
	}
	return away
}

// approach flies the run-in through steer(): a level line aimed to pass him
// 400 m on one side, re-aimed only when the pass would come inside the margin
// or go wider than a fight can be joined from, then the lead turn toward his
// side. A turn toward him closes the pass, so while the pass would come inside
// the margin the turn holds its line until he has gone by (stage 15's merge
// learnt that at 115 m).
func (i *instance) approach(slot int, a *craft, spot, want flight.Vec3, span float64, tick uint64) flight.Inputs {
	b, m := a.brain, a.model
	me := &m.State
	s := &b.routine
	b.shoot = false // no head-on guns: steer() would take the stick to track him through the pass
	pass := projected(b.prey, me, spot)
	if s.side.Length() < 0.5 {
		miss := me.Position.Subtract(pass)
		miss.Y = 0
		if miss.Length() >= 100 {
			s.side = miss.Normalize() // the side the run-in already gives
		} else {
			flat := flight.Vec3{X: want.X, Z: want.Z}.Normalize()
			s.side = flight.Vec3{X: -flat.Z, Z: flat.X}
			if battle.Roll(i.environment.Seed, uint64(slot)+173, tick) < 0.5 {
				s.side = s.side.Scale(-1)
			}
		}
	}
	apart := me.Position.Subtract(pass).Dot(s.side)
	clear := me.Position.Subtract(pass).Length() // the same miss in three dimensions
	flat := flight.Vec3{X: me.Velocity.X, Z: me.Velocity.Z}.Normalize()
	climb := clamp(me.Velocity.Y/math.Max(me.Velocity.Length(), 1), -0.15, 0.25)
	if span < b.tactics.script.lead && b.skill.library >= 2 {
		s.turning = true // the novice holds its line through the pass and turns after it
	}
	b.g, b.throttle, b.reheat, b.brake = b.skill.pull, 1, 0, 0 // military power, as the user runs in
	closing := math.Max(me.Velocity.Subtract(b.prey.velocity).Dot(want), 50)
	line := func() {
		point := spot.Add(b.prey.velocity.Scale(span / closing)).Add(s.side.Scale(offset)).Subtract(me.Position)
		s.course = flight.Vec3{X: point.X, Z: point.Z}.Normalize()
	}
	// A full lead turn from 1.6 km moves the jet about 390 m toward him before
	// the pass, the whole offset, so the turn stops at the margin and the jet
	// flies straight on; only a pass he closes himself, turning onto my line,
	// is moved out again. Latching the stop and re-aiming the line to 400 m
	// turned the jet back the other way on every pass, and still met a pilot
	// turning toward it at 59-152 m.
	//
	// The hold costs the most against a pilot lead-turning toward my side: he
	// crosses my line turning while I fly straight, and mimic doing it won all
	// 8 such jousts, first heater at 18.0 s, the bandit 141-154 degrees off him
	// at the pass and he 53. So the instructor tiers (library 3 and up) take the
	// pass's separation in the VERTICAL: the lead turn goes on toward his side
	// with the line raised, or lowered when he is above, 2 parts to its 2, and
	// he goes by over or under the canopy instead of across a held line.
	// Measured with the cold rule (reheat), the joust's 32 against mimic
	// (mimic's deaths/the bandit's): ace 24/7 -> 30/2, superhuman 28/4 -> 32/0,
	// and no pass inside the bubble in TestScriptPass. A gain of 1 read 31/1 and
	// 32/0 but passed inside the bubble 18 times; before the cold rule, gains of
	// 1, 2 and 4 read 11/5, 9/5 and 8/8 over 16 against 8/8, with 24, 1 and 1
	// passes inside it, every tier flying it.
	//
	// Those passes were measured with stages 6, 11, 14 and 15 of the old planner
	// beneath the script. Flown as single player flies it, the merge came
	// inside the bubble in 11 of the ace's 32 fights in still air and 16 in
	// single player's wind (the superhuman 0 and 16, the pilot tier 6 and 8),
	// closest 93-129 m, and 01a118cd met the pilot at 141 m: the hold judged the
	// pass on the side distance alone and never counted the climb. So the pass
	// is judged in three dimensions, and once it would come inside 1.3 bubbles
	// every tier that lead-turns puts the whole pull into the vertical, four
	// parts up and none toward him; and a tier that sees his turn late stops its
	// lead turn earlier, 300 m further out for each second of perception delay
	// (the pilot tier's 0.5 s; at 100 m it still met him at 104-115 m). No
	// merge comes inside the bubble in either air, and it costs the instructor
	// tiers about a third of their wins, which they took from the close pass;
	// the user ruled the bubble wins. Won and lost against mimic over 32,
	// still air then wind, before and after: pilot 21/11 20/11 -> 17/12 21/11,
	// ace 30/0 28/3 -> 19/12 19/6, superhuman 31/1 31/1 -> 19/11 21/8. Declined:
	// the escape with the lead turn kept, four parts up (20/10 17/13, two
	// merges inside in wind), one part toward him and three up (19/11 19/10, one
	// inside), triggered at 1.1 bubbles (20/9 16/15), at the margin (19/11
	// 17/10), and the stick mapping the flight control law flies (stage 6's
	// delivery) for the run-in instead (25/6 24/8, inside the bubble in 16 of
	// 32 fights, closest 39 m).
	//
	// The cost was all in one geometry: a pilot lead-turning toward my side
	// closed the pass inside 1.3 bubbles every time, the escape fired a second
	// or two before it, and the instructor tiers came out of the merge pointing
	// at the sky. Against mimic turning toward it the ace won 3 and lost 12 of
	// 16 (3/6, 7 undecided, in wind) where it had won 14 and lost 1, and fired
	// the first heater in none of them. So the instructor tiers fly their line
	// to a pass `vertical` off his height and the separation is there before he
	// turns: the lead turn goes on, the escape never fires (TestScriptPass:
	// every instructor pass at 247 m or more, 189-195 m without it), and no
	// merge comes inside the bubble. Against mimic over 32: ace 32/0 31/1,
	// superhuman 30/1 32/0, still air then wind, first heater in all 32.
	stop := margin + 300*b.skill.delay
	rise := 1.0
	if me.Position.Y < spot.Y {
		rise = -1
	}
	if b.skill.library >= 3 {
		// The line climbs, or dives, to the height of the pass: vertical off
		// his level by the time we meet, at the closure we have.
		travel := math.Max(me.Velocity.Length()*span/closing, 1)
		climb = clamp((spot.Y+rise*vertical-me.Position.Y)/travel, -0.15, 0.25)
	}
	switch {
	case s.turning && clear < 1.3*bubble:
		s.branch = "hold"
		b.aim = flat.Add(flight.Vec3{Y: 4 * rise}).Normalize()
	case s.turning && apart < stop && b.skill.library >= 3:
		s.branch = "hold"
		b.aim = flat.Add(s.side.Scale(-2)).Add(flight.Vec3{Y: 2 * rise}).Normalize()
	case s.turning && apart < 1.3*bubble:
		s.branch = "hold"
		line()
		b.aim = flight.Vec3{X: s.course.X, Y: climb, Z: s.course.Z}.Normalize()
	case s.turning && apart < stop:
		s.branch = "hold"
		b.aim = flight.Vec3{X: flat.X, Y: climb, Z: flat.Z}.Normalize()
	case s.turning:
		s.branch = "lead"
		b.aim = flat.Add(s.side.Scale(-2)).Normalize() // toward his side of me
	default:
		s.branch = "line"
		if s.course.Length() < 0.5 || apart < margin || apart > 1000 {
			line()
		}
		b.aim = flight.Vec3{X: s.course.X, Y: climb, Z: s.course.Z}.Normalize()
		if apart >= margin {
			b.g = 3 // a line, gently; a pass closing inside the margin is moved out with the whole pull
		}
	}
	b.play = s.branch
	in := b.steer(m, tick)
	// The heaters stay live on the way in: a joust's hold (free) keeps them on
	// the rail until the merge, and where weapons are free from the first tick
	// a head-on 9M is a shot (stage 0 won the free-for-all against the mush
	// 15-0 with it, where holding it lost 2-6).
	b.shoot = true
	return in
}

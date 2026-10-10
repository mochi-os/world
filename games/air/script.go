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
}

// drive flies the scripted doctrine for one tick, from what decide() last saw.
func (i *instance) drive(slot int, a *craft, tick uint64) flight.Inputs {
	b, m := a.brain, a.model
	me := &m.State
	s := &b.routine
	if b.prey == nil || deck(me) {
		s.branch = "deck"
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

	// A guard for the bubble after the merge was measured and declined. Once the
	// closest approach, both jets flown on as turns at the rates they were
	// flying, would come inside 1.3 bubbles within three seconds, the lift
	// vector went the way the miss lay and the gun waited. Against mimic, 32
	// fights for each of the pilot tier, the ace and the superhuman, still air
	// and wind, the fights with a close approach inside 150 m after the merge
	// went from 11 of 192 to 9, and the fast one-circle went from 21 won and 2
	// lost to 2 and 10 for the ace (the superhuman 19/3 to 8/9, its slow
	// one-circle 17/5 to 14/6). Triggered at the bubble itself, 11 and 2/12;
	// at 1.15 bubbles, 10 and 3/7. Judged on the present line with his turn
	// alone, or only as a pass of 100 m/s or more, it missed the crossings and
	// the slow drifts together that most of them are. The rest came in the
	// regain or under the sea guard, unloaded or held off the water, and
	// letting the guard steer through those changed none of them.
	// A nose-low slice, the lift vector 20 or 35 degrees below him while he sat
	// more than 45 degrees off, read 8/8 and 9/7 over the joust's 16 against
	// 8/8, and in the replay of 01a10e335a moved the ace's first heater from
	// 19.6 s, a second before the pilot's, to 21.7 s. Declined.
	angle := math.Acos(clamp(want.Dot(axis), -1, 1)) * 180 / math.Pi
	riding := m.Alpha() * 180 / math.Pi
	// The lift vector onto him, wherever he sits round the nose, and the pull
	// sized by how far off he is; inside 1.5 km the fighting alpha is held
	// whatever the nose is doing, which is what sustains the turn.
	roll := 0.0
	if aside := want.Subtract(axis.Scale(want.Dot(axis))); aside.Length() > 1e-6 {
		aside = aside.Normalize()
		roll = clamp(math.Atan2(aside.Dot(right), aside.Dot(up))*2.5, -1, 1)
	}
	limit := clamp((speed-60)/90, 0.15, 1)
	goal := clamp(angle/180*math.Pi*3, 0, 1) * b.skill.alpha
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
	pitch := clamp((goal-riding)/8, -1, limit)
	s.branch = "fight"
	if breaking {
		s.branch = "break"
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
	if me.Position.Y < 2500 && !s.regaining && riding < 30 {
		pitch = math.Max(pitch, math.Min(0.35, limit))
		s.branch = "sea"
	}
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
func (i *instance) reheat(b *brain, me *flight.State, span, speed float64) float64 {
	n := &b.tactics.script.cold
	if b.skill.library < 3 || !i.missiles || span >= b.tactics.missile.span || b.routine.regaining {
		return 1
	}
	cone := n.cone
	if speed > n.speed {
		cone = n.wide
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
	stop := margin + 300*b.skill.delay
	rise := 1.0
	if me.Position.Y < spot.Y {
		rise = -1
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

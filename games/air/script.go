package air

import (
	"math"

	"world/games/air/battle"
	"world/games/air/flight"
)

// The scripted doctrine (stage 16): the teamless bandit flies one fixed fight
// instead of choosing among rehearsed plays. It is plan step 6 of the difficulty
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
	speed := me.Velocity.Length()

	// The run-in: a line 400 m beside him, then the lead turn toward his side at
	// mimic's 1.6 km, held until he has gone by. Flown through steer(), which
	// holds a line well; the fight's own law below is for the fight.
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
	if span < 1500 {
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
	return flight.Inputs{Pitch: pitch, Roll: roll, Throttle: 1, Reheat: 1, Fire: fire}
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
	relative := b.prey.velocity.Subtract(me.Velocity)
	at := 0.0
	if speed := relative.Dot(relative); speed > 1 {
		at = clamp(-spot.Subtract(me.Position).Dot(relative)/speed, 0, 60)
	}
	// Where he goes by me, on my present line, WITH his measured turn carried on
	// through the last few seconds: a pass projected as if he flew straight saw a
	// pilot's lead turn onto my line only once it had closed the pass, and met
	// him at 59-137 m. What is left along the line of closure is the range still
	// to run, not the miss.
	pass, least := spot.Add(relative.Scale(at)), math.Inf(1)
	for k := math.Max(0, at-3); k <= at+3; k += 0.05 {
		his := spot.Add(relative.Scale(k)).Add(b.prey.swing.Scale(0.5 * k * k))
		if gap := his.Subtract(me.Position).Length(); gap < least {
			pass, least = his, gap
		}
	}
	if closure := relative.Length(); closure > 1 {
		along := relative.Scale(1 / closure)
		pass = pass.Subtract(along.Scale(pass.Subtract(me.Position).Dot(along)))
	}
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
	switch {
	case s.turning && apart < 1.3*bubble:
		s.branch = "hold"
		line()
		b.aim = flight.Vec3{X: s.course.X, Y: climb, Z: s.course.Z}.Normalize()
	case s.turning && apart < margin:
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

// Mochi world: The decision journal — what the arbiter chose, and how wrong it was about him
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// An instrument, never a behaviour. A recording carried the play the arbiter
// flew (Doctrine) and nothing about WHY: which candidates it weighed, what each
// scored, how far its picture of the opponent sat from where he actually went,
// and how much of the wing its command asked for at each stage between the law
// and the stick. A human fight (recording 01a0b090, 2026-09-17) was traced to
// four candidate mechanisms that only this can separate: the opponent forecast
// measured after the fact at 84 m by 4 s and 851 m by 12 s, a wing share flat at
// 70% in every play, a reflex pre-empting the arbiter, and 22.7 s of one
// offensive play flown under guns. A brain with no journal allocated pays
// nothing and flies bit for bit as before; TestJournalIsInert pins that.

package air

import "world/games/air/flight"

// kept bounds each list if nobody drains it: the newest entries survive.
const kept = 64

// spans are the lookaheads a forecast is checked at, seconds: the base horizon
// either side, and the two long play spans the arc was always weakest at.
var spans = []float64{2, 4, 8, 12}

// decision is one re-plan: every candidate the arbiter rehearsed with its
// score (selection noise and incumbency included - the number it compared),
// the winner, how long the winner was judged over, and the posture it was
// judged under.
type decision struct {
	Tick    uint64             `json:"tick"`
	Play    string             `json:"play"`
	Scores  map[string]float64 `json:"scores"`
	Horizon float64            `json:"horizon"`
	Intent  string             `json:"intent"`
	Promise float64            `json:"promise"`
}

// forecast is one prediction come due: how far the subject was from where the
// re-plan put him. "his" is the opponent phantom the candidates were rehearsed
// against; "own" is the winner's rehearsed track against the jet actually
// flown, which is what says whether the rollout flies the same aeroplane.
type forecast struct {
	Tick    uint64  `json:"tick"`
	Subject string  `json:"subject"`
	Span    float64 `json:"span"`
	Error   float64 `json:"error"`
}

// bypass is the arbiter being pre-empted: the mode that took the controls, and
// the two numbers that tripped it.
type bypass struct {
	Tick  uint64  `json:"tick"`
	Name  string  `json:"name"`
	Speed float64 `json:"speed"`
	Gate  float64 `json:"gate"`
}

type pending struct {
	due     uint64
	made    uint64
	subject string
	span    float64
	point   flight.Vec3
}

type journal struct {
	decisions []decision
	forecasts []forecast
	bypasses  []bypass
	pending   []pending
	mode      string        // the last mode seen, so a bypass is journalled on its edge and not every decision
	tracing   bool          // the winner is being re-flown to capture its own track
	path      []flight.Vec3 // that track, one point per entry of spans the horizon reached
}

// Journal is what a drain hands back. Demand is the latest stack, not a list:
// it changes every tick and the recorder samples it like any other channel.
type Journal struct {
	Decisions []decision `json:"decisions"`
	Forecasts []forecast `json:"forecasts"`
	Bypasses  []bypass   `json:"bypasses"`
	Demand    Demand     `json:"demand"`
}

// Demand is the g the brain asked for at each stage between the play and the
// stick: what the law commanded, what corner discipline left of it, what the
// aero cap left of that, and the pitch stick compose() finally sent. Beside
// the delivered load already in the recording, it says whether an unused wing
// is a pilot easing or an authority lost on the way down.
type Demand struct {
	Law    float64 `json:"law"`
	Corner float64 `json:"corner"`
	Capped float64 `json:"capped"`
	Stick  float64 `json:"stick"`
}

func trim[T any](list []T) []T {
	if len(list) > kept {
		return list[len(list)-kept:]
	}
	return list
}

// decide records a re-plan and books the opponent forecasts that fall due.
func (j *journal) decide(tick uint64, b *brain, scores map[string]float64, horizon int, prey *track) {
	j.decisions = trim(append(j.decisions, decision{Tick: tick, Play: b.play, Scores: scores,
		Horizon: float64(horizon) / 60, Intent: b.intent, Promise: b.promise}))
	age := float64(tick-prey.when) / 60
	for _, span := range spans {
		where, _ := evolve(prey, age+span)
		j.pending = append(j.pending, pending{due: tick + uint64(span*60), made: tick, subject: "his", span: span, point: where})
	}
	for n, where := range j.path {
		j.pending = append(j.pending, pending{due: tick + uint64(spans[n]*60), made: tick, subject: "own", span: spans[n], point: where})
	}
	j.path = j.path[:0]
}

// resolve settles every forecast that has come due against where the two jets
// really are. A respawn or a lost target simply drops what was pending.
func (j *journal) resolve(tick uint64, own flight.Vec3, his *flight.Vec3, wrap float64) {
	waiting := j.pending[:0]
	for _, p := range j.pending {
		if tick < p.due {
			waiting = append(waiting, p)
			continue
		}
		actual := own
		if p.subject == "his" {
			if his == nil {
				continue
			}
			actual = *his
		}
		gap := flight.Vec3{
			X: flight.Shortest(p.point.X, actual.X, wrap),
			Y: actual.Y - p.point.Y,
			Z: flight.Shortest(p.point.Z, actual.Z, wrap),
		}
		j.forecasts = trim(append(j.forecasts, forecast{Tick: p.made, Subject: p.subject, Span: p.span, Error: gap.Length()}))
	}
	j.pending = waiting
}

// notice journals a bypass on the tick its mode first appears.
func (j *journal) notice(tick uint64, mode string, speed, gate float64) {
	if mode != j.mode {
		switch mode {
		case "rebuild", "regain", "evade", "limp":
			j.bypasses = trim(append(j.bypasses, bypass{Tick: tick, Name: mode, Speed: speed, Gate: gate}))
		}
	}
	j.mode = mode
}

// drain hands the lists over and empties them.
func (j *journal) drain(demand Demand) Journal {
	out := Journal{Decisions: j.decisions, Forecasts: j.forecasts, Bypasses: j.bypasses, Demand: demand}
	j.decisions, j.forecasts, j.bypasses = nil, nil, nil
	return out
}

// chronicle is the journal's per-decision upkeep: settle the forecasts that
// have come due against the truth, and notice an arbiter bypass on its edge.
// Truth, not the brain's delayed track: the question is how wrong the PICTURE
// was, so it is measured against where he really is.
func (i *instance) chronicle(slot int, a *craft, tick uint64) {
	b := a.brain
	me := &a.model.State
	var his *flight.Vec3
	if quarry, found := i.aircraft[b.target]; found && quarry != nil && quarry.alive && quarry.model != nil {
		his = &quarry.model.State.Position
	}
	b.journal.resolve(tick, me.Position, his, i.environment.Wrap)
	b.journal.notice(tick, b.mode, me.Velocity.Length(), 0.55*corner(a.model))
}

// minute writes one re-plan into the journal. The winner is flown once more
// with tracing on, purely to capture where the rollout believed the jet would
// BE at each lookahead: rehearse() touches only its scratch model and a copy
// of the brain, so the extra pass cannot move the live bot.
func (i *instance) minute(a *craft, b *brain, sim *flight.Model, prey *track, tick uint64, scores map[string]float64) {
	horizon := 60*2 + 30*b.skill.library
	for _, p := range plays {
		if p.name != b.play {
			continue
		}
		horizon = b.horizon(p)
		b.journal.path = b.journal.path[:0]
		b.journal.tracing = true
		i.rehearse(a, b, sim, p, prey, tick, horizon, 0)
		b.journal.tracing = false
		break
	}
	b.journal.decide(tick, b, scores, horizon, prey)
}

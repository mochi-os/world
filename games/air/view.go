// Mochi world: Air - the frame a bot thinks in
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The brain reads +Y as up and its own y as its height above the sea, which
// is true in the tangent frame under it and nowhere else on the curved earth:
// 15 km from a frame's anchor its up leans 2.4 milliradians and the sea lies
// 18 m below its y = 0. The session keeps its state Earth-centred, where y is
// up nowhere, so a bot always thinks in a frame of its own laid on the sea
// under it, and laid again under it once it has flown a kilometre from that
// frame's anchor. Its view is the same wherever on the earth the fight is:
// a frame shared on a fixed grid would read the sea and up differently for a
// fight moved to another place, by where the fight fell in the grid. Another
// jet's height it asks of the earth model (height).
//
// While a bot thinks in a frame of its own, every aircraft, chaff bloom and
// missile it can read is carried into that frame and carried back after,
// with the frame named in every environment, so the core's sea, air and
// wind read true there and anything the bot launches comes back to the
// world frame with the rest. What the brain remembers between ticks stays
// in its own frame, and is carried across when that frame changes.

package air

import (
	"math"

	"world/games/air/flight"
)

// reach is how far a bot flies from the anchor of the frame it thinks in, m,
// before the frame is laid again under it: a kilometre out the sea lies 8 cm
// under the frame's level and its up leans 0.16 milliradians.
const reach = 1000

// outlook is the frame a bot thinks in: the one it thought in last, while it
// is within reach of that frame's anchor, and otherwise the level frame on
// the sea under it. A drone, which remembers nothing, thinks in the frame
// under it each tick.
func (i *instance) outlook(a *craft) flight.Frame {
	frame := i.environment.Theatre()
	position := a.model.State.Position
	if b := a.brain; b != nil && b.frame.Tangent() {
		if p := flight.Between(frame, b.frame).Point(position); math.Hypot(p.X, p.Z) <= reach {
			return b.frame
		}
	}
	place := frame.Place(position)
	return flight.Tangent(place.Latitude, place.Longitude)
}

// height is a point's height above the sea, from the earth model at the
// point. In the frame a bot thinks in its own y is its height to about a
// decimetre, the curve's 8 cm a kilometre out and the geoid's slope, but
// another jet's is not: the sea falls away under the frame's level as the
// square of the distance, so a jet 5 km off stands 2 m higher than its y says
// and one 20 km off 31 m.
func height(p flight.Vec3, air flight.Environment) float64 { return air.Theatre().Altitude(p) }

// minds runs every living bot's turn in slot order - its brain, or a drone's
// wander - each in the frame it thinks in. The session is carried from one
// bot's frame straight into the next's, and back to its own after the last.
func (i *instance) minds(tick uint64) {
	home := i.environment.Theatre()
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		if !a.bot || !a.alive {
			continue
		}
		if a.brain != nil {
			i.visit(a, home, func() { i.ponder(slot, a, tick) }) // the fighting brain (bot.go)
		} else {
			i.visit(a, home, func() { weave(slot, a, tick) }) // drones keep the original wander
		}
	}
	i.carry(home)
}

// think runs one bot's brain in the frame it thinks in.
func (i *instance) think(slot int, a *craft, tick uint64) {
	i.within(a, func() { i.ponder(slot, a, tick) })
}

// within runs a bot's turn in the frame it thinks in, carrying the session's
// state there and back.
func (i *instance) within(a *craft, turn func()) {
	home := i.environment.Theatre()
	i.visit(a, home, turn)
	i.carry(home)
}

// visit runs a bot's turn in the frame it thinks in, carrying the session
// there if it is not there already and leaving it there. home is the
// session's own frame, where a brain that has never thought remembers.
func (i *instance) visit(a *craft, home flight.Frame, turn func()) {
	frame := i.outlook(a)
	if b := a.brain; b != nil {
		was := b.frame
		if !was.Set() {
			was = home
		}
		if !was.Same(frame) {
			b.rebase(flight.Between(was, frame))
		}
		b.frame = frame
	}
	i.carry(frame)
	turn()
}

// carry moves everything a brain can read into a frame: every aircraft's
// model and its chaff and seeker lines, every missile, and the environment
// that names the frame. The map, the carrier and the wakes stay in the world
// frame, and the core reaches them through the frame its environment names.
func (i *instance) carry(frame flight.Frame) {
	r := flight.Between(i.environment.Theatre(), frame)
	if r.Plain() {
		return
	}
	place := flight.Environment{Frame: frame, Chart: i.environment.Chart}.Placement() // every model reads the session's chart
	for _, c := range i.aircraft {
		if c.model != nil {
			c.model.Carry(r, frame, place)
		}
		if c.cloud != (flight.Vec3{}) {
			c.cloud = r.Point(c.cloud)
		}
		c.sight = r.Vector(c.sight)
	}
	for _, m := range i.flying {
		m.position, m.velocity, m.sight = r.Point(m.position), r.Vector(m.velocity), r.Vector(m.sight)
		if m.lure != (flight.Vec3{}) {
			m.lure = r.Point(m.lure)
		}
		if m.radar != nil {
			m.radar.Carry(r, frame)
		}
	}
	i.environment.Frame = frame
}

// reframe moves the whole session into another frame for good: every model,
// missile, round, wreck and bloom, the environment, and the memory of every
// brain that thought in the old frame. Single player's arena follows the
// client's frame with it; the server's stays Earth-centred.
func (i *instance) reframe(frame flight.Frame) {
	was := i.environment.Theatre()
	r := flight.Between(was, frame)
	if r.Plain() {
		return
	}
	i.carry(frame)
	for k := range i.rounds {
		i.rounds[k].Carry(r)
	}
	for k := range i.debris {
		i.debris[k].Carry(r)
	}
	for _, w := range i.wrecks {
		if w.model != nil {
			w.model.Reframe(frame)
		}
	}
	for _, a := range i.aircraft {
		if b := a.brain; b != nil && (!b.frame.Set() || b.frame.Same(was)) {
			b.rebase(r)
			b.frame = frame
		}
	}
}

// rebase carries everything the brain remembers of the world through a
// motion: the frame it thinks in has moved. A zero point is a memory not yet
// made, and stays zero.
func (b *brain) rebase(r flight.Rigid) {
	if r.Plain() {
		return
	}
	point := func(p *flight.Vec3) {
		if *p != (flight.Vec3{}) {
			*p = r.Point(*p)
		}
	}
	vector := func(v *flight.Vec3) { *v = r.Vector(*v) }
	moved := map[*track]bool{}
	carry := func(t *track) {
		if t == nil || moved[t] {
			return
		}
		moved[t] = true
		point(&t.position)
		vector(&t.velocity)
		vector(&t.swing)
		vector(&t.nose)
		vector(&t.blown)
	}
	for _, t := range b.known {
		carry(t)
	}
	carry(b.prey)
	carry(b.pictured)
	vector(&b.lane)
	vector(&b.residual)
	vector(&b.aim)
	point(&b.sampleP)
	vector(&b.sampleV)
	vector(&b.sampleW)
	vector(&b.beam)
	point(&b.contact)
	point(&b.ring.centre)
	vector(&b.ring.normal)
	vector(&b.ring.drift)
	s := &b.routine
	vector(&s.beam)
	vector(&s.incoming)
	vector(&s.side)
	vector(&s.course)
	vector(&s.velocity)
	vector(&s.away)
	if j := b.journal; j != nil {
		for k := range j.pending {
			point(&j.pending[k].point)
		}
		for k := range j.path {
			point(&j.path[k])
		}
	}
}

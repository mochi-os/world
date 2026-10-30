// Mochi world: Air - the frame a bot thinks in
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The brain reads +Y as up and a jet's y as its height above the sea, which
// is true in the tangent frame of the ellipsoid under it and nowhere else on
// the curved earth: 15 km from a frame's anchor its up leans 2.4
// milliradians and the sea lies 18 m below its y = 0. So a bot thinks in a
// frame laid near it. Near Midway that is the world frame itself, where the
// match is simulated; farther out it is the tangent frame at the centre of
// the patch of the earth the bot is in, a fifth of a degree of latitude and
// of longitude across, so the bots fighting together share one and the
// session carries its state into it once for them all.
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

const (
	vicinity = 15000.0             // m: a bot this close to the session frame's anchor thinks in it
	patch    = 0.2 * math.Pi / 180 // rad of latitude and of longitude: the patches the earth is laid in farther out
)

// outlook is the frame a bot at this position thinks in.
func (i *instance) outlook(position flight.Vec3) flight.Frame {
	theatre := i.environment.Theatre()
	if position.X*position.X+position.Z*position.Z < vicinity*vicinity && position.Y > -1000 {
		return theatre // the far side of the earth reads small x and z too, a whole diameter below
	}
	place := theatre.Place(position)
	latitude := (math.Floor(place.Latitude/patch) + 0.5) * patch
	longitude := (math.Floor(place.Longitude/patch) + 0.5) * patch
	return flight.Tangent(latitude, longitude)
}

// think runs one bot's brain in the frame it thinks in.
func (i *instance) think(slot int, a *craft, tick uint64) {
	i.within(a, func() { i.ponder(slot, a, tick) })
}

// within runs a bot's turn - its brain, or a drone's wander - in the frame
// it thinks in, carrying the session's state there and back.
func (i *instance) within(a *craft, turn func()) {
	theatre := i.environment.Theatre()
	frame := i.outlook(a.model.State.Position)
	if b := a.brain; b != nil {
		was := b.frame
		if !was.Set() {
			was = theatre
		}
		if !was.Same(frame) {
			b.rebase(flight.Between(was, frame))
		}
		b.frame = frame
	}
	if frame.Same(theatre) {
		turn()
		return
	}
	i.carry(frame)
	turn()
	i.carry(theatre)
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
	for _, c := range i.aircraft {
		if c.model != nil {
			c.model.Reframe(frame)
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
// client's frame with it; the server's stays in the world frame.
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

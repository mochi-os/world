// Mochi world: Air carrier under way in a match
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"world/games/air/flight"
	"world/games/air/ship"
)

// underway plans the match's carrier: its station and operating area clear of
// the map's coasts, steaming into the session's wind from time zero. Every
// model in the match shares the chart's carrier, so the track it writes is
// the deck each of them meets.
func (i *instance) underway() {
	c := i.chart.world.Carrier
	if c == nil {
		return
	}
	i.ship = ship.Plan(c, i.environment.Wind, coasts(i.chart.world), 0)
}

// coasts gathers every coastline in a world: the land the ship keeps clear of.
func coasts(w flight.World) [][]flight.Vec3 {
	var list [][]flight.Vec3
	for _, f := range w.Fields {
		if len(f.Coast) >= 3 {
			list = append(list, f.Coast)
		}
	}
	return list
}

// clock is the session time the next tick begins at: where every jet's clock
// stands between ticks, and so the time a jet laid down now is given. The
// framework's first tick is 1.
func (i *instance) clock() float64 {
	return float64(i.stepped+1) / 60
}

// steer moves the ship on once a second, from the tick's opening clock: into
// the wind while anyone needs the deck, across its area while nobody does.
// A changed track goes to every client, a second before the turn it orders.
func (i *instance) steer(tick uint64, clock float64) {
	if i.chart == nil {
		return // an instance built without a map, as the benches build theirs, has no ship
	}
	c := i.chart.world.Carrier
	if c == nil || len(c.Track) == 0 || tick%60 != 0 {
		return
	}
	var states []flight.State
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a.alive && a.model != nil {
			states = append(states, a.model.State)
		}
	}
	if i.ship.Step(c, clock, ship.Needed(c, clock, i.environment.Wrap, states)) {
		i.events = append(i.events, map[string]any{"kind": "ship", "ship": i.voyage()})
	}
}

// voyage is the ship's track as the clients take it: the heading into the
// wind it works along, and each leg as [start, x, z, heading, speed, turn,
// change].
func (i *instance) voyage() map[string]any {
	c := i.chart.world.Carrier
	if c == nil {
		return nil
	}
	legs := make([][]float64, len(c.Track))
	for n, l := range c.Track {
		legs[n] = []float64{l.Start, l.Position.X, l.Position.Z, l.Heading, l.Speed, l.Turn, l.Change}
	}
	return map[string]any{"into": i.ship.Into, "track": legs}
}

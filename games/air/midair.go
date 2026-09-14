// Mochi world: Air midair collisions
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"world/games/air/battle"
)

// mover poses a craft's hit geometry for the midair sweep.
func mover(a *craft) battle.Mover {
	s := &a.model.State
	return battle.Mover{Parts: a.body.Parts, Position: s.Position, Attitude: s.Attitude, Velocity: s.Velocity, Stores: a.body.Stores}
}

// midair is the pair test, run once every aircraft has stepped: each living
// pair inside the bounding-sphere gate is swept capsule against capsule over
// the tick, and what met is struck through the midair table - a clip sheds a
// wing, a break-up is a kill. Nobody is credited: a ram costs the rammer the
// same death, and the invulnerable cheat covers weapons, not the sky. A joust
// goes to the survivor if there is one, and to nobody if there is not.
func (i *instance) midair(dt float64) {
	order := i.slots()
	for x := 0; x < len(order); x++ {
		a := i.aircraft[order[x]]
		for y := x + 1; y < len(order); y++ {
			if a == nil || !a.alive || a.model == nil {
				break
			}
			b := i.aircraft[order[y]]
			if b == nil || !b.alive || b.model == nil {
				continue
			}
			sa, sb := &a.model.State, &b.model.State
			gate := battle.Extent(a.body.Parts) + battle.Extent(b.body.Parts) + sa.Velocity.Subtract(sb.Velocity).Length()*dt
			if shortest(sa.Position, sb.Position, i.environment.Wrap).Length() > gate {
				continue
			}
			metA, metB := battle.Contact(mover(a), mover(b), dt, i.environment.Wrap)
			if len(metA) == 0 {
				continue
			}
			fatalA, tornA := battle.Ram(&a.body, metA)
			fatalB, tornB := battle.Ram(&b.body, metB)
			for _, event := range tornA {
				i.raise(order[x], event)
			}
			for _, event := range tornB {
				i.raise(order[y], event)
			}
			if fatalA {
				i.fell(order[x], -1, "midair", order[y])
			}
			if fatalB {
				i.fell(order[y], -1, "midair", order[x])
			}
			if (fatalA || fatalB) && i.mode == "joust" && !i.finished {
				switch {
				case fatalA && fatalB:
					i.finish(-1, -1)
				case fatalA:
					i.finish(order[y], order[x])
				default:
					i.finish(order[x], order[y])
				}
			}
		}
	}
}

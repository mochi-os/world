// Mochi world: Gun recoil
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

// recoil is the gun's kick while the trigger is held: the cannon's average
// recoil aft along the body axis at the port - about a metre per second
// squared on a fighter - and, with the port above the CG, a nose-up moment
// the FCS trims into the bobble pilots feel at the trigger. The hosts feed
// Fire only while rounds actually leave (the magazine, a joust's hold), so
// the core keeps no ammunition of its own.
func (m *Model) recoil(in Inputs, total *Forces) {
	gun := &m.Airframe.Gun
	if !in.Fire || gun.Recoil <= 0 {
		return
	}
	force := Vec3{X: -gun.Recoil}
	total.Force = total.Force.Add(force)
	total.Moment = total.Moment.Add(gun.Position.Subtract(m.center).Cross(force))
}

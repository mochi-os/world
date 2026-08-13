// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The dynamic launch zone: the cockpit's range ladder, computed by flying
// the round forward against the tracked target — no fitted polynomials, the
// model IS the table. Each rung bisects launch range for its own arrival
// criterion, so the ladder automatically honours the battery, the loft, the
// drag curve and the target's geometry.

package round

import "math"

// Zone is the launch-acceptability ladder for the current geometry, in
// metres of launch range, with the seconds-to-active estimate for the
// current range. Escape ≤ Max ≤ Aero; Minimum floors them all.
type Zone struct {
	Aero    float64 // arrives at all: any speed margin at the merge
	Max     float64 // arrives supersonic against the target flying on as now
	Escape  float64 // arrives supersonic even if the target turns 7.5 g away at launch
	Minimum float64 // fuse arming plus the turn-in floor
	Active  float64 // seconds until activation for a shot taken NOW (an A-time preview)
}

// escape is the defensive break assumed for the no-escape rung.
const escape = 7.5 * 9.80665

// Ladder computes the zone for a shooter and a tracked target. dt is the
// simulation stride — 0.2 s keeps a full ladder cheap enough for a cockpit
// refresh while staying inside the acceptance bands.
func Ladder(shooter Target, target Target, wrap float64) Zone {
	const dt = 0.2
	sight := relative(shooter.Position, target.Position, wrap)
	distance := sight.Length()
	if distance < 1 {
		distance = 1
	}
	direction := sight.Scale(1 / distance)

	// One flight at a virtual launch range, returning the arrival Mach (or
	// -1 for a miss). The virtual target starts at the trial range along
	// TODAY's line of sight with today's velocity; the escaping variant
	// banks 7.5 g away from the shooter until it points down the line.
	arrival := func(trial float64, breaking bool) float64 {
		virtual := Target{Position: shooter.Position.Add(direction.Scale(trial)), Velocity: target.Velocity}
		m := New(shooter.Position, shooter.Velocity, &Target{Position: virtual.Position, Velocity: virtual.Velocity}, wrap)
		closest, mach := math.MaxFloat64, -1.0
		for {
			support := &Target{Position: virtual.Position, Velocity: virtual.Velocity}
			if !m.Step(dt, support, &virtual) {
				break
			}
			if breaking {
				// Turn the victim's velocity away from the shooter at the
				// break rate, holding speed — the no-escape assumption.
				away := relative(shooter.Position, virtual.Position, wrap).Normalize()
				speed := virtual.Velocity.Length()
				if speed > 1 {
					heading := virtual.Velocity.Scale(1 / speed)
					if heading.Dot(away) < 0.999 {
						turned := heading.Add(away.Subtract(heading.Scale(away.Dot(heading))).Normalize().Scale(escape / speed * dt))
						virtual.Velocity = turned.Normalize().Scale(speed)
					}
				}
			}
			virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(dt))
			miss := relative(m.Position, virtual.Position, wrap).Length()
			if miss < closest {
				closest = miss
				_, sound := atmosphere(m.Position.Y)
				mach = m.Velocity.Length() / sound
			} else if miss > closest+2000 {
				break // past the merge and opening: done
			}
		}
		if closest > 500 {
			return -1
		}
		return mach
	}

	// Bisect the outermost range satisfying a criterion. Goodness shrinks
	// monotonically with range at the TOP — but not at the bottom: a
	// shooter whose velocity is off the line of sight (a crank, a beam, a
	// defensive turn at the moment of assessment) cannot make the turn-in
	// at point-blank range while a mid-range shot arrives cleanly. The
	// first version probed only the 2 km floor and declared the whole
	// ladder empty for exactly that geometry — measured as a cranking bot
	// whose every rung read zero against a hot target at 73 km, so it
	// never fired at all. Walk the floor outward past the dead inner band
	// before bisecting the outer edge.
	rung := func(good func(float64) bool) float64 {
		low, high := 2000.0, 150000.0
		for !good(low) {
			low *= 2
			if low > high/2 {
				return 0
			}
		}
		for i := 0; i < 12; i++ {
			mid := (low + high) / 2
			if good(mid) {
				low = mid
			} else {
				high = mid
			}
		}
		return low
	}

	zone := Zone{}
	zone.Aero = rung(func(r float64) bool { return arrival(r, false) >= 0.6 })
	zone.Max = rung(func(r float64) bool { return arrival(r, false) >= 1.0 })
	zone.Escape = rung(func(r float64) bool { return arrival(r, true) >= 1.0 })
	if zone.Escape > zone.Max {
		zone.Escape = zone.Max
	}

	// The floor: what the geometry closes while the fuse arms, plus room to
	// turn the round onto the target.
	closing := shooter.Velocity.Subtract(target.Velocity).Dot(direction)
	if closing < 0 {
		closing = 0
	}
	zone.Minimum = math.Max(800, closing*(Arming+0.8))

	// A-time preview for a shot at the CURRENT range: how long the round
	// would fly before its seeker wakes, assuming today's closure holds
	// plus the round's rough average edge over the jet.
	if distance > Activation {
		zone.Active = (distance - Activation) / math.Max(200, closing+350)
	}
	return zone
}

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

import (
	"math"

	"world/games/air/flight"
)

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
						// The turn is toward the component of the flee direction
						// PERPENDICULAR to the present heading. Exactly anti-parallel
						// -- a target flying straight down the line of sight at the
						// shooter -- makes that component the zero vector, so
						// Normalize() has nothing to point at and the break silently
						// never happened: measured, Escape read identically to Max and
						// the target's velocity was unchanged after a hundred seconds
						// of nominal 7.5 g. Real geometry carries enough float noise to
						// avoid it, but a synthetic head-on lands on it exactly. A jet
						// reversing picks a side; pick one rather than fly on.
						turn := away.Subtract(heading.Scale(away.Dot(heading)))
						if turn.Length() < 1e-6 {
							if turn = (flight.Vec3{Y: 1}).Cross(heading); turn.Length() < 1e-6 {
								turn = (flight.Vec3{X: 1}).Cross(heading)
							}
						}
						turned := heading.Add(turn.Normalize().Scale(escape / speed * dt))
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

	// The floor: what the geometry closes while the fuse arms, plus room to
	// turn the round onto the target.
	closing := shooter.Velocity.Subtract(target.Velocity).Dot(direction)
	if closing < 0 {
		closing = 0
	}
	floor := math.Max(800, closing*(Arming+0.8))

	// Bisect the outermost range satisfying a criterion. Goodness shrinks
	// monotonically with range at the top but not at the bottom - a shooter whose
	// velocity is off the line of sight cannot make the turn-in at point-blank -
	// so walk the floor outward past the dead inner band before bisecting.
	//
	// With edge, the inner end of the band is returned too, as the heater's
	// ladder does for its rung against the target flying on: everything inside
	// the first arriving rung used to be taken on trust down to the floor, and
	// against a crossing target it is exactly where the round cannot turn. Measured with the shooter's nose on a target
	// 1,500 m out crossing at 300 m/s, the round missed by more than 500 m from
	// every range between 800 and 1,800 m while the zone's minimum read 800 m;
	// crossing at 250 m/s it arrived from 800 and 1,000 m, missed from 1,200 m
	// and arrived again from 1,400 m. So below a first rung that arrives, four
	// ranges from the floor up are flown, and the inner edge is bisected above
	// the outermost that misses: a hole in the band takes the band under it too.
	rung := func(good func(float64) bool, edge bool) (float64, float64) {
		low, high := 2000.0, 150000.0
		refused := 0.0
		for !good(low) {
			refused = low
			low *= 2
			if low > high/2 {
				return 0, 0
			}
		}
		above := low
		if edge && refused == 0 {
			for k := 3; k >= 0; k-- {
				trial := floor + (low-floor)*float64(k)/4
				if trial < low && !good(trial) {
					refused, above = trial, floor+(low-floor)*float64(k+1)/4
					break
				}
			}
		}
		inner := 0.0
		if edge && refused > 0 {
			bottom, top := refused, above
			for i := 0; i < 6; i++ {
				mid := (bottom + top) / 2
				if good(mid) {
					top = mid
				} else {
					bottom = mid
				}
			}
			inner = top
		}
		for i := 0; i < 12; i++ {
			mid := (low + high) / 2
			if good(mid) {
				low = mid
			} else {
				high = mid
			}
		}
		return inner, low
	}

	zone := Zone{}
	_, zone.Aero = rung(func(r float64) bool { return arrival(r, false) >= 0.6 }, false)
	near, far := rung(func(r float64) bool { return arrival(r, false) >= 1.0 }, true)
	_, escape := rung(func(r float64) bool { return arrival(r, true) >= 1.0 }, false)
	zone.Max, zone.Escape = far, escape
	if zone.Escape > zone.Max {
		zone.Escape = zone.Max
	}
	// The band's own inner edge, where it sits outside the floor: the Max rung's,
	// against the target flying on. The no-escape rung's beam break saturates a
	// close shot, and its edge would refuse shots that kill a target that does
	// not break, as it did the heater's (heater.go).
	zone.Minimum = math.Max(floor, near)

	// A-time preview for a shot at the CURRENT range: how long the round
	// would fly before its seeker wakes, assuming today's closure holds
	// plus the round's rough average edge over the jet.
	if distance > Activation {
		zone.Active = (distance - Activation) / math.Max(200, closing+350)
	}
	return zone
}

// Lethal is the defender's credibility judgement on a round already in flight:
// from its present state, would it still arrive supersonic against a target
// flying on as now? The model is copied and flown virtually; the caller's round
// is untouched.
func Lethal(m Model, target Target, dt float64) bool {
	virtual := target
	closest, mach := math.MaxFloat64, -1.0
	for {
		support := &Target{Position: virtual.Position, Velocity: virtual.Velocity}
		if !m.Step(dt, support, &virtual) {
			break
		}
		virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(dt))
		miss := m.relative(m.Position, virtual.Position).Length()
		if miss < closest {
			closest = miss
			_, sound := atmosphere(m.Position.Y)
			mach = m.Velocity.Length() / sound
		} else if miss > closest+2000 {
			break
		}
	}
	return closest <= 500 && mach >= 1
}

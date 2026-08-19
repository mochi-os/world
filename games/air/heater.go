// Mochi world: The AIM-9M launch zone
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The heater's dynamic launch zone, the way the AMRAAM has one in
// round/ladder.go: no fitted table, the model IS the table. Each rung flies
// the same round pursue() flies — Mk 36 boost, base and induced drag,
// proportional navigation under the airframe's fading g limit, a seeker with
// a gimbal cone and a track-rate ceiling, the fuse — against a virtual target
// at a trial range along today's line of sight, and bisects the outermost
// range at which the round still arrives. The no-escape rung's break is the
// beam, the heater's own escape, not the AMRAAM's tail-on run.
//
// Built for the cockpit's SHOOT cue (#47): on the real jet the Sidewinder's
// SHOOT exists only with a radar lock, because only the radar knows range,
// and it says the target sits inside the computed zone for this aspect,
// altitude and closure. Ours said "tone" and lit on beam shots at 1.8 km
// that the seeker's 20 deg/s ceiling could not follow — six of six broke lock
// in the recorded fight. Flying the seeker's own rules here is what makes a
// beam shot read as outside the zone rather than as SHOOT.

package air

import (
	"math"

	"world/games/air/flight"
	"world/games/air/round"
)

// Heat computes the AIM-9M zone for a shooter against a target, in the same
// shape as the AMRAAM's so one cue reads both: Aero and Max are the outermost
// range at which the round arrives against the target flying on as now — his
// present turn included, which is what makes a shot at a hard-turning beam
// target read honestly (the heater has no supersonic-arrival distinction, so
// the two coincide); Escape is the range inside which it arrives even if he
// breaks 7.5 g into the beam at launch; Minimum is the fuse arming plus the turn-in
// floor. Active is unused: the seeker is live from the rail. `swing` is the
// target's present acceleration, m/s²; zero flies him straight. `lit` is his
// afterburner (0..1): the seeker's acquisition reach is the plume's, and the
// zone is capped at what the seeker can lock at this aspect — the kinematics
// alone carry the round to twelve kilometres, but a cue that draws a staff
// the seeker cannot fill is the old lie in a longer form.
func Heat(shooter round.Target, target round.Target, swing flight.Vec3, lit float64, wrap float64) round.Zone {
	sight := shortest(shooter.Position, target.Position, wrap)
	distance := math.Max(sight.Length(), 1)
	direction := sight.Scale(1 / distance)

	// One flight at a virtual launch range: the target starts there along
	// TODAY's line of sight with today's velocity, and either flies on or
	// bends 7.5 g away from the shooter until it points down the line. The
	// round leaves the rail as launch() builds it and steps as pursue()
	// steps it, without flares (the zone cannot know them) and without the
	// live seeker's acquisition roll (the tone the cockpit already has).
	arrives := func(trial float64, breaking bool) bool {
		const dt = 1.0 / 60
		virtual := round.Target{Position: shooter.Position.Add(direction.Scale(trial)), Velocity: target.Velocity}
		forward := shooter.Velocity
		if forward.Length() > 1 {
			forward = forward.Normalize()
		} else {
			forward = direction
		}
		m := missile{
			position: shooter.Position.Add(forward.Scale(3)),
			velocity: shooter.Velocity.Add(forward.Scale(30)),
			life:     missile_life,
			burn:     missile_boost,
		}
		m.sight = shortest(m.position, virtual.Position, wrap).Normalize()
		closest := math.MaxFloat64
		for m.life > 0 {
			m.life -= dt
			m.flew += dt
			speed := m.velocity.Length()
			// The proximity fuse: closest approach inside this step, exactly
			// as pursue() judges it.
			if m.flew > missile_arm {
				relative := shortest(m.position, virtual.Position, wrap)
				closure := virtual.Velocity.Subtract(m.velocity)
				step := 0.0
				if squared := closure.Dot(closure); squared > 1e-9 {
					step = clamp(-relative.Dot(closure)/squared, 0, dt)
				}
				if near := relative.Add(closure.Scale(step)).Length(); near < closest {
					closest = near
				}
				if closest < missile_fuse {
					return true
				}
			}
			if m.flew > missile_arm {
				direction := shortest(m.position, virtual.Position, wrap).Normalize()
				axis := m.velocity.Normalize()
				if direction.Dot(axis) < missile_gimbal {
					m.loose = true
				}
				rate := direction.Subtract(m.sight).Scale(1 / dt)
				rate = rate.Subtract(direction.Scale(rate.Dot(direction)))
				if rate.Length() > missile_track {
					m.loose = true
				}
				m.sight = direction
				if !m.loose {
					closing := math.Abs(virtual.Velocity.Subtract(m.velocity).Dot(direction))
					accel := rate.Scale(missile_n * closing)
					limit := missile_g * 9.81 * clamp(speed/600, 0.15, 1)
					if pull := accel.Length(); pull > limit {
						accel = accel.Scale(limit / pull)
					}
					m.velocity = m.velocity.Add(accel.Scale(dt))
					speed = m.velocity.Length()
					bleed := missile_dragk * speed * speed * (1 + 3*(accel.Length()/(missile_g*9.81))*(accel.Length()/(missile_g*9.81)))
					m.velocity = m.velocity.Scale(math.Max(speed-bleed*dt, 60) / math.Max(speed, 1e-6))
					if m.burn <= 0 && speed < virtual.Velocity.Length()+60 && closing < 40 {
						return false // energy death: coasting below convergence speed, opening
					}
				}
			} else {
				m.sight = shortest(m.position, virtual.Position, wrap).Normalize()
			}
			if m.loose {
				// Ballistic, fuse live: it may still pass close, but a lost
				// lock at any range short of the fuse is a miss for the zone.
				return false
			}
			if m.burn > 0 {
				m.burn -= dt
				m.velocity = m.velocity.Add(m.velocity.Normalize().Scale(missile_thrust * dt))
			}
			m.position = m.position.Add(m.velocity.Scale(dt))
			if m.position.Y <= 0 {
				return false
			}
			if speed := virtual.Velocity.Length(); speed > 1 {
				heading := virtual.Velocity.Scale(1 / speed)
				if breaking {
					// The heater's real escape is not the tail-on run the AMRAAM's
					// ladder assumes — this round has the legs for that anywhere
					// it can acquire, and the rung read equal to Rmax at every
					// aspect when it tried. It is the BEAM: square the missile's
					// line of sight at 7.5 g, re-squared as the line rotates, so
					// the endgame hands the seeker more line-of-sight rate than
					// its 20 deg/s ceiling can follow. The side is the one his
					// present turn already favours, if he has one.
					line := shortest(m.position, virtual.Position, wrap).Normalize()
					side := flight.Vec3{Y: 1}.Cross(line).Normalize()
					if side.Dot(swing) < 0 || (swing.Length() < 1 && side.Dot(heading) < 0) {
						side = side.Scale(-1)
					}
					beam := side.Subtract(line.Scale(side.Dot(line))).Normalize()
					if heading.Dot(beam) < 0.999 {
						turned := heading.Add(beam.Subtract(heading.Scale(beam.Dot(heading))).Normalize().Scale(7.5 * 9.80665 / speed * dt))
						virtual.Velocity = turned.Normalize().Scale(speed)
					}
				} else if turn := swing.Subtract(heading.Scale(swing.Dot(heading))); turn.Length() > 1 {
					// Flying on as now means holding his present turn: the
					// velocity rotates at his measured rate, speed held, the
					// same local curve the arbiter's evolve() extrapolates.
					virtual.Velocity = heading.Add(turn.Scale(dt / speed)).Normalize().Scale(speed)
				}
			}
			virtual.Position = virtual.Position.Add(virtual.Velocity.Scale(dt))
		}
		return false
	}

	// Bisect the outermost range satisfying a criterion, walking the floor
	// outward first as the AMRAAM's ladder does: a shooter whose velocity
	// is off the line cannot make the turn-in at point-blank range while a
	// mid-range shot arrives cleanly.
	rung := func(good func(float64) bool) float64 {
		low, high := 400.0, 12000.0
		for !good(low) {
			low *= 1.5
			if low > high/2 {
				return 0
			}
		}
		for i := 0; i < 10; i++ {
			mid := (low + high) / 2
			if good(mid) {
				low = mid
			} else {
				high = mid
			}
		}
		return low
	}
	zone := round.Zone{}
	zone.Max = rung(func(r float64) bool { return arrives(r, false) })
	// The seeker's reach at this aspect, exactly as acquire() judges a lock:
	// full range square at a tailpipe, the plume's floor head-on.
	tail := 0.0
	if speed := target.Velocity.Length(); speed > 1 {
		tail = math.Max(0, direction.Dot(target.Velocity.Scale(1/speed)))
	}
	floor := 0.15 + 0.35*clamp(lit, 0, 1)
	if reach := missile_range * (floor + (1-floor)*tail); zone.Max > reach {
		zone.Max = reach
	}
	zone.Aero = zone.Max
	zone.Escape = rung(func(r float64) bool { return arrives(r, true) })
	if zone.Escape > zone.Max {
		zone.Escape = zone.Max
	}
	// Expect Escape to equal Max at most aspects: the seeker's 5 km reach sits
	// well inside the round's kinematic reach (twenty seconds at 600-800 m/s),
	// and a 35 g round leads a 7.5 g beam with its line-of-sight rate two
	// hundredths of a radian a second below the ceiling — measured across the
	// three aspects and the twelve recorded launches, the beam escape buys
	// nothing inside acquisition range. The rung is kept because it is the
	// right question; the answer this model gives is that a 9M fired with
	// tone and a radar lock inside its zone is a shot to be defended with
	// flares, not flown away from — so the cue flashes rather than holds.
	// The floor: what the geometry closes while the fuse arms, plus room
	// to turn the round onto the target — the same shape as the AMRAAM's,
	// scaled to a round that arms in 0.6 s rather than several.
	closing := shooter.Velocity.Subtract(target.Velocity).Dot(direction)
	if closing < 0 {
		closing = 0
	}
	zone.Minimum = math.Max(300, closing*(missile_arm+0.5))
	return zone
}

// shortest is the wrap-aware displacement from one point to another.
func shortest(from, to flight.Vec3, wrap float64) flight.Vec3 {
	if wrap <= 0 {
		return to.Subtract(from)
	}
	return flight.Vec3{
		X: flight.Shortest(from.X, to.X, wrap),
		Y: to.Y - from.Y,
		Z: flight.Shortest(from.Z, to.Z, wrap),
	}
}

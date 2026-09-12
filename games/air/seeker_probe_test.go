package air

import (
	"math"
	"testing"

	"world/games/air/flight"
	"world/games/air/round"
)

// The seeker instrument (#207). Nine live AIM-9Ms in a twelve-minute joust
// guided for under 1.6 s each and then coasted; not one fused, and every lock
// broke at roughly HALF the launch range for both shooters at every geometry.
// Two conditions in pursue() can break a lock and both tighten as range closes
// — the gimbal cone off the velocity axis, and the track-rate ceiling — so the
// recording alone could not name the culprit.
//
// This probe named it. It drives the LIVE pursue() path frame by frame and
// reconstructs both gates from the missile's own state, without instrumenting
// the guidance loop: after a step, m.sight IS the direction that frame's check
// used, and the velocity sampled before the step IS the axis it used. So the
// reconstruction is exact rather than a second implementation that could
// disagree with the first. The answer was the track ceiling, every time, at
// exactly missile_arm — guidance was suppressed for the round's first 0.6 s,
// so the line-of-sight rate went un-nulled and GREW as the range closed, and
// the first frame the seeker was allowed to look it was judged on the
// amplified value. "Half the launch range" was never a proportion of
// anything: it is simply where a boosting round has got to at 0.6 s.
//
// These arms stay as measurement, not as gates — they are how the envelope is
// re-measured after any change to the seeker constants or the guidance law.
// The one exception is TestSeekerPredictor, which asserts: heater.go and
// air.go implement the same seeker in two files and nothing else couples them.

// seeker is one frame of the reconstruction.
type seeker struct {
	flew  float64 // seconds since launch
	span  float64 // range to target at the check
	cone  float64 // direction.Dot(axis) — breaks below missile_gimbal
	rate  float64 // LOS rotation rate, rad/s — breaks above missile_track
	loose bool    // lock state AFTER this frame
}

// seek flies one missile at a target turning at a fixed g and returns the
// per-frame reconstruction. The target is advanced before pursue exactly as
// instance.Step advances it (i.fly then i.pursue), so the direction the
// guidance sees here is the direction it sees in a live match.
func seek(t *testing.T, span float64, off float64, aspect float64, speed float64, load float64) ([]seeker, *missile, float64) {
	t.Helper()
	// The launch geometry, built in the shooter's frame: the target sits
	// `span` metres away, `off` radians off the nose, and flies `aspect`
	// radians away from straight-away — aspect 0 is a pure tail chase.
	line := flight.Vec3{X: math.Cos(off), Z: math.Sin(off)}
	place := flight.Vec3{X: 0, Y: 3000, Z: 0}.Add(line.Scale(span))
	heading := flight.Vec3{X: math.Cos(off + aspect), Z: math.Sin(off + aspect)}
	i, target, m := missileRange(t, place, heading.Scale(speed))
	// The round leaves the rail on the shooter's boresight, as launch() does.
	m.velocity = flight.Vec3{X: 280}
	m.sight, _ = i.bearing(m.position, place)

	dt := 1.0 / 60
	frames := []seeker{}
	closest := math.MaxFloat64
	for tick := 0; tick < int(missile_life*60); tick++ {
		// The target holds a level turn at `load` g, rotating its velocity.
		if v := target.model.State.Velocity; v.Length() > 1 {
			rate := load * 9.80665 / v.Length()
			side := flight.Vec3{Y: 1}.Cross(v.Normalize()).Normalize()
			turned := v.Normalize().Add(side.Scale(rate * dt)).Normalize().Scale(v.Length())
			target.model.State.Velocity = turned
			target.model.State.Attitude = flight.Look(turned.Normalize())
		}
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))

		was := *m
		// The CONTINUOUS closest approach inside this step, measured from
		// the state pursue's fuse is about to judge — BEFORE the call, not
		// after. A fusing round leaves i.flying inside pursue, so measuring
		// afterwards skips the one frame that decides whether it hit, and
		// reports every kill as a near miss.
		relative := flight.Vec3{
			X: flight.Shortest(was.position.X, target.model.State.Position.X, i.environment.Wrap),
			Y: target.model.State.Position.Y - was.position.Y,
			Z: flight.Shortest(was.position.Z, target.model.State.Position.Z, i.environment.Wrap),
		}
		closure := target.model.State.Velocity.Subtract(was.velocity)
		step := 0.0
		if squared := closure.Dot(closure); squared > 1e-9 {
			step = clamp(-relative.Dot(closure)/squared, 0, dt)
		}
		if near := relative.Add(closure.Scale(step)).Length(); near < closest {
			closest = near
		}

		i.pursue(dt, uint64(tick))
		if len(i.flying) == 0 {
			break
		}
		// Reconstruct the frame the guidance just judged.
		direction, span := i.bearing(was.position, target.model.State.Position)
		axis := was.velocity.Normalize()
		rate := direction.Subtract(was.sight).Scale(1 / dt)
		rate = rate.Subtract(direction.Scale(rate.Dot(direction)))
		frames = append(frames, seeker{flew: m.flew, span: span, cone: direction.Dot(axis), rate: rate.Length(), loose: m.loose})
		if m.loose && was.loose {
			break // already ballistic: the question is answered
		}
	}
	return frames, m, closest
}

// broke reports the frame the lock broke on, and which gate did it.
func broke(frames []seeker) (seeker, string) {
	for n, f := range frames {
		if f.loose && (n == 0 || !frames[n-1].loose) {
			why := ""
			if f.cone < missile_gimbal {
				why = "gimbal"
			}
			if f.rate > missile_track {
				if why != "" {
					why += "+track"
				} else {
					why = "track"
				}
			}
			if why == "" {
				why = "neither(energy/flare)"
			}
			return f, why
		}
	}
	return seeker{}, "held"
}

// TestSeekerBreak is the decisive run: the t=338.0 shot from the 2026-09-12
// joust — 552 m into the rear quarter, 12 deg off the nose, the target pulling
// half a g. Before the fix it guided 1.2 s, went ballistic at 297 m and passed
// 44 m, with the break landing at exactly 0.60 s like every other lock in that
// fight. It still breaks, but now it breaks HONESTLY and early: this geometry
// genuinely presents 0.36 rad/s (20.7 deg/s) at the instant of launch, just
// over the seeker's 20 deg/s ceiling, so the round is refused on physics
// rather than on an artefact of the coast. One notch wider in range or
// narrower in aspect and it holds — see TestSeekerEnvelope.
func TestSeekerBreak(t *testing.T) {
	frames, m, closest := seek(t, 552, 12*math.Pi/180, 45*math.Pi/180, 200, 0.5)
	at, why := broke(frames)
	t.Logf("the t=338 shot: %d frames, lock %s", len(frames), map[bool]string{true: "BROKEN", false: "held"}[m.loose])
	t.Logf("  break at %.2f s, %.0f m, cone %.4f (limit %.4f), rate %.4f rad/s (limit %.2f) -> %s",
		at.flew, at.span, at.cone, missile_gimbal, at.rate, missile_track, why)
	t.Logf("  closest approach %.0f m (fuse %.0f m)", closest, missile_fuse)
	t.Log("  flew   range     cone    limit      rate   limit  lock")
	for n, f := range frames {
		if n%6 != 0 && !(f.loose && (n == 0 || !frames[n-1].loose)) {
			continue // every tenth of a second, plus the break frame itself
		}
		t.Logf("  %4.2f %7.0f  %7.4f %7.4f  %8.4f %7.2f  %s",
			f.flew, f.span, f.cone, missile_gimbal, f.rate, missile_track,
			map[bool]string{true: "loose", false: "track"}[f.loose])
	}
}

// TestSeekerSweep asks which gate breaks the lock across the launch envelope
// the recording actually shows: 540-1680 m, rear quarter to beam, a target
// from wings-level to a hard break.
func TestSeekerSweep(t *testing.T) {
	t.Log(" range  aspect     g   break@   flew   cone    rate  gate                  closest")
	for _, span := range []float64{552, 656, 898, 1669} {
		for _, aspect := range []float64{0, 45, 90} {
			for _, load := range []float64{0, 0.5, 4, 7} {
				frames, _, closest := seek(t, span, 12*math.Pi/180, aspect*math.Pi/180, 200, load)
				at, why := broke(frames)
				where := "-"
				if why != "held" {
					where = ""
				}
				_ = where
				t.Logf("%6.0f %7.0f %5.1f %8.0f %6.2f %6.4f %7.4f  %-20s %7.0f",
					span, aspect, load, at.span, at.flew, at.cone, at.rate, why, closest)
			}
		}
	}
}

// TestSeekerLaunchRate states the mechanism in one table, with no missile
// flown at all: the line-of-sight rate AT THE INSTANT OF LAUNCH, against the
// ceiling the seeker will be judged by. λ̇ = |v_relative perpendicular to the
// sight line| / range, so it scales as 1/range and with the target's crossing
// component. Guidance is suppressed until missile_arm, so nothing nulls this
// rate for the first 0.6 s — whatever it is at launch is roughly what the
// seeker meets the first time it is allowed to look.
func TestSeekerLaunchRate(t *testing.T) {
	t.Logf("line-of-sight rate at launch, against the %.2f rad/s (%.0f deg/s) seeker ceiling", missile_track, missile_track*180/math.Pi)
	t.Log("  range   aspect    rate  deg/s   verdict")
	for _, span := range []float64{552, 656, 898, 1669, 3000} {
		for _, aspect := range []float64{0, 20, 45, 70, 90} {
			off := 12 * math.Pi / 180
			line := flight.Vec3{X: math.Cos(off), Z: math.Sin(off)}
			heading := flight.Vec3{X: math.Cos(off + aspect*math.Pi/180), Z: math.Sin(off + aspect*math.Pi/180)}
			// The round leaves the rail at aircraft speed plus 30 along the
			// shooter's nose, exactly as launch() builds it.
			rail := flight.Vec3{X: 250}.Add(flight.Vec3{X: 1}.Scale(30))
			relative := heading.Scale(200).Subtract(rail)
			rate := relative.Subtract(line.Scale(relative.Dot(line))).Length() / span
			verdict := "inside"
			if rate > missile_track {
				verdict = "OVER THE CEILING AT LAUNCH"
			}
			t.Logf("  %5.0f %7.0f  %6.4f %6.1f   %s", span, aspect, rate, rate*180/math.Pi, verdict)
		}
	}
}

// TestSeekerPredictor asks whether the doctrine's own predictor agrees with
// the weapon that actually flies. reaches() is what every rung of the heater
// ladder bisects over and what the bot's launch gate consults; if it endorses
// shots the live round cannot hold, the whole employment doctrine is tuned
// against a weapon that does not exist.
func TestSeekerPredictor(t *testing.T) {
	t.Log("  range  aspect   predictor    live")
	disagree := 0
	for _, span := range []float64{552, 656, 898, 1669} {
		for _, aspect := range []float64{0, 45, 90} {
			off := 12 * math.Pi / 180
			line := flight.Vec3{X: math.Cos(off), Z: math.Sin(off)}
			heading := flight.Vec3{X: math.Cos(off + aspect*math.Pi/180), Z: math.Sin(off + aspect*math.Pi/180)}
			shooter := round.Target{Position: flight.Vec3{Y: 3000}, Velocity: flight.Vec3{X: 250}}
			prey := round.Target{Position: flight.Vec3{Y: 3000}.Add(line.Scale(span)), Velocity: heading.Scale(200)}
			says := reaches(shooter, prey, line, flight.Vec3{}, 250000, span, false)

			frames, m, closest := seek(t, span, off, aspect*math.Pi/180, 200, 0)
			_, why := broke(frames)
			did := closest < missile_fuse
			mark := ""
			if says != did {
				mark, disagree = "   <- DISAGREE", disagree+1
			}
			t.Logf("  %5.0f %7.0f   %-9s   %-5v closest %3.0f m, lock %s%s",
				span, aspect, map[bool]string{true: "ARRIVES", false: "misses"}[says], did, closest,
				map[bool]string{true: "broken (" + why + ")", false: "held"}[m.loose], mark)
		}
	}
	t.Logf("predictor and live weapon disagree on %d of 12 shots", disagree)
	// The two implement the same seeker in two files, and the ladder is only
	// worth consulting while they agree. Nothing else couples them, so a change
	// to one and not the other shows up here and nowhere else (#207).
	if disagree > 0 {
		t.Errorf("heater.go's reaches() and air.go's pursue() disagree on %d of 12 shots — the doctrine is being scored against a round nobody fires", disagree)
	}
}

// TestSeekerEnvelope measures what the seeker actually accepts, and how much
// of the loss is the unguided coast rather than the launch geometry. Guidance
// is suppressed until missile_arm, so the launch LOS rate is never nulled —
// it GROWS as the round closes on a crossing target, and the first frame the
// seeker is allowed to look it is judged on the amplified value.
func TestSeekerEnvelope(t *testing.T) {
	t.Log("  range   widest aspect held   rate@launch  rate@arm  amplification")
	for _, span := range []float64{552, 656, 898, 1669, 3000} {
		widest, launch, armed := -1.0, 0.0, 0.0
		for aspect := 0.0; aspect <= 90; aspect += 2.5 {
			frames, m, _ := seek(t, span, 12*math.Pi/180, aspect*math.Pi/180, 200, 0)
			if m.loose {
				break
			}
			widest = aspect
			if len(frames) > 0 {
				launch, armed = frames[0].rate, frames[len(frames)-1].rate
				for _, f := range frames {
					if f.flew >= missile_arm && armed == frames[len(frames)-1].rate {
						armed = f.rate
						break
					}
				}
			}
		}
		t.Logf("  %5.0f %14.1f deg %11.4f %9.4f %12.1fx", span, widest, launch, armed, armed/math.Max(launch, 1e-9))
	}
}

// TestSeekerCoverage measures what the existing missile battery actually
// exercises. Every shot it fires is at 1.4 km or beyond, where the launch
// LOS rate is a third of the ceiling and the coast cannot amplify it past —
// so the close-in envelope where the seeker collapses has never been under
// test. It also asks which gate TestMissileGimbal's shot really trips: a
// test that asserts the right outcome through the wrong mechanism will stay
// green across a change to the mechanism it names.
func TestSeekerCoverage(t *testing.T) {
	i, target, m := missileRange(t, flight.Vec3{X: 1400, Y: 3000, Z: 60}, flight.Vec3{X: -80, Z: 320})
	dt := 1.0 / 60
	gate := "held"
	for tick := 0; tick < 600 && gate == "held"; tick++ {
		target.model.State.Position = target.model.State.Position.Add(target.model.State.Velocity.Scale(dt))
		was := *m
		i.pursue(dt, uint64(tick))
		if len(i.flying) == 0 {
			break
		}
		if m.loose && !was.loose {
			direction, span := i.bearing(was.position, target.model.State.Position)
			rate := direction.Subtract(was.sight).Scale(1 / dt)
			rate = rate.Subtract(direction.Scale(rate.Dot(direction)))
			cone := direction.Dot(was.velocity.Normalize())
			gate = "track"
			if cone < missile_gimbal {
				gate = "gimbal"
			}
			t.Logf("TestMissileGimbal's shot breaks on the %s gate at %.2f s, %.0f m", gate, m.flew, span)
			t.Logf("  cone %.4f (breaks below %.4f), rate %.4f rad/s (breaks above %.2f)", cone, missile_gimbal, rate.Length(), missile_track)
			if gate != "gimbal" {
				t.Logf("  -> the test named for the gimbal is measuring the track ceiling")
			}
		}
	}
}

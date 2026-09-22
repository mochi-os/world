// Mochi world: the post-stall probe — what the wing gives and costs past its lift peak
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Measurement only, no assertions: set AIR_POINT=1.
//
// The rollout surrogate (glide, rollout.go) stops at the lift peak: the load is
// held to a lift coefficient of 1.55 and the nose to the alpha that gives it,
// about 15 degrees. The real jet does not stop there. Full aft stick below
// corner takes it on to the 40 degree alpha limit, where the wing still carries
// most of its peak load, the drag is several times the pre-stall figure, and the
// nose sits far inside the flight path. A human won a recorded fight that way
// (01a0b090: 254 kt to 138 in fourteen seconds, radius 1,667 m to 735), and an
// arbiter rehearsing with glide can neither choose that turn nor see it coming.
//
// This flies the bandit's own jet in a full-aft turn from several entry speeds
// and bins what the blade-element model actually delivers by alpha: effective
// lift coefficient (load carried over dynamic pressure, thrust component and
// all, which is what glide's load is) and effective drag coefficient from the
// energy actually lost along the path.

package air

import (
	"fmt"
	"math"
	"os"
	"testing"

	"world/games/air/flight"
)

func TestStallProbe(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	type bin struct{ lift, drag, rate, normal, count float64 }
	for _, reheat := range []float64{0, 1} {
		bins := map[int]*bin{}
		for _, entry := range []float64{230, 190, 160, 130} {
			b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, false)
			b.Spawn(flight.Vec3{Y: 4000}, flight.Vec3{X: entry})
			m := b.craft.model
			area := m.Airframe.Reference.Area
			before := m.State.Velocity
			for tick := 0; tick < 240*14; tick++ {
				s := &m.State
				up := s.Attitude.Rotate(flight.Vec3{Y: 1})
				right := s.Attitude.Rotate(flight.Vec3{Z: 1})
				bank := math.Atan2(right.Y, up.Y)
				// Hold the bank that keeps the turn near level for the load being
				// carried, so the energy lost is drag and not a climb.
				load := math.Max(s.Fcs.Normal, 1.05)
				target := -math.Acos(clamp(1/load, 0, 1))
				roll := clamp((bank-target)*2.5, -1, 1)
				m.Step(flight.Inputs{Pitch: 1, Roll: roll, Throttle: 1, Reheat: reheat})
				speed := s.Velocity.Length()
				local := flight.Atmosphere(s.Position.Y, m.Environment)
				pressure := 0.5 * local.Density * speed * speed
				thrust := 0.0
				for n := range m.Airframe.Engines {
					dry, boost := flight.Output(s.Engine[n], &m.Airframe.Engines[n], local.Density, speed/local.Sound)
					thrust += dry + boost
				}
				// The specific force, from the path itself: the acceleration the jet
				// actually flew, less gravity. Across the path it is the load that
				// turns the jet - glide's `lift`, thrust component and all, and NOT
				// the body-normal load the FCS reports, which at 35 degrees of alpha
				// is mostly drag. Along the path it is thrust less drag.
				force := s.Velocity.Subtract(before).Scale(1 / flight.Dt).Add(flight.Vec3{Y: 9.81})
				before = s.Velocity
				if tick < 240 || speed < 60 {
					continue // the entry transient, and the mush below flying speed
				}
				ahead := s.Velocity.Scale(1 / speed)
				along := force.Dot(ahead)
				across := force.Subtract(ahead.Scale(along)).Length() / 9.81
				drag := thrust - m.Mass()*along
				degrees := int(m.Alpha() * 180 / math.Pi / 5)
				if bins[degrees] == nil {
					bins[degrees] = &bin{}
				}
				c := bins[degrees]
				c.lift += across * m.Mass() * 9.81 / (pressure * area)
				c.drag += drag / (pressure * area)
				c.rate += 9.81 * math.Sqrt(math.Max(across*across-1, 0)) / speed * 180 / math.Pi
				c.normal += s.Fcs.Normal * m.Mass() * 9.81 / (pressure * area)
				c.count++
			}
		}
		fmt.Printf("\nfull aft stick, reheat %.0f: effective coefficients by alpha\n", reheat)
		fmt.Println("  alpha      lift    drag   lift/drag   turn deg/s   body-normal   samples")
		for degrees := 0; degrees < 12; degrees++ {
			c := bins[degrees]
			if c == nil || c.count < 50 {
				continue
			}
			fmt.Printf("  %2d-%2d    %6.2f  %6.2f   %8.2f   %9.1f   %11.2f   %7.0f\n", degrees*5, degrees*5+5,
				c.lift/c.count, c.drag/c.count, c.lift/c.drag, c.rate/c.count, c.normal/c.count, c.count)
		}
	}
}

// TestBleedFidelity holds glide's post-stall branch to the jet it stands for:
// the same licensed order - idle, boards, the whole stick, turning hard right -
// flown through the blade-element model by the bot's own executor and through
// the surrogate, compared on the two things the arbiter buys a bleed for: the
// speed it sheds and the heading it turns.
func TestBleedFidelity(t *testing.T) {
	if testing.Short() {
		t.Skip("flies the full model")
	}
	for _, entry := range []float64{170, 130} {
		fly := func(reduced bool) (speed, turned, alpha float64) {
			b := NewBandit("ace", 7, 250000, "", false, true, "fox2", 0, false)
			b.Spawn(flight.Vec3{Y: 4000}, flight.Vec3{X: entry})
			m, brain := b.craft.model, b.craft.brain
			shadow := *brain
			heading := 0.0
			for tick := 1; tick <= 60*6; tick++ {
				v := m.State.Velocity
				flat := flight.Vec3{X: v.X, Z: v.Z}.Normalize()
				o := order{aim: flight.Vec3{X: -flat.Z, Y: 0.02, Z: flat.X}.Normalize(), g: brain.skill.pull, throttle: 0, brake: 1, alpha: true}
				if reduced {
					glide(m, o, 4*flight.Dt)
				} else {
					shadow.aim, shadow.g, shadow.throttle, shadow.reheat, shadow.brake = o.aim, o.g, o.throttle, o.reheat, o.brake
					in := shadow.steer(m, uint64(tick))
					for sub := 0; sub < 4; sub++ {
						m.Step(in)
					}
				}
				after := flight.Vec3{X: m.State.Velocity.X, Z: m.State.Velocity.Z}.Normalize()
				heading += math.Asin(clamp(flat.X*after.Z-flat.Z*after.X, -1, 1))
				if !reduced {
					alpha = math.Max(alpha, m.Alpha())
				}
			}
			return m.State.Velocity.Length(), heading * 180 / math.Pi, alpha * 180 / math.Pi
		}
		realSpeed, realTurn, peak := fly(false)
		glideSpeed, glideTurn, _ := fly(true)
		t.Logf("entry %.0f m/s, six seconds of bleed: real jet %.0f m/s after %.0f deg (peak alpha %.0f deg) | glide %.0f m/s after %.0f deg",
			entry, realSpeed, realTurn, peak, glideSpeed, glideTurn)
		if peak < 25 {
			t.Errorf("entry %.0f: the licensed order never took the real jet past the lift peak (peak alpha %.0f deg): the bleed is not a bleed", entry, peak)
		}
		if math.Abs(glideSpeed-realSpeed) > 0.2*realSpeed {
			t.Errorf("entry %.0f: glide ends at %.0f m/s where the real jet ends at %.0f: outside 20%%", entry, glideSpeed, realSpeed)
		}
		if math.Abs(glideTurn-realTurn) > 0.25*math.Abs(realTurn) {
			t.Errorf("entry %.0f: glide turns %.0f deg where the real jet turns %.0f: outside 25%%", entry, glideTurn, realTurn)
		}
	}
}

// Mochi world: Blade-element aerodynamics
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The heart of the model: two fixed-order passes over every element. Pass
// one gathers each surface's lift coefficient at geometric incidence; the
// induced downwash, wing-on-tail downwash, and ground effect derived from it
// correct pass two, which accumulates forces and moments about the CG.
// Induced drag is not an input anywhere — it emerges from the lift vector
// tilting with the induced flow.

package flight

import (
	"math"
)

const (
	crossflow = 1.2 // fuselage crossflow drag coefficient on planform
	viscous   = 0.7 // reversed-flow lift knockdown lives in aerofoil.go
)

// aero accumulates aerodynamic forces for a trial state.
func (m *Model) aero(s *State, total *Forces, local Air) {
	a := m.Airframe
	v := s.Attitude.Unrotate(s.Velocity.Subtract(m.gust)) // aircraft velocity relative to the air, body frame
	if v.Length() < 1 {
		return // parked in calm air
	}

	// LEX state first: the coupling applies to BOTH passes, so the downwash
	// follows the lift the energised wing actually makes.
	lex := loading(alpha(v))

	// Ground effect: closer than a span, induced flow weakens.
	height := math.Max(s.Position.Y-m.World.Sea, 1)
	ratio := 16 * height / a.Reference.Span
	ground := ratio * ratio / (1 + ratio*ratio)

	// Pass 1: per-surface lift coefficient, sampled exactly as pass 2 will
	// sample (extension + vortex), so the induced correction is consistent
	// with the real loading. Wings go first so the wing-on-tail wash exists
	// before the tail is sampled — a tail evaluated at its RAW angle sits in
	// phantom stall the washed pass-2 tail never sees, and the disagreement
	// used to put a violent pitch-up kink at 8-12° alpha.
	wingLift, wingArea := 0.0, 0.0
	wash := 0.0
	for _, wings := range []bool{true, false} {
		for si := range a.Surfaces {
			surface := &a.Surfaces[si]
			if (surface.Kind == Wing) != wings {
				continue
			}
			if surface.Kind == Brake {
				m.lift[si] = 0
				continue
			}
			sum, area := 0.0, 0.0
			for ei := range surface.Elements {
				e := &surface.Elements[ei]
				w := v.Add(s.Omega.Cross(e.Position.Subtract(m.center))).Scale(-1)
				if surface.Kind == Stabilator {
					w = w.Subtract(e.Normal.Scale(w.Length() * wash)) // the same tilt pass 2 applies
				}
				angle, shift := m.section(s, surface, e, w)
				raw := angle + shift*0.5
				hold := retention(surface, ei, lex)
				if surface.Kind == Wing {
					hold = clamp(hold+s.Fcs.Slat/0.44*0.5, 0, 0.9)
				}
				cl, _, _ := extended(e.Aerofoil, raw, hold, surface.Slope)
				if shift != 0 {
					cl += surface.Slope * shift * 0.55 * math.Cos(clamp(raw, -1.2, 1.2))
				}
				if surface.Vortex > 0 {
					extra, _ := vortex(surface.Vortex, surface.Breakdown, raw)
					cl += extra
				}
				sum += cl * e.Area
				area += e.Area
			}
			if area > 0 {
				m.lift[si] = sum / area
			}
			if surface.Kind == Wing {
				wingLift += sum
				wingArea += area
			}
		}
		if !wings {
			break
		}
		// Wing-on-tail downwash from the wing's SELF-CONSISTENT lift: the
		// pass-one coefficient is at geometric incidence, so scale it by the
		// finite-wing factor before deriving the wash (see induced below).
		wingCoefficient := 0.0
		if wingArea > 0 {
			wingCoefficient = wingLift / wingArea
		}
		for si := range a.Surfaces {
			if a.Surfaces[si].Kind == Wing {
				w := &a.Surfaces[si]
				span := math.Pi * w.Ratio * w.Oswald
				wingCoefficient *= span / (span + w.Slope)
				break
			}
		}
		wash = 2 / (math.Pi * 3.5) * wingCoefficient * ground
	}

	// Pass 2: corrected incidence, force and moment accumulation.
	for si := range a.Surfaces {
		surface := &a.Surfaces[si]
		// Self-consistent one-shot downwash: pass one sampled the polar at
		// geometric incidence, so the raw CL over-states the loaded wing.
		// alpha_i = CL1/(pi*AR*e + a) is the closed-form consistent answer in
		// the linear region (equivalent to iterating CL -> alpha_i -> CL to
		// convergence) and a sound approximation beyond it.
		induced := 0.0
		if surface.Kind != Brake {
			induced = m.lift[si] / (math.Pi*surface.Ratio*surface.Oswald/math.Max(ground, 0.05) + surface.Slope)
		}
		for ei := range surface.Elements {
			e := &surface.Elements[ei]
			r := e.Position.Subtract(m.center)
			w := v.Add(s.Omega.Cross(r)).Scale(-1)
			// Section flow: remove the span component.
			section := w.Subtract(e.Axis.Scale(w.Dot(e.Axis)))
			speed := section.Length()
			if speed < 0.5 {
				continue
			}
			pressure := 0.5 * local.Density * speed * speed
			if surface.Kind == Brake {
				_, cd, _ := e.Aerofoil.Sample(0)
				drag := section.Normalize().Scale(pressure * e.Area * cd * s.Fcs.Speedbrake)
				total.Force = total.Force.Add(drag)
				total.Moment = total.Moment.Add(r.Cross(drag))
				continue
			}
			// Induced flow: the downwash adds a real velocity component along
			// -Normal, tilting the local flow. Lift stays perpendicular to the
			// TILTED flow, so its aft component IS the induced drag — emergent,
			// not added.
			downwash := induced
			if surface.Kind == Stabilator {
				downwash += wash
			}
			section = section.Subtract(e.Normal.Scale(speed * downwash))
			speed = section.Length()
			pressure = 0.5 * local.Density * speed * speed
			angle, shift := m.section(s, surface, e, section)
			effective := angle + shift*0.5
			hold := retention(surface, ei, lex)
			if surface.Kind == Wing {
				hold = clamp(hold+s.Fcs.Slat/0.44*0.5, 0, 0.9) // slats keep the wing attached
			}
			cl, cd, cm := extended(e.Aerofoil, effective, hold, surface.Slope)
			// Camber lift: the half of the flap deflection that raises CLmax
			// rather than spending stall margin; rolls off in deep stall.
			if shift != 0 {
				cl += surface.Slope * shift * 0.55 * math.Cos(clamp(effective, -1.2, 1.2))
				cd += 0.01 * shift * shift
			}
			if surface.Vortex > 0 {
				extra, suction := vortex(surface.Vortex, surface.Breakdown, effective)
				cl += extra
				cd += suction
			}
			slope, wave, shift := compress(speed/local.Sound, cl, a.Wave.Hump)
			// Prandtl-Glauert amplifies attached potential flow, not a
			// separated wake: fade the slope factor out across the stall, or
			// the transonic lift-loss at the break doubles and the swept-wing
			// pitch-up kink becomes violent.
			if at := math.Abs(effective); at > e.Aerofoil.Stall && slope > 1 {
				slope = 1 + (slope-1)*clamp(1-(at-e.Aerofoil.Stall)/0.1, 0, 1)
			}
			cl *= slope
			cd += wave
			cm += shift
			cl *= m.State.Damage.element(si)
			flow := section.Normalize()
			lift := flow.Cross(e.Axis).Normalize()
			if lift.Dot(e.Normal) < 0 {
				lift = lift.Scale(-1)
			}
			force := lift.Scale(pressure * e.Area * cl).Add(flow.Scale(pressure * e.Area * cd))
			total.Force = total.Force.Add(force)
			total.Moment = total.Moment.Add(r.Cross(force))
			total.Moment = total.Moment.Add(lift.Cross(flow).Scale(pressure * e.Area * e.Chord * cm))
		}
	}

	// Fuselage stations: axial drag, nose potential lift, crossflow.
	w := v.Scale(-1)
	speed := w.Length()
	pressure := 0.5 * local.Density * speed * speed
	flow := w.Normalize()
	bodyAlpha := alpha(v)
	bodyBeta := beta(v)
	bodyMach := speed / local.Sound
	// Transonic body wave drag: the fuselage is the area-rule offender.
	bodyWave := 0.0
	if bodyMach > 0.92 {
		ramp := clamp((bodyMach-0.92)/0.12, 0, 1)
		bodyWave = a.Wave.Body * ramp * ramp
		if bodyMach > 1.02 {
			bodyWave /= 1 + (bodyMach-1.02)*3.0 // the hump decays supersonic
		}
	}
	for bi := range a.Body {
		station := &a.Body[bi]
		r := station.Position.Subtract(m.center)
		force := flow.Scale(pressure * station.Area * (station.Drag + bodyWave))
		// Crossflow drag normal to the body axis.
		normal := pressure * station.Plan * crossflow
		force = force.Add(Vec3{Y: normal * math.Sin(bodyAlpha) * math.Abs(math.Sin(bodyAlpha))})
		force = force.Add(Vec3{Z: -normal * math.Sin(bodyBeta) * math.Abs(math.Sin(bodyBeta))})
		if bi == 0 {
			// Slender-body potential lift on the nose station.
			force = force.Add(Vec3{Y: pressure * station.Area * 2 * math.Sin(bodyAlpha) * math.Cos(bodyAlpha)})
			force = force.Add(Vec3{Z: -pressure * station.Area * 2 * math.Sin(bodyBeta) * math.Cos(bodyBeta)})
		}
		total.Force = total.Force.Add(force)
		total.Moment = total.Moment.Add(r.Cross(force))
	}
	total.Force = total.Force.Add(flow.Scale(pressure * m.State.Damage.Drag))
}

// incidence is the effective section angle of attack: geometry, control
// deflection, and any induced correction.
func (m *Model) incidence(s *State, surface *Surface, e *Element, w Vec3, correction float64) float64 {
	angle, shift := m.section(s, surface, e, w)
	return angle + shift*0.5 + correction
}

// section splits the raw geometric angle from the control-surface shift:
// half the flap deflection acts as an incidence change, the rest as camber
// lift added after the polar (real flaps raise CLmax; a pure alpha shift
// would just stall the section early).
func (m *Model) section(s *State, surface *Surface, e *Element, w Vec3) (float64, float64) {
	plane := w.Subtract(e.Axis.Scale(w.Dot(e.Axis)))
	chord := e.Axis.Cross(e.Normal) // points aft
	raw := math.Atan2(plane.Dot(e.Normal), plane.Dot(chord))
	shift := 0.0
	switch surface.Channel {
	case Symmetric: // all-moving: deflection IS incidence, full authority
		if surface.Side < 0 {
			raw += s.Fcs.Stabilator.Left
		} else {
			raw += s.Fcs.Stabilator.Right
		}
	case Differential:
		deflection := s.Fcs.Flap // PA droop, both sides
		if surface.Side < 0 {
			deflection += s.Fcs.Flaperon.Left
		} else {
			deflection += s.Fcs.Flaperon.Right
		}
		shift = Effectiveness(e.Flap) * deflection
	case Rudder:
		// Mirrored fin frames flip the meaning of a camber shift: without
		// the side sign the two rudders' side forces cancel exactly.
		shift = Effectiveness(e.Flap) * s.Fcs.Rudder * -surface.Side
	}
	return raw + e.Incidence, shift
}

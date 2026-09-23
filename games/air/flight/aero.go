// Mochi world: Blade-element aerodynamics
// Copyright © 2026 Mochisoft OÜ
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

	// Flat plates: configuration drag the element polars cannot see, each an area
	// in m² (the Damage.Drag convention) applied against the local flow with the
	// moment of its position; a dead engine's plate is off-centre.
	{
		speed := v.Length()
		dynamic := 0.5 * local.Density * speed * speed
		direction := v.Normalize()
		plate := func(area float64, at Vec3) {
			if area <= 1e-6 {
				return
			}
			force := direction.Scale(-dynamic * area)
			total.Force = total.Force.Add(force)
			total.Moment = total.Moment.Add(at.Subtract(m.center).Cross(force))
		}
		plate(0.02*a.Reference.Area*s.Gear.Extension, m.center) // undercarriage: ΔCD 0.02 on the reference area, the published class increment; CG-applied (the extension trim change is small and the FCS trims it regardless)
		plate(0.05*m.probe, m.center)                           // refuelling probe
		plate(0.01*m.arrestor, m.center)                        // arrestor hook
		for i := range a.Stores {
			if m.stores&(1<<uint(i)) != 0 {
				plate(a.Stores[i].Area, a.Stores[i].Position)
			}
		}
		for i := 0; i < len(a.Engines) && i < len(s.Damage.Engine); i++ {
			plate(0.15*s.Damage.Engine[i], a.Engines[i].Position) // windmilling/seized core: drag grows as the engine dies
		}
	}

	// LEX state first: the coupling applies to BOTH passes, so the downwash
	// follows the lift the energised wing actually makes.
	lex := loading(alpha(v))
	// Battle damage: a shot-away strake stops energising its own side, so the
	// LEX coupling is scaled by per-side strake health before retention.
	health := [2]float64{1, 1}
	for si := range a.Surfaces {
		if a.Surfaces[si].Kind != Strake || len(a.Surfaces[si].Elements) == 0 {
			continue
		}
		sum := 0.0
		for ei := range a.Surfaces[si].Elements {
			sum += m.State.Damage.element(m.base[si] + ei)
		}
		mean := sum / float64(len(a.Surfaces[si].Elements))
		if a.Surfaces[si].Side < 0 {
			health[0] = mean
		} else if a.Surfaces[si].Side > 0 {
			health[1] = mean
		}
	}
	coupled := func(surface *Surface) float64 {
		if surface.Side < 0 {
			return lex * health[0]
		}
		if surface.Side > 0 {
			return lex * health[1]
		}
		return lex * (health[0] + health[1]) * 0.5
	}

	// Ground effect: closer than a span, induced flow weakens. Height is above the
	// surface actually beneath the jet - an elevated field or the carrier deck,
	// not sea level. The abrupt step at the round-down is deliberate.
	beneath := m.World.Sea
	ceiling := m.World.top() // the probe gate must clear the world's HIGHEST surface: a sea-referenced gate skipped the probe exactly over the elevated terrain the reference exists for
	if s.Position.Y-ceiling < 6*a.Reference.Span {
		if top, _, _, found := m.World.surface(s.Position, s.Time, m.Environment.Wrap); found && top > beneath {
			beneath = top
		}
	}
	height := math.Max(s.Position.Y-beneath, 1)
	ratio := 16 * height / a.Reference.Span
	ground := ratio * ratio / (1 + ratio*ratio)

	// Pass 1: per-surface lift coefficient, sampled exactly as pass 2 will sample
	// it. Wings go first so the wing-on-tail wash exists before the tail is
	// sampled - a raw-angle tail sits in stall pass 2 never sees.
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
				across := sweep(e)
				angle, shift := m.section(s, surface, e, w)
				raw := angle + shift*0.5/math.Sqrt(across) // the flap's incidence share is set streamwise: the swept section sees it over cos
				hold := retention(surface, ei, coupled(surface))
				if surface.Kind == Wing {
					hold = clamp(hold+s.Fcs.Slat/0.44*0.5, 0, 0.9)
				}
				sectional, _, _ := extended(e.Aerofoil, raw, hold, surface.Slope/math.Sqrt(across))
				// The induced flow is the surface's whole vortex system, shared by
				// both panels, so its loading takes the symmetric share of the
				// pressure - cos² of the sweep at small alpha, more as the normal
				// flow grows - and not a panel's own sideslip advantage, which fed
				// straight back as that panel's downwash and cancelled the dihedral
				// effect the sweep had just made.
				level := Vec3{X: w.X, Y: w.Y}
				plane := level.Subtract(e.Axis.Scale(level.Dot(e.Axis)))
				cl := sectional * plane.Dot(plane) / math.Max(level.Dot(level), 1e-9) // the surface's reference coefficient, which the camber and vortex terms are written on
				body := unswept(raw, across)
				if shift != 0 {
					cl += surface.Slope * shift * 0.55 * math.Cos(clamp(body, -1.2, 1.2))
				}
				if surface.Vortex > 0 {
					extra, _ := vortex(surface.Vortex, surface.Breakdown, body)
					cl += extra
				}
				cl *= m.State.Damage.element(m.base[si] + ei) // damaged elements stop lifting in BOTH passes, so the induced wash tracks the damaged loading. Dead elements keep their AREA in this mean and the aspect ratio stays full-span: right for scattered hole damage (circulation drops, span doesn't), but a contiguous tip amputation hands the survivors the tips' downwash relief, and with the FCS alpha backstop gating the flown onset by alpha the sink onset INVERTS ~6% under clean symmetric clipping — accepted (see TestWingLossStalls; the honest alternative is a span-tracking effective aspect ratio)
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
		// Self-consistent one-shot downwash: pass one sampled the polar at geometric
		// incidence, so raw CL over-states the loaded wing. alpha_i = CL1/(pi*AR*e +
		// a) is the converged answer in closed form.
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
				// A panel hinged out of the flow: the plate's normal-force drag times
				// sin² of its deflection, so half travel is a third of the drag, not
				// half. An airframe that declares no travel keeps the linear stand-in.
				_, cd, _ := e.Aerofoil.Sample(0)
				open := s.Fcs.Speedbrake
				if throw := a.Control.Throw.Brake; throw > 0 {
					open = math.Pow(math.Sin(throw*s.Fcs.Speedbrake), 2)
				}
				drag := section.Normalize().Scale(pressure * e.Area * cd * open)
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
			section = section.Subtract(e.Normal.Scale(w.Length() * downwash)) // the induced velocity is normal to the surface and scales with the whole flow, so a swept section's alpha drops by the induced angle over cos of its sweep
			speed = section.Length()
			pressure = 0.5 * local.Density * speed * speed
			// A swept element's section sees only the flow normal to its axis:
			// its dynamic pressure runs cos² of the sweep below the surface's
			// reference and its coefficients 1/cos² above, and its polar is
			// rescaled to match (fa18c.go strips). The calibrated terms below -
			// drag-due-to-lift, the polar break, camber, vortex lift and the
			// compressibility - are all written on the REFERENCE coefficients
			// and the body alpha and Mach, so they convert on the way in.
			across := sweep(e)
			angle, shift := m.section(s, surface, e, section)
			effective := angle + shift*0.5/math.Sqrt(across) // the flap's incidence share is set streamwise: the swept section sees it over cos
			body := unswept(effective, across)
			hold := retention(surface, ei, coupled(surface))
			if surface.Kind == Wing {
				hold = clamp(hold+s.Fcs.Slat/0.44*0.5, 0, 0.9) // slats keep the wing attached
			}
			cl, cd, cm := extended(e.Aerofoil, effective, hold, surface.Slope/math.Sqrt(across))
			// The calibrated terms are priced on the body dynamic pressure, so
			// they carry no sweep-at-sideslip sensitivity of their own: the
			// potential lift's is the dihedral effect; a flap's camber or the
			// vortex system riding the section pressure as well doubled it.
			tilted := w.Subtract(e.Normal.Scale(w.Length() * downwash)) // the flow the element meets, downwash and all, without the section's spanwise lean: the body frame the calibrated terms and the hump's Mach were written in
			plain := 0.5 * local.Density * tilted.Dot(tilted)
			// boost carries the calibrated terms, written on the flow the element
			// meets, onto the section pressure. The section flow is what the span
			// and the downwash leave of that flow, and it can all but vanish while
			// the flow does not; the terms riding the ratio then run quadratic
			// through the polar break and the compressibility, and one step's force
			// has no bound. The ratio stops where sweep() stops the section theory,
			// at 1/0.05: healthy flight stays under 3.2 at the 99.99th percentile.
			boost := 1 / 0.05
			if plain < boost*pressure {
				boost = plain / pressure
			}
			reference := cl * pressure / plain                    // the surface's reference coefficient, on the pressure share the section sees
			cd += surface.Induced * reference * reference * boost // calibrated drag-due-to-lift the emergent tilt under-prices
			if over := math.Abs(reference) - 1.1; over > 0 {
				cd += 0.07 * over * over * boost // the polar break: past cl ~1.1 the real polar departs the parabola (separation growth the parabolic K cannot price). Lets the mid-cl K sit at its EM-plateau fit (0.14, fa18c.go) without freeing the high-cl stations past the chart bands (TestEnvelopeMap 250 kt / 15,000 ft)
			}
			// Camber lift: the half of the flap deflection that raises CLmax
			// rather than spending stall margin; rolls off in deep stall.
			if shift != 0 {
				cl += surface.Slope * shift * 0.55 * math.Cos(clamp(body, -1.2, 1.2)) * boost
				cd += 0.01 * shift * shift * boost
			}
			if surface.Vortex > 0 {
				extra, suction := vortex(surface.Vortex, surface.Breakdown, body)
				cl += extra * boost
				cd += suction * boost
			}
			slope, wave, shift := compress(tilted.Length()/local.Sound, cl*across, a.Wave.Hump)
			// Prandtl-Glauert amplifies attached potential flow, not a
			// separated wake: fade the slope factor out across the stall, or
			// the transonic lift-loss at the break doubles and the swept-wing
			// pitch-up kink becomes violent.
			if at := math.Abs(effective); at > e.Aerofoil.Stall && slope > 1 {
				slope = 1 + (slope-1)*clamp(1-(at-e.Aerofoil.Stall)/0.1, 0, 1)
			}
			cl *= slope
			cd += wave * boost
			cm += shift * boost
			cl *= m.State.Damage.element(m.base[si] + ei)
			// The lift stands perpendicular to the flow the element meets, in
			// the plane of that flow and the surface normal: for an unswept
			// strip that is flow × axis, and for a swept one it keeps the lift
			// upright rather than leaning it in the plane normal to the swept
			// axis, which shed cos of the lean at high alpha and lost the
			// pitch-recovery gates a second.
			flow := tilted.Normalize()
			lift := e.Normal.Subtract(flow.Scale(e.Normal.Dot(flow))).Normalize()
			// The profile drag runs along the flow the element meets, tilted by
			// the induced downwash like the lift (at a low-aspect surface's
			// tilt that costs real lift), but not along the section flow: a
			// swept section's flow leans spanwise, and drag laid along it sheds
			// cos of the sweep and leaves a spanwise push.
			force := lift.Scale(pressure * e.Area * cl).Add(tilted.Normalize().Scale(pressure * e.Area * cd))
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
	// Onset 0.86 over a 0.20 ramp (#95): the old 0.92/0.12 step dropped the
	// whole hump between the 600 KCAS acceleration gate and terminal, so
	// the deck run held flat 2.4 s segments to M0.91 and then the rise bit
	// hard enough to park terminal at 660 KCAS — the creep now starts
	// where the real jet's last hundred knots begin to stretch and peaks
	// past the terminal band.
	bodyWave := 0.0
	if bodyMach > 0.86 {
		ramp := clamp((bodyMach-0.86)/0.20, 0, 1)
		bodyWave = a.Wave.Body * ramp * ramp
		if bodyMach > 1.06 {
			bodyWave /= 1 + (bodyMach-1.06)*3.0 // the hump decays supersonic
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
			// Slender-body potential lift on the nose station. At sideslip the
			// force works on the forebody's full cross-section, LEX included -
			// the F-18's side force lives in its forebody (HARV Cy_beta) - where
			// the pitch-plane term keeps the nose station it was calibrated on.
			forebody := a.Forebody
			if forebody <= 0 {
				forebody = station.Area
			}
			force = force.Add(Vec3{Y: pressure * station.Area * 2 * math.Sin(bodyAlpha) * math.Cos(bodyAlpha)})
			force = force.Add(Vec3{Z: -pressure * forebody * 2 * math.Sin(bodyBeta) * math.Cos(bodyBeta)})
		}
		total.Force = total.Force.Add(force)
		total.Moment = total.Moment.Add(r.Cross(force))
	}
	total.Force = total.Force.Add(flow.Scale(pressure * m.State.Damage.Drag))
}

// sweep is cos² of an element's sweep: the share of the dynamic pressure
// its section sees, from the span axis's lean out of the lateral plane. One
// for an unswept element.
func sweep(e *Element) float64 {
	return math.Max(1-e.Axis.X*e.Axis.X, 0.05)
}

// unswept maps a section alpha back to the body alpha it came from, for the
// terms calibrated on the body angle.
func unswept(sectional float64, across float64) float64 {
	return math.Atan(math.Tan(clamp(sectional, -1.5, 1.5)) * math.Sqrt(across))
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
	case Symmetric: // all-moving: deflection IS incidence, full authority - pivoting about a lateral axis, the swept section sees it over cos of the sweep
		deflection := s.Fcs.Stabilator.Right
		if surface.Side < 0 {
			deflection = s.Fcs.Stabilator.Left
		}
		raw += deflection / math.Sqrt(sweep(e))
	case Differential:
		// The flaperon actuator ALREADY carries the PA droop (fcs.go slews it
		// toward droop±differential; Fcs.Flap is a readout only). Adding Flap
		// here doubled the droop camber — the whole PA envelope was tuned
		// around ~2x the displayed flap angle (the balloon when 45° was tried).
		deflection := 0.0
		if surface.Side < 0 {
			deflection += s.Fcs.Flaperon.Left
		} else {
			deflection += s.Fcs.Flaperon.Right
		}
		shift = Effectiveness(e.Flap) * deflection
	case Rudder:
		// Mirrored fin frames flip the meaning of a camber shift: without
		// the side sign the two rudders' side forces cancel exactly.
		deflection := s.Fcs.Rudder * -surface.Side
		if s.Gear.Wow && !m.Direct {
			// Rudder toe-in: on the canted fins both trailing edges inboard cancel
			// laterally and add vertically - tail downforce, nose-up for rotation. Pure
			// function of Wow, so replay needs no state.
			deflection -= m.Airframe.Control.Toe
		}
		shift = Effectiveness(e.Flap) * deflection
	}
	return raw + e.Incidence/math.Sqrt(sweep(e)), shift // the twist is set streamwise; the swept section sees it over cos of the sweep
}

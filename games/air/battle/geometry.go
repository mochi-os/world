// Mochi world: Battle hit geometry
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Parts are capsules built from the airframe's real element and station
// geometry — the same positions the aero loop flies — so a round that hits
// "the left outboard wing" damages exactly the elements whose lift then
// disappears. Built once per airframe and shared.

package battle

import (
	"math"
	"sort"

	"world/games/air/flight"
)

// Part kinds.
const (
	Structure = iota // a surface element
	Fuselage         // a body station
	Turbine          // an engine
	Cockpit          // the pilot
	Tank             // the fuel tank
	Gear             // a landing-gear leg (nose, left, right)
)

// Part is one capsule of hit geometry in the target's body frame.
type Part struct {
	Kind    int
	Index   int // flat element index (Structure), engine index (Turbine), station index (Fuselage)
	Surface int // owning surface (Structure), else -1
	A, B    flight.Vec3
	Radius  float64
	Root    bool // innermost element of its surface: actuator territory
	Wet     bool // inboard wing element: fuel inside
	Flapped bool // the element carries a control surface
}

// Parts builds the hit geometry for an airframe.
func Parts(a *flight.Airframe) []Part {
	var parts []Part
	base := 0
	for si := range a.Surfaces {
		s := &a.Surfaces[si]
		for ei := range s.Elements {
			e := &s.Elements[ei]
			length := 0.0
			if e.Chord > 0 {
				length = e.Area / e.Chord
			}
			half := e.Axis.Scale(length / 2)
			parts = append(parts, Part{
				Kind:    Structure,
				Index:   base + ei,
				Surface: si,
				A:       e.Position.Subtract(half),
				B:       e.Position.Add(half),
				Radius:  math.Max(0.35*e.Chord, 0.25),
				Root:    ei == 0,
				Wet:     s.Kind == flight.Wing && ei < 3,
				Flapped: e.Flap > 0,
			})
		}
		base += len(s.Elements)
	}
	for bi := range a.Body {
		b := &a.Body[bi]
		reach := 1.0
		if b.Area > 0 && b.Plan > 0 {
			reach = b.Plan / (2 * math.Sqrt(b.Area/math.Pi)) / 2
		}
		parts = append(parts, Part{
			Kind:    Fuselage,
			Index:   bi,
			Surface: -1,
			A:       b.Position.Subtract(flight.Vec3{X: reach}),
			B:       b.Position.Add(flight.Vec3{X: reach}),
			Radius:  math.Max(math.Sqrt(b.Area/math.Pi), 0.4),
		})
	}
	for gi := range a.Engines {
		parts = append(parts, Part{
			Kind:    Turbine,
			Index:   gi,
			Surface: -1,
			A:       a.Engines[gi].Position.Subtract(flight.Vec3{X: 1.2}),
			B:       a.Engines[gi].Position.Add(flight.Vec3{X: 1.2}),
			Radius:  0.45,
		})
	}
	parts = append(parts, Part{Kind: Cockpit, Index: 0, Surface: -1, A: a.Cockpit, B: a.Cockpit, Radius: 0.7})
	parts = append(parts, Part{Kind: Tank, Index: 0, Surface: -1,
		A: a.Tank.Subtract(flight.Vec3{X: 1.8}), B: a.Tank.Add(flight.Vec3{X: 1.8}), Radius: 0.8})
	for gi, leg := range [3]flight.Strut{a.Gear.Nose, a.Gear.Left, a.Gear.Right} {
		parts = append(parts, Part{Kind: Gear, Index: gi, Surface: -1,
			A: leg.Attach, B: leg.Attach.Add(flight.Vec3{Y: -1.2}), Radius: 0.3}) // the bay and the leg below it: hittable stowed too — doors are no armour
	}
	return parts
}

// pierce lists every part along the ray, nearest first — a 20 mm SAPHEI
// round does not stop at the first thing it meets (#144: dead-astern rounds
// parked in already-dead engines while the fuel and cockpit sat untouched
// behind them, and the stern kill only ever came from the one fire the
// victim's own fire drill could put out).
func pierce(parts []Part, origin flight.Vec3, direction flight.Vec3, reach float64) []int {
	type met struct {
		part  int
		along float64
	}
	found := []met{}
	for pi := range parts {
		if t := capsule(origin, direction, parts[pi].A, parts[pi].B, parts[pi].Radius, reach); t >= 0 {
			found = append(found, met{pi, t})
		}
	}
	sort.Slice(found, func(x, y int) bool { return found[x].along < found[y].along })
	order := make([]int, len(found))
	for n, f := range found {
		order[n] = f.part
	}
	return order
}

// trace finds the first part a ray hits: origin and direction in the
// target's BODY frame, direction unit length. Returns the part index and
// the distance along the ray, or (-1, 0) for a miss.
func trace(parts []Part, origin flight.Vec3, direction flight.Vec3, reach float64) (int, float64) {
	best, nearest := -1, reach
	for pi := range parts {
		p := &parts[pi]
		t := capsule(origin, direction, p.A, p.B, p.Radius, nearest)
		if t >= 0 && t < nearest {
			best, nearest = pi, t
		}
	}
	if best < 0 {
		return -1, 0
	}
	return best, nearest
}

// capsule intersects a ray with a capsule, returning the smallest positive
// distance along the ray within limit, or -1. Conservative and cheap: it
// samples the closest approach between the ray and the capsule's segment.
func capsule(origin flight.Vec3, direction flight.Vec3, a flight.Vec3, b flight.Vec3, radius float64, limit float64) float64 {
	// Ray: origin + direction·t. Segment: a + (b-a)·u, u in [0,1].
	d := b.Subtract(a)
	w := origin.Subtract(a)
	dd := d.Dot(d)
	rd := direction.Dot(d)
	wd := w.Dot(d)
	wr := w.Dot(direction)
	denominator := dd - rd*rd // |d|²·sin²(angle) — 0 when parallel
	t, u := 0.0, 0.0
	if denominator > 1e-9 {
		t = (rd*wd - dd*wr) / denominator
		u = (t*rd + wd) / dd
	} else {
		t = -wr // parallel: closest approach of origin to the line
		u = wd / math.Max(dd, 1e-9)
	}
	u = clamp(u, 0, 1)
	// Re-derive t against the clamped segment point for correctness at the caps.
	point := a.Add(d.Scale(u))
	t = point.Subtract(origin).Dot(direction)
	if t < 0 || t > limit {
		return -1
	}
	along := origin.Add(direction.Scale(t))
	if along.Subtract(point).Length() <= radius {
		return t
	}
	return -1
}

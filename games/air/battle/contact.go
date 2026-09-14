// Mochi world: Battle midair contact
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Two airframes meeting in the air: the same capsules a round is traced
// against, swept against each other over one tick, and a strike table of
// their own - a wingtip clip tears the outboard wing off, anything inboard of
// that or in the body is a break-up.

package battle

import (
	"math"
	"sort"
	"sync"

	"world/games/air/flight"
)

// Mover is an airframe's hit geometry posed in the world at the end of a tick.
type Mover struct {
	Parts    []Part
	Position flight.Vec3
	Attitude flight.Quat
	Velocity flight.Vec3
	Stores   uint64 // attached-station bitmask: an empty rail is empty air
}

// stride is the sweep's sample spacing along the relative motion: well under
// the sum of any two capsule radii, so a thin crossing cannot fall between two
// samples.
const stride = 0.4

// Extent is the radius of the sphere about the body origin that holds every
// part - the broad-phase gate for a pair is two extents plus the relative step
// over the tick, and anything further apart cannot have met. Cached per
// geometry: Parts is built once per airframe and shared.
func Extent(parts []Part) float64 {
	if len(parts) == 0 {
		return 0
	}
	key := &parts[0]
	if reach, found := extents.Load(key); found {
		return reach.(float64)
	}
	reach := 0.0
	for pi := range parts {
		p := &parts[pi]
		reach = math.Max(reach, math.Max(p.A.Length(), p.B.Length())+p.Radius)
	}
	extents.Store(key, reach)
	return reach
}

var extents sync.Map

// carried reports whether a part is there to be hit: everything but a store
// whose rail is empty.
func carried(p *Part, stores uint64) bool {
	return p.Kind != Ordnance || (p.Index >= 0 && p.Index < 64 && stores&(1<<uint(p.Index)) != 0)
}

// Contact sweeps b's capsules through a's over the tick just stepped - both
// poses are the end of the tick, so the sweep runs back along the relative
// motion - and returns the parts of each that met, a's indices then b's.
func Contact(a Mover, b Mover, dt float64, wrap float64) ([]int, []int) {
	// b in a's body frame at the end of the tick, and where it came from.
	offset := flight.Vec3{
		X: flight.Shortest(a.Position.X, b.Position.X, wrap),
		Y: b.Position.Y - a.Position.Y,
		Z: flight.Shortest(a.Position.Z, b.Position.Z, wrap),
	}
	origin := a.Attitude.Unrotate(offset)
	back := a.Attitude.Unrotate(a.Velocity.Subtract(b.Velocity).Scale(dt))
	samples := int(math.Ceil(back.Length()/stride)) + 1
	metA, metB := map[int]bool{}, map[int]bool{}
	for j := range b.Parts {
		pb := &b.Parts[j]
		if !carried(pb, b.Stores) {
			continue
		}
		ba := origin.Add(a.Attitude.Unrotate(b.Attitude.Rotate(pb.A)))
		bb := origin.Add(a.Attitude.Unrotate(b.Attitude.Rotate(pb.B)))
		for s := 0; s < samples; s++ {
			fraction := 0.0
			if samples > 1 {
				fraction = float64(s) / float64(samples-1)
			}
			shift := back.Scale(fraction)
			sa, sb := ba.Add(shift), bb.Add(shift)
			for i := range a.Parts {
				pa := &a.Parts[i]
				if !carried(pa, a.Stores) {
					continue
				}
				if segments(pa.A, pa.B, sa, sb) < pa.Radius+pb.Radius {
					metA[i], metB[j] = true, true
				}
			}
		}
	}
	return keys(metA), keys(metB)
}

func keys(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// segments is the distance between the segments p1-q1 and p2-q2.
func segments(p1, q1, p2, q2 flight.Vec3) float64 {
	const tiny = 1e-9
	d1, d2, r := q1.Subtract(p1), q2.Subtract(p2), p1.Subtract(p2)
	a, e, f := d1.Dot(d1), d2.Dot(d2), d2.Dot(r)
	var s, t float64
	switch {
	case a <= tiny && e <= tiny:
		return r.Length()
	case a <= tiny:
		t = clamp(f/e, 0, 1)
	default:
		c := d1.Dot(r)
		if e <= tiny {
			s = clamp(-c/a, 0, 1)
		} else {
			b := d1.Dot(d2)
			if denominator := a*e - b*b; denominator > tiny {
				s = clamp((b*f-c*e)/denominator, 0, 1)
			}
			t = (b*s + f) / e
			if t < 0 {
				t, s = 0, clamp(-c/a, 0, 1)
			} else if t > 1 {
				t, s = 1, clamp((b-c)/a, 0, 1)
			}
		}
	}
	return p1.Add(d1.Scale(s)).Subtract(p2.Add(d2.Scale(t))).Length()
}

// Ram applies a midair to the parts of one body that met the other: whether
// the airframe is lost outright, and the shed events for what tore. An
// outboard wing or tail element tears the outboard half of that surface off -
// the clip a jet flies home from; an inboard wing element, which is the spar
// and the wing tank, or anything in the body is a break-up; a gear leg folds;
// a store meets nothing that matters to the airframe carrying it.
func Ram(body *Body, met []int) (bool, []Event) {
	fatal := false
	var events []Event
	for _, pi := range met {
		p := &body.Parts[pi]
		switch p.Kind {
		case Structure:
			s := &body.Airframe.Surfaces[p.Surface]
			base := 0
			for si := 0; si < p.Surface; si++ {
				base += len(body.Airframe.Surfaces[si].Elements)
			}
			if s.Kind == flight.Wing && p.Index-base < len(s.Elements)/2 {
				fatal = true
				continue
			}
			if shed(body, p.Surface) {
				events = append(events, Event{Kind: "shed", Engine: -1, Surface: p.Surface})
			}
		case Gear:
			body.Damage.Gear[p.Index] = 1
		case Ordnance:
		default:
			fatal = true
		}
	}
	return fatal, events
}

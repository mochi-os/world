// Mochi world: Wake vortices
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Every lifting aircraft leaves a pair of counter-rotating vortices behind
// it: the wingtip and LEX vortices roll up a few spans aft into one pair,
// spaced π/4 of the span apart, whose strength is the lift they carry. The
// pair sinks away from the lift that made it, drifts with the air, holds its
// strength for a while and then decays. A jet that flies into it feels the
// air the pair induces at each of its own elements: a jolt crossing it, a
// roll kick along one vortex, a sink between the two and a little lift just
// outboard of either - and, at the end of a hard 360, its own.
//
// Wakes keeps each aircraft's recent track; Near lays the pair from it, aged
// to the moment asked for, and returns the pieces close enough to matter. The
// host hands them to Model.Wake before each step. The model's own freshest
// wake is left out: the aerodynamics already carries it as the downwash.
// Every mark and piece is in the world frame, Home, wherever the host works.

package flight

import (
	"math"
)

const (
	wake_interval = 0.2   // s between the marks a trail is laid from
	wake_life     = 60.0  // s: the longest any wake lives
	wake_fresh    = 2.0   // s of its own wake an aircraft never meets: that part is its own downwash
	wake_reach    = 5.0   // vortex spacings beyond which a piece induces nothing; it fades from three
	wake_pieces   = 24    // the most pieces one model is handed, nearest first
	wake_gap      = 150.0 // m beyond the distance flown between two marks that breaks a trail (a respawn, a reset)
	wake_core     = 0.08  // the core radius a pair starts with, as a fraction of its spacing
)

// Vortex is one straight piece of a trailing vortex, from A to B: its
// circulation in m²/s, right-handed about A to B; its core radius, m; and its
// reach, m, beyond which it induces nothing.
type Vortex struct {
	A, B        Vec3
	Circulation float64
	Core        float64
	Reach       float64
}

// induced is the air velocity one piece induces at p: the Biot–Savart law
// for a straight segment, with a Burnham–Hallock core so the speed peaks at
// the core radius and falls to zero on the axis rather than going infinite,
// and a taper that brings it to zero at the reach.
func (v Vortex) induced(p Vec3) Vec3 {
	first, second := p.Subtract(v.A), p.Subtract(v.B) // A to p, B to p
	along := first.Subtract(second)                   // A to B
	length := along.Length()
	near, far := first.Length(), second.Length()
	if length < 1e-6 || near < 1e-9 || far < 1e-9 {
		return Vec3{}
	}
	cross := first.Cross(second)
	squared := cross.Dot(cross) // (h·|AB|)², h the distance from the line
	fade := 1 - smooth(0.6*v.Reach, v.Reach, math.Sqrt(squared)/length)
	if fade <= 0 {
		return Vec3{}
	}
	core := v.Core * length
	factor := v.Circulation / (4 * math.Pi) / (squared + core*core) * along.Dot(first.Scale(1/near).Subtract(second.Scale(1/far)))
	return cross.Scale(factor * fade)
}

// distance is how far p, in Home, lies from the piece's line: what its taper
// reads.
func (v Vortex) distance(p Vec3) float64 {
	first, second := p.Subtract(v.A), p.Subtract(v.B)
	length := first.Subtract(second).Length()
	if length < 1e-6 {
		return first.Length()
	}
	return first.Cross(second).Length() / length
}

// Swirl is the air the wake induced at the model's CG on its last step, in
// the environment's frame, m/s: zero in clean air.
func (m *Model) Swirl() Vec3 { return m.swirl }

// Induced is the air velocity the pieces induce at p, m/s: p and the result
// in the caller's frame, which place carries to Home, where the pieces are.
func Induced(p Vec3, vortices []Vortex, place Rigid) Vec3 {
	q := place.Point(p)
	var sum Vec3
	for _, v := range vortices {
		sum = sum.Add(v.induced(q))
	}
	return place.Inverse().Vector(sum)
}

// mark is one moment of an aircraft's track: where its two vortices were
// shed, which way they sink (away from the lift), the air they drift with,
// and the pair's circulation (signed: negative under a push) and spacing.
type mark struct {
	time        float64
	left, right Vec3
	sink        Vec3    // unit, opposite the lift
	air         Vec3    // the air's velocity where they were shed, m/s
	up          Vec3    // the level up where they were shed, Home: what the sea's floor is measured along
	height      float64 // the altitude they were shed at, m
	circulation float64
	spacing     float64
	broken      bool // the track jumped here (a respawn or reset): no piece joins it to the mark before
}

// trail is one aircraft's marks, oldest first, and what every aircraft asking
// at one moment shares of them: the marks aged to that moment, and the trail's
// length. Neither depends on who asks, and a 99-jet furball worked both for
// every jet in every tick, a quarter of its tick, until they were kept here,
// each keyed on everything it reads so a cached value is exactly the one a
// fresh computation would give.
type trail struct {
	marks   []mark
	version uint64 // counts the changes to marks

	pairs                   []pair  // the marks aged to moment over sea at turbulence
	pieces                  []piece // the vortex pieces between them, two to a pair of marks
	runs                    []run   // runs of those pieces, each in one sphere
	moment, sea, turbulence float64 // what they were worked for
	stood                   uint64  // one past the version they were worked for (0: none yet)

	length   float64 // the trail's length
	measured uint64  // one past the version length was measured for (0: none yet)
}

// pair is a mark's two vortices as aged() leaves them at one moment.
type pair struct {
	left, right       Vec3
	circulation, core float64
}

// piece is one vortex piece between two marks at one moment, with what Near
// tests of it: its middle, and its bound, beyond which from the middle it
// induces nothing (its reach plus half its length).
type piece struct {
	vortex Vortex
	middle Vec3
	bound  float64
	time   float64 // the later mark's: an aircraft never meets its own wake younger than wake_fresh
	skip   bool    // the later mark broke the trail, or either end has decayed to nothing
}

// run is a stretch of a trail's pieces, pieces[first:last], whose middles all lie
// within radius of centre, and whose largest bound is bound. An aircraft further
// than radius plus bound from centre is beyond every piece in it, so Near skips
// the run without looking at a piece: on a torus the minimum-image distance is
// a true metric, so the triangle inequality the test rests on holds across the
// wrap. A 99-jet furball tested every piece of every trail for every jet.
type run struct {
	centre        Vec3
	radius, bound float64
	first, last   int
}

const wake_run = 32 // pieces to a run: sixteen pairs of marks, 3.2 s of a trail

// Wakes keeps each aircraft's recent track. The zero value is ready to use.
type Wakes struct {
	trails map[int]*trail
	owners []int // the trails' aircraft, ascending: walked in this order, never the map's, so every host sums the same pieces in the same order
	// Turbulence, σ m/s, shortens every wake's life: the eddies tear the pair apart.
	Turbulence float64
}

// Record lays a mark for aircraft id from its model, no more often than every
// wake_interval seconds of time (the host's clock, shared by every aircraft).
func (w *Wakes) Record(id int, time float64, m *Model) {
	s := &m.State
	if n := w.latest(id); n != nil && time >= n.time && time-n.time < wake_interval {
		return // the conversions below are for a mark that will be laid
	}
	lift := s.Fcs.Normal * m.Mass() * m.Gravity
	home := m.Environment.Placement()
	density := air(m.Environment.Theatre().Altitude(s.Position), m.Environment).Density
	w.Shed(id, time, home.Point(s.Position), home.Attitude(s.Attitude), home.Vector(s.Velocity), lift, m.Airframe.Reference.Span, density, home.Vector(m.gust.Subtract(m.swirl)))
}

// latest is aircraft id's newest mark, nil before its first.
func (w *Wakes) latest(id int) *mark {
	if t := w.trails[id]; t != nil && len(t.marks) > 0 {
		return &t.marks[len(t.marks)-1]
	}
	return nil
}

// Shed lays a mark from the explicit state of an aircraft the host knows only
// by its pose, in Home: position, attitude, velocity over the ground, the lift
// it carries (N, negative under a push), its span (m), the air's density there
// and the air's own velocity.
func (w *Wakes) Shed(id int, time float64, position Vec3, attitude Quat, velocity Vec3, lift float64, span float64, density float64, air Vec3) {
	if w.trails == nil {
		w.trails = map[int]*trail{}
	}
	t := w.trails[id]
	if t == nil {
		t = &trail{}
		w.trails[id] = t
		at := 0
		for at < len(w.owners) && w.owners[at] < id {
			at++
		}
		w.owners = append(w.owners, 0)
		copy(w.owners[at+1:], w.owners[at:])
		w.owners[at] = id
	}
	if n := len(t.marks); n > 0 && time >= t.marks[n-1].time && time-t.marks[n-1].time < wake_interval {
		return
	}
	speed := velocity.Subtract(air).Length()
	spacing := math.Pi / 4 * span
	circulation := 0.0
	if speed > 30 && density > 0 && spacing > 0 {
		circulation = lift / (density * speed * spacing)
	}
	up := attitude.Rotate(Vec3{Y: 1})
	sink := up.Scale(-1)
	if circulation < 0 {
		sink = up // a pushed jet's pair moves away from its lift, which points down
	}
	half := attitude.Rotate(Vec3{Z: 1}).Scale(spacing / 2)
	place := Home.Place(position)
	next := mark{time: time, left: position.Subtract(half), right: position.Add(half), sink: sink, air: air,
		up: Home.Normal(place), height: place.Altitude, circulation: circulation, spacing: spacing}
	if n := len(t.marks); n > 0 {
		prior := t.marks[n-1]
		middle := prior.left.Add(prior.right).Scale(0.5)
		if time < prior.time || position.Subtract(middle).Length() > velocity.Length()*(time-prior.time)+wake_gap {
			next.broken = true
		}
	}
	drop := 0 // what has outlived any wake
	for drop < len(t.marks) && time-t.marks[drop].time > wake_life {
		drop++
	}
	t.marks = append(t.marks[drop:], next)
	t.version++
}

// Forget drops an aircraft's trail: it has left the match.
// Trails is how many jets have a wake laid.
func (w *Wakes) Trails() int {
	return len(w.owners)
}

func (w *Wakes) Forget(id int) {
	delete(w.trails, id)
	for k, owner := range w.owners {
		if owner == id {
			w.owners = append(w.owners[:k], w.owners[k+1:]...)
			break
		}
	}
}

// descent is how fast a pair sinks under its own induction, m/s.
func descent(circulation float64, spacing float64) float64 {
	return math.Abs(circulation) / (2 * math.Pi * spacing)
}

// life is how long a pair of this strength and spacing lasts: six of its
// characteristic times (the time it takes to sink one spacing), shortened by
// turbulence, and no less than eight seconds nor more than a minute.
func (w *Wakes) life(circulation float64, spacing float64) float64 {
	sinking := descent(circulation, spacing)
	if sinking < 1e-3 {
		return 0
	}
	return clamp(6*(spacing/sinking)/(1+w.Turbulence/math.Max(sinking, 0.1)), 8, wake_life)
}

// aged is a mark's pair as it stands at time: sunk away from its lift at the
// rate its own strength drives, never below half a spacing over the sea;
// drifted with the air; its circulation held for two-fifths of its life, then
// decayed to nothing at the end of it; and its core grown as it diffuses.
func (w *Wakes) aged(k mark, time float64, sea float64) (left Vec3, right Vec3, circulation float64, core float64) {
	age := time - k.time
	life := w.life(k.circulation, k.spacing)
	if age < 0 || age >= life {
		return k.left, k.right, 0, 0
	}
	sinking := descent(k.circulation, k.spacing)
	shift := k.sink.Scale(sinking * age).Add(k.air.Scale(age))
	left, right = k.left.Add(shift), k.right.Add(shift)
	// The pair levels off over the sea (a pair shed lower stays where it was
	// shed): heights along the level up where it was shed, which holds over
	// the kilometre or two any pair drifts.
	floor := sea + k.spacing/2
	middle := k.left.Add(k.right).Scale(0.5)
	level := func(at Vec3, start Vec3) Vec3 {
		was := k.height + start.Subtract(middle).Dot(k.up)
		now := was + at.Subtract(start).Dot(k.up)
		if least := math.Min(was, floor); now < least {
			at = at.Add(k.up.Scale(least - now))
		}
		return at
	}
	left, right = level(left, k.left), level(right, k.right)
	circulation = k.circulation
	if hold := 0.4 * life; age > hold {
		circulation *= 1 - (age-hold)/(life-hold)
	}
	core = wake_core * k.spacing * math.Sqrt(1+age*sinking/k.spacing)
	return left, right, circulation, core
}

// Near is the wake aircraft id meets at position and time: every piece of
// every trail within reach, aged to time, nearest first and no more than
// wake_pieces of them. Its own wake counts once it is wake_fresh seconds old.
// position is in Home, and sea the height the pairs level off above.
func (w *Wakes) Near(id int, position Vec3, time float64, sea float64) []Vortex {
	type candidate struct {
		vortex   Vortex
		distance float64
	}
	var found []candidate // nearest first, at most wake_pieces: an insertion list, stable, so equal distances keep the owners' order
	keep := func(c candidate) {
		at := len(found)
		for at > 0 && found[at-1].distance > c.distance {
			at--
		}
		if at >= wake_pieces {
			return
		}
		if len(found) < wake_pieces {
			found = append(found, candidate{})
		}
		copy(found[at+1:], found[at:len(found)-1])
		found[at] = c
	}
	for _, owner := range w.owners {
		t := w.trails[owner]
		n := len(t.marks)
		if n < 2 {
			continue
		}
		// A trail wholly out of reach is skipped on its ends: every piece lies
		// within half the track's length of the nearer end, and the margin
		// covers the drift and the sink since the oldest mark.
		newest, oldest := t.marks[n-1], t.marks[0]
		margin := wake_reach*newest.spacing + (newest.air.Length()+30)*(time-oldest.time) + t.path()/2
		if distance(position, newest.left) > margin && distance(position, oldest.left) > margin {
			continue
		}
		pieces, runs := w.stand(t, time, sea)
		for _, b := range runs {
			if distance(position, b.centre)-b.radius > b.bound+1 { // a metre over: rounding can never skip a piece in reach
				continue
			}
			for k := b.first; k < b.last; k++ {
				p := &pieces[k]
				if p.skip || (owner == id && time-p.time < wake_fresh) {
					continue
				}
				d := distance(position, p.middle)
				if d > p.bound {
					continue
				}
				keep(candidate{p.vortex, d})
			}
		}
	}
	out := make([]Vortex, len(found))
	for k := range found {
		out[k] = found[k].vortex
	}
	return out
}

// stand is a trail's pieces at time over sea, and its runs: worked by the
// first aircraft to ask at that moment and shared by the rest.
func (w *Wakes) stand(t *trail, time float64, sea float64) ([]piece, []run) {
	if t.stood == t.version+1 && t.moment == time && t.sea == sea && t.turbulence == w.Turbulence {
		return t.pieces, t.runs
	}
	t.pairs = t.pairs[:0]
	for k := range t.marks {
		left, right, circulation, core := w.aged(t.marks[k], time, sea)
		t.pairs = append(t.pairs, pair{left: left, right: right, circulation: circulation, core: core})
	}
	t.pieces = t.pieces[:0]
	for k := 1; k < len(t.marks); k++ {
		earlier, later := &t.marks[k-1], &t.marks[k]
		p0, p1 := &t.pairs[k-1], &t.pairs[k]
		skip := later.broken || p0.circulation == 0 || p1.circulation == 0
		strength, core := (p0.circulation+p1.circulation)/2, (p0.core+p1.core)/2
		reach := wake_reach * (earlier.spacing + later.spacing) / 2
		// The left vortex turns with the lift's circulation along the track,
		// the right against it: downwash between them, upwash outboard.
		for _, v := range []Vortex{{A: p0.left, B: p1.left, Circulation: strength, Core: core, Reach: reach}, {A: p0.right, B: p1.right, Circulation: -strength, Core: core, Reach: reach}} {
			t.pieces = append(t.pieces, piece{vortex: v, middle: v.A.Add(v.B).Scale(0.5), bound: reach + v.B.Subtract(v.A).Length()/2, time: later.time, skip: skip})
		}
	}
	t.runs = t.runs[:0]
	for first := 0; first < len(t.pieces); first += wake_run {
		last := min(first+wake_run, len(t.pieces))
		b := run{centre: t.pieces[first].middle, first: first, last: last}
		for k := first; k < last; k++ {
			b.radius = math.Max(b.radius, distance(b.centre, t.pieces[k].middle))
			b.bound = math.Max(b.bound, t.pieces[k].bound)
		}
		t.runs = append(t.runs, b)
	}
	t.stood, t.moment, t.sea, t.turbulence = t.version+1, time, sea, w.Turbulence
	return t.pieces, t.runs
}

// path is the length of a trail, oldest mark to newest, measured once a change.
func (t *trail) path() float64 {
	if t.measured == t.version+1 {
		return t.length
	}
	sum := 0.0
	for k := 1; k < len(t.marks); k++ {
		sum += t.marks[k].left.Subtract(t.marks[k-1].left).Length()
	}
	t.length, t.measured = sum, t.version+1
	return sum
}

// distance is the distance between two points.
func distance(a Vec3, b Vec3) float64 {
	return b.Subtract(a).Length()
}

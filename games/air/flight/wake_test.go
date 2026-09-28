// Mochi world: Wake vortex tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"math"
	"testing"
)

// A Hornet's numbers: 16 t, 12.3 m across the tip rails, sea-level air.
const (
	hornet  = 16000.0
	span    = 12.3
	density = 1.225
)

// straight lays a level trail for aircraft id along +x at speed, one mark
// every wake_interval from time zero to until, at height y, at one g.
func straight(w *Wakes, id int, speed float64, y float64, until float64) {
	for time := 0.0; time <= until+1e-9; time += wake_interval {
		w.Shed(id, time, Vec3{X: speed * time, Y: y}, Quat{W: 1}, Vec3{X: speed}, hornet*gravity, span, density, Vec3{})
	}
}

// TestInducedLine: a long straight piece induces the speed of an infinite line
// vortex, Γ/2π·r/(r²+rc²) - peaking at the core, zero on the axis - turning
// about the piece's direction by the right-hand rule, and nothing past its
// reach.
func TestInducedLine(t *testing.T) {
	line := Vortex{A: Vec3{X: -5000}, B: Vec3{X: 5000}, Circulation: 200, Core: 1, Reach: 50}
	for _, r := range []float64{2, 10, 25} {
		want := 200 / (2 * math.Pi) * r / (r*r + 1)
		got := line.induced(Vec3{Z: r}, 0)
		if math.Abs(-got.Y-want) > 0.02*want+1e-6 || math.Abs(got.X)+math.Abs(got.Z) > 1e-6 {
			t.Errorf("r=%.0f m: induced %+.3f, want %.3f straight down (the right-hand rule about +x, on the +z side)", r, got, want)
		}
	}
	peak, at := 0.0, 0.0
	for r := 0.1; r < 5; r += 0.05 {
		if s := line.induced(Vec3{Z: r}, 0).Length(); s > peak {
			peak, at = s, r
		}
	}
	if math.Abs(at-1) > 0.1 {
		t.Errorf("the speed peaks at %.2f m, not the 1 m core", at)
	}
	if axis := line.induced(Vec3{}, 0).Length(); axis > 1e-9 {
		t.Errorf("%.3g m/s on the axis: the core must bring it to zero", axis)
	}
	if far := line.induced(Vec3{Z: 51}, 0).Length(); far != 0 {
		t.Errorf("%.3g m/s past the reach", far)
	}
	// Across the seam of a wrapped world, the short way round.
	wrapped := Vortex{A: Vec3{X: -5000, Z: 4995}, B: Vec3{X: 5000, Z: 4995}, Circulation: 200, Core: 1, Reach: 50}
	if got := wrapped.induced(Vec3{Z: -4995}, 10000); math.Abs(got.Y+200/(2*math.Pi)*10/101) > 0.1 {
		t.Errorf("across the seam: %+.3f", got)
	}
}

// TestPair: a level jet's wake, a few seconds old, is a downwash between the
// tips and an upwash outboard, of the strength its lift sets.
func TestPair(t *testing.T) {
	var w Wakes
	straight(&w, 1, 150, 3000, 20)
	now := 20.0
	spacing := math.Pi / 4 * span
	circulation := hornet * gravity / (density * 150 * spacing)
	sunk := circulation / (2 * math.Pi * spacing) * 10 // the pair ten seconds back has sunk this far
	behind := Vec3{X: 150 * 10, Y: 3000 - sunk}
	pieces := w.Near(2, behind, now, 0, 0)
	if len(pieces) == 0 {
		t.Fatal("no wake ten seconds behind a level jet")
	}
	if centre := Induced(behind, pieces, 0); centre.Y > -0.5*circulation/(math.Pi*spacing) {
		t.Errorf("between the vortices: %+.2f m/s, want a downwash near Γ/πb (%.2f)", centre.Y, circulation/(math.Pi*spacing))
	}
	outboard := behind.Add(Vec3{Z: spacing})
	if up := Induced(outboard, w.Near(2, outboard, now, 0, 0), 0); up.Y <= 0.3 {
		t.Errorf("outboard of the right vortex: %+.2f m/s, want an upwash", up.Y)
	}
	if calm := Induced(behind.Add(Vec3{Y: 200}), w.Near(2, behind.Add(Vec3{Y: 200}), now, 0, 0), 0); calm.Length() > 1e-9 {
		t.Errorf("200 m above the wake: %.3g m/s", calm.Length())
	}
}

// TestSink: the pair sinks at its own induced speed, Γ/2πb, away from the lift
// that made it - down for a jet at one g, up for one pushing negative g - and
// levels off half a spacing over the sea.
func TestSink(t *testing.T) {
	spacing := math.Pi / 4 * span
	circulation := hornet * gravity / (density * 150 * spacing)
	rate := circulation / (2 * math.Pi * spacing)
	var w Wakes
	w.Shed(1, 0, Vec3{Y: 3000}, Quat{W: 1}, Vec3{X: 150}, hornet*gravity, span, density, Vec3{})
	w.Shed(1, 0.2, Vec3{X: 30, Y: 3000}, Quat{W: 1}, Vec3{X: 150}, hornet*gravity, span, density, Vec3{})
	left, _, g, _ := w.aged(w.trails[1].marks[0], 5, 0)
	if math.Abs((3000-left.Y)-rate*5) > 1e-6 || g <= 0 {
		t.Errorf("sank %.2f m in 5 s, want %.2f (Γ %.1f)", 3000-left.Y, rate*5, g)
	}
	var pushed Wakes
	pushed.Shed(1, 0, Vec3{Y: 3000}, Quat{W: 1}, Vec3{X: 150}, -hornet*gravity, span, density, Vec3{})
	up, _, g, _ := pushed.aged(pushed.trails[1].marks[0], 5, 0)
	if up.Y <= 3000 || g >= 0 {
		t.Errorf("a pushed jet's pair: height %.1f, circulation %.1f - it must rise, turning the other way", up.Y, g)
	}
	var low Wakes
	low.Shed(1, 0, Vec3{Y: 20}, Quat{W: 1}, Vec3{X: 70}, hornet*gravity, span, density, Vec3{})
	floor, _, _, _ := low.aged(low.trails[1].marks[0], 15, 0) // 3 m/s for 15 s would take it 45 m under the sea
	if math.Abs(floor.Y-spacing/2) > 1e-6 {
		t.Errorf("over the sea the pair came to %.2f m, not half a spacing (%.2f)", floor.Y, spacing/2)
	}
}

// TestDecay: the pair keeps its strength for two-fifths of its life, then
// fades to nothing; turbulence shortens the life; the air carries it.
func TestDecay(t *testing.T) {
	var w Wakes
	w.Shed(1, 0, Vec3{Y: 3000}, Quat{W: 1}, Vec3{X: 150}, hornet*gravity, span, density, Vec3{X: 5})
	k := w.trails[1].marks[0]
	life := w.life(k.circulation, k.spacing)
	if life < 8 || life > wake_life {
		t.Fatalf("life %.1f s", life)
	}
	_, _, early, _ := w.aged(k, 0.3*life, 0)
	_, _, late, _ := w.aged(k, 0.8*life, 0)
	_, _, gone, _ := w.aged(k, life, 0)
	if early != k.circulation || late <= 0 || late >= k.circulation || gone != 0 {
		t.Errorf("circulation %.1f → %.1f → %.1f (from %.1f): held, fading, gone", early, late, gone, k.circulation)
	}
	drifted, _, _, _ := w.aged(k, 10, 0)
	if math.Abs(drifted.X-(k.left.X+50)) > 1e-6 {
		t.Errorf("drifted %.1f m with a 5 m/s wind over 10 s", drifted.X-k.left.X)
	}
	rough := Wakes{Turbulence: 3}
	if shorter := rough.life(k.circulation, k.spacing); shorter >= life {
		t.Errorf("turbulence left the life at %.1f s (calm %.1f)", shorter, life)
	}
}

// TestFresh: an aircraft never meets the last two seconds of its own wake -
// that is its own downwash, which the aerodynamics already carries - but it
// meets the rest of it, and all of anyone else's.
func TestFresh(t *testing.T) {
	var w Wakes
	straight(&w, 1, 150, 3000, 10)
	behind := Vec3{X: 150 * 9.5, Y: 3000}
	if own := w.Near(1, behind, 10, 0, 0); len(own) != 0 {
		t.Errorf("met %d pieces of its own half-second-old wake", len(own))
	}
	if other := w.Near(2, behind, 10, 0, 0); len(other) == 0 {
		t.Error("another jet met none of that wake")
	}
	older := Vec3{X: 150 * 5, Y: 3000 - 3}
	if own := w.Near(1, older, 10, 0, 0); len(own) == 0 {
		t.Error("met none of its own five-second-old wake")
	}
}

// TestBreak: a trail that jumps - a respawn, a reset - lays no piece across
// the jump.
func TestBreak(t *testing.T) {
	var w Wakes
	straight(&w, 1, 150, 3000, 5)
	for time := 5.2; time <= 10; time += wake_interval {
		w.Shed(1, time, Vec3{X: 20000 + 150*(time-5.2), Y: 3000}, Quat{W: 1}, Vec3{X: 150}, hornet*gravity, span, density, Vec3{})
	}
	across := Vec3{X: 10000, Y: 2995}
	if pieces := w.Near(2, across, 10, 0, 0); len(pieces) != 0 {
		t.Errorf("%d pieces span the jump", len(pieces))
	}
	if pieces := w.Near(2, Vec3{X: 20300, Y: 2995}, 10, 0, 0); len(pieces) == 0 {
		t.Error("the trail after the jump laid nothing")
	}
}

// TestNearest: a jet deep in a long wake is handed the nearest pieces and no
// more than wake_pieces of them; a trail far away is never walked.
func TestNearest(t *testing.T) {
	w := dense()
	straight(&w, 3, 150, 90000, 40)
	at := Vec3{X: 60 * 34, Y: 2985}
	pieces := w.Near(2, at, 40, 0, 0)
	if len(pieces) != wake_pieces {
		t.Fatalf("%d pieces, want the cap of %d", len(pieces), wake_pieces)
	}
	for k := 1; k < len(pieces); k++ {
		if distance(at, pieces[k].A.Add(pieces[k].B).Scale(0.5), 0) < distance(at, pieces[k-1].A.Add(pieces[k-1].B).Scale(0.5), 0)-1e-9 {
			t.Fatal("not nearest first")
		}
	}
	for _, p := range pieces {
		if p.A.Y > 5000 {
			t.Fatal("handed a piece of the far trail")
		}
	}
}

// dense is two slow jets' trails side by side, laid for 40 s: far more pieces
// near their tracks than one model is handed.
func dense() Wakes {
	var w Wakes
	straight(&w, 1, 60, 3000, 40)
	straight(&w, 4, 60, 2980, 40)
	return w
}

// cruise holds a jet level at 150 m/s for seconds with the given wake pieces,
// and reports, step by step, its load factor and roll rate, and its final speed.
func cruise(pieces []Vortex, start Vec3, seconds float64) (loads []float64, rolls []float64, speed float64) {
	m := New(Fighter, Environment{}, World{Sea: -1000})
	m.State = Level(m, start, Vec3{X: 1}, 150, 3000)
	in := Inputs{Throttle: m.State.Engine[0].Spool}
	for i := 0; i < int(seconds*240); i++ {
		m.Wake = pieces
		m.Step(in)
		loads = append(loads, m.State.Fcs.Normal)
		rolls = append(rolls, m.State.Omega.X)
	}
	return loads, rolls, m.State.Velocity.Length()
}

// apart is the largest step-by-step difference between two runs.
func apart(a []float64, b []float64) float64 {
	most := 0.0
	for k := range a {
		most = math.Max(most, math.Abs(a[k]-b[k]))
	}
	return most
}

// TestCrossing: flown straight across a pair lying across its path, a jet is
// jolted up and down; the same jet in clean air flies steadily.
func TestCrossing(t *testing.T) {
	spacing := math.Pi / 4 * span
	circulation := hornet * gravity / (density * 150 * spacing)
	pair := []Vortex{
		{A: Vec3{X: 80, Z: 500}, B: Vec3{X: 80, Z: -500}, Circulation: circulation, Core: 0.8, Reach: 50},
		{A: Vec3{X: 80 + spacing, Z: 500}, B: Vec3{X: 80 + spacing, Z: -500}, Circulation: -circulation, Core: 0.8, Reach: 50},
	}
	for i := range pair {
		pair[i].A.Y, pair[i].B.Y = 3001.5, 3001.5
	}
	clean, _, _ := cruise(nil, Vec3{Y: 3000}, 1.2)
	crossed, _, _ := cruise(pair, Vec3{Y: 3000}, 1.2)
	jolt := apart(clean, crossed)
	t.Logf("crossing the pair moved the load factor up to %.2f g from the clean run's", jolt)
	// About three degrees of alpha for an instant at 150 m/s, where one g is a
	// lift coefficient of 0.3: half a g, for a wake of the jet's own size.
	if jolt < 0.3 {
		t.Errorf("crossing the pair moved the load factor only %.2f g", jolt)
	}
	if calm, _, _ := cruise(nil, Vec3{Y: 3000}, 1.2); apart(clean, calm) != 0 {
		t.Error("the clean run is not repeatable")
	}
}

// TestRolling: a vortex running along under one wing - up on one side of it,
// down on the other - rolls the jet; clean air does not.
func TestRolling(t *testing.T) {
	spacing := math.Pi / 4 * span
	circulation := hornet * gravity / (density * 150 * spacing)
	_, clean, _ := cruise(nil, Vec3{Y: 3000}, 1)
	// At the right tip: the whole right wing on one side of the vortex, the
	// left wing far from it. Under the middle of one wing its two sides
	// partly cancel.
	for _, at := range []float64{span / 2, 3} {
		along := []Vortex{{A: Vec3{X: -2000, Y: 3000, Z: at}, B: Vec3{X: 2000, Y: 3000, Z: at}, Circulation: circulation, Core: 0.8, Reach: 50}}
		_, rolled, _ := cruise(along, Vec3{Y: 3000}, 1)
		kick := apart(clean, rolled) * 180 / math.Pi
		t.Logf("a vortex along the right wing at %.1f m: roll rate up to %.1f°/s from the clean run's, the controls fighting it", at, kick)
		if kick < 3 {
			t.Errorf("a vortex along the right wing at %.1f m rolled the jet only %.1f°/s", at, kick)
		}
	}
}

// TestUpwash: flown just outboard of a leader's tip vortex, in its upwash, a
// jet makes the same lift for less drag and keeps more speed - the reason
// geese fly in a V.
func TestUpwash(t *testing.T) {
	spacing := math.Pi / 4 * span
	circulation := hornet * gravity / (density * 150 * spacing)
	// The leader's right vortex runs at the follower's left tip: all of the
	// follower's wing sits outboard of it, in its upwash.
	vortex := []Vortex{{A: Vec3{X: -3000, Y: 3000, Z: -span / 2}, B: Vec3{X: 3000, Y: 3000, Z: -span / 2}, Circulation: -circulation, Core: 0.8, Reach: 50}}
	_, _, clean := cruise(nil, Vec3{Y: 3000}, 6)
	_, _, riding := cruise(vortex, Vec3{Y: 3000}, 6)
	// The same vortex turning the other way puts the wing in its downwash.
	vortex[0].Circulation = -vortex[0].Circulation
	_, _, sinking := cruise(vortex, Vec3{Y: 3000}, 6)
	t.Logf("speed after 6 s: clean %.2f m/s, in the upwash %.2f, in the downwash %.2f", clean, riding, sinking)
	if riding <= clean+0.1 {
		t.Errorf("the upwash bought %.2f m/s over six seconds", riding-clean)
	}
	if sinking >= clean-0.1 {
		t.Errorf("the downwash cost %.2f m/s over six seconds", clean-sinking)
	}
}

// TestTurning: a banked jet's lift points into the turn, so its pair moves
// out of the circle, away from the lift. A perfectly flown level circle never
// meets its own wake; a jet that widens its circle flies into it.
func TestTurning(t *testing.T) {
	var w Wakes
	radius, speed := 500.0, 150.0
	period := 2 * math.Pi * radius / speed
	bank := math.Atan(speed * speed / (radius * gravity))
	lift := hornet * gravity / math.Cos(bank)
	at := func(time float64, r float64) Vec3 { // on a circle of radius r about (0, 3000, radius)
		angle := time / period * 2 * math.Pi
		return Vec3{X: r * math.Sin(angle), Y: 3000, Z: radius - r*math.Cos(angle)}
	}
	for time := 0.0; time < 8; time += wake_interval {
		angle := time / period * 2 * math.Pi
		heading := Vec3{X: math.Cos(angle), Z: math.Sin(angle)}
		attitude := Axis(heading, bank).Multiply(Look(heading)).Normalize()
		w.Shed(1, time, at(time, radius), attitude, heading.Scale(speed), lift, span, density, Vec3{})
	}
	// Five seconds on, the pair laid at the start has moved out of the circle
	// by its own sinking speed, very nearly level.
	k := w.trails[1].marks[0]
	sinking := descent(k.circulation, k.spacing)
	left, right, _, _ := w.aged(k, 5, 0)
	middle := left.Add(right).Scale(0.5)
	out := Vec3{X: middle.X, Z: middle.Z - radius}.Length() - radius
	if math.Abs(out-sinking*5*math.Sin(bank)) > 1 || math.Abs(3000-middle.Y-sinking*5*math.Cos(bank)) > 1 {
		t.Errorf("five seconds on the pair is %.1f m out of the circle and %.1f m down, want %.1f and %.1f", out, 3000-middle.Y, sinking*5*math.Sin(bank), sinking*5*math.Cos(bank))
	}
	on := at(0, radius)
	if pieces := w.Near(1, on, 5, 0, 0); len(pieces) != 0 && Induced(on, pieces, 0).Length() > 1 {
		t.Errorf("back on its own circle the jet met %.2f m/s of the wake that has moved out of it", Induced(on, pieces, 0).Length())
	}
	wider := at(0, radius+sinking*5*math.Sin(bank)).Subtract(Vec3{Y: sinking * 5 * math.Cos(bank)})
	pieces := w.Near(1, wider, 5, 0, 0)
	if swirl := Induced(wider, pieces, 0); len(pieces) == 0 || swirl.Length() < 3 {
		t.Errorf("on the wider circle, where its wake went, the jet met %d pieces inducing %.2f m/s", len(pieces), swirl.Length())
	}
}

// TestOrder: the pieces come out the same, in the same order, whatever order
// the trails were first laid in - every host sums the same air.
func TestOrder(t *testing.T) {
	forward, backward := dense(), Wakes{}
	straight(&backward, 4, 60, 2980, 40)
	straight(&backward, 1, 60, 3000, 40)
	at := Vec3{X: 60 * 34, Y: 2990}
	a, b := forward.Near(2, at, 40, 0, 0), backward.Near(2, at, 40, 0, 0)
	if len(a) != len(b) {
		t.Fatalf("%d pieces against %d", len(a), len(b))
	}
	for k := range a {
		if a[k] != b[k] {
			t.Fatalf("piece %d differs with the trails laid in another order", k)
		}
	}
	if Induced(at, a, 0) != Induced(at, b, 0) {
		t.Fatal("the induced air differs with the trails laid in another order")
	}
	w := dense()
	w.Forget(1)
	for _, p := range w.Near(2, at, 40, 0, 0) {
		if p.A.Y > 2995 {
			t.Fatal("a forgotten trail still lays pieces")
		}
	}
}

// TestPassOne: the first aerodynamic pass, which sizes each surface's lift for
// the downwash it throws on the tail, samples the wake exactly as the second
// does: a vortex under the left wing changes that wing's lift there and not
// the right's.
func TestPassOne(t *testing.T) {
	spacing := math.Pi / 4 * span
	circulation := hornet * gravity / (density * 150 * spacing)
	under := []Vortex{{A: Vec3{X: -2000, Y: 3000, Z: -span / 2}, B: Vec3{X: 2000, Y: 3000, Z: -span / 2}, Circulation: -circulation, Core: 0.8, Reach: 50}}
	lifted := func(pieces []Vortex) *Model {
		m := New(Fighter, Environment{}, World{Sea: -1000})
		m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 150, 3000)
		m.Wake = pieces
		m.Step(Inputs{Throttle: m.State.Engine[0].Spool})
		return m
	}
	clean, stirred := lifted(nil), lifted(under)
	left, right := -1, -1
	for si, surface := range Fighter.Surfaces {
		if surface.Kind == Wing && surface.Side < 0 && left < 0 {
			left = si
		}
		if surface.Kind == Wing && surface.Side > 0 && right < 0 {
			right = si
		}
	}
	if left < 0 || right < 0 {
		t.Fatal("no left and right wing on the airframe")
	}
	near := stirred.lift[left] - clean.lift[left]
	far := stirred.lift[right] - clean.lift[right]
	t.Logf("pass-one lift coefficient in the upwash: left wing %+.4f, right wing %+.4f", near, far)
	if near <= 0.01 || math.Abs(far) >= near {
		t.Errorf("an upwash under the left wing moved its pass-one lift %+.4f and the right's %+.4f", near, far)
	}
}

// TestRecord: a flying model's own mark carries the lift it is making, in the
// right sense.
func TestRecord(t *testing.T) {
	m := New(Fighter, Environment{}, World{Sea: -1000})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 150, 3000)
	m.Step(Inputs{Throttle: m.State.Engine[0].Spool})
	var w Wakes
	w.Record(1, 0, m)
	k := w.trails[1].marks[0]
	want := m.Mass() * gravity / (air(3000, Environment{}).Density * m.State.Velocity.Length() * math.Pi / 4 * Fighter.Reference.Span)
	if math.Abs(k.circulation-want) > 0.15*want {
		t.Errorf("circulation %.1f, want about %.1f at one g", k.circulation, want)
	}
}

// TestCull: leaving out the pieces that cannot reach the airframe changes
// nothing any element meets, and does leave them out.
func TestCull(t *testing.T) {
	m := New(Fighter, Environment{}, World{Sea: -1000})
	m.State = Level(m, Vec3{Y: 3000}, Vec3{X: 1}, 150, 3000)
	piece := func(aside float64) Vortex {
		return Vortex{A: Vec3{X: -30, Y: 3001, Z: aside}, B: Vec3{X: 30, Y: 3001, Z: aside}, Circulation: 120, Core: 0.8, Reach: 45}
	}
	// Two close aboard, then one every half metre from 40 m out to 60 m,
	// across the edge of what can reach the airframe, and one far off.
	m.Wake = []Vortex{piece(8), piece(-15), piece(-200)}
	for aside := 40.0; aside <= 60; aside += 0.5 {
		m.Wake = append(m.Wake, piece(aside))
	}
	m.Step(Inputs{Throttle: m.State.Engine[0].Spool})
	m.stir()
	if len(m.near) <= 3 || len(m.near) >= len(m.Wake)-1 {
		t.Fatalf("%d of the %d pieces kept for the elements, want the close ones and part of the sweep", len(m.near), len(m.Wake))
	}
	s := &m.State
	k := 0
	for si := range m.Airframe.Surfaces {
		for _, e := range m.Airframe.Surfaces[si].Elements {
			at := s.Position.Add(s.Attitude.Rotate(e.Position.Subtract(m.center)))
			want := s.Attitude.Unrotate(Induced(at, m.Wake, 0).Subtract(m.swirl))
			if got := m.swirls[k]; got.Subtract(want).Length() > 1e-12 {
				t.Fatalf("element %d of surface %d meets %v, want %v from every piece", k, si, got, want)
			}
			k++
		}
	}
	if m.swirls[0].Length() == 0 {
		t.Fatal("the kept pieces induce nothing: the check compares zeros")
	}
}

// TestWakeBudget: a jet deep in a wake steps at no more than two and a half
// times the cost of one in clean air, on whatever the core is built for: the
// server, or the browser's WebAssembly, where every step is several times
// slower. The two are timed in interleaved rounds and each round's quickest
// kept, so a busy machine does not decide it.
func TestWakeBudget(t *testing.T) {
	w := dense()
	pieces := w.Near(2, Vec3{X: 60 * 34, Y: 2985}, 40, 0, 0)
	if len(pieces) != wake_pieces {
		t.Fatalf("%d pieces, want the full %d", len(pieces), wake_pieces)
	}
	step := func(wake []Vortex) int64 {
		m := New(Fighter, Environment{Turbulence: 1}, World{})
		m.State = Level(m, Vec3{X: 60 * 34, Y: 2985}, Vec3{X: 1}, 150, 3000)
		in := Inputs{Throttle: 0.9}
		start := m.State
		return testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if i%240 == 0 {
					m.State = start
				}
				m.Wake = wake
				m.Step(in)
			}
		}).NsPerOp()
	}
	clean, deep := int64(math.MaxInt64), int64(math.MaxInt64)
	for round := 0; round < 3; round++ {
		clean = min(clean, step(nil))
		deep = min(deep, step(pieces))
	}
	t.Logf("a step in clean air: %d ns; in a full wake: %d ns", clean, deep)
	if float64(deep) > 2.5*float64(clean) {
		t.Fatalf("a step in a full wake costs %d ns, %.1f times one in clean air (%d ns)", deep, float64(deep)/float64(clean), clean)
	}
}

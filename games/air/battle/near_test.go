// Near is what a burst that MISSES leaves behind. Strike returns nothing
// whatever when a round goes past, so until this existed a burst that connected
// was fully described by its hit count and a burst that missed was silent —
// and a debrief could only reconstruct it from the recorded tracks, against the
// body ORIGIN rather than the structure. On a 17 m airframe seen end-on that is
// the wrong body: recording 01a0b090 put 237 rounds at 210-238 m into one hit
// while the reconstruction reported 3-5 m of aim error, which is not a
// contradiction, just a measurement of the wrong thing.
//
// Body axes throughout, as the airframe declares them (fa18c puts the gun at
// X 6.53 and the wing stations at Z ±5.94): X ahead along the nose, Y up,
// Z out along the span. The whole value of the miss VECTOR is that those
// labels are trustworthy, so they are asserted rather than assumed.
package battle

import (
	"math"
	"testing"

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

// step is one tick at the rate the hosts run the battle core, which is the
// window a single round is measured over.
const step = 1.0 / 120.0

// travel is how far a round travels in that window: a shot is started half a
// step short of the target so its closest approach falls inside the sweep.
var travel = Muzzle * step

// shot fires a round from offset along direction and asks Near what it saw.
// The target sits at the origin, wings level and still, so the body frame and
// the world frame agree and the arithmetic stays readable.
func shot(offset flight.Vec3, direction flight.Vec3) (float64, flight.Vec3, bool) {
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe)}
	round := &Round{Position: offset, Velocity: direction.Scale(Muzzle)}
	return Near(round, flight.Vec3{}, flight.Quat{W: 1}, flight.Vec3{}, body, step, 0)
}

// chase is a round closing from behind along the nose axis, which is the
// geometry a tail-chase burst actually flies, offset by height above the jet.
func chase(height float64) (float64, flight.Vec3, bool) {
	return shot(flight.Vec3{X: -travel / 2, Y: height}, flight.Vec3{X: 1})
}

func TestNearMeasuresTheGapFromTheSkin(t *testing.T) {
	gap, _, found := chase(20)
	if !found {
		t.Fatal("a round passing 20 m over the target was not measured at all")
	}
	// The skin is subtracted, so the gap must be under the raw 20 m offset and
	// must not have collapsed to nothing.
	if gap <= 0 || gap >= 20 {
		t.Fatalf("gap %.2f m is not a plausible skin distance for a 20 m offset", gap)
	}
}

func TestNearIsZeroWhenTheRoundIsInTheStructure(t *testing.T) {
	// Straight up the tailpipe. Strike owns this case in the live path, but
	// Near must not report a negative distance if it ever sees one.
	gap, _, found := chase(0)
	if !found {
		t.Fatal("a round through the airframe was not measured")
	}
	if gap != 0 {
		t.Fatalf("a round inside the structure reported %.3f m, want 0", gap)
	}
}

func TestNearOrdersMissesByHowCloseTheyCame(t *testing.T) {
	// The property the channel exists for: a tighter miss must read tighter.
	previous := -1.0
	for _, height := range []float64{6, 10, 15, 20, 30, 40} {
		gap, _, found := chase(height)
		if !found {
			t.Fatalf("a round passing %.0f m over the target was not measured", height)
		}
		if gap <= previous {
			t.Fatalf("a miss at %.0f m read %.2f m, no further out than the previous %.2f m", height, gap, previous)
		}
		previous = gap
	}
}

func TestNearPlacesTheMissInTheTargetBodyFrame(t *testing.T) {
	// A round passing over the target must report ABOVE it and one passing
	// under must report below. This is the half a bare distance cannot give a
	// pilot: which way to move the pipper.
	over, above, _ := chase(18)
	_, below, _ := chase(-18)
	if over <= 0 {
		t.Fatal("the reference shot did not miss")
	}
	if above.Y <= 0 {
		t.Fatalf("a round passing over the target reported Y %.2f, want positive", above.Y)
	}
	if below.Y >= 0 {
		t.Fatalf("a round passing under the target reported Y %.2f, want negative", below.Y)
	}
	// And a round out past a wingtip reads along the span, not as height.
	_, wide, found := shot(flight.Vec3{X: -travel / 2, Z: 18}, flight.Vec3{X: 1})
	if !found {
		t.Fatal("a round passing wide of the wingtip was not measured")
	}
	if math.Abs(wide.Z) <= math.Abs(wide.Y) {
		t.Fatalf("a miss out past the wing should read along the span: got y %.2f z %.2f", wide.Y, wide.Z)
	}
}

func TestNearDeclinesRoundsNowhereNear(t *testing.T) {
	// The broad phase. A round a kilometre off is not a near miss and must not
	// cost the narrow sweep, nor land a meaningless number in a recording.
	if _, _, found := chase(1000); found {
		t.Fatal("a round 1 km away was reported as a near miss")
	}
	// And the gate is where it says it is: well outside Graze, nothing.
	if _, _, found := chase(Graze * 3); found {
		t.Fatalf("a round %.0f m away passed a %.0f m gate", Graze*3, Graze)
	}
}

func TestNearIgnoresAStationaryRound(t *testing.T) {
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe)}
	round := &Round{Position: flight.Vec3{Y: 20}}
	if _, _, found := Near(round, flight.Vec3{}, flight.Quat{W: 1}, flight.Vec3{}, body, step, 0); found {
		t.Fatal("a round with no relative motion was measured")
	}
}

func TestNearFollowsTheTargetAttitude(t *testing.T) {
	// The miss vector is in the TARGET's frame, so rolling the target must move
	// the report even though nothing about the round changed. A round passing
	// over a wings-level jet passes out along the WING of one rolled ninety
	// degrees, and the body frame has to say so or the channel is lying about
	// which way to correct.
	body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe)}
	round := &Round{
		Position: flight.Vec3{X: -travel / 2, Y: 18},
		Velocity: flight.Vec3{X: 1}.Scale(Muzzle),
	}
	level := flight.Quat{W: 1}
	half := math.Pi / 4 // a quaternion carries half the angle: this is 90 degrees of roll
	rolled := flight.Quat{W: math.Cos(half), X: math.Sin(half)}

	_, flat, ok := Near(round, flight.Vec3{}, level, flight.Vec3{}, body, step, 0)
	if !ok {
		t.Fatal("the level reference shot was not measured")
	}
	_, banked, ok := Near(round, flight.Vec3{}, rolled, flight.Vec3{}, body, step, 0)
	if !ok {
		t.Fatal("the rolled shot was not measured")
	}
	if math.Abs(flat.Y) <= math.Abs(flat.Z) {
		t.Fatalf("over a level jet the miss should read as height: got y %.2f z %.2f", flat.Y, flat.Z)
	}
	if math.Abs(banked.Z) <= math.Abs(banked.Y) {
		t.Fatalf("over a rolled jet the same miss should read along the span: got y %.2f z %.2f", banked.Y, banked.Z)
	}
}

func TestNearAgreesWithStrikeAboutWhatCounts(t *testing.T) {
	// The two must not disagree about the same round: whatever Strike calls a
	// hit, Near must not be reporting metres of clearance for, and whatever
	// Strike passes over, Near must be willing to measure. Strike writes damage
	// when it connects, so this one needs a body that can take it.
	body, _ := target()
	hits, misses := 0, 0
	for height := 0.0; height <= 12; height += 0.5 {
		round := &Round{
			Position: flight.Vec3{X: -travel / 2, Y: height},
			Velocity: flight.Vec3{X: 1}.Scale(Muzzle),
		}
		copied := *round
		hit, _, _ := Strike(&copied, flight.Vec3{}, flight.Quat{W: 1}, flight.Vec3{}, body, step, 0, 1)
		gap, _, found := Near(round, flight.Vec3{}, flight.Quat{W: 1}, flight.Vec3{}, body, step, 0)
		if hit && found && gap > 1 {
			t.Fatalf("at %.1f m Strike called a hit while Near reported %.2f m of clearance", height, gap)
		}
		if !hit && !found {
			t.Fatalf("at %.1f m Strike passed and Near declined to measure", height)
		}
		if hit {
			hits++
		} else {
			misses++
		}
	}
	// The sweep has to straddle the skin or it agrees about nothing: all hits
	// or all misses would satisfy every assertion above without testing them.
	if hits == 0 || misses == 0 {
		t.Fatalf("the sweep never crossed the airframe edge: %d hits, %d misses", hits, misses)
	}
}

// The near-miss block sits at a fixed offset in fly's output, past the whole
// impact table, and the reader on the other side of the wasm boundary hardcodes
// that offset: GRAZED in apps/air/web/src/game/flight.ts is 3 + 3 * 8. Nothing
// links the two, so a change to ImpactPoints here would silently move the block
// under a reader still looking at slot 27 — and the failure is not a crash but
// a plausible wrong number in a debrief, which is the worst kind.
func TestImpactPointsMatchesTheRecorderMirror(t *testing.T) {
	if ImpactPoints != 8 {
		t.Fatalf("ImpactPoints is %d: update GRAZED in apps/air/web/src/game/flight.ts to 3+3*%d and this test with it", ImpactPoints, ImpactPoints)
	}
}

// A whole burst, at the geometry that prompted the channel. Recording 01a0b090
// put 83 rounds into the bandit's rear quarter at 210 m with 3 m of measured
// aim error and landed nothing, and the reconstruction could not say why. This
// is why: an aim error of a few metres from the body CENTRE is a miss of
// centimetres from the SKIN, and which side of that line a burst falls on is
// not something a reconstruction against the origin can see.
func TestNearExplainsABurstThatLandsNothing(t *testing.T) {
	// At altitude: Fly destroys a round that dips below the sea, and a burst
	// aimed downward from y=0 dies on its first step.
	const span, height = 210.0, 3000.0
	where := flight.Vec3{Y: height}

	// burst fires `rounds` from `span` behind the target, aimed `drop` metres
	// below its centre, and returns the hits and the closest miss recorded.
	burst := func(drop float64, rounds int) (int, float64, flight.Vec3) {
		body, _ := target()
		pose := Pose{
			Position: flight.Vec3{X: -span, Y: height},
			Forward:  flight.Vec3{X: 1, Y: -drop / span}.Normalize(),
			Up:       flight.Vec3{Y: 1},
			Right:    flight.Vec3{Z: 1},
		}
		flying := Volley(pose, rounds, 1, 0, 0)
		hits, best, at := 0, math.Inf(1), flight.Vec3{}
		for tick := 0; tick < 400 && len(flying) > 0; tick++ {
			alive := flying[:0]
			for r := range flying {
				round := &flying[r]
				if hit, _, _ := Strike(round, where, flight.Quat{W: 1}, flight.Vec3{}, body, step, 0, 1); hit {
					hits++
					continue
				}
				if gap, spot, near := Near(round, where, flight.Quat{W: 1}, flight.Vec3{}, body, step, 0); near && gap < best {
					best, at = gap, spot
				}
				if !Fly(round, step) {
					alive = append(alive, *round)
				}
			}
			flying = alive
		}
		return hits, best, at
	}

	// Aimed at him, the burst connects: the control, without which nothing
	// below says anything about aim.
	if hits, _, _ := burst(0, 40); hits == 0 {
		t.Fatal("a burst aimed straight at the target landed nothing")
	}

	// Eight metres low at 210 m. Nothing lands, and the channel says why: the
	// stream went UNDER him by about three metres. That is the sentence the
	// debrief could not write before.
	hits, gap, at := burst(8, 40)
	if hits != 0 {
		t.Fatalf("a burst aimed 8 m low still landed %d rounds", hits)
	}
	if math.IsInf(gap, 1) {
		t.Fatal("a burst that missed by metres was not measured at all")
	}
	// The gap is from the SKIN, so it reads well under the aim error: the
	// airframe fills about five metres of the difference. That relationship is
	// the whole reason a miss measured against the body ORIGIN misleads.
	if gap < 1 || gap > 6 {
		t.Fatalf("an 8 m aim error read as a %.2f m skin miss, which is neither the aim error nor close to the skin", gap)
	}
	if at.Y >= 0 {
		t.Fatalf("a burst aimed low reported above the target: y %.2f", at.Y)
	}

	// The cliff. Between aiming at him and aiming six metres low, a burst goes
	// from landing most of itself to landing none — and both sides of that
	// line sit inside the few metres of "aim error" a reconstruction reports.
	// This is the measurement that answers why 83 rounds at 210 m hit nothing.
	if hits, _, _ := burst(4, 40); hits == 0 {
		t.Fatal("a burst aimed 4 m low landed nothing: the hit band is narrower than measured")
	}
	if hits, _, _ := burst(6, 40); hits != 0 {
		t.Fatalf("a burst aimed 6 m low landed %d rounds: the hit band is wider than measured", hits)
	}

	// And it scales, so the number is a measurement and not a constant.
	if _, wide, _ := burst(30, 40); wide < 15 {
		t.Fatalf("a burst aimed 30 m low read only %.2f m from the skin", wide)
	}
}

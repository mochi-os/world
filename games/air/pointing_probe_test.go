package air

import (
	"fmt"
	"math"
	"os"
	"testing"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/flight"
)

// Does pointing beat holding energy?
//
// #42 carries the claim that the bot loses slow fights because it will not
// trade speed for nose authority. TestOneCirclePoint established that in this
// flight model slowing down costs BOTH rate and radius, so the trade cannot be
// justified by turn performance -- the only thing it buys is alpha, 41 deg at
// 150 kt against 27.7 deg at 450 kt, which points the nose off the flight
// path. This probe asks whether that alpha is worth anything.
//
// Two jets, one controller, one difference. Both fly pure pursuit at full
// throttle and burner, because pointing the nose at the opponent IS pure
// pursuit and the metric is nose-on-line-of-sight. The holder eases its pull
// once it drops below corner speed; the pointer never eases. Every other term
// is shared, so the comparison isolates the energy-discipline clause and
// nothing else.
//
// Run pointer v holder for the answer, and the two mirror matches as controls:
// if pointer beats pointer or holder beats holder by a similar margin, the
// result is seat bias rather than policy.
func aimed(s *flight.State, at flight.Vec3, hold bool, pace float64) flight.Inputs {
	want := at.Subtract(s.Position)
	if want.Length() < 1 {
		return flight.Inputs{Throttle: 1, Reheat: 1}
	}
	want = want.Normalize()
	// The floor guard, applied identically to both policies so it cannot bias
	// the comparison. Without it a pure-pursuit pair simply spirals into the
	// sea and every duration below is really a measure of how long it took
	// them to hit the water.
	floored := false
	if s.Position.Y < 3000 {
		lift := 0.8 * (3000 - s.Position.Y) / 3000 // harder the lower it gets
		if want.Y < lift {
			want = flight.Vec3{X: want.X, Y: lift, Z: want.Z}.Normalize()
			floored = true
		}
	}
	axis := s.Attitude.Rotate(flight.Vec3{X: 1})
	up := s.Attitude.Rotate(flight.Vec3{Y: 1})
	right := s.Attitude.Rotate(flight.Vec3{Z: 1})
	roll := 0.0
	if off := want.Subtract(axis.Scale(want.Dot(axis))); off.Length() > 1e-6 {
		off = off.Normalize()
		roll = clamp(math.Atan2(off.Dot(right), off.Dot(up))*2.5, -1, 1)
	}
	pitch := clamp(math.Acos(clamp(axis.Dot(want), -1, 1))*3, 0.05, 1)
	// The one difference: a jet holding energy unloads below corner rather
	// than dragging itself into the mush. The pointer accepts the bleed.
	// ... except against the ground. A pilot pulls to miss the sea whatever
	// his energy plan, and without this exemption the holder simply flies
	// into the water at about 45 s and every metric below is measured over a
	// shorter fight than its opponent's.
	if hold && !floored && s.Velocity.Length() < pace {
		pitch = math.Min(pitch, 0.3)
	}
	return flight.Inputs{Pitch: pitch, Roll: roll, Throttle: 1, Reheat: 1}
}

type seat struct {
	model   *flight.Model
	hold    bool
	first   float64 // seconds to the first gun solution, -1 never
	guns    int     // ticks holding a gun solution
	heater  int     // ticks at heater parameters: nose within 25 deg, 400-3000 m
	slowest float64
	floor   float64 // lowest altitude reached, ft
}

func joustPair(a, b *seat, split, closing float64, seconds int) (float64, float64) {
	build := func(at, facing flight.Vec3, speed float64) *flight.Model {
		m := flight.New(aircraft.Get("fa18c"), flight.Environment{Seed: 1, Wrap: 250000}, flight.World{Sea: sea})
		m.State.Position = at
		m.State.Velocity = facing.Normalize().Scale(speed)
		m.State.Attitude = flight.Look(facing.Normalize())
		m.State.Gear.Extension = 0
		m.Stores(0)
		m.State.Fuel = 2450
		m.State.Engine[0] = flight.EngineState{Spool: 1, Reheat: 1}
		m.State.Engine[1] = flight.EngineState{Spool: 1, Reheat: 1}
		return m
	}
	height := 25000 / 3.281
	a.model = build(flight.Vec3{Y: height}, flight.Vec3{X: 1}, closing)
	b.model = build(flight.Vec3{X: split, Y: height, Z: 400}, flight.Vec3{X: -1}, closing)
	a.first, b.first = -1, -1
	a.slowest, b.slowest = math.MaxFloat64, math.MaxFloat64
	a.floor, b.floor = math.MaxFloat64, math.MaxFloat64
	nearest := math.MaxFloat64
	flown := 0

	for tick := 0; tick < 240*seconds; tick++ {
		flown = tick
		for _, pair := range [][2]*seat{{a, b}, {b, a}} {
			me, you := pair[0], pair[1]
			me.model.Step(aimed(&me.model.State, you.model.State.Position, me.hold, corner(me.model)))
		}
		for _, pair := range [][2]*seat{{a, b}, {b, a}} {
			me, you := pair[0], pair[1]
			s := &me.model.State
			if v := s.Velocity.Length(); v < me.slowest {
				me.slowest = v
			}
			if h := s.Position.Y * 3.281; h < me.floor {
				me.floor = h
			}
			line := you.model.State.Position.Subtract(s.Position)
			span := line.Length()
			if span < nearest {
				nearest = span
			}
			if span < 1 {
				continue
			}
			axis := s.Attitude.Rotate(flight.Vec3{X: 1})
			off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
			// Heater parameters, the shot this fight is actually armed for:
			// inside the seeker cone AND at a range a 9M can be taken from.
			// Without the range bound a slow jet pointing at an opponent
			// departing five kilometres away scores, which is not a shot.
			if off < 25 && span > 400 && span < 3000 {
				me.heater++
			}
			// A gun solution: nose on inside 5 deg, at a range the cannon owns.
			if off < 5 && span > 250 && span < 900 {
				me.guns++
				if me.first < 0 {
					me.first = float64(tick) / 240
				}
			}
		}
		if a.model.State.Position.Y < sea+50 || b.model.State.Position.Y < sea+50 {
			break
		}
	}
	return nearest, float64(flown) / 240
}

func TestPointingVersusEnergy(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	label := func(hold bool) string {
		if hold {
			return "holder"
		}
		return "pointer"
	}
	for _, match := range [][2]bool{{false, true}, {false, false}, {true, true}} {
		fmt.Printf("\n=== %s (seat A) v %s (seat B) ===\n", label(match[0]), label(match[1]))
		fmt.Println("  entry       |  seat A: first   guns  heater slowest |  seat B: first   guns  heater slowest | closest flown")
		for _, entry := range []struct{ split, closing float64 }{
			{2400, 350 / 1.944}, {2400, 250 / 1.944}, {2400, 450 / 1.944},
			{1500, 350 / 1.944}, {3500, 350 / 1.944},
		} {
			a := &seat{hold: match[0]}
			b := &seat{hold: match[1]}
			nearest, flown := joustPair(a, b, entry.split, entry.closing, 90)
			show := func(s *seat, flown float64) string {
				first := "  never"
				if s.first >= 0 {
					first = fmt.Sprintf("%6.1fs", s.first)
				}
				span := math.Max(flown, 1)
				return fmt.Sprintf("%s %5.1f%% %5.1f%% %6.0f kt", first,
					100*float64(s.guns)/240/span, 100*float64(s.heater)/240/span, s.slowest*1.944)
			}
			fmt.Printf("  %4.0fm %3.0fkt | %s | %s | %5.0f m %5.0fs\n",
				entry.split, entry.closing*1.944, show(a, flown), show(b, flown), nearest, flown)
		}
	}
}

// The keyboard player, scripted: pure pursuit straight at the bandit, full aft
// stick whenever the nose is off, burner lit, and no energy discipline at all.
// This is what the 2026-09-06 recordings show a human doing on a keyboard --
// 33-35 deg of alpha at 130-190 kt -- and it is the opponent the higher tiers
// currently have no answer to.
type mush struct {
	armed  bool   // heaters carried and used
	rested uint64 // tick the last round left the rail
	fired  int
	flared int
}

// The keyboard player, scripted: pure pursuit straight at the bandit, full aft
// stick whenever the nose is off, burner lit, no energy discipline at all.
// This is what the 2026-09-06 recordings show a human doing on a keyboard --
// 33-35 deg of alpha at 130-190 kt -- and it is the opponent the higher tiers
// lose to. When armed it also SHOOTS, because a mush that holds fire measures
// only whether the bot can kill a punchbag; the fights being explained are
// ones the human won.
func (m *mush) fly(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	span := toward.Length()
	if span < 1 {
		return map[string]any{}
	}
	want := toward.Scale(1 / span)
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	forward := me.Attitude.Rotate(flight.Vec3{X: 1})
	roll := clamp(math.Asin(clamp(want.Dot(right), -1, 1))*2.2, -1, 1)
	if want.Dot(forward) < 0 {
		roll = clamp(math.Asin(clamp(want.Dot(right), -1, 1))*4, -1, 1)
	}
	pitch := clamp(math.Asin(clamp(want.Dot(up), -1, 1))*2.5+0.35, -1, 1)
	if me.Position.Y < 2500 { // the one concession: do not fly into the sea
		pitch = math.Max(pitch, 0.35)
	}
	data := map[string]any{"pitch": pitch, "roll": roll, "throttle": 1.0, "reheat": 1.0}
	if !m.armed {
		return data
	}
	// A defensive player pumping flares whenever the bandit is close enough to
	// shoot. This is the specific risk in removing the aspect precondition:
	// the doctrine it replaced existed to stop the disciplined tiers feeding
	// flares at the merge, so the fix has to survive an opponent that flares.
	if span < 3500 && tick%45 == 0 {
		data["flare"] = true
		m.flared++
	}
	off := math.Acos(clamp(want.Dot(forward), -1, 1)) * 180 / math.Pi
	// Guns whenever the pipper is near: a bad pilot holds the trigger.
	data["fire"] = off < 4 && span < 900
	// And a heater off the nose, pulsed because the input is edge-triggered,
	// with a two-second breath between rounds.
	if off < 12 && span > 600 && span < 2500 && tick-m.rested > 120 {
		data["missile"] = true
		m.rested = tick
		m.fired++
	}
	return data
}

// TestTierAgainstTheMush is the arm that decides what to do about the slow
// fight. TestPointingVersusEnergy compared two abstract policies; this puts the
// REAL tier brains, with their real catalogue and licences, against the real
// opponent, and asks how much shooting they get to do.
func TestTierAgainstTheMush(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	armed := os.Getenv("AIR_PASSIVE") == ""
	fmt.Printf("scripted human: armed=%v\n", armed)
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		heater, guns, ticks, launches, downed, lost, flares := 0, 0, 0, 0, 0, 0, 0
		slowest, plays := math.MaxFloat64, map[string]int{}
		for seed := uint64(1); seed <= 16; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("mush%s%d", level, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": true, "weapons": "fox2", "bots": map[string]any{level: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
				t.Fatal(err)
			}
			bot := -1
			for slot, a := range i.aircraft {
				if a != nil && a.brain != nil {
					bot = slot
				}
			}
			if bot < 0 {
				t.Fatal("no bot in the session")
			}
			// A merge: 2,400 m apart, nose to nose, co-speed.
			place(i, bot, 0, 2400)
			me := &i.aircraft[0].model.State
			me.Velocity = me.Velocity.Scale(-1)
			me.Attitude = flight.Look(me.Velocity.Normalize())
			pilot_ := &mush{armed: armed}
			started := i.aircraft[bot].brain.missiles // the BRAIN's magazine: craft.missiles is the human's

			for tick := uint64(0); tick < 90*60; tick++ {
				i.Step(tick, map[int][]game.Input{0: {{Data: pilot_.fly(me, &i.aircraft[bot].model.State, tick)}}})
				if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
					i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
					break
				}
				s := &i.aircraft[bot].model.State
				line := i.aircraft[0].model.State.Position.Subtract(s.Position)
				span := line.Length()
				ticks++
				if v := s.Velocity.Length(); v < slowest {
					slowest = v
				}
				if b := i.aircraft[bot].brain; b != nil && b.play != "" {
					plays[b.play]++
				}
				if span < 1 {
					continue
				}
				axis := s.Attitude.Rotate(flight.Vec3{X: 1})
				off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
				if off < 25 && span > 400 && span < 3000 {
					heater++
				}
				if off < 5 && span > 250 && span < 900 {
					guns++
				}
			}
			launches += started - i.aircraft[bot].brain.missiles
			flares += pilot_.flared
			if i.aircraft[0].model == nil || !i.aircraft[0].alive {
				downed++
			}
			if i.aircraft[bot].model == nil || !i.aircraft[bot].alive {
				lost++
			}
			i.Close()
		}
		top, best := "", 0
		for name, n := range plays {
			if n > best {
				top, best = name, n
			}
		}
		fmt.Printf("%-11s killed the human %2d/16 | died %2d/16 | launched %2d | mean fight %4.0f s | human flares %3d | slowest %3.0f kt | most-flown %s %.0f%%\n",
			level, downed, lost, launches, float64(ticks)/60/16, flares, slowest*1.944, top, 100*float64(best)/math.Max(float64(ticks), 1))
	}
}

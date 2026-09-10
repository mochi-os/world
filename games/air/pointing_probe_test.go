package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
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

func (m *mush) spent() (int, int) { return m.fired, m.flared }

// flyer is a scripted opponent: what it flies this tick, and what it has spent.
type flyer interface {
	fly(me, foe *flight.State, tick uint64) map[string]any
	spent() (fired, flared int)
}

// hornet flies the slow fight the 2026-09-09 recording shows the HOTAS player
// beating the ace with, which is what the mush is not: an alpha fight WITH
// energy management. Pull for the nose only when a shot is developing, a
// measured turn otherwise so the speed builds back, and below the floor the
// stick comes forward whatever the geometry until the band is bought back in
// burner. Always toward: nose-to-nose at the merge is the one-circle, and the
// slow fighter wants it. The recording is this instrument's specification -
// 49.9% of the fight above 20 deg alpha, mean 290 kt, slowest 157 kt, burner
// throughout - and the sweep prints the hornet's own signature beside the
// bot's, so a drift from that recording is seen rather than assumed away.
type hornet struct {
	armed     bool
	regaining bool // below the floor: unloaded until the band is back
	rested    uint64
	fired     int
	flared    int
}

func (h *hornet) spent() (int, int) { return h.fired, h.flared }

func (h *hornet) fly(me, foe *flight.State, tick uint64) map[string]any {
	toward := foe.Position.Subtract(me.Position)
	span := toward.Length()
	if span < 1 {
		return map[string]any{}
	}
	want := toward.Scale(1 / span)
	axis := me.Attitude.Rotate(flight.Vec3{X: 1})
	up := me.Attitude.Rotate(flight.Vec3{Y: 1})
	right := me.Attitude.Rotate(flight.Vec3{Z: 1})
	angle := math.Acos(clamp(want.Dot(axis), -1, 1)) * 180 / math.Pi
	speed := me.Velocity.Length()
	// BFM, not proportional pursuit: roll the lift vector onto him wherever
	// he is round the nose - atan2 covers the full circle, so a bandit below
	// the nose is a roll-and-pull, never a push - and pull in proportion to
	// how far off the nose he is. The mush's pitch law pushes when he drops
	// below the nose after a break, which is how it loses the turn.
	roll := 0.0
	if aside := want.Subtract(axis.Scale(want.Dot(axis))); aside.Length() > 1e-6 {
		aside = aside.Normalize()
		roll = clamp(math.Atan2(aside.Dot(right), aside.Dot(up))*2.5, -1, 1)
	}
	// The recording's pilot peaked at 6.0 g on a 7.5 g jet and modulated: the
	// everyday pull is three quarters of the stick, scheduled down with speed
	// because nobody holds 6 g at 200 kt - the jet departs (#97) - and the
	// whole stick is kept for the merge break below.
	limit := clamp((speed-60)/90, 0.15, 0.85) // 34 deg at 290 kt is what the recording shows, and the schedule has to reach it
	pitch := clamp(angle/180*math.Pi*3, 0.05, limit)
	// The merge. Pursuit on a nose-to-nose target is a straight line into his
	// guns, which is what the mush flies and why it dies at the pass; the
	// recording's pilot was turning from the first second. Break INTO him
	// now - lift vector across to his side, the whole pull - and let pursuit
	// carry the turn once he is off the nose. Toward is the one-circle, and
	// the slow fighter wants the one-circle.
	if closing := me.Velocity.Subtract(foe.Velocity).Dot(want); angle < 25 && span < 2600 && closing > 120 {
		side := want.Dot(right)
		if math.Abs(side) < 0.05 {
			side = 1 // dead ahead: any side, but pick one
		}
		roll = clamp(side*8, -1, 1)
		pitch = clamp((speed-70)/100, 0.1, 1) // the whole pull the speed allows: 344 kt at the merge gives the whole stick, a slow later pass does not
	}
	// The one thing the mush lacks, and the whole difference between the two
	// scripts: below the floor the stick comes forward whatever the geometry,
	// and the band is bought back in burner before the fight resumes. With
	// hysteresis so it does not flutter at the floor.
	const floor, band = 115.0, 170.0 // 224 kt and 330 kt: the recording's mean of 290 sits between them
	if speed < floor {
		h.regaining = true
	} else if speed > band {
		h.regaining = false
	}
	if h.regaining {
		// Unloaded to near 1 g, wings nearly still, and the NOSE TO THE HORIZON
		// or below: unloading nose-high after a pull decelerated the jet at 1 g
		// from 200 kt into an 80-degree tail-slide (the self-check's peak of
		// 82-153 degrees, three configurations running) - a pilot pushes the
		// nose down first and lets the burner do the work.
		pitch, roll = clamp(-axis.Y*1.5, -0.4, 0.1), roll*0.3
	}
	// Full roll stick while slow is crossed controls, and #97 built the
	// departure that follows: a pilot rolls with the speed he has - and not
	// at the limiter. A target crossing the nose plane from behind flips the
	// lift-vector roll every quarter second, and at 47 degrees alpha under
	// the whole pull that chatter departed the jet at 300 kt (seed 10 of the
	// heater arm, peak 103 degrees): above 30 degrees the roll is unloaded to
	// a nudge and the pull does the turning.
	roll *= clamp((speed-60)/100, 0.15, 1)
	{
		body := me.Attitude.Unrotate(me.Velocity) // the wind in body axes: X forward, Y up
		if body.X > 1 {
			alpha := math.Atan2(-body.Y, body.X) * 180 / math.Pi
			roll *= clamp((36-alpha)/8, 0.1, 1)
		}
	}
	if me.Position.Y < 2500 { // the one concession: do not fly into the sea - with the pull the speed allows
		pitch = math.Max(pitch, math.Min(0.35, limit))
	}
	data := map[string]any{"pitch": pitch, "roll": roll, "throttle": 1.0, "reheat": 1.0}
	if !h.armed {
		return data
	}
	if span < 3500 && tick%45 == 0 {
		data["flare"] = true
		h.flared++
	}
	data["fire"] = angle < 4 && span < 900
	if angle < 12 && span > 600 && span < 2500 && tick-h.rested > 120 {
		data["missile"] = true
		h.rested = tick
		h.fired++
	}
	return data
}

// bout is one tier's sweep against one scripted opponent, with the SAME
// measurements taken on both seats: what the bot got to do, and what the
// opponent actually flew - the second is how an instrument built from a
// recording is checked against it. Seat 0 is the bot, seat 1 the opponent.
type bout struct {
	seeds, ticks           int
	downed, lost, launches int
	flares                 int
	burner                 int        // ticks the bot spent in reheat: the throttle law that holds the fast match
	starving               int        // ticks the bot spent below its energy floor, recovering instead of fighting
	corner                 float64    // the armed jet's corner speed as the bot prices it, m/s
	guns, heater           [2]int     // ticks at gun / heater parameters
	high                   [2]int     // ticks above 20 deg alpha
	close, closeHigh       [2]int     // ticks engaged (inside 1,500 m), and of those above 20 deg: the pilot's own doctrine, with the other jet's extensions taken out
	peak                   [2]float64 // deg
	speed                  [2]float64 // summed m/s, for the mean
	slowest                [2]float64 // m/s
	rebuilds               int        // bot ticks rebuilding energy below its floor
	plays                  map[string]int
}

// sweep runs one tier against one scripted opponent across the seeds. mode is
// the session's: "furball" frees the weapons from the first tick, which is the
// arm #107 was measured on; "joust" HOLDS them until either jet crosses the
// other's 3/9 line, exactly as the live single-player joust does - and that
// hold is why a live ace's first shot comes after the pass, not nose-on at two
// seconds. An instrument standing in for a joust has to be a joust.
func sweep(t *testing.T, level, mode string, opponent func() flyer, missiles bool, entry float64, seeds, seconds int) bout {
	t.Helper()
	b := bout{plays: map[string]int{}, slowest: [2]float64{math.MaxFloat64, math.MaxFloat64}}
	for seed := uint64(1); seed <= uint64(seeds); seed++ {
		parameters := map[string]any{"missiles": missiles, "bots": map[string]any{level: 1.0}}
		if missiles {
			parameters["weapons"] = "fox2"
		}
		session := game.Session{Identifier: fmt.Sprintf("slow%s%d", level, seed), Game: "air", Mode: mode, Seed: seed, Parameters: parameters}
		if mode == "furball" {
			session.Capacity = 8
		}
		g := New()
		made, err := g.Create(session)
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
		if entry > 0 {
			me.Velocity = me.Velocity.Normalize().Scale(entry) // the recording's merge speed, not the bot's spawn speed
		}
		me.Attitude = flight.Look(me.Velocity.Normalize())
		pilot := opponent()
		started := i.aircraft[bot].brain.missiles // the BRAIN's magazine: craft.missiles is the human's
		b.seeds++
		traced := uint64(1)
		if s, err := strconv.ParseUint(os.Getenv("AIR_TRACE_SEED"), 10, 64); err == nil {
			traced = s
		}
		trace := os.Getenv("AIR_TRACE") != "" && seed == traced // one seed (AIR_TRACE_SEED, default 1), the opponent's seat, twice a second: what the script commanded and what the jet did
		peakHis := 0.0                                          // the opponent's peak alpha this seed: a scripted human that departs is the script's defect, and this says which seed to trace
		for tick := uint64(0); tick < uint64(seconds*60); tick++ {
			data := pilot.fly(me, &i.aircraft[bot].model.State, tick)
			if trace && tick%15 == 0 && (tick < 15*60 || i.aircraft[0].model.Alpha() > 0.8) {
				foe := &i.aircraft[bot].model.State
				line := foe.Position.Subtract(me.Position)
				off := math.Acos(clamp(me.Attitude.Rotate(flight.Vec3{X: 1}).Dot(line.Normalize()), -1, 1)) * 180 / math.Pi
				his := math.Acos(clamp(foe.Attitude.Rotate(flight.Vec3{X: 1}).Dot(line.Normalize().Scale(-1)), -1, 1)) * 180 / math.Pi
				fmt.Printf("    t=%5.2f range %5.0f | him: off %5.1f %3.0f kt alpha %5.1f g %4.1f pitch %5.2f roll %5.2f | bot: off %5.1f %3.0f kt alpha %4.1f %-7s msl %d\n",
					float64(tick)/60, line.Length(), off, me.Velocity.Length()*1.944, i.aircraft[0].model.Alpha()*180/math.Pi,
					i.aircraft[0].model.Nz(), data["pitch"], data["roll"],
					his, foe.Velocity.Length()*1.944, i.aircraft[bot].model.Alpha()*180/math.Pi, i.aircraft[bot].brain.play, i.aircraft[bot].brain.missiles)
			}
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if i.aircraft[0].model != nil {
				peakHis = math.Max(peakHis, i.aircraft[0].model.Alpha()*180/math.Pi)
			}
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			b.ticks++
			brain := i.aircraft[bot].brain
			if brain.reheat > 0.5 {
				b.burner++
			}
			if brain.starving {
				b.starving++
			}
			b.corner = corner(i.aircraft[bot].model)
			if brain.play != "" {
				b.plays[brain.play]++
			}
			if brain.mode == "rebuild" {
				b.rebuilds++
			}
			for seat, jet := range []*craft{i.aircraft[bot], i.aircraft[0]} {
				other := i.aircraft[0]
				if seat == 1 {
					other = i.aircraft[bot]
				}
				s := &jet.model.State
				v := s.Velocity.Length()
				b.speed[seat] += v
				if v < b.slowest[seat] {
					b.slowest[seat] = v
				}
				alpha := jet.model.Alpha() * 180 / math.Pi
				if alpha > b.peak[seat] {
					b.peak[seat] = alpha
				}
				if alpha > 20 {
					b.high[seat]++
				}
				line := other.model.State.Position.Subtract(s.Position)
				span := line.Length()
				if span < 1500 {
					b.close[seat]++
					if alpha > 20 {
						b.closeHigh[seat]++
					}
				}
				if span < 1 {
					continue
				}
				axis := s.Attitude.Rotate(flight.Vec3{X: 1})
				off := math.Acos(clamp(axis.Dot(line.Scale(1/span)), -1, 1)) * 180 / math.Pi
				if off < 25 && span > 400 && span < 3000 {
					b.heater[seat]++
				}
				if off < 5 && span > 250 && span < 900 {
					b.guns[seat]++
				}
			}
		}
		b.launches += started - i.aircraft[bot].brain.missiles
		_, flared := pilot.spent()
		b.flares += flared
		if i.aircraft[0].model == nil || !i.aircraft[0].alive {
			b.downed++
		}
		if i.aircraft[bot].model == nil || !i.aircraft[bot].alive {
			b.lost++
		}
		if os.Getenv("AIR_TRACE") != "" {
			fmt.Printf("    seed %d: the opponent's peak alpha %.1f deg\n", seed, peakHis)
		}
		i.Close()
	}
	return b
}

// report renders a bout as two lines: the bot's, then the opponent's.
func report(name, level string, b bout) string {
	top, best := "", 0
	for play, n := range b.plays {
		if n > best {
			top, best = play, n
		}
	}
	ticks := math.Max(float64(b.ticks), 1)
	share := func(n int) float64 { return 100 * float64(n) / ticks }
	mean := func(seat int) float64 { return b.speed[seat] / ticks * 1.944 }
	engaged := func(seat int) float64 { return 100 * float64(b.closeHigh[seat]) / math.Max(float64(b.close[seat]), 1) }
	return fmt.Sprintf("%-6s v %-10s killed %2d/%d died %2d/%d fight %4.0f s engaged %4.1f%% | bot  >20deg %5.1f%% (engaged %5.1f%%) peak %4.1f mean %3.0f kt slowest %3.0f | guns %4.1f%% heater %4.1f%% | rebuild %4.1f%% burner %4.1f%% starving %4.1f%% corner %3.0f kt top %s %.0f%% | launched %d\n"+
		"%-6s   %-10s                                                     | him  >20deg %5.1f%% (engaged %5.1f%%) peak %4.1f mean %3.0f kt slowest %3.0f | guns %4.1f%% heater %4.1f%% | flares %d",
		name, level, b.downed, b.seeds, b.lost, b.seeds, float64(b.ticks)/60/math.Max(float64(b.seeds), 1), share(b.close[0]),
		share(b.high[0]), engaged(0), b.peak[0], mean(0), b.slowest[0]*1.944, share(b.guns[0]), share(b.heater[0]), share(b.rebuilds), share(b.burner), share(b.starving), b.corner*1.944, top, share(best), b.launches,
		"", "", share(b.high[1]), engaged(1), b.peak[1], mean(1), b.slowest[1]*1.944, share(b.guns[1]), share(b.heater[1]), b.flares)
}

// TestPilotEngagesTheMush: the pilot tier has a way UP. With every vertical
// play at tier 3 or 4 its catalogue could go flat or down, and against a
// sinking slow target its one out-of-plane play - a burner dive to 300 m
// below him - was a dive that never ended: 528 kt mean and 120 seconds
// nobody won (#172). With the high yo-yo at tier 2 the same fight is flown
// at 378 kt under the pilot's cap of 1.0 (357 kt and a third of it above 20
// degrees alpha under the 1.5 the battery refused). The gate is the speed:
// the cap decides the alpha, and at 1.0 a pilot that no longer flees at 500
// kt is the whole of what the yo-yo buys.
func TestPilotEngagesTheMush(t *testing.T) {
	heavy(t)
	b := sweep(t, "pilot", "furball", func() flyer { return &mush{armed: true} }, false, 0, 16, 120)
	ticks := math.Max(float64(b.ticks), 1)
	mean := b.speed[0] / ticks * 1.944
	engaged := 100 * float64(b.closeHigh[0]) / math.Max(float64(b.close[0]), 1)
	t.Logf("pilot v mush, guns: mean %.0f kt, %.1f%% of the engaged fight above 20 deg alpha, killed %d died %d of %d", mean, engaged, b.downed, b.lost, b.seeds)
	if mean > 450 {
		t.Errorf("pilot mean %.0f kt against the mush: the fast match - it has no way up and dives in burner (tier-3 yo-yo: 528 kt)", mean)
	}
}

// TestTierAgainstTheMush is the arm that decided what to do about the slow
// fight in #107. TestPointingVersusEnergy compared two abstract policies; this
// puts the REAL tier brains, with their real catalogue and licences, against
// the keyboard player, and asks how much shooting they get to do.
func TestTierAgainstTheMush(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	armed := os.Getenv("AIR_PASSIVE") == ""
	fmt.Printf("scripted human: armed=%v\n", armed)
	for _, level := range []string{"pilot", "ace", "superhuman"} {
		b := sweep(t, level, "furball", func() flyer { return &mush{armed: armed} }, true, 0, 16, 90)
		top, best := "", 0
		for name, n := range b.plays {
			if n > best {
				top, best = name, n
			}
		}
		fmt.Printf("%-11s killed the human %2d/16 | died %2d/16 | launched %2d | mean fight %4.0f s | human flares %3d | slowest %3.0f kt | most-flown %s %.0f%%\n",
			level, b.downed, b.lost, b.launches, float64(b.ticks)/60/16, b.flares, b.slowest[0]*1.944, top, 100*float64(best)/math.Max(float64(b.ticks), 1))
	}
}

// TestTierAgainstTheHornet is #153's instrument. #107 measured the tiers
// against the mush and the ace won 13 of 16; the same ace then lost the live
// joust of 2026-09-09 to a player flying slow WITH energy management, a fight
// no probe flew. Both opponents run here side by side so the difference is
// one table, and the hornet's own line is checked against the recording it
// was built from: an instrument that does not fly the fight it claims to fly
// measures nothing. AIR_TIER narrows to one tier, AIR_WEAPONS=guns takes the
// missiles away (the 2026-09-09 human-v-human fight was guns only), and
// AIR_PASSIVE disarms the opponent.
func TestTierAgainstTheHornet(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	armed := os.Getenv("AIR_PASSIVE") == ""
	missiles := os.Getenv("AIR_WEAPONS") != "guns"
	tiers := []string{"novice", "pilot", "ace", "superhuman"}
	if only := os.Getenv("AIR_TIER"); only != "" {
		tiers = []string{only}
	}
	// Each arm carries the rules of the live fight it stands in for. Heaters:
	// the 2026-09-09 single-player joust-ace, a JOUST, weapons held until the
	// 3/9 crossing. Guns: the same day's human-v-human match on the standing
	// server FURBALL, guns only, weapons free from the first tick - the
	// recording calls it a joust only because the client stamps its forced
	// cfg.task into the file.
	mode := "furball"
	if missiles {
		mode = "joust"
	}
	fmt.Printf("scripted human: armed=%v missiles=%v | %s rules | 16 seeds, 120 s, merge at 2,400 m\n", armed, missiles, mode)
	for _, level := range tiers {
		for _, opponent := range []struct {
			name  string
			entry float64 // merge speed, m/s; 0 = co-speed with the bot as placed
			make  func() flyer
		}{
			{"mush", 0, func() flyer { return &mush{armed: armed} }},
			{"hornet", 344 / 1.944, func() flyer { return &hornet{armed: armed} }}, // the recording's 344 kt at the merge
		} {
			b := sweep(t, level, mode, opponent.make, missiles, opponent.entry, 16, 120)
			fmt.Println(report(opponent.name, level, b))
			if level == "ace" && opponent.name == "hornet" && missiles {
				// Only the arm the recording was flown on: guns-only fights run
				// twice as long and the same script rightly regains more.
				// The instrument's own check, against the recording it stands in for.
				// Judged while ENGAGED, inside 1,500 m: the recording was a
				// continuous close fight, and a harness fight the bot spends
				// extending away from dilutes a whole-fight share with time the
				// pilot has nothing to pull at.
				high := 100 * float64(b.closeHigh[1]) / math.Max(float64(b.close[1]), 1)
				mean := b.speed[1] / math.Max(float64(b.ticks), 1) * 1.944
				if high < 35 || high > 65 || mean < 240 || mean > 340 || b.peak[1] > 50 {
					t.Errorf("the hornet flew %.1f%% above 20 deg while engaged (peak %.1f) at a mean of %.0f kt; the recording it stands in for flew 49.9%% (peak 34.2) at 290 kt - recalibrate the script before reading the bot's line", high, b.peak[1], mean)
				}
			}
		}
	}
}

// TestHornetProbe traces ONE seed of a tier against the hornet: what it chose
// at every re-plan, how every candidate scored, and where `high` - the high
// yo-yo Chris won the human-v-human match with, nine times over - ranked. The
// question it answers is whether that play is rehearsed and loses narrowly
// (the horizon, #169), loses by a mile (the scorer cannot see the slow fight at
// all), or is never a candidate (a licence). AIR_PROBE_SEED selects the seed,
// AIR_TIER the tier, AIR_WEAPONS=guns takes the missiles away.
func TestHornetProbe(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	seed := uint64(1)
	if s := os.Getenv("AIR_PROBE_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			seed = uint64(n)
		}
	}
	level := "ace"
	if only := os.Getenv("AIR_TIER"); only != "" {
		level = only
	}
	missiles := os.Getenv("AIR_WEAPONS") != "guns"
	mode := "furball"
	if missiles {
		mode = "joust"
	}
	parameters := map[string]any{"missiles": missiles, "bots": map[string]any{level: 1.0}}
	if missiles {
		parameters["weapons"] = "fox2"
	}
	session := game.Session{Identifier: "hornetprobe", Game: "air", Mode: mode, Seed: seed, Parameters: parameters}
	if mode == "furball" {
		session.Capacity = 8
	}
	g := New()
	made, err := g.Create(session)
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
	place(i, bot, 0, 2400)
	me := &i.aircraft[0].model.State
	me.Velocity = me.Velocity.Normalize().Scale(-344 / 1.944)
	me.Attitude = flight.Look(me.Velocity.Normalize())
	foe := &i.aircraft[bot].model.State
	pilot := &hornet{armed: true}
	b := i.aircraft[bot].brain
	fmt.Printf("%s v hornet, seed %d, %s rules, missiles=%v\n", level, seed, mode, missiles)
	plans, seen, best, rankSum, gapSum := 0, 0, 0, 0, 0.0
	for tick := uint64(0); tick < 120*60; tick++ {
		i.Step(tick, map[int][]game.Input{0: {{Data: pilot.fly(me, foe, tick)}}})
		if !i.aircraft[0].alive || !i.aircraft[bot].alive || i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
			fmt.Printf("t=%.1f END hornet alive=%v bot alive=%v bot kills=%d\n", float64(tick)/60, i.aircraft[0].alive, i.aircraft[bot].alive, i.aircraft[bot].kills)
			break
		}
		if b.picked == tick && b.prey != nil {
			scores := map[string]float64{}
			sim := flight.New(i.aircraft[bot].model.Airframe, i.aircraft[bot].model.Environment, i.aircraft[bot].model.World)
			i.choose(bot, i.aircraft[bot], b, sim, b.prey, tick, b.distance, scores)
			type e struct {
				n string
				s float64
			}
			list := []e{}
			for n, s := range scores {
				list = append(list, e{n, s})
			}
			sort.Slice(list, func(a, c int) bool { return list[a].s > list[c].s })
			plans++
			rank, gap := -1, 0.0
			for k, x := range list {
				if x.n == "high" {
					rank, gap = k, list[0].s-x.s
				}
			}
			fmt.Printf("  t=%5.1f PLAN %-7s intent=%-7s |", float64(tick)/60, b.play, b.intent)
			for k, x := range list {
				if k >= 4 {
					break
				}
				fmt.Printf(" %s %.2f", x.n, x.s)
			}
			if rank < 0 {
				fmt.Printf(" | high: NOT A CANDIDATE (%d rehearsed)\n", len(list))
			} else {
				seen++
				rankSum += rank
				gapSum += gap
				if rank == 0 {
					best++
				}
				fmt.Printf(" | high: rank %d/%d score %.2f gap %.2f\n", rank+1, len(list), scores["high"], gap)
			}
		}
		if tick%(5*60) == 0 {
			toward := foe.Position.Subtract(me.Position)
			r := toward.Length()
			botBehind := math.Acos(clamp(toward.Scale(1/r).Dot(me.Velocity.Normalize().Scale(-1)), -1, 1))*57.3 < 45 && r < 1500
			hornetBehind := math.Acos(clamp(toward.Scale(-1/r).Dot(foe.Velocity.Normalize().Scale(-1)), -1, 1))*57.3 < 45 && r < 1500
			fmt.Printf("t=%5.1f range %5.0f | bot %-7s %3.0f kt alpha %4.1f g %3.1f alt %5.0f ft | hornet %3.0f kt alpha %4.1f alt %5.0f ft | bot behind %v, hornet behind %v\n",
				float64(tick)/60, r, b.play, foe.Velocity.Length()*1.944, i.aircraft[bot].model.Alpha()*57.3, i.aircraft[bot].model.Nz(), foe.Position.Y*3.281,
				me.Velocity.Length()*1.944, i.aircraft[0].model.Alpha()*57.3, me.Position.Y*3.281, botBehind, hornetBehind)
		}
	}
	if seen > 0 {
		fmt.Printf("SUMMARY: %d re-plans; high a candidate in %d, best in %d; mean rank %.1f, mean gap to the top %.2f\n",
			plans, seen, best, float64(rankSum)/float64(seen)+1, gapSum/float64(seen))
	} else {
		fmt.Printf("SUMMARY: %d re-plans; high was never a candidate\n", plans)
	}
}

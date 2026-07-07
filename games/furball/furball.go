// Mochi world: Furball game module
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Furball multiplayer: air combat over the atoll with the real blade-element
// flight model (package flight) and server-authoritative weapons. Two modes:
// "furball" — long-running, anyone joins or leaves at any time, endless
// respawns (the standing match uses this); "joust" — 1v1, ends the moment one
// player is destroyed. Creators set weather and rules through the session
// parameters: "tod", "clouds" (relayed to clients in the welcome) and
// "missiles" (enforced here). Matches are air-start; deck operations stay
// single-player for now, so ANY surface contact in multiplayer is a kill.
package furball

import (
	"encoding/binary"
	"fmt"
	"errors"
	"math"
	"sort"

	"world/game"
	"world/games/furball/aircraft"
	"world/games/furball/battle"
	"world/games/furball/flight"
)

const (
	altitude = 4572   // 15,000 ft — the merge altitude
	ring     = 2778   // spawn radius (1.5 NM)
	sea      = 3      // world y at which flight ends
	speed    = 220    // spawn airspeed
	fuel     = 3000.0 // spawn fuel load, kg (~half internal)
	pause    = 5.0    // seconds from death to respawn (furball mode)
	rounds   = 578    // M61 magazine per life
	rate     = 100.0  // rounds per second (6,000 rpm)
	muzzle   = 6.0    // m forward of the datum, matching the client's tracer port
	derelict = 30.0   // s a pilot-dead/ejected wreck keeps flying
)

// Missile constants: pursuit guidance with an aspect-aware seeker, graded
// flare decoys, and a real proximity fuse feeding the battle warhead.
const (
	missile_speed = 700.0  // m/s, constant
	missile_life  = 12.0   // s
	missile_fuse  = 12.0   // m, proximity fuse envelope (battle grades the warhead inside)
	missile_range = 5000.0 // rear-aspect acquisition range (m); weak head-on
	missile_cone  = 0.866  // cos of the acquisition half-angle (~30°)
	flare_window  = 0.8    // s after a flare drop in which it can decoy
)

type Furball struct{}

func New() *Furball { return &Furball{} }

func (f *Furball) Name() string     { return "furball" }
func (f *Furball) Rate() (int, int) { return 60, 20 }

func (f *Furball) Create(session game.Session) (game.Instance, error) {
	wrap := 250000.0
	if v, found := session.Parameters["wrap"]; found {
		if n, valid := v.(float64); valid && n >= 0 {
			wrap = n
		}
	}
	mode := session.Mode
	if mode != "joust" {
		mode = "furball"
	}
	allowed, _ := session.Parameters["missiles"].(bool)
	i := &instance{
		mode:        mode,
		missiles:    allowed,
		environment: flight.Environment{Seed: session.Seed, Wrap: wrap},
		aircraft:    map[int]*craft{},
	}
	// Practice bots (#81 verification / solo flying): server-flown aircraft on
	// scripted gentle manoeuvres, filling slots from the TOP so the framework's
	// low-slot assignment for real players cannot collide in practice. Open
	// mode only — a joust is strictly the human pair.
	if bots := number(session.Parameters, "bots"); mode != "joust" && bots > 0 {
		count := int(bots)
		if count > 99 {
			count = 99
		}
		for n := 0; n < count; n++ {
			slot := 99 - n
			m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
			i.spawn(slot, m)
			b := &craft{player: game.Player{Name: fmt.Sprintf("Bot %d", n+1), Slot: slot}, kind: "fa18c", model: m, alive: true, flared: 1e9, bot: true}
			b.arm()
			i.aircraft[slot] = b
		}
	}
	return i, nil
}

type craft struct {
	player    game.Player
	kind      string // aircraft catalogue name
	bot       bool   // server-flown practice aircraft: scripted inputs, no link
	close     map[int]bool // this recipient's sticky near set (interest hysteresis)
	model     *flight.Model
	body      battle.Body
	condition battle.Condition
	latest    flight.Inputs
	alive     bool
	ammunition int     // gun rounds left this life
	charge    float64 // fractional rounds accumulated at the fire rate
	ejected   bool    // eject edge consumed this life
	wait      float64 // seconds until respawn (furball mode)
	flared    float64 // sim seconds since the last flare drop (large when none)
	kills     int
	deaths    int
}

// arm rebinds the craft's battle body to its current model's damage state.
func (a *craft) arm() {
	a.condition = battle.Condition{Damager: -1}
	a.body = battle.Body{
		Airframe:  a.model.Airframe,
		Parts:     battle.Parts(a.model.Airframe),
		Damage:    &a.model.State.Damage,
		Condition: &a.condition,
	}
	a.ammunition = rounds
	a.charge = 0
	a.ejected = false
}

type missile struct {
	shooter  int
	target   int
	position flight.Vec3
	velocity flight.Vec3
	life     float64
	number   uint64 // per-instance launch counter, for deterministic decoys
	window   bool   // a flare window has been judged (one decoy roll per flare)
}

// wreck is a pilot-dead or ejected airframe that keeps flying until it hits
// something or burns out; purely spectacle, no further scoring.
type wreck struct {
	model *flight.Model
	burn  [2]float64
	life  float64
}

type instance struct {
	mode        string // furball (open, endless) or joust (1v1, first kill ends it)
	started     bool   // joust: false until BOTH players are present — the first joiner is held frozen at the ring, and the pair merges fresh together
	merged      bool   // joust: weapons hold until the MERGE — either aircraft crossing the other's 3/9 line (x < -margin in the other's body frame); one-shot, announced with a "fighton" event
	missiles    bool   // rule: missiles allowed
	environment flight.Environment
	aircraft    map[int]*craft
	flying      []*missile
	wrecks      []*wreck
	launched    uint64
	events      []map[string]any
	finished    bool
	results     map[string]any
}

// slots iterates the aircraft in slot order: map order is random per
// process, and battle rolls are keyed on tick — determinism requires a
// stable iteration order.
func (i *instance) slots() []int {
	order := make([]int, 0, len(i.aircraft))
	for slot := range i.aircraft {
		order = append(order, slot)
	}
	sort.Ints(order)
	return order
}

// spawn resets a model to the merge ring, facing the centre: slots 0/1 meet
// head-on (the joust pair); later slots spread by the golden angle.
func (i *instance) spawn(slot int, m *flight.Model) {
	angle := float64(slot) * math.Pi
	if slot > 1 {
		angle = float64(slot) * 2.399963
	}
	position := flight.Vec3{X: math.Cos(angle) * ring, Y: altitude, Z: math.Sin(angle) * ring}
	inward := flight.Vec3{X: -math.Cos(angle), Y: 0, Z: -math.Sin(angle)}
	m.State = flight.Level(m, position, inward, speed, fuel)
}

// state_payload is one aircraft's snapshot entry: the legacy derived fields
// every client renders from, the world velocity, and the full encoded core
// state whose own entry feeds the sender's prediction replay.
func state_payload(s *flight.State) map[string]any {
	direction := s.Velocity.Normalize()
	if s.Velocity.Length() < 1 {
		direction = s.Attitude.Rotate(flight.Vec3{X: 1})
	}
	words := make([]float64, flight.Size)
	s.Encode(words)
	// The wire core: 57 base words at full precision, then the damage tail
	// QUANTIZED to uint16 — the tail is unit-interval losses plus one mass,
	// and at float64 it pushed the snapshot past the QUIC datagram MTU
	// (SendDatagram drops oversized frames silently; TestPair catches it).
	// The client re-expands to the full 106-word layout before the core
	// feeds prediction; 1.5e-5 quantisation steps are far below anything
	// the aero can feel. Scale for Loss (words beyond the unit losses): kg/8000.
	core := make([]byte, 57*8+(flight.Size-57)*2)
	for i := 0; i < 57; i++ {
		binary.LittleEndian.PutUint64(core[i*8:], math.Float64bits(words[i]))
	}
	for i := 57; i < flight.Size; i++ {
		v := words[i]
		if i == 57+flight.Elements+flight.Channels { // Loss, kg
			v /= 8000
		}
		binary.LittleEndian.PutUint16(core[57*8+(i-57)*2:], uint16(clamp(v, 0, 1)*65535+0.5))
	}
	return map[string]any{
		"position":  []float64{s.Position.X, s.Position.Y, s.Position.Z},
		"direction": []float64{direction.X, direction.Y, direction.Z},
		"attitude":  []float64{s.Attitude.W, s.Attitude.X, s.Attitude.Y, s.Attitude.Z},
		"speed":     s.Velocity.Length(),
		"velocity":  []float64{s.Velocity.X, s.Velocity.Y, s.Velocity.Z},
		"core":      core,
	}
}

func (i *instance) Join(player game.Player) (map[string]any, error) {
	if i.finished {
		return nil, errors.New("over")
	}
	if i.mode == "joust" && len(i.aircraft) >= 2 {
		return nil, errors.New("full")
	}
	kind := "fa18c" // the only catalogue entry today; a requested type would arrive on the join payload
	m := flight.New(aircraft.Get(kind), i.environment, flight.World{Sea: sea})
	i.spawn(player.Slot, m)
	a := &craft{player: player, kind: kind, model: m, alive: true, flared: 1e9}
	a.arm()
	i.aircraft[player.Slot] = a
	i.events = append(i.events, map[string]any{"kind": "roster", "slot": player.Slot, "name": player.Name})
	for slot, b := range i.aircraft {
		if b.bot {
			i.events = append(i.events, map[string]any{"kind": "roster", "slot": slot, "name": b.player.Name})
		}
	}
	if i.mode == "joust" && len(i.aircraft) == 2 && !i.started {
		// The pair is complete THIS instant: the match starts, and both merge
		// fresh and simultaneously however long the first arrival sat frozen.
		i.started = true
		for _, slot := range i.slots() {
			b := i.aircraft[slot]
			i.spawn(slot, b.model)
			b.model.State.Damage = flight.DamageState{}
			b.arm()
			b.alive = true
			i.events = append(i.events, map[string]any{"kind": "respawn", "slot": slot, "state": state_payload(&b.model.State)})
		}
	}
	waiting := i.mode == "joust" && !i.started
	return map[string]any{"state": state_payload(&a.model.State), "wrap": i.environment.Wrap, "model": flight.Version, "aircraft": kind, "waiting": waiting, "mode": i.mode}, nil
}

func (i *instance) Leave(player game.Player) {
	delete(i.aircraft, player.Slot)
	// A joust abandoned mid-fight ends in favour of whoever stayed — but only
	// once it actually started: bailing out of the waiting room is not a win.
	if i.mode == "joust" && i.started && !i.finished && len(i.aircraft) == 1 {
		for slot := range i.aircraft {
			i.finish(slot, player.Slot)
		}
	}
}

// free reports whether weapons are enabled: jousts hold fire until the merge.
func (i *instance) free() bool { return i.mode != "joust" || i.merged }

// merge watches the joust pair for the 3/9-line crossing — the BFM merge: the
// fight is on when either aircraft passes behind the other's wing line (a few
// metres of margin so mutual-beam jitter cannot flicker it). One-shot.
func (i *instance) merge() {
	if i.mode != "joust" || i.merged || !i.started {
		return
	}
	slots := i.slots()
	if len(slots) < 2 {
		return
	}
	a, b := i.aircraft[slots[0]], i.aircraft[slots[1]]
	if a.model == nil || b.model == nil {
		return
	}
	behind := func(from, of *flight.Model) bool {
		rel := flight.Vec3{
			X: flight.Shortest(of.State.Position.X, from.State.Position.X, i.environment.Wrap),
			Y: from.State.Position.Y - of.State.Position.Y,
			Z: flight.Shortest(of.State.Position.Z, from.State.Position.Z, i.environment.Wrap),
		} // Shortest returns b-a: arguments ordered (of, from) so rel = from - of""}
		return rel.Dot(of.State.Attitude.Rotate(flight.Vec3{X: 1})) < -5
	}
	if behind(a.model, b.model) || behind(b.model, a.model) {
		i.merged = true
		i.events = append(i.events, map[string]any{"kind": "fighton"})
	}
}

// input converts a wire sample into flight inputs.
func input(data map[string]any) flight.Inputs {
	flag := func(key string) bool { v, _ := data[key].(bool); return v }
	return flight.Inputs{
		Pitch:      clamp(number(data, "pitch"), -1, 1),
		Roll:       clamp(number(data, "roll"), -1, 1),
		Yaw:        clamp(number(data, "yaw"), -1, 1),
		Throttle:   clamp(number(data, "throttle"), 0, 1),
		Speedbrake: clamp(number(data, "speedbrake"), 0, 1),
		Reheat:     clamp(number(data, "reheat"), 0, 1),
		Brake:      flag("brake"),
		Gear:       flag("gear"),
		Hook:       flag("hook"),
		Launch:     flag("launch"),
		Override:   flag("override"),
		Eject:      flag("eject"),
		Fire:       flag("fire"),
		Flare:      flag("flare"),
		Missile:    flag("missile"),
		Sequence:   uint32(number(data, "sequence")),
	}
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

func number(data map[string]any, key string) float64 {
	switch v := data[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case uint64:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func (i *instance) Step(tick uint64, inputs map[int][]game.Input) {
	dt := 1.0 / 60
	for slot, list := range inputs {
		a := i.aircraft[slot]
		if a == nil || len(list) == 0 {
			continue
		}
		for _, sample := range list {
			in := input(sample.Data)
			if in.Flare && a.alive {
				a.flared = 0
				i.events = append(i.events, map[string]any{"kind": "flare", "slot": slot})
			}
			if in.Missile && a.alive && i.missiles && i.free() {
				i.launch(slot, a)
			}
			if in.Eject && a.alive && !a.ejected {
				a.ejected = true
				i.eject(slot, a)
			}
		}
		a.latest = input(list[len(list)-1].Data)
	}
	if i.mode == "joust" && !i.started {
		return // waiting room: no physics until the opponent arrives (the lone jet hangs frozen at the ring; Join starts the match and merges both fresh)
	}
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a.bot && a.alive {
			// Bot autopilot: CLOSED loops, not open-loop weaves (those spiralled
			// every bot into the sea within minutes). Bank tracks a slow
			// slot-staggered wander, pitch holds a per-slot altitude with climb
			// damping plus a bank-compensation bias, throttle holds airspeed.
			s := &a.model.State
			up := s.Attitude.Rotate(flight.Vec3{Y: 1})
			right := s.Attitude.Rotate(flight.Vec3{Z: 1})
			bank := math.Atan2(right.Y, up.Y)
			t := float64(tick) / 60
			phase := float64(slot) * 2.399
			bankTarget := 0.35 * math.Sin(t*0.03+phase)
			altTarget := 3200 + float64(slot%40)*60
			speed := s.Velocity.Length()
			a.latest = flight.Inputs{
				Throttle: clamp(0.55+(200-speed)*0.01, 0.3, 1),
				Roll:     clamp((bank-bankTarget)*1.5, -0.5, 0.5), // positive stick rolls right = NEGATIVE bank in the atan2(right.Y, up.Y) convention (the first sign spiralled every bot inverted within seconds)
				Pitch:    clamp((altTarget-s.Position.Y)*4e-4-s.Velocity.Y*4e-3+math.Abs(bank)*0.15, -0.3, 0.5),
			}
		}
	}
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		a.flared += dt
		if !a.alive {
			if i.mode == "joust" {
				continue // no respawns — the match is over
			}
			a.wait -= dt
			if a.wait <= 0 {
				if a.model == nil { // the previous airframe left as a wreck
					a.model = flight.New(aircraft.Get(a.kind), i.environment, flight.World{Sea: sea})
				}
				i.spawn(slot, a.model)
				a.model.State.Damage = flight.DamageState{} // a fresh jet
				a.arm()
				a.alive = true
				i.events = append(i.events, map[string]any{"kind": "respawn", "slot": slot, "state": state_payload(&a.model.State)})
			}
			continue
		}
		for substep := 0; substep < 4; substep++ {
			a.model.Step(a.latest) // 4 × Dt (1/240) per 60 Hz tick
		}
		// The damage cascade: fires feed or starve on the throttle, fuel
		// fires run their fuse, weakened wings shed under g.
		for _, event := range battle.Advance(&a.body, a.model, a.latest.Throttle, 60, i.environment.Seed, uint64(slot), tick) {
			i.raise(slot, event)
		}
		if a.alive && a.condition.Killed {
			i.down(slot, a, "pilot")
			continue
		}
		if !a.alive {
			continue // the cascade exploded this aircraft
		}
		// Air-start rule: any surface contact — water, a crash probe, even a
		// gentle touch — is a kill until multiplayer deck operations land.
		state := &a.model.State
		if state.Position.Y <= sea || state.Gear.Touch.Occurred || state.Gear.Contact >= 0 {
			i.kill(slot, credit(a)) // whoever wrecked the jet gets the splash
		}
	}
	i.merge()
	i.guns(dt, tick)
	i.pursue(dt, tick)
	i.drift(dt)
}

// credit names the killer: the last player to damage this aircraft within
// the last minute, else the environment.
func credit(a *craft) int {
	if a.condition.Damager >= 0 && a.condition.Damaged < 60 {
		return a.condition.Damager
	}
	return -1
}

// raise converts a battle event into wire events and verdicts.
func (i *instance) raise(slot int, event battle.Event) {
	a := i.aircraft[slot]
	switch event.Kind {
	case "explode":
		i.events = append(i.events, map[string]any{"kind": "explode", "slot": slot})
		if a != nil {
			i.kill(slot, credit(a))
		}
	case "hit":
		// Raised with shooter context in guns(); ignored here.
	default:
		i.events = append(i.events, map[string]any{"kind": event.Kind, "slot": slot, "engine": event.Engine, "surface": event.Surface})
	}
}

// down retires a flying airframe whose pilot is gone — killed or ejected.
// The jet flies on as a wreck; the slot dies for scoring and respawn.
func (i *instance) down(slot int, a *craft, reason string) {
	i.events = append(i.events, map[string]any{"kind": reason, "slot": slot, "by": credit(a)})
	i.kill(slot, credit(a)) // while the model still exists: the kill event carries its position
	i.wrecks = append(i.wrecks, &wreck{model: a.model, burn: a.condition.Fire, life: derelict})
	a.model = nil
}

// eject: the pilot's out — a kill credit for whoever wrecked the jet, and
// the airframe flies on pilotless.
func (i *instance) eject(slot int, a *craft) {
	i.down(slot, a, "eject")
}

// drift flies the wrecks: neutral controls, idle throttle, burning until
// they hit the sea or burn out.
func (i *instance) drift(dt float64) {
	keep := i.wrecks[:0]
	for _, w := range i.wrecks {
		w.life -= dt
		for substep := 0; substep < 4; substep++ {
			w.model.Step(flight.Inputs{})
		}
		state := &w.model.State
		if w.life <= 0 || state.Position.Y <= sea || state.Gear.Contact >= 0 {
			i.events = append(i.events, map[string]any{
				"kind": "splash",
				"position": []float64{state.Position.X, state.Position.Y, state.Position.Z},
			})
			continue
		}
		keep = append(keep, w)
	}
	i.wrecks = keep
	if len(i.wrecks) > 4 { // MTU guard: oldest wrecks vanish quietly
		i.wrecks = i.wrecks[len(i.wrecks)-4:]
	}
}

// guns fires real rounds: the M61's rate accumulates fractional rounds per
// tick, each round is a dispersed ray traced against every hostile's part
// geometry, and each hit strikes a specific system.
func (i *instance) guns(dt float64, tick uint64) {
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		if !a.alive || !a.latest.Fire || a.ammunition <= 0 || !i.free() {
			a.charge = 0
			continue
		}
		a.charge += rate * dt
		burst := int(a.charge)
		if burst <= 0 {
			continue
		}
		a.charge -= float64(burst)
		if burst > a.ammunition {
			burst = a.ammunition
		}
		a.ammunition -= burst
		state := &a.model.State
		shooter := battle.Pose{
			Position: state.Position.Add(state.Attitude.Rotate(flight.Vec3{X: muzzle})),
			Forward:  state.Attitude.Rotate(flight.Vec3{X: 1}),
			Up:       state.Attitude.Rotate(flight.Vec3{Y: 1}),
			Right:    state.Attitude.Rotate(flight.Vec3{Z: 1}),
		}
		for _, other := range i.slots() {
			b := i.aircraft[other]
			if other == slot || !b.alive {
				continue
			}
			hits, events := battle.Burst(shooter, b.model.State.Position, b.model.State.Attitude, &b.body, burst, i.environment.Wrap, i.environment.Seed, uint64(slot), tick)
			if hits == 0 {
				continue
			}
			b.condition.Damager = slot
			b.condition.Damaged = 0
			for _, event := range events {
				if event.Kind == "hit" {
					i.events = append(i.events, map[string]any{"kind": "hit", "slot": other, "by": slot, "count": event.Count})
					continue
				}
				i.raise(other, event)
			}
		}
	}
}

// bearing returns the unit minimum-image direction and distance from a to b.
func (i *instance) bearing(a flight.Vec3, b flight.Vec3) (flight.Vec3, float64) {
	dx := flight.Shortest(a.X, b.X, i.environment.Wrap)
	dy := b.Y - a.Y
	dz := flight.Shortest(a.Z, b.Z, i.environment.Wrap)
	distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if distance < 1e-9 {
		return flight.Vec3{X: 1}, 0
	}
	return flight.Vec3{X: dx / distance, Y: dy / distance, Z: dz / distance}, distance
}

// launch fires a missile at the best target in the seeker cone. The seeker
// is aspect-aware: it sees a tailpipe from the rear hemisphere much farther
// than a cold nose head-on.
func (i *instance) launch(slot int, a *craft) {
	forward := a.model.State.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
	best, nearest := -1, missile_range+1
	for _, other := range i.slots() {
		b := i.aircraft[other]
		if other == slot || !b.alive {
			continue
		}
		direction, distance := i.bearing(a.model.State.Position, b.model.State.Position)
		if distance < 1 {
			continue
		}
		tail := direction.Dot(b.model.State.Attitude.Rotate(flight.Vec3{X: 1})) // 1 = square at the tailpipe
		if distance > missile_range*(0.4+0.6*math.Max(0, tail)) {
			continue
		}
		if forward.X*direction.X+forward.Y*direction.Y+forward.Z*direction.Z < missile_cone {
			continue
		}
		if distance < nearest {
			best, nearest = other, distance
		}
	}
	if best < 0 {
		return
	}
	i.launched++
	i.flying = append(i.flying, &missile{
		shooter:  slot,
		target:   best,
		position: a.model.State.Position,
		velocity: flight.Vec3{X: forward.X * missile_speed, Y: forward.Y * missile_speed, Z: forward.Z * missile_speed},
		life:     missile_life,
		number:   i.launched,
	})
	i.events = append(i.events, map[string]any{"kind": "missile", "slot": slot, "target": best})
}

// pursue advances every missile: pure pursuit toward the target, graded
// flare decoys judged once per flare drop, and a proximity fuse feeding the
// battle warhead — a direct hit kills outright, a fringe burst fragments.
func (i *instance) pursue(dt float64, tick uint64) {
	alive := i.flying[:0]
	for _, m := range i.flying {
		m.life -= dt
		target := i.aircraft[m.target]
		if m.life <= 0 || target == nil || !target.alive {
			continue
		}
		// A flare inside the window decoys with aspect-graded probability:
		// looking up a hot tailpipe the seeker holds; from the beam a flare
		// is the brighter thing in view. Reheat anchors the seeker harder.
		if target.flared < flare_window {
			if !m.window {
				m.window = true
				direction, _ := i.bearing(m.position, target.model.State.Position)
				tail := direction.Dot(target.model.State.Attitude.Rotate(flight.Vec3{X: 1}))
				decoy := 0.35 + 0.40*(1-clamp(tail, 0, 1))
				if target.latest.Reheat > 0.05 {
					decoy *= 0.5
				}
				if battle.Roll(i.environment.Seed, m.number, tick) < decoy {
					i.events = append(i.events, map[string]any{"kind": "decoy", "slot": m.target})
					continue
				}
			}
		} else {
			m.window = false
		}
		direction, distance := i.bearing(m.position, target.model.State.Position)
		if distance < missile_fuse {
			kill, events := battle.Blast(m.position, target.model.State.Position, target.model.State.Attitude, &target.body, i.environment.Wrap, i.environment.Seed, uint64(m.shooter), tick)
			target.condition.Damager = m.shooter
			target.condition.Damaged = 0
			for _, event := range events {
				if event.Kind != "explode" {
					i.raise(m.target, event)
				}
			}
			if kill {
				i.kill(m.target, m.shooter)
			}
			continue
		}
		// Steer the velocity toward the target with a limited blend rate —
		// pursuit guidance at the placeholder fidelity level.
		turn := 4.0 * dt
		v := flight.Vec3{
			X: m.velocity.X + (direction.X*missile_speed-m.velocity.X)*turn,
			Y: m.velocity.Y + (direction.Y*missile_speed-m.velocity.Y)*turn,
			Z: m.velocity.Z + (direction.Z*missile_speed-m.velocity.Z)*turn,
		}
		length := math.Sqrt(v.X*v.X+v.Y*v.Y+v.Z*v.Z) + 1e-9
		m.velocity = flight.Vec3{X: v.X / length * missile_speed, Y: v.Y / length * missile_speed, Z: v.Z / length * missile_speed}
		m.position = flight.Vec3{X: m.position.X + m.velocity.X*dt, Y: m.position.Y + m.velocity.Y*dt, Z: m.position.Z + m.velocity.Z*dt}
		if m.position.Y <= 0 {
			continue // splashed
		}
		alive = append(alive, m)
	}
	i.flying = alive
}

// mask packs per-surface damage into a bitfield: bit set when a surface's
// mean element loss exceeds 0.4 — the client's visual-damage summary.
func mask(a *flight.Airframe, element []float64) int {
	if element == nil {
		return 0
	}
	bits, base := 0, 0
	for si := range a.Surfaces {
		n := len(a.Surfaces[si].Elements)
		if n > 0 && si < 16 {
			sum := 0.0
			for ei := 0; ei < n && base+ei < len(element); ei++ {
				sum += element[base+ei]
			}
			if sum/float64(n) > 0.4 {
				bits |= 1 << si
			}
		}
		base += n
	}
	return bits
}

// kill downs a victim; by is the killer's slot or -1 for the environment.
// In joust mode the first kill finishes the match.
func (i *instance) kill(victim int, by int) {
	a := i.aircraft[victim]
	if a == nil || !a.alive {
		return
	}
	a.alive = false
	a.wait = pause
	a.deaths++
	if killer := i.aircraft[by]; by >= 0 && killer != nil {
		killer.kills++
	}
	i.events = append(i.events, map[string]any{
		"kind": "kill", "slot": victim, "by": by,
		"position": []float64{a.model.State.Position.X, a.model.State.Position.Y, a.model.State.Position.Z},
	})
	if i.mode == "joust" && !i.finished {
		winner := by
		if winner < 0 { // flew into the sea: the other player wins
			for slot := range i.aircraft {
				if slot != victim {
					winner = slot
				}
			}
		}
		i.finish(winner, victim)
	}
}

// finish records the joust outcome.
func (i *instance) finish(winner int, loser int) {
	i.finished = true
	name := func(slot int) string {
		if a := i.aircraft[slot]; a != nil {
			return a.player.Name
		}
		return ""
	}
	i.results = map[string]any{"winner": winner, "loser": loser, "name": name(winner)}
}

// Snapshot wire (#81, 100 players): every remote aircraft is a fixed 34-byte
// binary pose record — CBOR maps with string keys cost ~300 B per player and
// burst the QUIC datagram at three. Each recipient gets the NEAREST `near`
// remotes every snapshot plus `roving` of the far tail round-robin (full far
// coverage in a fraction of a second at snapshot rate); missiles are the
// recipient's nearest few. Names, aircraft types, and scores never ride the
// hot path: names arrive in the welcome and the session mirror, and clients
// count kills from the kill events.
const (
	near   = 20 // rank at which a remote ENTERS the recipient's sticky near set
	depart = 24 // rank past which it leaves (hysteresis: weaving aircraft at the boundary flapped between full- and slow-rate updates — the jerk was visible)
	roving = 6  // far-tail remotes rotated through per snapshot (sized with the sticky set + missiles to fit the poses datagram)
)

// pose packs one aircraft into the 34-byte wire record.
func pose(slot int, a *craft) []byte {
	s := &a.model.State
	b := make([]byte, 34)
	b[0] = byte(slot)
	binary.LittleEndian.PutUint32(b[1:], math.Float32bits(float32(s.Position.X)))
	binary.LittleEndian.PutUint32(b[5:], math.Float32bits(float32(s.Position.Y)))
	binary.LittleEndian.PutUint32(b[9:], math.Float32bits(float32(s.Position.Z)))
	quat := func(offset int, v float64) { binary.LittleEndian.PutUint16(b[offset:], uint16(int16(clamp(v, -1, 1)*32767))) }
	quat(13, s.Attitude.W)
	quat(15, s.Attitude.X)
	quat(17, s.Attitude.Y)
	quat(19, s.Attitude.Z)
	direction := s.Velocity.Normalize()
	if s.Velocity.Length() < 1 {
		direction = s.Attitude.Rotate(flight.Vec3{X: 1})
	}
	b[21] = byte(int8(clamp(direction.X, -1, 1) * 127))
	b[22] = byte(int8(clamp(direction.Y, -1, 1) * 127))
	b[23] = byte(int8(clamp(direction.Z, -1, 1) * 127))
	binary.LittleEndian.PutUint16(b[24:], uint16(clamp(s.Velocity.Length(), 0, 6553)*10))
	flags := byte(0)
	if a.alive {
		flags |= 1
	}
	if s.Gear.Extension > 0.5 {
		flags |= 2
	}
	if a.latest.Hook {
		flags |= 4
	}
	if a.latest.Fire {
		flags |= 8
	}
	if !a.condition.Killed {
		flags |= 16
	}
	b[26] = flags
	b[27] = byte(clamp(a.latest.Reheat, 0, 1) * 255)
	b[28] = byte(clamp(a.model.State.Fcs.Speedbrake, 0, 1) * 255)
	b[29] = byte(clamp(a.condition.Fire[0], 0, 1) * 255)
	b[30] = byte(clamp(a.condition.Fire[1], 0, 1) * 255)
	b[31] = byte(clamp(a.model.State.Damage.Leak*10, 0, 255))
	binary.LittleEndian.PutUint16(b[32:], uint16(mask(a.model.Airframe, a.model.State.Damage.Element)))
	return b
}

// span is the wrap-aware distance between two aircraft.
func (i *instance) span(a, b *flight.Model) float64 {
	return flight.Vec3{
		X: flight.Shortest(a.State.Position.X, b.State.Position.X, i.environment.Wrap),
		Y: b.State.Position.Y - a.State.Position.Y,
		Z: flight.Shortest(a.State.Position.Z, b.State.Position.Z, i.environment.Wrap),
	}.Length()
}

func (i *instance) Snapshot(tick uint64) map[string]any {
	cores := map[int]any{}
	poses := map[int]any{}
	order := i.slots()
	for _, self := range order {
		me := i.aircraft[self]
		if me.model == nil {
			continue
		}
		entry := state_payload(&me.model.State)
		cores[self] = entry["core"]
		// Interest management: everyone else sorted by wrap distance; the
		// nearest `near` every snapshot, the far tail rotated `roving` at a
		// time so distant contacts still refresh several times a second.
		others := make([]int, 0, len(order)-1)
		for _, slot := range order {
			if slot != self && i.aircraft[slot].model != nil {
				others = append(others, slot)
			}
		}
		sort.Slice(others, func(x, y int) bool {
			return i.span(me.model, i.aircraft[others[x]].model) < i.span(me.model, i.aircraft[others[y]].model)
		})
		picked := others
		if len(others) > near {
			// Sticky near set with hysteresis: enter at rank <= near, leave past
			// rank depart — a jet weaving across the plain rank-20 boundary used
			// to flap between full-rate and round-robin updates.
			if me.close == nil {
				me.close = map[int]bool{}
			}
			rank := map[int]int{}
			for r, slot := range others {
				rank[slot] = r
			}
			for slot := range me.close {
				if r, ok := rank[slot]; !ok || r >= depart {
					delete(me.close, slot)
				}
			}
			for _, slot := range others[:near] {
				me.close[slot] = true
			}
			for len(me.close) > depart { // bound the set: evict the worst-ranked
				worst, at := -1, -1
				for slot := range me.close {
					if rank[slot] > at {
						worst, at = slot, rank[slot]
					}
				}
				delete(me.close, worst)
			}
			set := make([]int, 0, len(me.close))
			far := make([]int, 0, len(others)-len(me.close))
			for _, slot := range others { // keep distance order within each group
				if me.close[slot] {
					set = append(set, slot)
				} else {
					far = append(far, slot)
				}
			}
			cycle := (len(far) + roving - 1) / roving
			at := int(tick/3%uint64(cycle)) * roving // snapshots fire every 3 ticks (60/20): advance one window per snapshot, no skipped stretches
			stop := at + roving
			if stop > len(far) {
				stop = len(far)
			}
			picked = append(set, far[at:stop]...)
		}
		blob := make([]byte, 0, (len(picked)+1)*34)
		blob = append(blob, pose(self, me)...) // self first: the client's no-prediction fallback reads its own pose from the wire
		for _, slot := range picked {
			blob = append(blob, pose(slot, i.aircraft[slot])...)
		}
		// The recipient's nearest missiles, 24 bytes each, capped at 6.
		type shot struct {
			m *missile
			d float64
		}
		shots := make([]shot, 0, len(i.flying))
		for _, m := range i.flying {
			d := flight.Vec3{
				X: flight.Shortest(me.model.State.Position.X, m.position.X, i.environment.Wrap),
				Y: m.position.Y - me.model.State.Position.Y,
				Z: flight.Shortest(me.model.State.Position.Z, m.position.Z, i.environment.Wrap),
			}.Length()
			shots = append(shots, shot{m, d})
		}
		sort.Slice(shots, func(x, y int) bool { return shots[x].d < shots[y].d })
		if len(shots) > 6 {
			shots = shots[:6]
		}
		darts := make([]byte, 0, len(shots)*24)
		for _, sh := range shots {
			var d [24]byte
			binary.LittleEndian.PutUint32(d[0:], math.Float32bits(float32(sh.m.position.X)))
			binary.LittleEndian.PutUint32(d[4:], math.Float32bits(float32(sh.m.position.Y)))
			binary.LittleEndian.PutUint32(d[8:], math.Float32bits(float32(sh.m.position.Z)))
			binary.LittleEndian.PutUint32(d[12:], math.Float32bits(float32(sh.m.velocity.X)))
			binary.LittleEndian.PutUint32(d[16:], math.Float32bits(float32(sh.m.velocity.Y)))
			binary.LittleEndian.PutUint32(d[20:], math.Float32bits(float32(sh.m.velocity.Z)))
			darts = append(darts, d[:]...)
		}
		poses[self] = map[string]any{"blob": blob, "missiles": darts}
	}
	// Wrecks: global, capped, 42-byte records (position, attitude, velocity, burn).
	limit := len(i.wrecks)
	if limit > 4 {
		limit = 4
	}
	derelicts := make([]byte, 0, limit*42)
	for _, w := range i.wrecks[:limit] {
		s := &w.model.State
		var d [42]byte
		binary.LittleEndian.PutUint32(d[0:], math.Float32bits(float32(s.Position.X)))
		binary.LittleEndian.PutUint32(d[4:], math.Float32bits(float32(s.Position.Y)))
		binary.LittleEndian.PutUint32(d[8:], math.Float32bits(float32(s.Position.Z)))
		quat := func(offset int, v float64) { binary.LittleEndian.PutUint16(d[offset:], uint16(int16(clamp(v, -1, 1)*32767))) }
		quat(12, s.Attitude.W)
		quat(14, s.Attitude.X)
		quat(16, s.Attitude.Y)
		quat(18, s.Attitude.Z)
		binary.LittleEndian.PutUint32(d[20:], math.Float32bits(float32(s.Velocity.X)))
		binary.LittleEndian.PutUint32(d[24:], math.Float32bits(float32(s.Velocity.Y)))
		binary.LittleEndian.PutUint32(d[28:], math.Float32bits(float32(s.Velocity.Z)))
		d[32] = byte(clamp(w.burn[0], 0, 1) * 255)
		d[33] = byte(clamp(w.burn[1], 0, 1) * 255)
		derelicts = append(derelicts, d[:34]...)
	}
	return map[string]any{"wrecks": derelicts, "cores": cores, "poses": poses}
}

func (i *instance) Events() []map[string]any {
	events := i.events
	i.events = nil
	return events
}

func (i *instance) Finished() (bool, map[string]any) { return i.finished, i.results }

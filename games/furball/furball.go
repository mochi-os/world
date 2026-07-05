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
	"errors"
	"math"

	"world/game"
	"world/games/furball/aircraft"
	"world/games/furball/flight"
)

const (
	altitude = 4572   // 15,000 ft — the merge altitude
	ring     = 2778   // spawn radius (1.5 NM)
	sea      = 3      // world y at which flight ends
	speed    = 220    // spawn airspeed
	fuel     = 3000.0 // spawn fuel load, kg (~half internal)
	health   = 100.0  // gun damage pool
	damage   = 40.0   // per second while guns are on target
	reach    = 700.0  // gun range (m)
	aim      = 0.9986 // cos of the gun cone half-angle (~3°)
	pause    = 5.0    // seconds from death to respawn (furball mode)
)

// Missile constants: a deliberately simple pursuit weapon — enough to make
// the "guns and missiles" rule real; the full seeker/countermeasure model
// belongs to the damage-model phase.
const (
	missile_speed = 700.0  // m/s, constant
	missile_life  = 12.0   // s
	missile_kill  = 30.0   // proximity fuse (m)
	missile_range = 5000.0 // acquisition range (m)
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
	return &instance{
		mode:        mode,
		missiles:    allowed,
		environment: flight.Environment{Seed: session.Seed, Wrap: wrap},
		aircraft:    map[int]*craft{},
	}, nil
}

type craft struct {
	player game.Player
	kind   string // aircraft catalogue name
	model  *flight.Model
	latest flight.Inputs
	alive  bool
	health float64
	wait   float64 // seconds until respawn (furball mode)
	flared float64 // sim seconds since the last flare drop (large when none)
	kills  int
	deaths int
}

type missile struct {
	shooter  int
	target   int
	position flight.Vec3
	velocity flight.Vec3
	life     float64
	number   uint64 // per-instance launch counter, for deterministic decoys
}

type instance struct {
	mode        string // furball (open, endless) or joust (1v1, first kill ends it)
	missiles    bool   // rule: missiles allowed
	environment flight.Environment
	aircraft    map[int]*craft
	flying      []*missile
	launched    uint64
	events      []map[string]any
	finished    bool
	results     map[string]any
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
	core := make([]byte, flight.Size*8)
	for i, w := range words {
		binary.LittleEndian.PutUint64(core[i*8:], math.Float64bits(w))
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
	i.aircraft[player.Slot] = &craft{player: player, kind: kind, model: m, alive: true, health: health, flared: 1e9}
	return map[string]any{"state": state_payload(&m.State), "wrap": i.environment.Wrap, "model": flight.Version, "aircraft": kind}, nil
}

func (i *instance) Leave(player game.Player) {
	delete(i.aircraft, player.Slot)
	// A joust abandoned mid-fight ends in favour of whoever stayed.
	if i.mode == "joust" && !i.finished && len(i.aircraft) == 1 {
		for slot := range i.aircraft {
			i.finish(slot, player.Slot)
		}
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
		Reheat:     flag("reheat"),
		Brake:      flag("brake"),
		Gear:       flag("gear"),
		Hook:       flag("hook"),
		Launch:     flag("launch"),
		Override:   flag("override"),
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
			if in.Missile && a.alive && i.missiles {
				i.launch(slot, a)
			}
		}
		a.latest = input(list[len(list)-1].Data)
	}
	for slot, a := range i.aircraft {
		a.flared += dt
		if !a.alive {
			if i.mode == "joust" {
				continue // no respawns — the match is over
			}
			a.wait -= dt
			if a.wait <= 0 {
				i.spawn(slot, a.model)
				a.alive = true
				a.health = health
				i.events = append(i.events, map[string]any{"kind": "respawn", "slot": slot, "state": state_payload(&a.model.State)})
			}
			continue
		}
		for substep := 0; substep < 4; substep++ {
			a.model.Step(a.latest) // 4 × Dt (1/240) per 60 Hz tick
		}
		// Air-start rule: any surface contact — water, a crash probe, even a
		// gentle touch — is a kill until multiplayer deck operations land.
		state := &a.model.State
		if state.Position.Y <= sea || state.Gear.Touch.Occurred || state.Gear.Contact >= 0 {
			i.kill(slot, -1)
		}
	}
	i.guns(dt)
	i.pursue(dt)
}

// guns applies cone-tracking damage from every firing aircraft.
func (i *instance) guns(dt float64) {
	for slot, a := range i.aircraft {
		if !a.alive || !a.latest.Fire {
			continue
		}
		forward := a.model.State.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
		for other, b := range i.aircraft {
			if other == slot || !b.alive {
				continue
			}
			direction, distance := i.bearing(a.model.State.Position, b.model.State.Position)
			if distance > reach || distance < 1 {
				continue
			}
			if forward.X*direction.X+forward.Y*direction.Y+forward.Z*direction.Z < aim {
				continue
			}
			b.health -= damage * dt
			if b.health <= 0 {
				b.health = 0
				i.kill(other, slot)
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

// launch fires a missile at the best target in the seeker cone.
func (i *instance) launch(slot int, a *craft) {
	forward := a.model.State.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
	best, nearest := -1, missile_range+1
	for other, b := range i.aircraft {
		if other == slot || !b.alive {
			continue
		}
		direction, distance := i.bearing(a.model.State.Position, b.model.State.Position)
		if distance > missile_range || distance < 1 {
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

// pursue advances every missile: pure pursuit toward the target, decoyed by
// a recent flare (deterministic per missile), proximity kill.
func (i *instance) pursue(dt float64) {
	alive := i.flying[:0]
	for _, m := range i.flying {
		m.life -= dt
		target := i.aircraft[m.target]
		if m.life <= 0 || target == nil || !target.alive {
			continue
		}
		// A flare dropped inside the window decoys this missile with an
		// instance-deterministic coin flip (seed and launch number).
		if target.flared < flare_window && (i.environment.Seed+m.number)%3 != 0 {
			i.events = append(i.events, map[string]any{"kind": "decoy", "slot": m.target})
			continue
		}
		direction, distance := i.bearing(m.position, target.model.State.Position)
		if distance < missile_kill {
			i.kill(m.target, m.shooter)
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

func (i *instance) Snapshot(tick uint64) map[string]any {
	players := []map[string]any{}
	cores := map[int]any{}
	for slot, a := range i.aircraft {
		entry := state_payload(&a.model.State)
		cores[slot] = entry["core"]
		delete(entry, "core") // per-recipient: N cores would burst the datagram MTU
		entry["slot"] = slot
		entry["aircraft"] = a.kind
		entry["name"] = a.player.Name
		entry["alive"] = a.alive
		entry["health"] = a.health
		entry["kills"] = a.kills
		entry["deaths"] = a.deaths
		entry["gear"] = a.model.State.Gear.Extension > 0.5
		entry["hook"] = a.latest.Hook
		entry["speedbrake"] = a.model.State.Fcs.Speedbrake
		entry["reheat"] = a.latest.Reheat
		entry["fire"] = a.latest.Fire
		players = append(players, entry)
	}
	missiles := []map[string]any{}
	for _, m := range i.flying {
		missiles = append(missiles, map[string]any{
			"position": []float64{m.position.X, m.position.Y, m.position.Z},
			"velocity": []float64{m.velocity.X, m.velocity.Y, m.velocity.Z},
		})
	}
	return map[string]any{"players": players, "missiles": missiles, "cores": cores}
}

func (i *instance) Events() []map[string]any {
	events := i.events
	i.events = nil
	return events
}

func (i *instance) Finished() (bool, map[string]any) { return i.finished, i.results }

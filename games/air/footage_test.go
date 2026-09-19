// Mochi world: recorded human fights as test fixtures
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Every "recording-derived" opponent in this package is a hand-written script
// calibrated against printed statistics; until this file nothing here could
// read a recording at all. footage parses the client's ACMI flight recorder,
// and replay flies the recorded human OPEN-LOOP as the bandit's opponent, so a
// moment a real pilot actually created can be put back in front of the brain.
//
// Open-loop is the limit of what this can say. The human on the tape was
// reacting to the bot that flew THEN; the moment today's bot departs from that
// track, the tape stops reacting to it. So a scene is short (10-20 s, from both
// jets' recorded states) and asserts what the BRAIN DECIDES when handed the
// picture - which play, whether a reflex pre-empts it, whether it points and
// shoots at what is offered - never whether it out-flew a pursuer who cannot
// see it.
//
// Recordings stay outside the repository: they are a user's own flying.
// AIR_FOOTAGE names the directory (the dev instance's recordings). Unset, or a
// file missing, the scene SKIPS and names what it wanted - so a run must be
// read for skips before its green means anything.

package air

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"world/games/air/flight"
)

// The recorder's projection (apps/air/web/src/game/acmi.ts position()): a flat
// world about Midway, +x east, +z SOUTH. The engine's frame and this package's
// are the same frame - the bandit's state words cross between them unconverted.
const (
	recorded_latitude  = 28.2072
	recorded_longitude = -177.3735
	recorded_degree    = 111320.0
)

// still is one recorded sample of one aircraft, in world metres.
type still struct {
	time     float64
	position flight.Vec3
	forward  flight.Vec3
	up       flight.Vec3
	rounds   float64
	fuel     float64
	burner   float64
}

// footage is a parsed recording: each aircraft's samples in time order, keyed
// by the recorder's object id (1 the ownship, 2 the bandit).
type footage map[int][]still

// view opens a recording by id from AIR_FOOTAGE, or skips the test saying why.
func view(t *testing.T, id string) footage {
	t.Helper()
	root := os.Getenv("AIR_FOOTAGE")
	if root == "" {
		t.Skipf("scene needs recording %s: set AIR_FOOTAGE to the recordings directory", id)
	}
	path := filepath.Join(root, id)
	file, err := os.Open(path)
	if err != nil {
		t.Skipf("scene needs recording %s, absent from AIR_FOOTAGE=%s", id, root)
	}
	defer file.Close()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("recording %s is not gzipped ACMI: %v", id, err)
	}
	reel, err := parse(bufio.NewReaderSize(zipped, 1<<20))
	if err != nil {
		t.Fatalf("recording %s: %v", id, err)
	}
	return reel
}

// parse reads ACMI text. The format is delta-encoded: a field, and each
// component of the T= transform, holds its last value until rewritten, so the
// running state per object is what a sample IS.
func parse(reader *bufio.Reader) (footage, error) {
	type running struct {
		longitude, latitude, altitude, roll, pitch, yaw float64
		rounds, fuel, burner                            float64
		placed                                          bool
	}
	reel := footage{}
	current := map[int]*running{}
	now := 0.0
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if line[0] == '#' {
			stamp, err := strconv.ParseFloat(line[1:], 64)
			if err != nil {
				return nil, fmt.Errorf("bad time %q", line)
			}
			now = stamp
			continue
		}
		comma := strings.IndexByte(line, ',')
		if comma <= 0 || line[0] == '-' {
			continue // a removal, or not an object line
		}
		id64, err := strconv.ParseInt(line[:comma], 16, 32) // ids are HEX: the recorder writes toString(16)
		if err != nil || id64 == 0 {
			continue // id 0 is the header
		}
		id := int(id64)
		if id != 1 && id != 2 {
			continue // missiles and remotes are not part of a scene
		}
		state := current[id]
		if state == nil {
			state = &running{}
			current[id] = state
		}
		for _, field := range strings.Split(line[comma+1:], ",") {
			key, value, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			switch key {
			case "T":
				for n, part := range strings.Split(value, "|") {
					if part == "" {
						continue
					}
					number, err := strconv.ParseFloat(part, 64)
					if err != nil {
						continue
					}
					switch n {
					case 0:
						state.longitude = number
					case 1:
						state.latitude = number
					case 2:
						state.altitude = number
					case 3:
						state.roll = number
					case 4:
						state.pitch = number
					case 5:
						state.yaw = number
					}
				}
				state.placed = true
			case "Rounds":
				state.rounds, _ = strconv.ParseFloat(value, 64)
			case "FuelWeight":
				state.fuel, _ = strconv.ParseFloat(value, 64)
			case "Afterburner":
				state.burner, _ = strconv.ParseFloat(value, 64)
			}
		}
		if !state.placed {
			continue
		}
		yaw, pitch, roll := state.yaw*math.Pi/180, state.pitch*math.Pi/180, state.roll*math.Pi/180
		// Heading 0 is north (-z), 90 east (+x): yaw = atan2(forward.x, -forward.z).
		forward := flight.Vec3{X: math.Sin(yaw) * math.Cos(pitch), Y: math.Sin(pitch), Z: -math.Cos(yaw) * math.Cos(pitch)}
		level := flight.Vec3{Y: 1}.Subtract(forward.Scale(forward.Y))
		if level.Length() < 1e-6 {
			level = flight.Vec3{X: 1}
		}
		level = level.Normalize()
		right := forward.Cross(level).Normalize()
		// The recorder's roll is atan2(right.y, up.y), so POSITIVE is right wing UP:
		// rolling the wings-level up toward the right wing by -roll recovers it.
		up := level.Scale(math.Cos(roll)).Subtract(right.Scale(math.Sin(roll)))
		reel[id] = append(reel[id], still{
			time: now,
			position: flight.Vec3{
				X: (state.longitude - recorded_longitude) * recorded_degree * math.Cos(recorded_latitude*math.Pi/180),
				Y: state.altitude,
				Z: (recorded_latitude - state.latitude) * recorded_degree, // +z is south
			},
			forward: forward, up: up, rounds: state.rounds, fuel: state.fuel, burner: state.burner,
		})
	}
	return reel, scanner.Err()
}

// at reads an aircraft's state at an instant: position on a cubic Hermite
// through the samples (central-difference velocities), so both the position and
// the velocity the brain perceives are smooth. Piecewise-linear would hand its
// track filter a velocity that steps nine times a second.
func (reel footage) at(id int, when float64) (position, velocity, forward, up flight.Vec3, sample still, firing, found bool) {
	samples := reel[id]
	if len(samples) < 4 || when < samples[1].time || when > samples[len(samples)-2].time {
		return
	}
	low, high := 1, len(samples)-2
	for high-low > 1 {
		middle := (low + high) / 2
		if samples[middle].time <= when {
			low = middle
		} else {
			high = middle
		}
	}
	a, b := samples[low], samples[low+1]
	slope := func(n int) flight.Vec3 {
		before, after := samples[n-1], samples[n+1]
		return after.position.Subtract(before.position).Scale(1 / math.Max(after.time-before.time, 1e-6))
	}
	span := math.Max(b.time-a.time, 1e-6)
	s := (when - a.time) / span
	va, vb := slope(low), slope(low+1)
	h00, h10 := 2*s*s*s-3*s*s+1, s*s*s-2*s*s+s
	h01, h11 := -2*s*s*s+3*s*s, s*s*s-s*s
	position = a.position.Scale(h00).Add(va.Scale(h10 * span)).Add(b.position.Scale(h01)).Add(vb.Scale(h11 * span))
	d00, d10 := (6*s*s-6*s)/span, 3*s*s-4*s+1
	d01, d11 := (-6*s*s+6*s)/span, 3*s*s-2*s
	velocity = a.position.Scale(d00).Add(va.Scale(d10)).Add(b.position.Scale(d01)).Add(vb.Scale(d11))
	forward = a.forward.Scale(1 - s).Add(b.forward.Scale(s)).Normalize()
	up = a.up.Scale(1 - s).Add(b.up.Scale(s)).Normalize()
	// The belt stepping down across THIS interval is the trigger, held for the
	// whole interval: at one tick per sample the brain's perception would miss it.
	return position, velocity, forward, up, a, b.rounds < a.rounds, true
}

// glimpse is what a scene sees each tick.
type glimpse struct {
	second   float64 // since the scene opened
	mode     string
	play     string
	firing   bool
	distance float64
	pointed  float64 // degrees between the bandit's nose and the line to the human
	trailed  float64 // degrees between the line to the human and the bandit's own tail: small means he is on its six
	faced    float64 // degrees between the HUMAN's nose and the line to the bandit
	asked    float64 // the g the brain commanded after its cap
	afforded float64 // the g this pilot's aero cap allows at this speed: what a whole-hearted pull would ask for
	speed    float64 // m/s
}

// replay opens a scene: the bandit spawned at its recorded state, the human
// flown open-loop from the tape into slot 0 exactly as Bandit.Mirror would
// place him, for `length` seconds from `start`.
func replay(t *testing.T, reel footage, level string, seed uint64, start, length float64, watch func(glimpse)) {
	t.Helper()
	position, velocity, forward, up, sample, _, found := reel.at(2, start)
	if !found {
		t.Fatalf("the recording has no bandit at t=%.1f", start)
	}
	b := NewBandit(level, seed, 250000, "", false, true, "fox2", sample.fuel)
	b.Stage(evaluating, doctrine.omit) // AIR_STAGE, AIR_OMIT: the scenes judge whichever brain is under evaluation
	b.Spawn(position, velocity)
	b.craft.model.State.Attitude = flight.Basis(forward, up)
	human := b.arena.aircraft[0]
	for tick := 1; tick <= int(length*60); tick++ {
		when := start + float64(tick)/60
		his, moving, nose, crown, frame, firing, found := reel.at(1, when)
		if !found {
			t.Fatalf("the recording has no ownship at t=%.1f", when)
		}
		human.model.State.Position, human.model.State.Velocity = his, moving
		human.model.State.Attitude = flight.Basis(nose, crown)
		for n := range human.model.State.Engine {
			human.model.State.Engine[n] = flight.EngineState{Spool: 1, Reheat: frame.burner}
		}
		human.latest.Fire = firing
		human.alive = true
		b.Menace(nil)
		fire, _, _, _, _ := b.Step()
		me := &b.craft.model.State
		sight := his.Subtract(me.Position)
		distance := math.Max(sight.Length(), 1)
		line := sight.Scale(1 / distance)
		angle := func(a, b flight.Vec3) float64 { return math.Acos(clamp(a.Dot(b), -1, 1)) * 180 / math.Pi }
		tail := me.Velocity.Normalize().Scale(-1)
		watch(glimpse{
			second: float64(tick) / 60, mode: b.craft.brain.mode, play: b.craft.brain.play, firing: fire,
			distance: distance, pointed: angle(me.Attitude.Rotate(flight.Vec3{X: 1}), line),
			trailed: angle(line, tail), faced: angle(nose, line.Scale(-1)), asked: b.craft.brain.demand.Capped,
			afforded: b.craft.brain.skill.capped(b.craft.brain.skill.pull, me.Velocity.Length(), corner(b.craft.model)/math.Sqrt(b.craft.model.Airframe.Limit.Positive)),
			speed:    me.Velocity.Length(),
		})
	}
}

// TestFootageReadsTheRecorder pins the reader against the recorder's own
// conventions with a hand-written tape: no recording needed.
func TestFootageReadsTheRecorder(t *testing.T) {
	east := recorded_longitude + 1000/(recorded_degree*math.Cos(recorded_latitude*math.Pi/180))
	south := recorded_latitude - 500/recorded_degree
	tape := strings.Join([]string{
		"FileType=text/acmi/tacview", "FileVersion=2.2", "0,ReferenceTime=2026-09-17T00:00:00Z",
		"#0",
		fmt.Sprintf("1,T=%.7f|%.7f|3000|0|0|90,Name=FA-18C,Rounds=578,FuelWeight=4000", recorded_longitude, recorded_latitude),
		fmt.Sprintf("2,T=%.7f|%.7f|3100|-30|10|0,Name=FA-18C", east, south),
		"a4,T=1|1|1|0|0|0,Name=AIM-9M", // a missile: hex id, ignored
		"#0.1",
		"1,T=||3010|||,Rounds=570", // delta: only the altitude and the belt moved
	}, "\n")
	reel, err := parse(bufio.NewReader(strings.NewReader(tape)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reel[1]) != 2 || len(reel[2]) != 1 || len(reel) != 2 {
		t.Fatalf("samples: ownship %d, bandit %d, objects %d", len(reel[1]), len(reel[2]), len(reel))
	}
	own, other := reel[1][0], reel[2][0]
	if math.Abs(own.position.X) > 1e-6 || math.Abs(own.position.Z) > 1e-6 || own.position.Y != 3000 {
		t.Fatalf("the ownship at the reference point read %+v", own.position)
	}
	if math.Abs(other.position.X-1000) > 0.5 || math.Abs(other.position.Z-500) > 0.5 || other.position.Y != 3100 {
		t.Fatalf("1,000 m east and 500 m SOUTH read %+v: +z is south", other.position)
	}
	// Heading 90 is east, +x.
	if own.forward.X < 0.999 {
		t.Fatalf("heading 090 read forward %+v, want +x", own.forward)
	}
	// Heading 0 is north, -z, and 10 degrees of pitch lifts the nose.
	if other.forward.Z > -0.98 || math.Abs(other.forward.Y-math.Sin(10*math.Pi/180)) > 1e-6 {
		t.Fatalf("heading 000 pitched 10 up read forward %+v", other.forward)
	}
	// The recorder's roll is atan2(right.y, up.y): read back, the attitude must
	// give the same number, or every bank in every scene is mirrored.
	right := other.forward.Cross(other.up)
	if roll := math.Atan2(right.Y, other.up.Y) * 180 / math.Pi; math.Abs(roll+30) > 1e-6 {
		t.Fatalf("a recorded roll of -30 came back as %.3f", roll)
	}
	// The delta frame keeps what it did not rewrite.
	if later := reel[1][1]; later.position.Y != 3010 || later.forward.X < 0.999 || later.rounds != 570 || later.fuel != 4000 {
		t.Fatalf("the delta frame lost state: %+v", later)
	}
}

func TestFootageInterpolatesSmoothly(t *testing.T) {
	reel := footage{1: {}}
	for n := 0; n < 12; n++ {
		when := float64(n) * 0.12
		reel[1] = append(reel[1], still{time: when, position: flight.Vec3{X: 200 * when, Y: 3000 + 5*when*when},
			forward: flight.Vec3{X: 1}, up: flight.Vec3{Y: 1}})
	}
	position, velocity, _, _, _, _, found := reel.at(1, 0.5)
	if !found {
		t.Fatal("no state inside the tape")
	}
	if math.Abs(position.X-100) > 0.01 || math.Abs(position.Y-(3000+5*0.25)) > 0.01 {
		t.Fatalf("position at 0.5 s read %+v", position)
	}
	if math.Abs(velocity.X-200) > 0.01 || math.Abs(velocity.Y-5) > 0.05 {
		t.Fatalf("velocity at 0.5 s read %+v, want 200 along and 5 up", velocity)
	}
	if _, _, _, _, _, _, found := reel.at(1, 99); found {
		t.Fatal("a time past the tape must not be found")
	}
}

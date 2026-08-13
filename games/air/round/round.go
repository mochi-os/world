// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Package round is the AIM-120C's flight and guidance core (#27 phase 2):
// one integrator consumed by the server's missiles, the client's
// single-player rounds through the wasm, and the launch-zone arithmetic.
// The numbers come from the public record — the Zaretto AIM-120C-5
// performance assessment (CFD over declassified references), the US
// munitions hazard classification's propellant mass — and the acceptance
// tests in round_test.go pin the model to that record's measured ranges,
// burnout speeds, loft profiles and turn-capability collapse. Guidance is
// the real employment shape: command-inertial midcourse toward a datalinked
// prediction with an energy-buying loft, an active seeker from 18 km, and a
// proportional-navigation endgame. Seeker countermeasures (chaff, jamming,
// notching) are deliberately NOT here — defeat is kinematic until the tasks
// that own them land.
package round

import (
	"math"

	"world/games/air/flight"
)

// The missile, per the public record.
const (
	Mass       = 157.0   // kg at launch (AIM-120C-5)
	Propellant = 51.26   // kg, burned linearly over the boost
	Burn       = 7.75    // s — the all-boost WPU-16/B
	Thrust     = 16700.0 // N, constant across the burn
	Reference  = 0.4     // m² — the drag reference area PAIRED with the Cd table below
	Lift       = 0.173   // m² — lift area (CLmax·S): sized so 30 g runs out below ~700 m/s at medium altitude, matching the CFD
	Induced    = 0.45    // m² — the induced-drag area: drag-due-to-lift is QUADRATIC (lift² / (q·Induced)), so a 1 g level-hold costs almost nothing while a 30 g pull bleeds hard, sized for a max-AoA lift-to-drag near 2.5
	Structure  = 30 * 9.80665 // m/s² — the structural lateral ceiling
	Activation = 18000.0 // m — the seeker goes active (HUSKY) at this estimated range
	Terminal   = 9000.0  // m — the seeker holds its own track (PITBULL) inside this
	Gimbal     = 0.5     // cos 60°: the seeker's look cone about the velocity vector
	Battery    = 100.0   // s — thermal battery life, the default Life
	Fuse       = 20.0    // m — proximity fuse trigger, sized to the 22 kg warhead it carries (battle's Radar class fragments to ~24 m; the trigger sits just inside)
	Arming     = 1.5     // s — the fuse arms this long after launch
	Warhead    = 22.0    // kg — blast-fragmentation class, consumed by battle
)

// Chaff (#29). A bloomed cloud stops within seconds, so a pulse-doppler
// seeker rejects it on velocity — UNLESS the defender is beaming: with the
// defender's radial velocity inside the clutter notch, jet and cloud sit in
// the same range-doppler cell and the gate has nothing to discriminate on.
// So the seduction is DOPPLER-GATED and deterministic: chaff without the
// beam does essentially nothing, chaff in the notch takes the seeker every
// time — do it right and it works, which is the doctrine (beam the threat,
// then dispense). The deceived seeker flies at the hanging cloud; the hold
// releases as the round reaches it (or times out), and the normal
// reacquisition basket decides what happens next — a defender who stayed in
// the cone and left the notch gets reacquired late, which is chaff buying
// time and geometry rather than immunity.
const (
	Notch   = 60.0   // m/s — the defender's radial speed in the seeker's frame must sit inside this
	Hold    = 2.5    // s — how long the seeker stares at the cloud before the velocity gate shakes it off
	Window  = 2.0    // s — a bloom older than this has dispersed below a useful cell (callers stop offering it)
	Resolve = 2500.0 // m — inside this the seeker RESOLVES the jet from its cloud: the skin return dominates the cell and no bloom seduces, however perfect the beam. Chaff is a mid-game defence (the historical record shows no close-range chaff defeats of active seekers); the endgame belongs to the notch and geometry.
)

// The jammer (#31). Deception jamming attacks TRACKS — the victim radar's
// gate is stolen and walked off — so its effects live in the radar and
// datalink layers, not here. What lives here is the cost: HOME-ON-JAM is a
// submode of the seeker, and a radiating target is a beacon offering
// perfect angle at any range — no datalink needed, no notch to hide in, no
// cloud loud enough to matter. But angle is ALL it offers: no range, no
// closing velocity, so the round flies degraded pursuit at the noise
// rather than a lead collision. Burnthrough is the duel's other bound: the
// skin echo beats the jammer as geometry closes, so inside it the victim
// radar sees through and jamming is pure liability.
const Burnthrough = 9000.0 // m — inside this the echo wins and jamming stops working on the radar

// Guidance phases, in flight order.
const (
	Midcourse = iota // command inertial toward the datalinked prediction, loft available
	Active           // HUSKY: the seeker is on and hunting; datalink still corrects the estimate
	Pitbull          // the seeker holds the target: self-guiding, the supporter is free
	Loose            // no estimate worth flying and nothing in the seeker: ballistic, fuse live
)

// Target is a position and velocity pair: a datalink estimate or the truth.
type Target struct {
	Position flight.Vec3
	Velocity flight.Vec3
}

// Model is one round in flight. Positions are metres, velocities m/s, world
// +Y up. Wrap is the toroidal world size (0 = infinite), applied to every
// relative vector like the rest of the game.
type Model struct {
	Position flight.Vec3
	Velocity flight.Vec3
	Time     float64 // seconds since launch
	Life     float64 // seconds of battery remaining
	Fuel     float64 // propellant remaining, kg
	Phase    int
	Loft     bool // the loft autopilot (off for VISUAL/boresight shots)
	Wrap     float64

	Least    float64     // closest approach to the truth seen so far, m (0 = never measured)
	Beacon   bool        // the target is RADIATING this step (#31): the seeker homes on the jam — set by the caller, who owns emission truth
	Estimate flight.Vec3 // where the round believes the target is (datalinked, then coasted)
	Drift    flight.Vec3 // the estimate's velocity
	Stale    float64     // seconds since the last datalink update

	sight flight.Vec3 // last seeker line-of-sight unit vector (the PN rate measurement)
	moved flight.Vec3 // last seen target velocity (the augmentation's acceleration estimate)
	accel flight.Vec3 // estimated target acceleration, averaged across samples
	since float64     // time since the last distinct velocity sample
	seen  bool        // moved holds a real sample
	held  float64     // seconds left staring at a chaff cloud (0 = clean)
}

// Distract offers the seeker a chaff bloom. It takes only when the seeker
// is looking (Active or Pitbull), not already deceived, and the DEFENDER is
// in the notch — radial velocity in the seeker's frame inside the clutter
// gate. A hot or cold target is trivially rejected on doppler, which is why
// dispensing without the beam is wasted chaff. On seduction the cloud
// becomes the track: near-stationary, sinking gently, and immune to both
// datalink correction and the seeker's own truth capture until the hold
// releases.
func (m *Model) Distract(bloom flight.Vec3, truth Target) bool {
	if m.Phase != Active && m.Phase != Pitbull {
		return false
	}
	if m.held > 0 {
		return false // already on a cloud
	}
	sight := m.relative(m.Position, truth.Position)
	reach := sight.Length()
	if reach < Resolve || reach > Activation {
		return false // too close: the seeker resolves the jet from the cloud — burn-through, chaff's version
	}
	if math.Abs(truth.Velocity.Dot(sight.Scale(1/reach))) > Notch {
		return false // out of the notch: the velocity gate rejects the cloud outright
	}
	m.Phase = Active
	m.held = Hold
	m.Estimate = bloom
	m.Drift = flight.Vec3{Y: -1} // the cloud hangs, sinking
	m.Stale = 0
	return true
}

// New launches a round: the shooter's position and velocity (the round
// separates at aircraft speed; the launch mechanics — rail push or ejector
// punch — are the caller's, already applied to velocity), and the initial
// target estimate. A nil estimate is a VISUAL shot: the seeker is active
// immediately and takes whatever it finds.
func New(position flight.Vec3, velocity flight.Vec3, estimate *Target, wrap float64) *Model {
	m := &Model{Position: position, Velocity: velocity, Life: Battery, Fuel: Propellant, Wrap: wrap, Loft: true}
	if estimate != nil {
		m.Estimate = estimate.Position
		m.Drift = estimate.Velocity
	} else {
		m.Phase = Active
		m.Loft = false
	}
	return m
}

// relative is to - from across the wrap (0 = infinite world).
func relative(from flight.Vec3, to flight.Vec3, wrap float64) flight.Vec3 {
	if wrap <= 0 {
		return to.Subtract(from)
	}
	return flight.Vec3{
		X: flight.Shortest(from.X, to.X, wrap),
		Y: to.Y - from.Y,
		Z: flight.Shortest(from.Z, to.Z, wrap),
	}
}

func (m *Model) relative(from flight.Vec3, to flight.Vec3) flight.Vec3 {
	return relative(from, to, m.Wrap)
}

// Range is the distance to the current estimate.
func (m *Model) Range() float64 { return m.relative(m.Position, m.Estimate).Length() }

// Step advances the round by dt seconds. support carries a fresh datalink
// estimate when the shooter's radar holds the track this tick (nil when
// unsupported); truth is the target's actual state for the seeker phases —
// the caller owns the world. Returns false once the round is spent (battery
// dead or below the floor); the fuse is the caller's, via Fused.
func (m *Model) Step(dt float64, support *Target, truth *Target) bool {
	if m.Life <= 0 {
		return false
	}
	m.Time += dt
	m.Life -= dt

	// A deceived seeker stares at its cloud: no datalink correction, no
	// truth capture, until the hold releases. HOW it releases matters. A
	// timeout is the velocity gate shaking the dispersing cloud off at
	// range — the seeker returns to its basket and may reacquire (chaff
	// bought seconds). Reaching the cloud is the overshoot: the round flew
	// through a target that was never there, the seeker cannot look behind
	// its own gimbal, and there is no coming back from that — ballistic,
	// fuse live. Without this, a high-energy round would orbit the stale
	// point until the departing defender's LOS geometry swung out of the
	// notch, then legally reacquire and re-attack: the overshoot is what
	// makes terminal chaff a DEFEAT rather than a two-second inconvenience.
	if m.held > 0 {
		m.held -= dt
		if m.Range() < 150 {
			m.held = 0
			m.Phase = Loose
		}
	}

	// Datalink: a fresh estimate replaces the coasted one; otherwise the
	// prediction flies on under its own remembered velocity.
	if support != nil && m.Phase <= Active && m.held <= 0 {
		m.Estimate = support.Position
		m.Drift = support.Velocity
		m.Stale = 0
	} else {
		m.Estimate = m.Estimate.Add(m.Drift.Scale(dt))
		m.Stale += dt
	}

	// Phase transitions. The seeker turns on at the activation range against
	// the ESTIMATE — a coasted estimate still schedules activation, which is
	// how a shot survives a dropped track. It holds the target (Pitbull)
	// once the truth sits inside the look cone at seeker range.
	speed := m.Velocity.Length()
	if speed < 1 {
		speed = 1
	}
	forward := m.Velocity.Scale(1 / speed)

	// HOME-ON-JAM (#31): a radiating target is the loudest thing in the sky.
	// The beacon overrides everything — the chaff hold (no cloud outshines
	// it), the notch (angle needs no doppler), the datalink (angle needs no
	// track), even the activation schedule (the jam is receivable at any
	// range). But angle is ALL it gives: the round flies pursuit at the
	// noise, no lead, no loft — and the moment the target goes quiet, the
	// seeker is back to its ordinary rules with whatever geometry the chase
	// left it. The toggle stays a live decision for the whole flight.
	if m.Beacon && truth != nil {
		m.held = 0
		sight := m.relative(m.Position, truth.Position)
		reach := sight.Length()
		unit := sight.Scale(1 / math.Max(reach, 1))
		// Bearing-only PN: the strobe gives a clean angle RATE, so the round
		// still leads the turn — what it cannot do without range or closing
		// velocity is compute a lead-collision point or a loft, and the gain
		// scales off its own speed rather than the true closure. Dangerous
		// against anyone not defending hard; a max-performance extension can
		// still bleed it dry. Naive pure pursuit was tried first and died
		// every time — a crossing defender outran it as it hairpinned.
		command := unit.Subtract(forward.Scale(unit.Dot(forward))).Scale(speed / steering)
		if m.sight.Length() > 0.5 {
			rate := unit.Subtract(m.sight).Scale(1 / dt)
			rate = rate.Subtract(unit.Scale(rate.Dot(unit)))
			command = command.Add(rate.Scale(3 * speed))
		}
		m.Estimate = truth.Position
		m.Drift = truth.Velocity
		m.Stale = 0
		m.sight = unit // also keeps the PN state fresh so a beacon-drop hands over cleanly
		if m.Phase == Midcourse || m.Phase == Loose {
			m.Phase = Active // the seeker is on the jam; a ballistic round recovers ONLY through the beacon
		}
		return m.integrate(dt, command, speed, forward)
	}
	if m.Phase == Midcourse && m.Range() <= Activation {
		m.Phase = Active
	}
	// The generalised overshoot: an ACTIVE round arriving at a STALE
	// estimate — no datalink refreshing it, no seeker capture correcting it
	// — has flown through a point nothing occupies, and the seeker cannot
	// look behind its gimbal. Without this the round ORBITS the stale point
	// (a chaff cloud it timed out on, a dead prediction) until something
	// blunders into the circle. A fresh estimate is different: the target
	// is genuinely there and the flythrough fuses on proximity.
	if m.Phase == Active && m.Stale > 1 && m.Range() < 150 {
		m.Phase = Loose
	}
	if m.Phase == Active && truth != nil && m.held <= 0 {
		sight := m.relative(m.Position, truth.Position)
		reach := sight.Length()
		unit := sight.Scale(1 / math.Max(reach, 1))
		// ACQUISITION respects the clutter notch: a searching seeker cannot
		// pick up a target with near-zero radial velocity — that return sits
		// in the rejection gate with the ground and the chaff. An
		// ESTABLISHED track is different: Pitbull holds through the notch
		// (ECCM track memory), so beaming alone never breaks a lock here —
		// deeper notch fidelity is the EW task's, not this file's. The pair
		// of rules is exactly what makes the doctrine work: chaff steals the
		// established track, and the released seeker cannot re-acquire a
		// defender who STAYS in the beam.
		if reach <= Activation && unit.Dot(forward) >= Gimbal && math.Abs(truth.Velocity.Dot(unit)) > Notch {
			// HUSKY: the seeker sees the target and becomes the estimate's
			// source — datalink no longer matters. PITBULL at terminal range.
			m.Estimate = truth.Position
			m.Drift = truth.Velocity
			m.Stale = 0
			if reach <= Terminal {
				m.Phase = Pitbull
				m.sight = unit
			}
		}
	}

	// Guidance: the commanded lateral acceleration.
	var command flight.Vec3
	switch m.Phase {
	case Pitbull:
		if truth == nil {
			m.Phase = Loose
			break
		}
		sight := m.relative(m.Position, truth.Position)
		reach := sight.Length()
		unit := sight.Scale(1 / math.Max(reach, 1))
		if unit.Dot(forward) < Gimbal {
			m.Phase = Loose // the target beat the look cone: ballistic
			break
		}
		// AUGMENTED proportional navigation, N=4. Plain PN nulls the
		// line-of-sight rate, which is enough against a straight target but
		// leaves a steady miss against a turning one: the round is always
		// correcting for a manoeuvre the target has already made, and the
		// residual grows with the target's acceleration and the square of
		// the time to go. The augmentation feeds the target's own lateral
		// acceleration forward at half the navigation gain — the textbook
		// term, and the difference between a graze and a kill against a
		// defending fighter.
		closing := truth.Velocity.Subtract(m.Velocity).Dot(unit) * -1
		if closing < 0 {
			closing = 0
		}
		rate := unit.Subtract(m.sight).Scale(1 / dt)
		m.sight = unit
		gain := 4 * math.Max(closing, speed*0.3)
		command = rate.Scale(gain)
		// The target's acceleration, estimated over the time the sample
		// actually spans. The caller may hand the same truth to several
		// slices (it samples the world once a frame), so dividing a frame's
		// whole velocity change by a slice would inflate it by the frame's
		// worth of slices and fly the round on noise. A fighter's own limit
		// clamps the estimate, and an average across samples keeps a jittery
		// feed from steering.
		m.since += dt
		if !m.seen {
			m.moved, m.seen, m.since = truth.Velocity, true, 0
		} else if change := truth.Velocity.Subtract(m.moved); change.Length() > 1e-6 && m.since > 1e-6 {
			sample := change.Scale(1 / m.since)
			if pull := sample.Length(); pull > 9*9.80665 {
				sample = sample.Scale(9 * 9.80665 / pull)
			}
			m.accel = m.accel.Scale(0.5).Add(sample.Scale(0.5))
			m.moved, m.since = truth.Velocity, 0
		}
		lateral := m.accel.Subtract(unit.Scale(m.accel.Dot(unit))) // only the part that bends the target's path
		command = command.Add(lateral.Scale(2))                   // N/2
	case Midcourse, Active:
		// Command inertial: fly a lead collision against the estimate — two
		// fixed-point passes on time-to-go — with the loft's climb bias
		// while far out. This is what makes the shooter's crank work: the
		// round needs no line of sight, only the datalinked prediction.
		sight := m.relative(m.Position, m.Estimate)
		reach := sight.Length()
		predicted := m.Estimate
		for i := 0; i < 2; i++ {
			togo := m.relative(m.Position, predicted).Length() / speed
			predicted = m.Estimate.Add(m.Drift.Scale(togo))
		}
		desired := m.relative(m.Position, predicted).Normalize()
		if m.Loft && m.Phase == Midcourse {
			// The energy-buying climb: full angle far out, fading to level
			// by the activation gate, applied above the line of sight and
			// capped at the flight-path ceiling.
			angle := loft * math.Min(1, math.Max(0, (reach-fade)/(full-fade)))
			flat := math.Hypot(desired.X, desired.Z)
			if angle > 0 && flat > 1e-6 {
				pitch := math.Min(math.Atan2(desired.Y, flat)+angle, climb)
				desired = flight.Vec3{X: desired.X / flat * math.Cos(pitch), Y: math.Sin(pitch), Z: desired.Z / flat * math.Cos(pitch)}
			}
		}
		// Steer the velocity vector toward the desired direction.
		across := desired.Subtract(forward.Scale(desired.Dot(forward)))
		command = across.Scale(speed / steering)
	case Loose:
		// Ballistic: gravity and drag only.
	}

	return m.integrate(dt, command, speed, forward)
}

// integrate applies one slice of physics for a commanded lateral
// acceleration: the shared tail of every guidance mode, including the
// home-on-jam pursuit that bypasses the phase machine entirely.
func (m *Model) integrate(dt float64, command flight.Vec3, speed float64, forward flight.Vec3) bool {
	// The lateral channel, capped by structure and by what the air gives.
	density, sound := atmosphere(m.Position.Y)
	dynamic := 0.5 * density * speed * speed
	mass := Mass - Propellant + m.Fuel
	ceiling := Ceiling(speed, m.Position.Y, mass)
	lateral := command.Subtract(forward.Scale(command.Dot(forward))) // guidance never commands along the axis
	demand := lateral.Length()
	if demand > ceiling {
		lateral = lateral.Scale(ceiling / demand)
		demand = ceiling
	}

	// The axial channel: thrust while the motor burns, parasite drag off the
	// Mach table (the plume fills the base while burning), induced drag
	// charged for every newton of turning.
	axial := 0.0
	if m.Fuel > 0 {
		axial += Thrust / mass
		m.Fuel = math.Max(0, m.Fuel-Propellant/Burn*dt)
	}
	mach := speed / sound
	parasite := drag(mach) * dynamic * Reference
	if m.Fuel > 0 {
		parasite *= 0.85
	}
	induced := 0.0
	if dynamic > 1 {
		induced = (demand * mass) * (demand * mass) / (dynamic * Induced)
	}
	axial -= (parasite + induced) / mass

	acceleration := forward.Scale(axial).Add(lateral).Add(flight.Vec3{Y: -9.80665})
	m.Velocity = m.Velocity.Add(acceleration.Scale(dt))
	m.Position = m.Position.Add(m.Velocity.Scale(dt))
	if m.Wrap > 0 {
		m.Position.X = flight.Shortest(0, m.Position.X, m.Wrap)
		m.Position.Z = flight.Shortest(0, m.Position.Z, m.Wrap)
	}
	return true
}

// Slice is the round's own integration stride. A guidance loop sampled at a
// render frame's rate flies a visibly worse missile — at Mach 2.5 a 60 Hz
// step jumps 17 m between corrections, and the terminal miss grows from
// metres to tens of metres — so every caller's dt is diced into slices no
// longer than this. It also makes a flight frame-rate independent, which the
// server needs and a player deserves.
const Slice = 1.0 / 240

// Advance steps the round across dt at the fixed Slice, checking the fuse on
// each slice (a burst is a sub-frame event: sampling only the frame boundary
// walks straight through the envelope). Returns whether the round lives on,
// whether the warhead fired, and where it burst.
func (m *Model) Advance(dt float64, support *Target, truth *Target) (bool, bool, flight.Vec3) {
	// The caller samples the world once per frame, so the target is FROZEN
	// across the slices unless we fly it too: at a merge closing 700 m/s a
	// 20 Hz frame leaves the truth 35 m stale, and the endgame resolves
	// against a ghost. Each slice advances the target along its own
	// velocity — straight flight is the best knowledge anyone has inside a
	// frame — and the same for the datalink's estimate.
	var moving, feed *Target
	if truth != nil {
		copied := *truth
		moving = &copied
	}
	if support != nil {
		copied := *support
		feed = &copied
	}
	alive := true
	for left := dt; left > 1e-9 && alive; {
		step := math.Min(Slice, left)
		left -= step
		alive = m.Step(step, feed, moving)
		if moving != nil {
			if fired, burst := m.Fused(step, *moving); fired {
				return alive, true, burst
			}
			moving.Position = moving.Position.Add(moving.Velocity.Scale(step))
		}
		if feed != nil {
			feed.Position = feed.Position.Add(feed.Velocity.Scale(step))
		}
	}
	return alive, false, flight.Vec3{}
}

// Fused reports whether the proximity fuse fires against this truth over the
// step just taken, and where the burst goes. It solves the CLOSEST APPROACH
// within the step rather than sampling the endpoint: at Mach 2.5 the round
// crosses 40 m per 60 Hz frame, so a sampled range walks straight through
// the fuse envelope and the warhead never speaks — and when it does speak,
// it must burst where the round actually passed the target, not where the
// frame happened to land. The burst point is the caller's blast origin.
func (m *Model) Fused(dt float64, truth Target) (bool, flight.Vec3) {
	if m.Time < Arming {
		return false, flight.Vec3{}
	}
	relative := m.relative(m.Position, truth.Position)
	closing := truth.Velocity.Subtract(m.Velocity)
	// Rewind to the step's start and walk forward to the minimum of the
	// separation: the pair moved linearly across it.
	back := relative.Subtract(closing.Scale(dt))
	square := closing.Dot(closing)
	at := 0.0
	if square > 1e-9 {
		at = math.Max(0, math.Min(dt, -back.Dot(closing)/square))
	}
	nearest := back.Add(closing.Scale(at))
	if miss := nearest.Length(); miss < m.Least || m.Least == 0 {
		m.Least = miss // the flight's closest approach: the endgame's own report card
	}
	if nearest.Length() > Fuse {
		return false, flight.Vec3{}
	}
	// The burst: the round's own position at that instant.
	return true, m.Position.Subtract(m.Velocity.Scale(dt - at))
}

// Ceiling is the lateral acceleration available at a state: the structural
// limit, or what the air gives through the lift area — whichever runs out
// first. This collapse with falling speed IS range-dependent escapability.
func Ceiling(speed float64, altitude float64, mass float64) float64 {
	density, _ := atmosphere(altitude)
	return math.Min(Structure, Lift*0.5*density*speed*speed/mass)
}

// Guidance shaping. steering is the velocity-steering time constant; the
// loft climbs at up to `loft` radians above the line of sight (never past
// the `climb` flight-path ceiling), fading between `full` and `fade` metres
// of range-to-go. Tuned against the acceptance gates, not hand-shaped.
const (
	steering = 1.2
	loft     = 28 * math.Pi / 180
	climb    = 45 * math.Pi / 180
	full     = 20000.0
	fade     = 12000.0
)

// drag is the Cd(Mach) table paired with the 0.4 m² reference: the classic
// transonic story — low subsonic, a sharp peak at the sound barrier, a slow
// supersonic decay — matching the CFD's shape and the record's ranges.
func drag(mach float64) float64 {
	points := [...][2]float64{{0, 0.016}, {0.85, 0.016}, {1.0, 0.045}, {1.5, 0.036}, {2.0, 0.030}, {3.0, 0.025}, {4.0, 0.022}, {6.0, 0.020}}
	if mach <= points[0][0] {
		return points[0][1]
	}
	for i := 1; i < len(points); i++ {
		if mach <= points[i][0] {
			a, b := points[i-1], points[i]
			t := (mach - a[0]) / (b[0] - a[0])
			return a[1] + t*(b[1]-a[1])
		}
	}
	return points[len(points)-1][1]
}

// atmosphere is the ISA in two segments: density and the speed of sound at
// altitude. Enough sky for any loft; the stratosphere is isothermal.
func atmosphere(altitude float64) (float64, float64) {
	h := math.Max(0, altitude)
	var temperature, pressure float64
	if h <= 11000 {
		temperature = 288.15 - 0.0065*h
		pressure = 101325 * math.Pow(temperature/288.15, 5.2561)
	} else {
		temperature = 216.65
		pressure = 22632 * math.Exp(-9.80665*(h-11000)/(287.05*216.65))
	}
	density := pressure / (287.05 * temperature)
	return density, math.Sqrt(1.4 * 287.05 * temperature)
}

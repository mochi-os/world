// Mochi world: Air game module
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Air multiplayer: air combat over the atoll with the real blade-element
// flight model (package flight) and server-authoritative weapons. Two modes:
// "furball" — long-running, anyone joins or leaves at any time, endless
// respawns (the standing match uses this); "joust" — 1v1, ends the moment one
// player is destroyed. Creators set weather and rules through the session
// parameters: "tod", "clouds" (relayed to clients in the welcome) and
// "missiles" (enforced here). Matches are air-start; deck operations stay
// single-player for now, so ANY surface contact in multiplayer is a kill.
package air

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/battle"
	"world/games/air/flight"
	"world/games/air/round"
)

const (
	altitude = 4572 // 15,000 ft — the merge altitude
	ring     = 2778 // spawn radius (1.5 NM)
	// The bots' world is SEA LEVEL ONLY, deliberately (2026-07-29). flight.World
	// carries Fields (island height, coast outline, paved strips) and a Carrier,
	// and none of it is populated here: every bot flies against a flat sea.
	//
	// That is accurate on the maps we ship. Midway's island tops out at 3.5 m, the
	// runway at ~5 m and the tallest mast at ~12 m, while the brain's own recovery
	// floor works in 400-900 m margins - so modelling the relief would move nothing
	// it can measure. What the flat model costs is avoidance of VERTICAL clutter
	// (masts, the carrier superstructure), and that is covered on the consequence
	// side instead: the client tests the bandit against the same terrain, buildings,
	// masts and deck the player is tested against, so a bot that hits one dies.
	//
	// Revisit when a map with real relief lands. The tell is a bot holding a level
	// break straight into rising ground - the floor will read its height above the
	// SEA and be perfectly happy.
	sea      = 3      // world y at which flight ends
	speed    = 220    // spawn airspeed
	fuel     = 2722.0 // default spawn fuel load, kg (~6,000 lb — the session may choose otherwise)
	pause    = 5.0    // seconds from death to respawn (air mode)
	rounds   = 578    // M61 magazine per life
	rate     = 100.0  // rounds per second (6,000 rpm)
	muzzle   = 6.53   // m forward of the datum — the M61 port on the nose top, matching the client's tracer origin
	derelict = 30.0   // s a pilot-dead/ejected wreck keeps flying
)

// The BVR spawn block (#32): separation is DERIVED from the round's own
// ladder, never hand-picked — head-on Rmax at the spawn state plus the
// commit buffer (the P-825 shape: employment plus fifteen nautical miles of
// organisation room). Geometry provides the pre-fight seconds, so there is
// no countdown and no weapons hold: the closing run IS the commit phase,
// and a flight-model retune moves the match geometry automatically
// (TestSeparation pins the relationship). Spaced open respawns use half the
// joust separation; anchored team walls use all of it.
const (
	bvraltitude = 6096.0  // m (20,000 ft)
	bvrspeed    = 272.0   // m/s (~M0.85 at the block): organised, fighting speed
	commit      = 27780.0 // m (15 nmi): organisation room beyond head-on Rmax
)

var separation = sync.OnceValue(func() float64 {
	ladder := round.Ladder(
		round.Target{Position: flight.Vec3{Y: bvraltitude}, Velocity: flight.Vec3{X: bvrspeed}},
		round.Target{Position: flight.Vec3{X: 60000, Y: bvraltitude}, Velocity: flight.Vec3{X: -bvrspeed}},
		0,
	)
	return ladder.Max + commit
})

// Missile constants: pursuit guidance with an aspect-aware seeker, graded
// flare decoys, and a real proximity fuse feeding the battle warhead.
// The AIM-9M (#126): a boost-coast point mass flying PROPORTIONAL NAVIGATION
// off its seeker's line-of-sight rate — a collision intercept, not a chase.
// The seeker has a gimbal cone and a track-rate ceiling; the airframe has a
// g limit that fades with airspeed and pays every turn in induced drag; the
// fuse arms after launch separation. Kills come from energy and geometry,
// and so do the escapes: beam it while it is slow and it cannot follow.
const (
	missile_boost  = 3.0    // s, Mk 36 burn
	missile_thrust = 260.0  // m/s² net boost acceleration (ΔV ≈ +780)
	missile_dragk  = 5.0e-5 // base drag: dv/dt = -k·v² (about -45 m/s² at Mach 2.7)
	missile_life   = 20.0   // s battery/coolant; energy death usually wins first
	missile_fuse   = 12.0   // m, proximity fuse envelope (battle grades the warhead inside)
	missile_arm    = 0.6    // s of flight before the fuse (and the guidance) arms
	missile_range  = 5000.0 // rear-aspect acquisition range (m); weak head-on
	missile_cone   = 0.866  // cos of the acquisition half-angle (~30°)
	missile_gimbal = 0.766  // cos of the ±40° seeker gimbal — beyond it the lock breaks
	missile_track  = 0.35   // rad/s seeker track ceiling (~20°/s) — beam it to saturate
	missile_g      = 35.0   // structural lateral limit, g
	missile_n      = 3.5    // the proportional-navigation constant
	flare_window   = 0.8    // s after a flare drop in which it can decoy
	flare_reject   = 0.55   // the 9M's counter-countermeasures: flares work this fraction as often
)

type Air struct{}

func New() *Air { return &Air{} }

func (f *Air) Name() string     { return "air" }
func (f *Air) Rate() (int, int) { return 60, 20 }

func (f *Air) Create(session game.Session) (game.Instance, error) {
	wrap := 250000.0
	if v, found := session.Parameters["wrap"]; found {
		// 0 disables wrapping; anything else must exceed the play area by a
		// wide margin. A tiny wrap once sent flight.Shortest into ~1e12
		// normalization iterations on the first Step and hung the session
		// goroutine forever (Shortest is loop-free now, but a sub-arena wrap
		// is still geometric nonsense).
		if n, valid := v.(float64); valid && (n == 0 || n >= 10000) {
			wrap = n
		}
	}
	mode := session.Mode
	if mode != "joust" && mode != "teams" {
		mode = "furball"
	}
	weapons, _ := session.Parameters["weapons"].(string)
	if weapons != "guns" && weapons != "fox2" && weapons != "open" {
		// Old clients send only the boolean; derive the class it always meant.
		if allowed, _ := session.Parameters["missiles"].(bool); allowed {
			weapons = "open"
		} else {
			weapons = "guns"
		}
	}
	i := &instance{
		mode:        mode,
		weapons:     weapons,
		missiles:    weapons != "guns",
		environment: flight.Environment{Seed: session.Seed, Wrap: wrap},
		aircraft:    map[int]*craft{},
	}
	if mode == "teams" {
		i.score = map[string]int{"red": 0, "blue": 0}
	}
	// Practice bots (#81 verification / solo flying): server-flown aircraft on
	// scripted gentle manoeuvres, filling slots from the TOP so the framework's
	// low-slot assignment for real players cannot collide in practice. Open
	// mode only — a joust is strictly the human pair.
	if clouds, _ := session.Parameters["clouds"].(string); clouds != "" {
		i.sky = clouds
	}
	if tod, _ := session.Parameters["tod"].(string); tod == "night" {
		i.night = true
	}
	if start, _ := session.Parameters["start"].(string); start == "bvr" && mode == "joust" {
		i.apart = separation() // the BVR joust (#32): the pair opens the full derived distance apart
	}
	if spaced, _ := session.Parameters["spaced"].(bool); spaced && mode != "joust" {
		i.apart = separation() // spaced/anchored spawns (#32): open respawns at half this, team anchors the full width
	}
	if cheats, found := session.Parameters["cheats"].(map[string]any); found {
		i.cheat.invulnerable, _ = cheats["invulnerable"].(bool)
		i.cheat.ammunition, _ = cheats["ammunition"].(bool)
		i.cheat.fuel, _ = cheats["fuel"].(bool)
		i.environment.Cheat.Fuel = i.cheat.fuel // the flight core itself freezes the tank — every model made from this environment (bots here, humans at Join) inherits it, and the client's wasm core mirrors it from the welcome
	}
	i.tank = fuel
	if pounds := number(session.Parameters, "fuel"); pounds > 0 {
		i.tank = clamp(pounds/2.2046, 500, 4900) // the UI speaks pounds like the IFEI; the sim burns kilograms
	}
	// Practice bots: the parameter is a per-level count map {"drone": n,
	// "novice": n, ...} — the match creator chooses how many of each. A bare
	// number still means drones (test-harness convenience). Bots fill slots
	// from 99 downward, grouped by level, named for the kill feed. A joust
	// stays strictly the pair — but the pair may include bots (settled
	// 2026-08-13): exactly two combatants, not two humans. Join's own
	// len(aircraft) >= 2 check counts the bots, so a full bot pair refuses
	// human joiners and a single bot leaves one seat. Head-on placement
	// falls out of the slot-parity angle in bvr(): 99 is odd, so the first
	// bot faces slot 0, and a second bot at 98 takes the opposite end.
	{
		type order struct {
			level string
			count int
			team  string
		}
		wanted := []order{}
		gather := func(levels map[string]any, team string) {
			for _, level := range []string{"drone", "novice", "pilot", "ace", "superhuman"} {
				if n := int(number(levels, level)); n > 0 {
					wanted = append(wanted, order{level, n, team})
				}
			}
		}
		if flat := number(session.Parameters, "bots"); flat > 0 {
			wanted = append(wanted, order{"drone", int(flat), ""})
		} else if levels, found := session.Parameters["bots"].(map[string]any); found {
			// Teams mode places bots per side ({red:{level:n}, blue:{...}});
			// a flat per-level map still works anywhere and alternates sides.
			sided := false
			for _, team := range []string{"red", "blue"} {
				if per, found := levels[team].(map[string]any); found {
					gather(per, team)
					sided = true
				}
			}
			if !sided {
				gather(levels, "")
			}
		}
		// Bots live ABOVE the players' range (#257). They fill the slot space
		// downward while joining players are assigned upward from zero out of
		// the server's own player map, which cannot see them — so a roster
		// large enough, or a session roomy enough, put the two populations on
		// the same slot, and Join overwrote unconditionally: every joiner past
		// the first silently deleted one of the creator's bots. Starting the
		// bots above the capacity makes the collision impossible by
		// construction rather than by hoping the numbers stay small. The
		// classic 99-downward numbering is kept wherever it still fits, which
		// is every ordinary session.
		want := 0
		for _, w := range wanted {
			want += w.count
		}
		if want > 99 {
			want = 99
		}
		if mode == "joust" && want > 2 {
			want = 2 // the pair, whoever fills it
		}
		startable := mode == "joust" && want == 2 // a full bot pair: nobody will ever Join, so the match starts at creation (below, once both exist)
		// Against the SERVER's budget, not just this session's. Ninety-nine
		// was a per-session clamp, and sessions are created by an
		// unauthenticated POST: a hundred of them is ~9900 server-flown
		// aircraft, every one costing CPU on this machine with nobody
		// connected. bots_reserve grants what is left and no more, so a flood
		// gets emptier and emptier matches instead of the whole machine.
		want = bots_reserve(want)
		i.bots = want
		top := 99
		if session.Capacity+want > 100 {
			top = session.Capacity + want - 1
		}
		slot, total := top, 0
		single := map[string]int{} // per side: the fighting bot still waiting for a pair partner
		for _, w := range wanted {
			// Bounded by what was actually granted, not the fixed 99: the
			// reservation above is only a reservation if the loop honours it.
			for n := 1; n <= w.count && total < want; n++ {
				team := w.team
				if mode != "teams" {
					team = "" // sides exist only in the teams mode
				} else if team == "" {
					team = []string{"red", "blue"}[total%2] // sideless counts in a team match: alternate
				}
				m := flight.New(aircraft.Get("fa18c"), i.environment, flight.World{Sea: sea})
				i.spawn(slot, m, team)
				title := fmt.Sprintf("%s%s %d", string(w.level[0]-32), w.level[1:], n)
				if team != "" {
					title = fmt.Sprintf("%s%s %s", string(team[0]-32), team[1:], title)
				}
				b := &craft{player: game.Player{Name: title, Slot: slot}, kind: "fa18c", model: m, alive: true, flared: 1e9, clouded: 1e9, bot: true, brain: mind(w.level), team: team, loadout: bots_loadout(i.weapons), lock: -1}
				b.arm()
				b.rearm()
				i.aircraft[slot] = b
				// Wingman pairs (#140): consecutive fighting bots on a side fly
				// as a section, for the instance's life. Levels are created in
				// ladder order, so the later (lower-slot) member is the senior —
				// he leads. An odd bot stays single and attaches to a human lead.
				if b.brain != nil && team != "" {
					if p, found := single[team]; found {
						b.brain.mate = p
						i.aircraft[p].brain.mate = slot
						delete(single, team)
					} else {
						single[team] = slot
					}
				}
				slot--
				total++
			}
		}
		if startable && len(i.aircraft) == 2 {
			// A full bot pair fills the joust at creation: nobody will ever
			// Join, so the start Join performs happens here. The pair was
			// spawned at its positions this instant — no respawn needed,
			// nobody sat frozen in the waiting room.
			i.started = true
			if i.apart > 0 {
				i.merged = true
				i.events = append(i.events, map[string]any{"kind": "fighton"})
			}
		}
	}
	return i, nil
}

type craft struct {
	player     game.Player
	kind       string       // aircraft catalogue name
	bot        bool         // server-flown practice aircraft: scripted inputs, no link
	brain      *brain       // fighting bots think; drones (and humans) carry nil
	close      map[int]bool // this recipient's sticky near set (interest hysteresis)
	model      *flight.Model
	body       battle.Body
	condition  battle.Condition
	latest     flight.Inputs
	alive      bool
	ammunition int         // gun rounds left this life
	spent      int         // rounds fired this life, cumulative (#258): under the ammunition cheat the magazine refills, so the counter alone can no longer answer "how much did he shoot" — this can
	charge     float64     // fractional rounds accumulated at the fire rate
	missiles   int         // heat-seekers left this life (the human magazine; fighting bots run their own b.missiles discipline)
	amraams    int         // AIM-120s left this life (#27): its own magazine, in the amraams() firing order
	loadout    loadout     // the granted per-station loadout (#17): humans from the clamped join request, bots their standard
	release    float64     // sim seconds since this craft's last missile left the rail (large when none)
	ejected    bool        // eject edge consumed this life
	wait       float64     // seconds until respawn (air mode)
	flared     float64     // sim seconds since the last flare drop (large when none)
	cloud      flight.Vec3 // where the last chaff bloomed (#29): the mixed program drops chaff with every flare
	clouded    float64     // sim seconds since that bloom (large when none)
	loud       bool        // the jammer is RADIATING this tick (#31): armed (latest.Jammer) and painted by a threat
	team       string      // "red"/"blue" in the teams mode, "" otherwise
	kills      int
	deaths     int
	emitter    int // radar emitter state (#30): 0 silent, 1 search, 2 STT — client-reported, relayed in every pose record
	lock       int // the STT'd slot when emitter is 2, -1 otherwise
}

// hostile reports whether two craft may engage each other: everyone in the
// modes without sides, the other team when sides exist.
func hostile(a, b *craft) bool {
	return a.team == "" || b.team == "" || a.team != b.team
}

// audible reports whether anyone human flies on this craft's side — the
// flavour radio (#146: TALLY, rejoin) speaks only when someone can hear it;
// the urgent calls carry their own human-victim gates.
func (i *instance) audible(a *craft) bool {
	if a.team == "" {
		return false
	}
	for _, slot := range i.slots() {
		if c := i.aircraft[slot]; c != nil && !c.bot && c.team == a.team {
			return true
		}
	}
	return false
}

// Team reports the side a slot flies for, "" outside the teams mode — the
// session's chat scoping asks (#84). Tick-goroutine only, like every other
// instance call.
func (i *instance) Team(slot int) string {
	if a := i.aircraft[slot]; a != nil {
		return a.team
	}
	return ""
}

// nearest_mate finds the closest living teammate, or nil (always nil outside
// the teams mode). Friendly positions are radio truth — no perception model.
func (i *instance) nearest_mate(slot int, a *craft) *craft {
	if a.team == "" {
		return nil
	}
	var best *craft
	gap := math.MaxFloat64
	for _, other := range i.slots() {
		mate := i.aircraft[other]
		if other == slot || mate == nil || !mate.alive || mate.model == nil || mate.team != a.team {
			continue
		}
		if _, span := i.bearing(a.model.State.Position, mate.model.State.Position); span < gap {
			best, gap = mate, span
		}
	}
	return best
}

// leader resolves the aircraft a bot holds formation on between engagements
// (#140): the assigned pair lead for a wingman, or — for the odd bot a roster
// leaves unpaired — the senior human teammate, so the bot flies the player's
// wing. Leads themselves get nil (their wing formates on them), as do
// solo-flagged control bots and anyone whose partner is dead or gone.
func (i *instance) leader(slot int, a *craft) *craft {
	b := a.brain
	if a.team == "" || b == nil || b.solo {
		return nil
	}
	if b.mate >= 0 {
		if slot < b.mate {
			return nil // I lead the pair
		}
		if mate := i.aircraft[b.mate]; mate != nil && mate.alive && mate.model != nil {
			return mate
		}
		return nil
	}
	for _, other := range i.slots() {
		c := i.aircraft[other]
		if other != slot && c != nil && !c.bot && c.alive && c.model != nil && c.team == a.team {
			return c
		}
	}
	return nil
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
	a.spent = 0 // a fresh life, a fresh expenditure record
	a.charge = 0
	// The magazine is the granted loadout's round count in the SMS firing
	// order (#17). Fighting bots keep their own two-round b.missiles
	// discipline regardless: the doctrine battery is calibrated against it.
	if a.loadout != nil {
		a.missiles = len(stores_rounds(a.loadout))
		a.amraams = len(stores_amraams(a.loadout))
	} else {
		a.missiles = 4
		a.amraams = 0
	}
	a.release = 1e9
	a.ejected = false
}

type missile struct {
	sight    flight.Vec3 // last line-of-sight unit vector (the PN rate measurement)
	burn     float64     // boost seconds remaining
	flew     float64     // seconds since launch (fuse arming)
	lure     flight.Vec3 // a swallowed flare's fall point; guided at while blind
	blind    float64     // seconds left chasing the lure (then ballistic)
	loose    bool        // lock broken (gimbal/track/energy): ballistic, fuse still live
	shooter  int
	target   int
	position flight.Vec3
	velocity flight.Vec3
	life     float64
	number   uint64       // per-instance launch counter, for deterministic decoys
	window   bool         // a flare window has been judged (one decoy roll per flare)
	called   bool         // a teammate has called this launch to its victim (#146): one call per round, ever
	radar    *round.Model // AIM-120 (#27 phase 2c): the shared core flies it; nil for the 9M, which keeps the model above
}

// wreck is a pilot-dead or ejected airframe that keeps flying until it hits
// something or burns out; purely spectacle, no further scoring.
type wreck struct {
	model *flight.Model
	burn  [2]float64
	life  float64
}

type instance struct {
	rehearsals  int     // arbiter re-plans spent this tick (#256): the allowance that bounds the rollout cost by construction
	mode        string  // furball (open, endless) or joust (1v1, first kill ends it)
	started     bool    // joust: false until BOTH players are present — the first joiner is held frozen at the ring, and the pair merges fresh together
	merged      bool    // joust: weapons hold until the MERGE — either aircraft crossing the other's 3/9 line (x < -margin in the other's body frame); one-shot, announced with a "fighton" event
	missiles    bool    // rule: any missiles allowed (weapons != "guns" — kept for the gates that only care about that much)
	weapons     string  // the loadout class rule (#32): "guns" | "fox2" | "open"
	apart       float64 // BVR spawn separation, m (0 = the merge ring): the joust's BVR start, and the spaced/anchored spawn distance
	tank        float64 // spawn fuel load, kg (the session's "fuel" parameter, in pounds on the wire)
	environment flight.Environment
	sky         string // session cloud preset (bot visibility occlusion)
	night       bool   // session time of day (bot visual range and glare)
	cheat       struct {
		invulnerable bool // humans take no weapon damage; bots still do (crashes still kill)
		ammunition   bool // guns and missiles never deplete — humans and bots alike
		fuel         bool // the tank never depletes — humans and bots alike
	}
	aircraft map[int]*craft
	flying   []*missile
	rounds   []battle.Round // gun rounds in flight: spawned by guns(), flown and resolved by fly() (#real-TOF)
	wrecks   []*wreck
	launched uint64
	events   []map[string]any
	finished bool
	results  map[string]any
	score    map[string]int // teams mode: running team score (enemy kill +1, teammate kill -1)
	warned   map[int]uint64 // last BREAK call per menaced human slot (#139): one warning per victim per few seconds, whoever calls it

	stepped    uint64         // the tick Step is on, so handlers off the Step path (Jettison) share its clock
	jettisoned map[int]uint64 // last jettison tick per slot: the cooldown's clock, same shape as warned

	bots int // practice bots reserved from the server-wide budget, released by Close
}

// BOTS_MAXIMUM is how many practice bots this SERVER will fly at once, across
// every session. Each is a full aircraft stepped at 60 Hz, and a session is
// created by an unauthenticated POST, so without a server-wide ceiling the
// per-session clamp of 99 multiplied by the 100-session cap: thousands of
// simulated jets on one machine with nobody connected.
//
// Sized for the practice cases the feature exists for — a solo pilot against a
// handful of bots, or a full furball — several times over, while staying a
// small fraction of what the machine could be asked for before.
const BOTS_MAXIMUM = 200

var bots_live atomic.Int64

// bots_reserve grants up to want bots from the server-wide budget, returning
// how many were actually granted (zero when the budget is spent). Reserve then
// trim rather than check then reserve: two concurrent Creates must not both
// pass the last slot.
func bots_reserve(want int) int {
	if want <= 0 {
		return 0
	}
	live := bots_live.Add(int64(want))
	if over := live - BOTS_MAXIMUM; over > 0 {
		if over >= int64(want) {
			bots_live.Add(-int64(want))
			return 0
		}
		bots_live.Add(-over)
		return want - int(over)
	}
	return want
}

// bots_release returns a reservation to the budget.
func bots_release(count int) {
	if count > 0 {
		bots_live.Add(-int64(count))
	}
}

// Close releases this match's share of the bot budget. The server calls it
// once, when the session ends (game.Closer).
func (i *instance) Close() {
	bots_release(i.bots)
	i.bots = 0
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
// head-on (the joust pair); later slots spread by the golden angle. Sides
// spawn in opposing 120° arcs — red centred on one bearing, blue opposite —
// so a team match opens as a line-abreast wall meeting a wall.
func (i *instance) spawn(slot int, m *flight.Model, team string) {
	if i.apart > 0 && i.bvr(slot, m, team) {
		return
	}
	angle := float64(slot) * math.Pi
	if slot > 1 {
		angle = float64(slot) * 2.399963
	}
	if team != "" {
		base := 0.0
		if team == "blue" {
			base = math.Pi
		}
		angle = base + (math.Mod(float64(slot)*2.399963, 2)-1)*math.Pi/3
	}
	position := flight.Vec3{X: math.Cos(angle) * ring, Y: altitude, Z: math.Sin(angle) * ring}
	inward := flight.Vec3{X: -math.Cos(angle), Y: 0, Z: -math.Sin(angle)}
	m.State = flight.Level(m, position, inward, speed, i.tank)
}

// bvr places a spawn for the separated match shapes (#32) and reports
// whether it did: the joust pair head-on across the full derived separation;
// anchored team walls, each side line abreast at its own anchor facing the
// other's; and spaced open respawns re-entering half the separation from the
// fight's centre of mass, approaching it — every life begins with a commit
// under the RWR rather than a merge. An empty open room falls back to the
// merge ring (nothing to space from).
func (i *instance) bvr(slot int, m *flight.Model, team string) bool {
	radius := i.apart / 2
	if team != "" {
		base := 0.0
		if team == "blue" {
			base = math.Pi
		}
		inward := flight.Vec3{X: -math.Cos(base), Y: 0, Z: -math.Sin(base)}
		across := flight.Vec3{X: -inward.Z, Z: inward.X}
		position := flight.Vec3{X: math.Cos(base) * radius, Y: bvraltitude, Z: math.Sin(base) * radius}
		position = position.Add(across.Scale(float64((slot%9)-4) * 500)) // line abreast, not a queue
		m.State = flight.Level(m, position, inward, bvrspeed, i.tank)
		return true
	}
	if i.mode == "joust" {
		angle := float64(slot) * math.Pi
		position := flight.Vec3{X: math.Cos(angle) * radius, Y: bvraltitude, Z: math.Sin(angle) * radius}
		inward := flight.Vec3{X: -math.Cos(angle), Y: 0, Z: -math.Sin(angle)}
		m.State = flight.Level(m, position, inward, bvrspeed, i.tank)
		return true
	}
	centre, count := flight.Vec3{}, 0
	for other, b := range i.aircraft {
		if other == slot || !b.alive || b.model == nil {
			continue
		}
		centre = centre.Add(b.model.State.Position)
		count++
	}
	if count == 0 {
		return false // an empty room: the merge ring is fine
	}
	centre = centre.Scale(1 / float64(count))
	angle := float64(slot) * 2.399963 // golden-angle bearings: re-entries spread, never queue up a lane
	away := flight.Vec3{X: math.Cos(angle), Z: math.Sin(angle)}
	position := flight.Vec3{X: centre.X + away.X*radius, Y: bvraltitude, Z: centre.Z + away.Z*radius}
	if i.environment.Wrap > 0 {
		position.X = flight.Shortest(0, position.X, i.environment.Wrap)
		position.Z = flight.Shortest(0, position.Z, i.environment.Wrap)
	}
	m.State = flight.Level(m, position, away.Scale(-1), bvrspeed, i.tank)
	return true
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
		if i == 57+flight.Elements+flight.Channels+4 { // Pitchwash, signed rad/s: map ±1.5 onto the unit interval
			v = v/3 + 0.5
		}
		if i == 57+flight.Elements+flight.Channels+8 { // External fuel, kg — same scale as Loss
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

// Occupied reports a slot already flying (game.Occupancy, #257). Bots take
// the slot space from the top down and the server assigns joining players
// from the bottom up out of its own player map, which cannot see them — so
// without this every joiner past the first landed on a bot, Join overwrote
// it unconditionally, and the creator's roster quietly evaporated as people
// arrived. Nothing panicked, which is what made it hard to see from outside.
func (i *instance) Occupied(slot int) bool { return i.aircraft[slot] != nil }

func (i *instance) Join(player game.Player) (map[string]any, error) {
	if i.finished {
		return nil, errors.New("over")
	}
	if i.aircraft[player.Slot] != nil {
		// Belt and braces with Occupied above: never overwrite a live
		// aircraft, whoever chose the slot. A refused join is recoverable;
		// a silently deleted bot is not.
		return nil, errors.New("slot taken")
	}
	if i.mode == "joust" && len(i.aircraft) >= 2 {
		return nil, errors.New("full")
	}
	// The airframe, granted like the loadout below: a valid request is
	// honoured, anything else flies the default. The requested type would
	// arrive on the join payload (the empty request is today's only case);
	// route whatever it carries through Grant, never Get — an unknown name
	// through Get hands flight.New a nil to dereference, and that panic
	// takes the whole session down.
	kind, airframe := aircraft.Grant("")
	team := ""
	if i.mode == "teams" {
		// Pick-on-join: honour a valid choice, assign anything else to the
		// smaller side (red on a tie — deterministic, and the client suggests
		// the smaller side anyway so the server assignment is the fallback).
		team = player.Team
		if team != "red" && team != "blue" {
			red, blue := 0, 0
			for _, b := range i.aircraft {
				switch b.team {
				case "red":
					red++
				case "blue":
					blue++
				}
			}
			team = "red"
			if blue < red {
				team = "blue"
			}
		}
	}
	m := flight.New(airframe, i.environment, flight.World{Sea: sea})
	i.spawn(player.Slot, m, team)
	// The requested loadout, validated and clamped against the match's
	// missiles rule (#17): the granted result spawns and is what everyone is
	// told about; the client's persisted choice is never echoed back.
	a := &craft{player: player, kind: kind, model: m, alive: true, flared: 1e9, clouded: 1e9, team: team, loadout: stores_grant(player.Stores, i.weapons)}
	a.arm()
	a.rearm()
	i.aircraft[player.Slot] = a
	// The full roster on every join: names, sides, AND loadouts for bots and
	// humans alike (the transport's own player list carries no team, so a
	// late joiner would otherwise never learn the sides already in the
	// fight; the loadout is how remotes render each other's stores).
	for _, slot := range i.slots() {
		b := i.aircraft[slot]
		i.events = append(i.events, map[string]any{"kind": "roster", "slot": slot, "name": b.player.Name, "team": b.team, "stores": b.loadout})
	}
	if i.mode == "joust" && len(i.aircraft) == 2 && !i.started {
		// The pair is complete THIS instant: the match starts, and both merge
		// fresh and simultaneously however long the first arrival sat frozen.
		i.started = true
		if i.apart > 0 {
			// BVR start (#32): no merge hold — the derived separation is the
			// organisation phase, and the fighton releases both clients now.
			i.merged = true
			i.events = append(i.events, map[string]any{"kind": "fighton"})
		}
		for _, slot := range i.slots() {
			b := i.aircraft[slot]
			i.spawn(slot, b.model, b.team)
			b.model.State.Damage = flight.DamageState{}
			b.arm()
			b.rearm()
			b.alive = true
			i.events = append(i.events, map[string]any{"kind": "respawn", "slot": slot, "state": state_payload(&b.model.State)})
		}
	}
	waiting := i.mode == "joust" && !i.started
	welcome := map[string]any{"state": state_payload(&a.model.State), "wrap": i.environment.Wrap, "model": flight.Version, "aircraft": kind, "waiting": waiting, "mode": i.mode}
	if i.mode == "teams" {
		welcome["team"] = team
		welcome["score"] = map[string]any{"red": i.score["red"], "blue": i.score["blue"]}
	}
	return welcome, nil
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
		Trim:       clamp(number(data, "trim"), -1, 1),
		Lean:       clamp(number(data, "lean"), -1, 1),
		Reset:      flag("reset"),
		Flap:       clamp(number(data, "flap"), 0, 2),
		Brake:      flag("brake"),
		Gear:       flag("gear"),
		Probe:      flag("probe"),
		Hook:       flag("hook"),
		Launch:     flag("launch"),
		Override:   flag("override"),
		Dump:       flag("dump"),
		Secure:     [2]bool{flag("port"), flag("starboard")}, // per-engine fuel OFF: engine 0 is the port core
		Eject:      flag("eject"),
		Fire:       flag("fire"),
		Flare:      flag("flare"),
		Missile:    flag("missile"),
		Radar:      flag("radar"),
		Jammer:     flag("jammer"),
		Sequence:   uint32(number(data, "sequence")),
	}
}

func clamp(v float64, low float64, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

func number(data map[string]any, key string) float64 {
	var v float64
	switch n := data[key].(type) {
	case float64:
		v = n
	case float32:
		v = float64(n)
	case uint64:
		v = float64(n)
	case int64:
		v = float64(n)
	default:
		return 0
	}
	// Reject non-finite wire numbers centrally (#174): a hostile client can
	// CBOR-encode NaN or ±Inf, and clamp() (math.Min/Max) passes NaN straight
	// through into the AUTHORITATIVE flight state — attitude, velocity,
	// snapshots, sort comparisons — corrupting the whole session from one
	// datagram. Inf survives a clamped field but not the ungated ones
	// (sequence, the fuel/bots session parameters), so bar both. Zero is the
	// safe neutral for every consumer: a clamped control centres, a >0-gated
	// parameter falls through to its default.
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func (i *instance) Step(tick uint64, inputs map[int][]game.Input) {
	dt := 1.0 / 60
	i.stepped = tick // Jettison runs on this goroutine but off this call: it reads the clock here
	i.rehearsals = 0 // the tick's arbiter allowance (#256)
	for slot, list := range inputs {
		a := i.aircraft[slot]
		if a == nil || len(list) == 0 {
			continue
		}
		previous := a.latest // edges span the batch boundary: a held button is one press, not one per sample
		for _, sample := range list {
			in := input(sample.Data)
			// Flare and missile are EDGES with server-side cooldowns, never
			// levels: as levels, every input sample fired (up to 64/tick), so
			// a hostile client had unlimited missiles and an amplified
			// reliable-broadcast flare storm. Bots bypass this path entirely
			// (their own discipline in bot.go), so their behaviour is untouched.
			if in.Flare && !previous.Flare && a.alive && a.flared > 0.5 {
				a.flared = 0
				// The mixed program (#29): every dispense is a flare AND a
				// chaff bloom — one magazine, both effects, and the round
				// package's doppler gate decides whether the chaff matters.
				a.cloud = a.model.State.Position
				a.clouded = 0
				i.events = append(i.events, map[string]any{"kind": "flare", "slot": slot})
			}
			if in.Missile && !previous.Missile && a.alive && i.missiles && i.free() && a.missiles > 0 && a.release > 1.0 {
				if i.launch(slot, a) {
					if !i.cheat.ammunition {
						a.missiles--
						a.model.Stores(a.attach()) // the departed round's bit clears in the SMS firing order (#17)
					}
					a.release = 0
				}
			}
			// The AIM-120 (#27 phase 2c) is its own trigger on the wire: a
			// separate magazine, a separate edge, and a shot that needs the
			// shooter's own radar rather than the 9M's rear-aspect cone.
			if in.Radar && !previous.Radar && a.alive && i.missiles && i.free() && a.amraams > 0 && a.release > 1.0 {
				if i.fox3(slot, a) {
					if !i.cheat.ammunition {
						a.amraams--
						a.model.Stores(a.attach())
					}
					a.release = 0
				}
			}
			if in.Eject && a.alive && !a.ejected {
				a.ejected = true
				i.eject(slot, a)
			}
			previous = in
		}
		a.latest = input(list[len(list)-1].Data)
	}
	if i.mode == "joust" && !i.started {
		return // waiting room: no physics until the opponent arrives (the lone jet hangs frozen at the ring; Join starts the match and merges both fresh)
	}
	for _, slot := range i.slots() {
		if a := i.aircraft[slot]; a.bot && a.alive {
			if a.brain != nil {
				i.think(slot, a, tick) // the fighting brain (bot.go)
			} else {
				weave(slot, a, tick) // drones keep the original wander
			}
		}
	}
	// Jammer radiation truth (#31): armed AND painted — a hostile STT
	// holding this craft, or an active radar round hunting it. The armed
	// state is the pilot's standing decision; whether it radiates right now
	// follows the threat picture, so forgetting it armed is a carried risk
	// rather than a constant beacon.
	for slot, a := range i.aircraft {
		a.loud = false
		if !a.alive || !a.latest.Jammer {
			// Bots pass through the same truth (settled 2026-08-13: same
			// equipment, same trade-offs) — their latest.Jammer stays false
			// until the brain arms it, so today this line changes nothing,
			// and when the top tier learns the emission trade its radiation
			// will cost it exactly what a human's costs.
			continue
		}
		for other, b := range i.aircraft {
			if other != slot && b.alive && b.emitter == 2 && b.lock == slot {
				a.loud = true
				break
			}
		}
		if !a.loud {
			for _, m := range i.flying {
				if m.radar != nil && m.target == slot && m.radar.Phase >= round.Active {
					a.loud = true
					break
				}
			}
		}
	}
	for _, slot := range i.slots() {
		a := i.aircraft[slot]
		a.flared += dt
		a.clouded += dt
		a.release += dt
		if !a.alive {
			if i.mode == "joust" {
				continue // no respawns — the match is over
			}
			a.wait -= dt
			if a.wait <= 0 {
				if a.model == nil { // the previous airframe left as a wreck
					_, respawned := aircraft.Grant(a.kind) // kind is data, so Grant: Get's nil would panic in flight.New
					a.model = flight.New(respawned, i.environment, flight.World{Sea: sea})
				}
				i.spawn(slot, a.model, a.team)
				a.model.State.Damage = flight.DamageState{} // a fresh jet
				a.arm()
				a.rearm() // full grant again, tanks refilled — every spawn is a fresh request/clamp cycle (#17)
				if a.brain != nil {
					a.brain.reborn()
				}
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
		for _, event := range battle.Advance(&a.body, a.model, a.latest.Throttle, a.latest.Secure, 60, i.environment.Seed, uint64(slot), tick) {
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
	i.fly(dt, tick)
	i.pursue(dt, tick)
	i.drift(dt)
}

// armed maps the bot brain's REMAINING discipline count to the attach mask
// over the armed standard loadout: the brain fires `shots` rounds in the SMS
// order and the other rounds stay carried (bot.go calls this on every
// launch; the discipline itself is #25's re-sweep). Loadout-less crafts'
// attach() shares it as the legacy fallback.
func armed(count int) uint64 {
	fired := shots - count
	if fired < 0 {
		fired = 0
	}
	return stores_mask(bots_loadout("fox2"), fired, 0)
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
				"kind":     "splash",
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
		if i.cheat.ammunition {
			// Infinite AMMUNITION, not a gun that stops counting (#258). The
			// burst is deducted and the magazine topped straight back up, so
			// the limiter stays live and expenditure stays observable — the
			// recorder and the debrief now treat rounds as ground truth
			// (#238), and a frozen counter feeds them a flat line that reads
			// as "never fired" rather than "cannot say".
			a.spent += burst
			a.ammunition = rounds
		}
		state := &a.model.State
		shooter := battle.Pose{
			Position: state.Position.Add(state.Attitude.Rotate(flight.Vec3{X: muzzle})),
			Forward:  state.Attitude.Rotate(flight.Vec3{X: 1}),
			Up:       state.Attitude.Rotate(flight.Vec3{Y: 1}),
			Right:    state.Attitude.Rotate(flight.Vec3{Z: 1}),
			Velocity: state.Velocity,
		}
		// Rounds fly REAL time of flight now: the volley enters the world and
		// fly() resolves each round against wherever its victims actually are
		// when it reaches them — so a break after the trigger is a defence,
		// which the instant model could never offer.
		i.rounds = append(i.rounds, battle.Volley(shooter, burst, i.environment.Seed, uint64(slot), tick)...)
	}
}

// fly advances every gun round one tick and resolves arrivals: each round is
// tested against each living aircraft's CURRENT pose over this tick's step,
// so the target's manoeuvring since the trigger genuinely moves it out of (or
// into) the stream. A landed round is spent; a spent round vanishes. The
// per-craft test is broad-phase gated — a round's step is ~20 m and an
// airframe ~20 m, so 80 m of slack collects every possible contact.
func (i *instance) fly(dt float64, tick uint64) {
	if len(i.rounds) == 0 {
		return
	}
	// The roster is read ONCE (#258): slots() allocates and sorts, and it sat
	// inside the loop over every round in flight — with ~40k rounds airborne
	// that is 40k allocations and sorts per tick for a list that cannot
	// change while the rounds are being flown.
	order := i.slots()
	alive := i.rounds[:0]
	for r := range i.rounds {
		round := &i.rounds[r]
		landed := false
		for _, slot := range order {
			a := i.aircraft[slot]
			if a == nil || !a.alive || a.model == nil || slot == round.Shooter {
				continue
			}
			if i.cheat.invulnerable && !a.bot {
				continue // rounds pass through a human under the cheat; bots still bleed
			}
			state := &a.model.State
			if math.Abs(round.Position.Y-state.Position.Y) > 80 ||
				math.Abs(flight.Shortest(state.Position.X, round.Position.X, i.environment.Wrap)) > 80 ||
				math.Abs(flight.Shortest(state.Position.Z, round.Position.Z, i.environment.Wrap)) > 80 {
				continue
			}
			hit, events, _ := battle.Strike(round, state.Position, state.Attitude, state.Velocity, &a.body, dt, i.environment.Wrap, i.environment.Seed)
			if !hit {
				continue
			}
			a.condition.Damager = round.Shooter
			a.condition.Damaged = 0
			for _, event := range events {
				if event.Kind == "hit" {
					i.events = append(i.events, map[string]any{"kind": "hit", "slot": slot, "by": round.Shooter, "count": event.Count})
					continue
				}
				i.raise(slot, event)
			}
			landed = true
			break // the round is spent in whoever it met first
		}
		if landed {
			continue
		}
		if !battle.Fly(round, dt) {
			alive = append(alive, *round)
		}
	}
	i.rounds = alive
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

// acquire returns the slot the seeker head would lock right now: the nearest
// living craft inside the cone and the aspect-weighted range — team-blind,
// exactly like the hardware (an AIM-9 sees tailpipes, not sides).
func (i *instance) acquire(slot int, a *craft) int {
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
		// The head-on floor is the PLUME's, not the airframe's (#255): a
		// burner-lit nose is lockable out to half the envelope, a cold one
		// only close aboard — the 9M-era truth that policed the all-aspect
		// merge. This is what starves the six-round volley meta at its
		// source (no lock, no launch), and it makes the afterburner a
		// beacon both sides can choose to hide: the run-in at MIL is
		// concealment, for bots and the human alike.
		floor := 0.15 + 0.35*clamp(b.latest.Reheat, 0, 1)
		if distance > missile_range*(floor+(1-floor)*math.Max(0, tail)) {
			continue
		}
		if forward.X*direction.X+forward.Y*direction.Y+forward.Z*direction.Z < missile_cone {
			continue
		}
		if distance < nearest {
			best, nearest = other, distance
		}
	}
	return best
}

// launch fires a missile at the best target in the seeker cone. The seeker
// is aspect-aware: it sees a tailpipe from the rear hemisphere much farther
// than a cold nose head-on.
func (i *instance) launch(slot int, a *craft) bool {
	if len(i.flying) >= 256 {
		return false // belt and braces above the per-life magazines: no session needs more missiles airborne than a full teams match volleying everything at once
	}
	forward := a.model.State.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
	best := i.acquire(slot, a)
	if best < 0 {
		return false
	}
	i.launched++
	separation := a.model.State.Velocity.Add(forward.Scale(30)) // off the rail at aircraft speed; the motor does the rest
	sight, _ := i.bearing(a.model.State.Position, i.aircraft[best].model.State.Position)
	i.flying = append(i.flying, &missile{
		shooter:  slot,
		target:   best,
		position: a.model.State.Position.Add(forward.Scale(3)),
		velocity: separation,
		life:     missile_life,
		burn:     missile_boost,
		sight:    sight,
		number:   i.launched,
	})
	i.events = append(i.events, map[string]any{"kind": "missile", "slot": slot, "target": best})
	return true
}

// fox3 launches an AIM-120 (#27 phase 2c). Unlike the heater's rear-aspect
// cone, this shot is the RADAR's: the shooter must be holding an STT lock,
// which is exactly the emitter state every client already reports and every
// RWR already hears — so employing the weapon means being seen doing it. A
// silent radar has no shot. The round then flies the shared round package,
// datalinked while that lock lives.
func (i *instance) fox3(slot int, a *craft) bool {
	if len(i.flying) >= 256 {
		return false
	}
	if a.emitter != 2 || a.lock < 0 {
		return false // no lock, no launch: the VISUAL/boresight shot is the client's own affair until it earns a wire flag
	}
	target := i.aircraft[a.lock]
	if target == nil || !target.alive || a.lock == slot {
		return false
	}
	if i.mode == "teams" && target.team != "" && target.team == a.team {
		return false // no fratricide in teams: the same rule the heater's acquire applies
	}
	forward := a.model.State.Attitude.Rotate(flight.Vec3{X: 1, Y: 0, Z: 0})
	i.launched++
	m := &missile{
		shooter: slot,
		target:  a.lock,
		number:  i.launched,
		life:    round.Battery,
	}
	m.radar = round.New(
		a.model.State.Position.Add(forward.Scale(3)),
		a.model.State.Velocity.Add(forward.Scale(30)),
		&round.Target{Position: target.model.State.Position, Velocity: target.model.State.Velocity},
		i.environment.Wrap,
	)
	m.position, m.velocity = m.radar.Position, m.radar.Velocity
	i.flying = append(i.flying, m)
	i.events = append(i.events, map[string]any{"kind": "fox3", "slot": slot, "target": a.lock})
	return true
}

// fly_radar steps one AIM-120 through the shared core and judges its
// warhead. Datalink support is the shooter's radar: while the lock on this
// round's target lives, the round gets fresh estimates and steers a
// lead-collision midcourse; go silent, break the lock, or point away and it
// coasts on the last prediction until its own seeker wakes. That is the
// crank, and it is why the emission gameplay matters.
func (i *instance) fly_radar(m *missile, dt float64, tick uint64) bool {
	target := i.aircraft[m.target]
	if target == nil {
		return false
	}
	var support, truth *round.Target
	if target.alive {
		truth = &round.Target{Position: target.model.State.Position, Velocity: target.model.State.Velocity}
	}
	if shooter := i.aircraft[m.shooter]; shooter != nil && shooter.alive && shooter.emitter == 2 && shooter.lock == m.target && truth != nil {
		// The datalink is only as good as the shooter's radar, and that
		// radar has the same clutter notch the seeker does (#29): a beamed
		// target's return sits in the rejection gate with the ground, so a
		// defender squared to the SHOOTER starves the round of updates too.
		// This is what makes beam-and-chaff the complete defence rather
		// than a two-second inconvenience — and the shooter's own display
		// catching up with that truth (track memory, broken lock) is the EW
		// task's, not this line's.
		direction, _ := i.bearing(shooter.model.State.Position, target.model.State.Position)
		if math.Abs(target.model.State.Velocity.Dot(direction)) > round.Notch {
			support = truth
		}
	}
	// A fresh chaff bloom from the target is offered to the seeker (#29):
	// the round package's doppler gate decides — in the notch it seduces,
	// out of it the velocity gate rejects it, exactly as briefed.
	if truth != nil && target.clouded < round.Window {
		m.radar.Distract(target.cloud, *truth)
	}
	m.radar.Beacon = target.loud && target.alive // home-on-jam (#31): the radiating defender is a beacon, and the round takes the angle it is given
	alive, fired, burst := m.radar.Advance(dt, support, truth)
	m.position, m.velocity = m.radar.Position, m.radar.Velocity
	m.life = m.radar.Life
	if fired && target.alive {
		if i.cheat.invulnerable && !target.bot {
			return false // the fuse fires but the warhead cannot hurt a human under the cheat
		}
		kill, events := battle.Warhead(battle.Radar, burst, target.model.State.Position, target.model.State.Attitude,
			&target.body, i.environment.Wrap, i.environment.Seed, uint64(m.shooter), tick)
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
		return false
	}
	return alive && m.position.Y > 0
}

// pursue advances every missile: pure pursuit toward the target, graded
// flare decoys judged once per flare drop, and a proximity fuse feeding the
// battle warhead — a direct hit kills outright, a fringe burst fragments.
func (i *instance) pursue(dt float64, tick uint64) {
	alive := i.flying[:0]
	for _, m := range i.flying {
		if m.radar != nil { // the AIM-120 flies the shared core (#27 phase 2c)
			m.flew += dt
			if i.fly_radar(m, dt, tick) {
				alive = append(alive, m)
			} else if shooter := i.aircraft[m.shooter]; shooter != nil && shooter.brain != nil {
				// The look half of shoot-look-shoot (hunt.go): a bot's round
				// that dies with its target still flying is a lesson about
				// that target's defence, not a reason to feed it another.
				if target := i.aircraft[m.target]; target != nil && target.alive && shooter.brain.futiled == m.target {
					shooter.brain.futile++
				}
			}
			continue
		}
		m.life -= dt
		m.flew += dt
		speed := m.velocity.Length()
		target := i.aircraft[m.target]
		tracking := !m.loose && m.blind <= 0 && target != nil && target.alive
		if m.life <= 0 || (m.target >= 0 && target == nil) {
			continue
		}
		// Flare seduction: inside the window, an aspect-graded roll — tempered
		// by the 9M's counter-countermeasures — hands the seeker the flare.
		// The missile then chases the falling flare point until it gives up.
		if tracking && target.flared < flare_window {
			if !m.window {
				m.window = true
				direction, _ := i.bearing(m.position, target.model.State.Position)
				tail := direction.Dot(target.model.State.Attitude.Rotate(flight.Vec3{X: 1}))
				decoy := (0.35 + 0.40*(1-clamp(tail, 0, 1))) * flare_reject
				if target.latest.Reheat > 0.05 {
					decoy *= 0.5 // the burner is the brightest thing in view
				}
				if battle.Roll(i.environment.Seed, m.number, tick) < decoy {
					m.lure = target.model.State.Position.Add(flight.Vec3{Y: -30})
					m.blind = 1.5
					tracking = false
					m.sight, _ = i.bearing(m.position, m.lure) // the seeker is ON the flare now: re-reference the track, or the aim-point swap reads as an LOS-rate spike and breaks the lock at the seduction instant
					i.events = append(i.events, map[string]any{"kind": "decoy", "slot": m.target})
				}
			}
		} else {
			m.window = false
		}

		// Proximity fuse (armed): independent of the seeker — a broken lock
		// leaves the warhead live, as the loose comment always promised but
		// the old tracking-gated check never delivered (the terminal LOS
		// rate breaks the lock against any evading target, which disarmed
		// every such pass). Detonates at the CLOSEST APPROACH inside this
		// step: fusing at the first per-tick range sample under the
		// envelope burst at 8-12 m — outside the 5 m lethal radius — so no
		// pass ever scored the direct kill.
		if m.flew > missile_arm && target != nil && target.alive {
			relative := flight.Vec3{
				X: flight.Shortest(m.position.X, target.model.State.Position.X, i.environment.Wrap),
				Y: target.model.State.Position.Y - m.position.Y,
				Z: flight.Shortest(m.position.Z, target.model.State.Position.Z, i.environment.Wrap),
			}
			closure := target.model.State.Velocity.Subtract(m.velocity)
			step := 0.0
			if squared := closure.Dot(closure); squared > 1e-9 {
				step = clamp(-relative.Dot(closure)/squared, 0, dt)
			}
			closest := relative.Add(closure.Scale(step))
			if closest.Length() < missile_fuse {
				if i.cheat.invulnerable && !target.bot {
					continue // the fuse fires but the warhead cannot hurt a human under the cheat
				}
				burst := target.model.State.Position.Subtract(closest) // the missile at its nearest point, anchored to the target
				kill, events := battle.Blast(burst, target.model.State.Position, target.model.State.Attitude, &target.body, i.environment.Wrap, i.environment.Seed, uint64(m.shooter), tick)
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
		}

		// The guidance point: the target, or a swallowed flare falling away.
		var aim flight.Vec3
		var mark *flight.Vec3
		if tracking {
			aim = target.model.State.Position
			mark = &aim
		} else if m.blind > 0 {
			m.blind -= dt
			m.lure.Y -= 45 * dt // flares fall
			aim = m.lure
			mark = &aim
			if m.blind <= 0 {
				m.loose = true // a swallowed flare is terminal: the seeker stares at the burnt-out decoy and never re-acquires (9M-realistic) — ballistic from here, fuse still live
			}
		}

		if mark != nil && m.flew > missile_arm {
			direction, _ := i.bearing(m.position, *mark)
			// The seeker: gimbal cone off the velocity axis, and a track-rate
			// ceiling. Beyond either the lock breaks — ballistic, fuse live.
			axis := m.velocity.Normalize()
			if direction.Dot(axis) < missile_gimbal {
				m.loose = true
			}
			rate := direction.Subtract(m.sight).Scale(1 / math.Max(dt, 1e-6))
			rate = rate.Subtract(direction.Scale(rate.Dot(direction))) // the rotation component only
			if rate.Length() > missile_track {
				m.loose = true
			}
			m.sight = direction
			if !m.loose {
				// PROPORTIONAL NAVIGATION: a = N·Vc·λ̇, perpendicular to the
				// line of sight — zero LOS rate means collision course.
				closing := 0.0
				if tracking {
					closing = target.model.State.Velocity.Subtract(m.velocity).Dot(direction)
				} else {
					closing = -m.velocity.Dot(direction)
				}
				closing = math.Abs(closing)
				accel := rate.Scale(missile_n * closing)
				// The airframe: the structural limit fades as dynamic pressure
				// dies — a slow missile cannot pull.
				limit := missile_g * 9.81 * clamp(speed/600, 0.15, 1)
				if pull := accel.Length(); pull > limit {
					accel = accel.Scale(limit / pull)
				}
				m.velocity = m.velocity.Add(accel.Scale(dt))
				// Induced drag: every commanded g is paid for in airspeed.
				speed = m.velocity.Length()
				bleed := missile_dragk * speed * speed * (1 + 3*(accel.Length()/(missile_g*9.81))*(accel.Length()/(missile_g*9.81)))
				m.velocity = m.velocity.Scale(math.Max(speed-bleed*dt, 60) / math.Max(speed, 1e-6))
				// Energy death: coasting below convergence speed, opening — done.
				if m.burn <= 0 && tracking && speed < target.model.State.Velocity.Length()+60 && closing < 40 {
					continue
				}
			}
		} else if m.flew <= missile_arm {
			// Not yet armed: fly out straight; remember the boresight LOS.
			if mark != nil {
				m.sight, _ = i.bearing(m.position, *mark)
			}
		}

		// Propulsion: boost hard, then coast against the base drag.
		if m.burn > 0 {
			m.burn -= dt
			axis := m.velocity.Normalize()
			m.velocity = m.velocity.Add(axis.Scale(missile_thrust * dt))
		} else if m.loose || mark == nil {
			speed = m.velocity.Length()
			m.velocity = m.velocity.Scale(math.Max(speed-missile_dragk*speed*speed*dt, 60) / math.Max(speed, 1e-6))
		}
		m.position = m.position.Add(m.velocity.Scale(dt))
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
		// Friendly fire is live in the teams mode, and it costs: a teammate
		// kill scores MINUS one for the shooter and the side.
		if killer.team != "" && killer.team == a.team {
			killer.kills--
			i.score[killer.team]--
		} else {
			killer.kills++
			if killer.team != "" {
				i.score[killer.team]++
			}
		}
	}
	event := map[string]any{
		"kind": "kill", "slot": victim, "by": by,
		"position": []float64{a.model.State.Position.X, a.model.State.Position.Y, a.model.State.Position.Z},
	}
	if i.score != nil {
		event["score"] = map[string]any{"red": i.score["red"], "blue": i.score["blue"]}
	}
	i.events = append(i.events, event)
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

// Snapshot wire (#81, 100 players): every remote aircraft is a fixed 35-byte
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

// pose packs one aircraft into the 35-byte wire record.
func pose(slot int, a *craft) []byte {
	s := &a.model.State
	b := make([]byte, 35)
	b[0] = byte(slot)
	binary.LittleEndian.PutUint32(b[1:], math.Float32bits(float32(s.Position.X)))
	binary.LittleEndian.PutUint32(b[5:], math.Float32bits(float32(s.Position.Y)))
	binary.LittleEndian.PutUint32(b[9:], math.Float32bits(float32(s.Position.Z)))
	quat := func(offset int, v float64) {
		binary.LittleEndian.PutUint16(b[offset:], uint16(int16(clamp(v, -1, 1)*32767)))
	}
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
	if a.condition.Burning {
		flags |= 32 // fuel fire: the one damage state with no continuous byte of its own, and the pilot's own cockpit needs it (#40)
	}
	if a.loud {
		flags |= 64 // the jammer is radiating (#31): victims draw the strobe and break their locks from this bit — additive, old clients ignore it
	}
	b[26] = flags
	b[27] = byte(clamp(a.latest.Reheat, 0, 1) * 255)
	b[28] = byte(clamp(a.model.State.Fcs.Speedbrake, 0, 1) * 255)
	b[29] = byte(clamp(a.condition.Fire[0], 0, 1) * 255)
	b[30] = byte(clamp(a.condition.Fire[1], 0, 1) * 255)
	b[31] = byte(clamp(a.model.State.Damage.Leak*10, 0, 255))
	binary.LittleEndian.PutUint16(b[32:], uint16(mask(a.model.Airframe, a.model.State.Damage.Element)))
	target := 63 // #30: byte 34 — high two bits the emitter mode, low six the locked slot (63 = none)
	if a.emitter == 2 && a.lock >= 0 && a.lock < 63 {
		target = a.lock
	}
	b[34] = byte(a.emitter<<6) | byte(target)
	return b
}

// Radar records a client's emitter report (#30): 0 silent, 1 search, 2 STT
// with the locked slot. The wire promises nothing — the mode is gated at the
// door, the target must name a present aircraft, and only STT carries one.
func (i *instance) Radar(slot int, mode int, target int) {
	a := i.aircraft[slot]
	if a == nil || a.bot || mode < 0 || mode > 2 {
		return
	}
	if mode != 2 {
		target = -1
	}
	if target >= 63 || (target >= 0 && i.aircraft[target] == nil) {
		target = -1 // the six wire bits cap the slot space at 62; an absent aircraft is no lock
	}
	a.emitter = mode
	a.lock = target
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
		if me.bot {
			// A bot has no link, so everything below — its interest set, its
			// sorted neighbours, its missile list, its whole per-recipient
			// blob — is built and then discarded (#258). With ninety-nine of
			// them that is about ninety-nine percent of the snapshot's work
			// thrown away. Its own pose still went into `cores` above, which
			// is what the humans actually need from it.
			continue
		}
		// Interest management: everyone else sorted by wrap distance; the
		// nearest `near` every snapshot, the far tail rotated `roving` at a
		// time so distant contacts still refresh several times a second.
		// Distance is measured ONCE per candidate, not once per comparison
		// (#258): span() is wrap-aware, and a comparator that recomputes it
		// pays for it O(n log n) times instead of n.
		type neighbour struct {
			slot int
			away float64
		}
		near_by := make([]neighbour, 0, len(order)-1)
		for _, slot := range order {
			if slot != self && i.aircraft[slot].model != nil {
				near_by = append(near_by, neighbour{slot, i.span(me.model, i.aircraft[slot].model)})
			}
		}
		sort.Slice(near_by, func(x, y int) bool { return near_by[x].away < near_by[y].away })
		others := make([]int, len(near_by))
		for n, entry := range near_by {
			others[n] = entry.slot
		}
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
			cycle := 1
			if len(far) > 0 { // the sticky set can hold every other aircraft, leaving nothing to rotate
				cycle = (len(far) + roving - 1) / roving
			}
			at := int(tick/3%uint64(cycle)) * roving // snapshots fire every 3 ticks (60/20): advance one window per snapshot, no skipped stretches
			stop := at + roving
			if stop > len(far) {
				stop = len(far)
			}
			picked = append(set, far[at:stop]...)
		}
		blob := make([]byte, 0, (len(picked)+1)*35)
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
		darts := make([]byte, 0, len(shots)*25)
		for _, sh := range shots {
			var d [25]byte
			binary.LittleEndian.PutUint32(d[0:], math.Float32bits(float32(sh.m.position.X)))
			binary.LittleEndian.PutUint32(d[4:], math.Float32bits(float32(sh.m.position.Y)))
			binary.LittleEndian.PutUint32(d[8:], math.Float32bits(float32(sh.m.position.Z)))
			binary.LittleEndian.PutUint32(d[12:], math.Float32bits(float32(sh.m.velocity.X)))
			binary.LittleEndian.PutUint32(d[16:], math.Float32bits(float32(sh.m.velocity.Y)))
			binary.LittleEndian.PutUint32(d[20:], math.Float32bits(float32(sh.m.velocity.Z)))
			d[24] = byte(sh.m.shooter) // the client hides its OWN darts (it already flies the launch visual) and shows everyone else's
			if sh.m.radar != nil {
				d[24] |= 0x80 // the round's KIND rides the shooter byte's high bit (#27): slots stop at 62, so the bit is free, the record stays 25 bytes and an older client simply draws the AIM-120 as a heater
			}
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
		quat := func(offset int, v float64) {
			binary.LittleEndian.PutUint16(d[offset:], uint16(int16(clamp(v, -1, 1)*32767)))
		}
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

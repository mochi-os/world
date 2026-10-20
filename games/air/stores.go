// Mochi world: air stores loadout (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The per-station loadout over the airframe's fitment catalog, mirroring the
// client's shape (game/stores.ts): station 1..9 (port tip to starboard tip) to
// a fixture id and one store id per fixture point. The catalog lives in
// aircraft/fa18c.

package air

import (
	"math"
	"strconv"
	"strings"

	"world/game"
	"world/games/air/aircraft"
	"world/games/air/flight"
)

type slot struct {
	Fixture string   `json:"fixture"`
	Stores  []string `json:"stores"`
}

type loadout map[string]slot

// stores_fixtures lists the fixtures a station accepts; the first entry is the
// default. Tips carry the integral rail (locked); "rail" on cheek stations 4/6
// means the LAU-116 ejector, and "twin" is the LAU-115C dual carriage.
func stores_fixtures(station int) []string {
	switch station {
	case 1, 9:
		return []string{"rail"}
	case 2, 8:
		return []string{"", "rail", "twin"}
	case 3, 7:
		return []string{"", "pylon", "twin"}
	case 5:
		return []string{"", "pylon"}
	case 4, 6:
		return []string{"", "rail"}
	}
	return []string{""}
}

// stores_points is how many store points a fixture exposes.
func stores_points(fixture string) int {
	switch fixture {
	case "twin":
		return 2
	case "":
		return 0
	}
	return 1
}

// stores_option reports whether a point on this station's fixture accepts a
// store id ("" is always legal — an empty point).
func stores_option(station int, fixture, store string) bool {
	if store == "" {
		return true
	}
	if station == 1 || station == 9 {
		return store == "9m"
	}
	if station == 4 || station == 6 {
		return fixture == "rail" && store == "120c" // the cheeks are the radar-missile positions (#27)
	}
	switch fixture {
	case "rail", "twin":
		return store == "9m" || store == "120c" // every rail point is a LAU-127 — either family, single or twin (#27)
	case "pylon":
		if store == "9m" || store == "120c" {
			return station == 3 || station == 7 // the Sparrow-capable inboards hang missiles on the LAU-115C; the centreline never does (#27)
		}
		return store == "tank"
	}
	return false
}

// stores_normalize coerces an arbitrary wire request into a legal loadout:
// all nine stations, unknown fixtures and stores dropped, point counts
// matched, tips locked to their rail.
func stores_normalize(raw map[string]any) loadout {
	out := loadout{}
	for station := 1; station <= 9; station++ {
		key := strconv.Itoa(station)
		allowed := stores_fixtures(station)
		fixture := allowed[0]
		var wanted []any
		if entry, found := raw[key].(map[string]any); found {
			if f, ok := entry["fixture"].(string); ok && station != 1 && station != 9 {
				for _, legal := range allowed {
					if f == legal {
						fixture = f
						break
					}
				}
			}
			wanted, _ = entry["stores"].([]any)
		}
		points := make([]string, stores_points(fixture))
		for p := range points {
			if p < len(wanted) {
				if id, ok := wanted[p].(string); ok && stores_option(station, fixture, id) {
					points[p] = id
				}
			}
		}
		out[key] = slot{Fixture: fixture, Stores: points}
	}
	return out
}

// stores_strip removes every missile — the guns-only match clamp, heaters and
// AMRAAMs alike (#27). Fixtures and tanks stay: an empty rail still weighs
// and drags.
func stores_strip(lo loadout) loadout {
	out := loadout{}
	for key, s := range lo {
		points := make([]string, len(s.Stores))
		for p, id := range s.Stores {
			if id != "9m" && id != "120c" {
				points[p] = id
			}
		}
		out[key] = slot{Fixture: s.Fixture, Stores: points}
	}
	return out
}

// stores_fuel reads the player's own fuel request, in pounds, from the same
// join map the loadout rides in: stores_normalize walks stations 1..9 and
// ignores every other key, so "fuel" passes through it untouched and needs no
// protocol change to carry.
//
// The bound is the airframe's, not the creator's (2026-09-15): a player takes
// whatever the tank holds, and underperforming or running out on a load they
// chose is theirs to pay for. Absent — old clients, and bots, which have no
// join request at all — takes the match's own default.
func stores_fuel(raw map[string]any, capacity float64, standard float64) float64 {
	pounds := number(raw, "fuel") // shared with the session parameters: the wire integer, and NaN/Inf already barred (#174)
	if pounds <= 0 {
		return standard
	}
	return math.Min(pounds/2.2046, capacity) // the UI speaks pounds like the IFEI; the sim burns kilograms
}

// stores_grant is the join-time clamp: normalize the request and apply the
// match's loadout class (#32). A join with NO request gets the armed wingtips -
// current clients always send all nine stations, so empty means absent, not
// "clean jet".
func stores_grant(raw map[string]any, weapons string) loadout {
	lo := stores_normalize(raw)
	if len(raw) == 0 {
		lo = tips_loadout()
	}
	switch weapons {
	case "guns":
		lo = stores_strip(lo)
	case "fox2":
		lo = stores_heaters(lo)
	}
	return lo
}

// stores_heaters removes the radar missiles and keeps everything else — the
// Fox 2 class clamp: heaters, tanks and fixtures stay.
func stores_heaters(lo loadout) loadout {
	out := loadout{}
	for key, s := range lo {
		points := make([]string, len(s.Stores))
		for p, id := range s.Stores {
			if id != "120c" {
				points[p] = id
			}
		}
		out[key] = slot{Fixture: s.Fixture, Stores: points}
	}
	return out
}

// tips_loadout is the bare armed wingtips — what a stale pre-loadout client's
// own model carries, and the grant for a join with no stores request.
func tips_loadout() loadout {
	return stores_normalize(map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	})
}

// bots_loadout is the server bot's standard: the Fox 2 fighter, six AIM-9Ms on
// tips and outboard twins when missiles are allowed, clean otherwise.
func bots_loadout(weapons string) loadout {
	// Bots arm to the match's weapons class exactly as humans are clamped by
	// stores_grant. The fox2 fit is what every dogfight battery calibrates
	// against.
	switch weapons {
	case "guns":
		return stores_normalize(nil)
	case "open": // the client's Fox 3 preset, its centreline tank included (2026-10-12)
		return stores_normalize(map[string]any{
			"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
			"2": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"3": map[string]any{"fixture": "pylon", "stores": []any{"120c"}},
			"4": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"5": map[string]any{"fixture": "pylon", "stores": []any{"tank"}},
			"6": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"7": map[string]any{"fixture": "pylon", "stores": []any{"120c"}},
			"8": map[string]any{"fixture": "rail", "stores": []any{"120c"}},
			"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		})
	default: // fox2
		return stores_normalize(map[string]any{
			"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
			"2": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
			"8": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
			"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		})
	}
}

// bots_fuel is the internal fuel a bot spawns with for its weapons class
// (decided 2026-10-12). Within visual range it is the most that leaves its fit
// at the weight the 7.5 g placard is written at, so it fights on the whole of
// its wing from the merge: 4,000 kg with the gun alone, 2,904 kg on the twin
// rails. The BVR fit flies full internal, as its fight opens where nobody
// pulls the placard for minutes. A human's stores presets load a round figure
// near the same weight; the bot no longer copies the player's slider, which
// may be the wrong choice.
func bots_fuel(weapons string) float64 {
	airframe := aircraft.Get("fa18c")
	if weapons == "open" || airframe.Limit.Reference <= 0 {
		return airframe.Mass.Fuel
	}
	room := airframe.Limit.Reference - airframe.Mass.Empty
	mask := stores_mask(bots_loadout(weapons), 0, 0)
	for i := range airframe.Stores {
		if mask&(1<<uint(i)) != 0 {
			room -= airframe.Stores[i].Mass + airframe.Stores[i].Fuel
		}
	}
	return math.Max(0, math.Min(room, airframe.Mass.Fuel))
}

// stores_entries names the catalog entries a station's slot attaches: the
// fixture hardware (tips: none — the integral rail lives in the empty
// weight), then one entry per occupied point, outer twin point first.
func stores_entries(station int, s slot) []string {
	key := strconv.Itoa(station)
	var out []string
	if station == 1 || station == 9 {
		if len(s.Stores) > 0 && s.Stores[0] == "9m" {
			out = append(out, "tip"+key)
		}
		return out
	}
	if s.Fixture == "" {
		return out
	}
	out = append(out, s.Fixture+key)
	switch s.Fixture {
	case "twin":
		if len(s.Stores) > 0 && s.Stores[0] != "" {
			out = append(out, s.Stores[0]+key+"a")
		}
		if len(s.Stores) > 1 && s.Stores[1] != "" {
			out = append(out, s.Stores[1]+key+"b")
		}
	default:
		if len(s.Stores) > 0 && s.Stores[0] != "" {
			out = append(out, s.Stores[0]+key) // any occupied single point: 9m2, 120c4, tank3, 9m7...
		}
	}
	return out
}

// stores_rounds lists the missile entries in SMS priority order: wingtips
// first, alternating to the opposite station at the same level, starboard
// seeding, outboard rails before inboard pylons; twins fire outer before inner.
func stores_rounds(lo loadout) []string {
	var out []string
	level := func(stations []int) {
		queues := make([][]string, len(stations))
		longest := 0
		for qi, station := range stations {
			s := lo[strconv.Itoa(station)]
			var names []string
			for _, name := range stores_entries(station, s) {
				if name[0] == '9' || name[0] == 't' && name[1] == 'i' { // 9m* and tip*
					names = append(names, name)
				}
			}
			queues[qi] = names
			if len(names) > longest {
				longest = len(names)
			}
		}
		for round := 0; round < longest; round++ {
			for _, q := range queues {
				if round < len(q) {
					out = append(out, q[round])
				}
			}
		}
	}
	level([]int{9, 1})
	level([]int{8, 2})
	level([]int{7, 3})
	return out
}

// stores_amraams lists the loadout's AIM-120 entries in firing order —
// cheeks, then outboard rails, then inboard pylons, alternating port-first
// within each ring so a full expenditure stays balanced; twins fire the
// outer round before the inner (#27). The mirror of the client's amraams().
func stores_amraams(lo loadout) []string {
	var out []string
	ring := func(stations []int) {
		queues := make([][]string, len(stations))
		longest := 0
		for qi, station := range stations {
			key := strconv.Itoa(station)
			s := lo[key]
			var names []string
			if s.Fixture == "twin" {
				if len(s.Stores) > 0 && s.Stores[0] == "120c" {
					names = append(names, "120c"+key+"a")
				}
				if len(s.Stores) > 1 && s.Stores[1] == "120c" {
					names = append(names, "120c"+key+"b")
				}
			} else if (s.Fixture == "rail" || s.Fixture == "pylon") && len(s.Stores) > 0 && s.Stores[0] == "120c" {
				names = append(names, "120c"+key)
			}
			queues[qi] = names
			if len(names) > longest {
				longest = len(names)
			}
		}
		for round := 0; round < longest; round++ {
			for _, q := range queues {
				if round < len(q) {
					out = append(out, q[round])
				}
			}
		}
	}
	ring([]int{4, 6})
	ring([]int{2, 8})
	ring([]int{3, 7})
	return out
}

// stores_eject reports whether an AMRAAM entry leaves on an EJECTOR (the cheek
// LAU-116 and the inboard pylon's LAU-115C punch the round down) rather than
// sliding off a LAU-127 rail: the wing rails and every twin point are rails.
// The mirror of the client's eject().
func stores_eject(lo loadout, name string) bool {
	key := strings.TrimRight(strings.TrimPrefix(name, "120c"), "ab")
	s, found := lo[key]
	if !found || s.Fixture == "twin" {
		return false
	}
	return key == "3" || key == "4" || key == "6" || key == "7"
}

// firing is a jet's launch order: its heaters and its AMRAAMs, each in
// the SMS priority order its loadout gives (stores_rounds, stores_amraams),
// with a round burning on the rail moved to the head of the rounds still
// carried so that the next press of the trigger fires it off. The rounds
// already fired are the first ones of each list, as many as the magazine has
// spent.
type firing struct {
	heaters []string
	amraams []string
}

// stores_order is a loadout's launch order before anything has burned.
func stores_order(lo loadout) firing {
	return firing{heaters: stores_rounds(lo), amraams: stores_amraams(lo)}
}

// promote moves name to index fired in list - the head of the rounds still to
// fire - when it is still carried, and returns the list unchanged otherwise.
func promote(list []string, fired int, name string) []string {
	at := -1
	for k, n := range list {
		if n == name {
			at = k
		}
	}
	if at < 0 || at < fired {
		return list
	}
	out := append([]string{}, list[:fired]...)
	out = append(out, name)
	out = append(out, list[fired:at]...)
	return append(out, list[at+1:]...)
}

// stores_mask is the flight-core attach bitmask for a loadout with `fired`
// heaters (stores_rounds order) and `expended` AMRAAMs (stores_amraams
// order) already away: each departure sheds its round's mass and drag.
func stores_mask(lo loadout, fired int, expended int) uint64 {
	return stores_rack(lo, stores_order(lo), fired, expended)
}

// stores_rack is stores_mask for a jet's own launch order.
func stores_rack(lo loadout, order firing, fired int, expended int) uint64 {
	index := map[string]int{}
	catalog := aircraft.Get("fa18c").Stores
	for i := range catalog {
		index[catalog[i].Name] = i
	}
	spent := map[string]bool{}
	for k, name := range order.heaters {
		if k >= fired {
			break
		}
		spent[name] = true
	}
	for k, name := range order.amraams {
		if k >= expended {
			break
		}
		spent[name] = true
	}
	mask := uint64(0)
	for station := 1; station <= 9; station++ {
		for _, name := range stores_entries(station, lo[strconv.Itoa(station)]) {
			if spent[name] {
				continue
			}
			if bit, found := index[name]; found {
				mask |= 1 << uint(bit)
			}
		}
	}
	return mask
}

// JETTISON_COOLDOWN is the minimum ticks between one jet's jettisons: one
// second.
const JETTISON_COOLDOWN = 60

// stores_occupied reports whether a station actually holds what the request
// asks to drop: any round for "stores", a fixture for "rack". An empty station
// is not a departure, so it must not count towards the broadcast.
func stores_occupied(lo loadout, station int, what string) bool {
	s := lo[strconv.Itoa(station)]
	if what == "rack" {
		return s.Fixture != ""
	}
	for _, name := range s.Stores {
		if name != "" {
			return true
		}
	}
	return false
}

// stores_jettison clears one station: "stores" empties its mounts and keeps
// the fixture, "rack" clears the fixture with everything on it. Wingtips
// (1/9) never jettison - no ejector.
func stores_jettison(lo loadout, station int, what string) loadout {
	out := loadout{}
	for key, value := range lo {
		out[key] = value
	}
	if station < 2 || station > 8 {
		return out
	}
	key := strconv.Itoa(station)
	s := out[key]
	s.Stores = make([]string, len(s.Stores))
	if what == "rack" {
		s.Fixture = ""
	}
	out[key] = s
	return out
}

// Jettison applies a player's stores departure (#18): removal only, tips
// protected, fired rounds come off the magazine, and the granted loadout is
// re-published on the roster. Runs on the tick goroutine, like Step.
func (i *instance) Jettison(slot int, departures []game.Departure) {
	a := i.aircraft[slot]
	if a == nil || a.loadout == nil || !a.alive {
		return
	}
	before := a.sequence().heaters
	fired := len(before) - a.missiles
	if fired < 0 {
		fired = 0
	}
	// The AIM-120s are a SECOND magazine (arm seeds a.amraams from
	// stores_amraams), and dropping their stations has to come off it exactly
	// as dropping a rail comes off the heaters below. Without this the counter
	// kept its pre-drop value while the loadout no longer held the rounds, and
	// the fox3 trigger gates on the counter alone — a full-fidelity AIM-120
	// from an aircraft carrying none, off a client-sent frame, against a
	// module whose whole design is server-authoritative weapons. attach()
	// hid it by clamping the resulting negative `expended` to zero.
	radar := a.sequence().amraams
	expended := len(radar) - a.amraams
	if expended < 0 {
		expended = 0
	}
	// One jettison per second per jet: every call appends a roster event to every
	// player's reliable queue, and a client whose queue fills is torn down as
	// "slow", so an unthrottled jettison would disconnect the whole match.
	if last, dropped := i.jettisoned[slot]; dropped && i.stepped-last < JETTISON_COOLDOWN {
		return
	}

	next := a.loadout
	changed := 0
	for _, d := range departures {
		if d.Station < 2 || d.Station > 8 || (d.What != "stores" && d.What != "rack") {
			continue
		}
		// Count only stations that HELD something. Counting every well-formed
		// request meant nine empty stations passed the changed==0 guard below
		// and bought a full broadcast, which is what made the flood free.
		if !stores_occupied(next, d.Station, d.What) {
			continue
		}
		next = stores_jettison(next, d.Station, d.What)
		changed++
	}
	if changed == 0 {
		return
	}
	if i.jettisoned == nil {
		i.jettisoned = map[int]uint64{}
	}
	i.jettisoned[slot] = i.stepped
	live := map[string]bool{}
	for _, name := range stores_rounds(next) {
		live[name] = true
	}
	gone := 0
	for _, name := range before[fired:] {
		if !live[name] {
			gone++
		}
	}
	carried := map[string]bool{}
	for _, name := range stores_amraams(next) {
		carried[name] = true
	}
	dropped := 0
	for _, name := range radar[expended:] {
		if !carried[name] {
			dropped++
		}
	}
	a.loadout = next
	// The launch order keeps its own sequence, a burning round's promotion
	// included, less the rounds that went with their stations.
	keep := func(list []string, carried map[string]bool) []string {
		var out []string
		for _, name := range list {
			if carried[name] {
				out = append(out, name)
			}
		}
		return out
	}
	a.order = &firing{heaters: keep(before, live), amraams: keep(radar, carried)}
	a.missiles -= gone
	if a.missiles < 0 {
		a.missiles = 0
	}
	a.amraams -= dropped
	if a.amraams < 0 {
		a.amraams = 0
	}
	// The release envelope (#43, NATOPS figure 4-4: 575 KCAS / Mach 0.95, +1.0 to
	// +2.0 g). A drop outside it is permitted but dents the airframe per offending
	// station. Mirrored in the client's jettison_stations - keep in sync.
	if severity := release_severity(a.model); severity > 0 {
		struck := math.Min(1, 2*severity)
		for s := 0; s < changed; s++ {
			a.model.State.Damage.Drag += 0.05 * struck
			a.model.State.Damage.Stress += 2 * severity
		}
	}
	a.model.Stores(a.attach())
	i.events = append(i.events, map[string]any{"kind": "roster", "slot": slot, "name": a.player.Name, "team": a.team, "stores": a.loadout})
}

// release_severity is how far outside the jettison envelope the jet is:
// zero inside 575 KCAS / Mach 0.95 between +1 and +2 g, growing with the
// overspeed ratio and the g deviation beyond it.
func release_severity(m *flight.Model) float64 {
	speed := math.Max(m.Cas()*1.9438/575, m.Mach()/0.95)
	g := m.State.Fcs.Normal
	deviation := math.Max(0, math.Max(1-g, g-2))
	return math.Max(0, speed-1) + 0.5*deviation
}

// attach is the craft's current mask: its granted loadout minus the rounds
// already fired. Crafts without a loadout (never granted) keep the legacy
// wingtip count mapping.
func (a *craft) attach() uint64 {
	return a.rack(a.missiles)
}

// lineup is the loadout the craft's rails carry: its grant, or, for a craft
// never granted one, the bots' Fox 2 fit the legacy wingtip mapping counts
// against.
func (a *craft) lineup() loadout {
	if a.loadout != nil {
		return a.loadout
	}
	return bots_loadout("fox2")
}

// sequence is the craft's launch order, seeded from its lineup the
// first time it is asked for in a life.
func (a *craft) sequence() *firing {
	if a.order == nil {
		order := stores_order(a.lineup())
		a.order = &order
	}
	return a.order
}

// rack is the attach mask with `heaters` heat-seekers still carried and the
// AMRAAMs a.amraams counts, the rounds gone in the craft's launch order.
func (a *craft) rack(heaters int) uint64 {
	order := a.sequence()
	return stores_rack(a.lineup(), *order, max(0, len(order.heaters)-heaters), max(0, len(order.amraams)-a.amraams))
}

// heaters is the heat-seekers still on the craft's rails: a fighting bot
// counts its own (the brain's discipline), everyone else the magazine.
func (a *craft) heaters() int {
	if a.bot && a.brain != nil {
		return a.brain.missiles
	}
	return a.missiles
}

// next is the round of a launch order list that leaves on the next press, with
// `left` still carried, or "" with none left.
func next(list []string, left int) string {
	if left <= 0 || left > len(list) {
		return ""
	}
	return list[len(list)-left]
}

// cooking reports whether the named store is burning on the craft's rail.
func (a *craft) cooking(name string) bool {
	if name == "" {
		return false
	}
	for k := range a.model.Airframe.Stores {
		if a.model.Airframe.Stores[k].Name == name {
			return k < len(a.condition.Cook) && a.condition.Cook[k] > 0
		}
	}
	return false
}

// alight is the first round still on the craft's rails whose motor is
// burning, heaters before AMRAAMs, or "" with none.
func (a *craft) alight() string {
	order := a.sequence()
	for _, name := range order.heaters[max(0, len(order.heaters)-a.heaters()):] {
		if a.cooking(name) {
			return name
		}
	}
	for _, name := range order.amraams[max(0, len(order.amraams)-a.amraams):] {
		if a.cooking(name) {
			return name
		}
	}
	return ""
}

// douse fires a bot's burning round off its rail: a heater through the
// launcher, at whatever its seeker has or at nothing, an AMRAAM as a visual
// shot. It goes to the head of the launch order first: the server's battle
// already put it there (ignited), but the single-player bandit's battle runs
// in the client's hulk, not in this arena.
func (i *instance) douse(slot int, a *craft, name string, tick uint64) {
	b := a.brain
	a.lead(name)
	order := a.sequence()
	if next(order.heaters, a.heaters()) == name {
		if i.launch(slot, a) && !i.cheat.ammunition {
			b.missiles--
			b.launched = tick
			a.release = 0
			a.model.Stores(a.rack(b.missiles))
		}
		return
	}
	if next(order.amraams, a.amraams) == name {
		if i.fox3(slot, a, true) && !i.cheat.ammunition {
			a.amraams--
			a.release = 0
			a.model.Stores(a.rack(a.heaters()))
		}
	}
}

// ignited moves a store that has caught fire on the rail to the head of the
// craft's launch order, so the trigger fires it off next, and reports
// its name.
func (a *craft) ignited(store int) string {
	if store < 0 || store >= len(a.model.Airframe.Stores) {
		return ""
	}
	name := a.model.Airframe.Stores[store].Name
	a.lead(name)
	return name
}

// lead moves a round still on the rails to the head of the craft's launch
// order.
func (a *craft) lead(name string) {
	order := a.sequence()
	order.heaters = promote(order.heaters, max(0, len(order.heaters)-a.heaters()), name)
	order.amraams = promote(order.amraams, max(0, len(order.amraams)-a.amraams), name)
}

// rearm re-asserts the full loadout on the model, cycling through zero so
// fuel-bearing entries see an off-to-on transition and refill — every spawn
// is a fresh, full-tank grant.
func (a *craft) rearm() {
	if a.loadout == nil {
		return
	}
	a.model.Stores(0)
	a.model.Stores(stores_mask(a.loadout, 0, 0))
}

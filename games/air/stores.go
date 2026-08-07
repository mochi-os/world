// Mochi world: air stores loadout (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The per-station loadout over the airframe's fitment catalog, mirroring the
// client's shape (game/stores.ts): station 1..9 (port tip to starboard tip)
// to a fixture id and one store id per fixture point. The server normalizes
// and clamps every requested loadout — the catalog's numbers live in
// aircraft/fa18c and the structure here is the authorization matrix.

package air

import (
	"strconv"

	"world/game"
	"world/games/air/aircraft"
)

type slot struct {
	Fixture string   `json:"fixture"`
	Stores  []string `json:"stores"`
}

type loadout map[string]slot

// stores_fixtures lists the fixtures a station accepts; the first entry is
// the default. Tips carry the integral rail (locked); cheek stations 4/6 are
// schema-present and empty until a future version.
func stores_fixtures(station int) []string {
	switch station {
	case 1, 9:
		return []string{"rail"}
	case 2, 8:
		return []string{"", "rail", "twin"}
	case 3, 5, 7:
		return []string{"", "pylon"}
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
	switch fixture {
	case "rail", "twin":
		return store == "9m"
	case "pylon":
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

// stores_strip removes every missile — the guns-only match clamp. Fixtures
// and tanks stay: an empty rail still weighs and drags.
func stores_strip(lo loadout) loadout {
	out := loadout{}
	for key, s := range lo {
		points := make([]string, len(s.Stores))
		for p, id := range s.Stores {
			if id != "9m" {
				points[p] = id
			}
		}
		out[key] = slot{Fixture: s.Fixture, Stores: points}
	}
	return out
}

// stores_grant is the join-time clamp: normalize the request and apply the
// match's missiles rule. The persisted client choice is never echoed back —
// the granted loadout is what spawns. A join with NO request (a stale
// pre-loadout client or a bare test driver) gets the armed wingtips — the
// calibrated configuration bots fly and exactly what such a client's own
// model carries. Current clients always send the full nine-station request,
// so empty means absent, never "asked for a clean jet".
func stores_grant(raw map[string]any, missiles bool) loadout {
	lo := stores_normalize(raw)
	if len(raw) == 0 {
		lo = tips_loadout()
	}
	if !missiles {
		lo = stores_strip(lo)
	}
	return lo
}

// shots is the brain's magazine, mirrored from bot.go (mind()/reborn() set
// b.missiles = 6 — keep in sync or the rack-mask arithmetic in armed() goes
// negative). The full six-round magazine is deliberate (#253): a joust IS
// the engagement, so rounds saved at its end are rounds wasted — the
// discipline that matters is PER-WINDOW, and the launch gates (patience,
// rear-aspect quality, shoot-shoot-look spacing) already carry it. The ace
// spends six good shots across a fight; the novice sprays its six early at
// flare-able geometry. Both are authentic.
const shots = 6

// tips_loadout is the bare armed wingtips — what a stale pre-loadout client's
// own model carries, and the grant for a join with no stores request.
func tips_loadout() loadout {
	return stores_normalize(map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	})
}

// bots_loadout is the server bot's standard: the Fox 2 fighter — six AIM-9Ms
// on tips and outboard twins (decided 2026-08-06) — when missiles are
// allowed, clean otherwise.
func bots_loadout(missiles bool) loadout {
	if !missiles {
		return stores_normalize(nil)
	}
	return stores_normalize(map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"2": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"8": map[string]any{"fixture": "twin", "stores": []any{"9m", "9m"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	})
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
		if len(s.Stores) > 0 && s.Stores[0] == "9m" {
			out = append(out, "9m"+key+"a")
		}
		if len(s.Stores) > 1 && s.Stores[1] == "9m" {
			out = append(out, "9m"+key+"b")
		}
	case "rail":
		if len(s.Stores) > 0 && s.Stores[0] == "9m" {
			out = append(out, "9m"+key)
		}
	default:
		if len(s.Stores) > 0 && s.Stores[0] == "tank" {
			out = append(out, "tank"+key)
		}
	}
	return out
}

// stores_rounds lists the missile entries in the SMS priority order
// (researched 2026-08-05): wingtips first, alternating to the opposite
// station at the same priority level, starboard seeding; twins fire the
// outer round before the inner. The k-th launch takes rounds[k].
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
	return out
}

// stores_mask is the flight-core attach bitmask for a loadout with `fired`
// rounds expended in stores_rounds order.
func stores_mask(lo loadout, fired int) uint32 {
	index := map[string]int{}
	catalog := aircraft.Get("fa18c").Stores
	for i := range catalog {
		index[catalog[i].Name] = i
	}
	spent := map[string]bool{}
	for k, name := range stores_rounds(lo) {
		if k >= fired {
			break
		}
		spent[name] = true
	}
	mask := uint32(0)
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

// stores_jettison returns the loadout with a station's departure applied
// (#18): "stores" empties the mounts and keeps the fixture, "rack" clears the
// fixture with everything on it. Wingtips (1/9) never jettison — the rails
// have no ejector.
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
// protected, live rounds leaving on their racks come off the magazine, and
// the granted loadout is re-published on the roster so every client
// re-renders this jet. Runs on the tick goroutine (the server's jettisoner
// interface), the same ownership as Step.
func (i *instance) Jettison(slot int, departures []game.Departure) {
	a := i.aircraft[slot]
	if a == nil || a.loadout == nil || !a.alive {
		return
	}
	before := stores_rounds(a.loadout)
	fired := len(before) - a.missiles
	if fired < 0 {
		fired = 0
	}
	next := a.loadout
	changed := false
	for _, d := range departures {
		if d.Station < 2 || d.Station > 8 || (d.What != "stores" && d.What != "rack") {
			continue
		}
		next = stores_jettison(next, d.Station, d.What)
		changed = true
	}
	if !changed {
		return
	}
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
	a.loadout = next
	a.missiles -= gone
	if a.missiles < 0 {
		a.missiles = 0
	}
	a.model.Stores(a.attach())
	i.events = append(i.events, map[string]any{"kind": "roster", "slot": slot, "name": a.player.Name, "team": a.team, "stores": a.loadout})
}

// attach is the craft's current mask: its granted loadout minus the rounds
// already fired. Crafts without a loadout (never granted) keep the legacy
// wingtip count mapping.
func (a *craft) attach() uint32 {
	if a.loadout == nil {
		return armed(a.missiles)
	}
	order := stores_rounds(a.loadout)
	fired := len(order) - a.missiles
	if fired < 0 {
		fired = 0
	}
	return stores_mask(a.loadout, fired)
}

// rearm re-asserts the full loadout on the model, cycling through zero so
// fuel-bearing entries see an off-to-on transition and refill — every spawn
// is a fresh, full-tank grant.
func (a *craft) rearm() {
	if a.loadout == nil {
		return
	}
	a.model.Stores(0)
	a.model.Stores(stores_mask(a.loadout, 0))
}

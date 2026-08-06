// Mochi world: loadout wire tests (#17)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"testing"
	"time"
)

// events reads stream messages until the wanted event kind arrives (the
// roster rides the reliable stream as {kind:"event", event:{...}}).
func event_wait(t *testing.T, p *probe, want string, deadline time.Duration) map[string]any {
	t.Helper()
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		message := p.receive(t)
		if text(message, "kind") != "event" {
			continue
		}
		event, _ := message["event"].(map[string]any)
		if event != nil && text(event, "kind") == want {
			return event
		}
	}
	t.Fatalf("no %s event arrived", want)
	return nil
}

// TestLoadout: a join's stores request is validated, clamped, and granted;
// the roster event publishes the granted loadout; junk is dropped; and a
// guns-only match strips missiles while the listing advertises the rule.
func TestLoadout(t *testing.T) {
	s, err := sessions_create("air", "furball", "loadout test", 4, map[string]any{"missiles": true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a := dial(t)
	defer a.session.CloseWithError(0, "done")
	request := map[string]any{
		"1": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
		"2": map[string]any{"fixture": "twin", "stores": []any{"9m", "brick"}},
		"5": map[string]any{"fixture": "pylon", "stores": []any{"tank"}},
		"9": map[string]any{"fixture": "rail", "stores": []any{"9m"}},
	}
	a.send(t, map[string]any{"kind": "join", "session": s.identifier, "name": "alpha", "protocol": protocol, "stores": request})
	if text(a.receive(t), "kind") != "welcome" {
		t.Fatal("not welcomed")
	}
	roster := event_wait(t, a, "roster", 5*time.Second)
	granted, _ := roster["stores"].(map[string]any)
	if granted == nil {
		t.Fatal("roster carries no loadout")
	}
	station2, _ := granted["2"].(map[string]any)
	if station2 == nil || text(station2, "fixture") != "twin" {
		t.Fatalf("station 2 fixture lost: %v", granted["2"])
	}
	points, _ := station2["stores"].([]any)
	if len(points) != 2 || points[0] != "9m" || points[1] != "" {
		t.Fatalf("station 2 points %v, want [9m ] with the junk dropped", points)
	}
	station5, _ := granted["5"].(map[string]any)
	if station5 == nil || text(station5, "fixture") != "pylon" {
		t.Fatalf("station 5 tank lost: %v", granted["5"])
	}

	guns, err := sessions_create("air", "furball", "guns only", 4, map[string]any{"missiles": false, "cheats": map[string]any{"ammunition": true}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := dial(t)
	defer b.session.CloseWithError(0, "done")
	b.send(t, map[string]any{"kind": "join", "session": guns.identifier, "name": "bravo", "protocol": protocol, "stores": request})
	if text(b.receive(t), "kind") != "welcome" {
		t.Fatal("bravo not welcomed")
	}
	roster = event_wait(t, b, "roster", 5*time.Second)
	granted, _ = roster["stores"].(map[string]any)
	for station, raw := range granted {
		slot, _ := raw.(map[string]any)
		if slot == nil {
			continue
		}
		list, _ := slot["stores"].([]any)
		for _, id := range list {
			if id == "9m" {
				t.Fatalf("guns-only grant kept a missile on station %s", station)
			}
		}
	}

	found := false
	for _, entry := range sessions_list("air", "") {
		if entry["session"] == guns.identifier {
			found = true
			parameters, _ := entry["parameters"].(map[string]any)
			if parameters == nil || parameters["missiles"] != false {
				t.Fatalf("listing parameters %v, want the missiles rule advertised", entry["parameters"])
			}
			cheats, _ := parameters["cheats"].(map[string]any)
			if cheats == nil || cheats["ammunition"] != true {
				t.Fatalf("listing parameters %v, want the cheat set advertised (#19)", entry["parameters"])
			}
		}
	}
	if !found {
		t.Fatal("guns session missing from the listing")
	}
}

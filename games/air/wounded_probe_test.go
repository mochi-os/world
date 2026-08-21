// Mochi world: TEMPORARY wounded-flight probe — how much of a fight do bots
// spend badly hurt but not limping, and what do they do there?
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"os"
	"testing"

	"world/game"
)

// TestWoundedBand measures how much time bots spend badly hurt and still
// fighting. The brain's only thrust-driven behaviour is a cliff at 0.35 thrust:
// below it the bot limps, above it nothing changes.
func TestWoundedBand(t *testing.T) {
	if os.Getenv("AIR_WOUNDED") == "" {
		t.Skip("measurement harness: set AIR_WOUNDED=1 to run")
	}
	heavy(t)
	type bucket struct {
		ticks, pressing, limping int
	}
	bands := map[string]*bucket{"healthy >0.90": {}, "scratched 0.60-0.90": {}, "hurt 0.35-0.60": {}, "crippled <0.35": {}}
	fights, entered, hurtFights := 0, 0, 0
	var longest float64
	for _, missiles := range []bool{false, true} {
		arm := map[bool]string{false: "guns", true: "fox2"}[missiles]
		for _, pair := range [][2]string{{"ace", "pilot"}, {"superhuman", "ace"}} {
			for seed := uint64(1); seed <= 8; seed++ {
				made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("wound%s%s%d", arm, pair[0], seed),
					Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
					Parameters: map[string]any{"missiles": missiles, "weapons": arm,
						"bots": map[string]any{pair[0]: 1.0, pair[1]: 1.0}}})
				if err != nil {
					t.Fatal(err)
				}
				i := made.(*instance)
				fights++
				run := map[*craft]int{} // consecutive ticks in the hurt band, per craft
				sawHurt := false
				for tick := uint64(0); tick < 60*240; tick++ {
					i.Step(tick, nil)
					living := 0
					for _, slot := range i.slots() {
						c := i.aircraft[slot]
						if c == nil || c.brain == nil || !c.alive || c.model == nil {
							continue
						}
						living++
						d := &c.model.State.Damage
						thrust := 1 - (d.Engine[0]+d.Engine[1])/2
						name := "healthy >0.90"
						switch {
						case thrust < 0.35:
							name = "crippled <0.35"
						case thrust < 0.60:
							name = "hurt 0.35-0.60"
						case thrust < 0.90:
							name = "scratched 0.60-0.90"
						}
						b := bands[name]
						b.ticks++
						if c.brain.mode == "limp" {
							b.limping++
						}
						if c.brain.press > 0 || c.brain.prey != nil {
							b.pressing++
						}
						if name == "hurt 0.35-0.60" {
							run[c]++
							sawHurt = true
							if s := float64(run[c]) / 60; s > longest {
								longest = s
							}
						} else {
							run[c] = 0
						}
					}
					if living < 2 {
						break
					}
				}
				if sawHurt {
					hurtFights++
					entered++
				}
				i.Close()
			}
		}
	}
	total := 0
	for _, b := range bands {
		total += b.ticks
	}
	fmt.Printf("\nWOUNDED-FLIGHT BAND, %d two-way duels (%d bot-ticks)\n", fights, total)
	for _, name := range []string{"healthy >0.90", "scratched 0.60-0.90", "hurt 0.35-0.60", "crippled <0.35"} {
		b := bands[name]
		if b.ticks == 0 {
			fmt.Printf("  %-20s   none\n", name)
			continue
		}
		fmt.Printf("  %-20s %6.2f%% of bot-time | pressing %5.1f%% of it | in limp mode %5.1f%%\n",
			name, 100*float64(b.ticks)/float64(total), 100*float64(b.pressing)/float64(b.ticks),
			100*float64(b.limping)/float64(b.ticks))
	}
	fmt.Printf("  fights that ever reached the hurt band: %d of %d | longest unbroken spell %.1f s\n",
		hurtFights, fights, longest)
}

// TestWoundedBehaviour injects the wound rather than waiting for it: the band
// never arises bot-versus-bot, only against a human who wounds and takes
// minutes to finish. The question is whether a jet with one engine gone keeps
// pressing.
func TestWoundedBehaviour(t *testing.T) {
	if os.Getenv("AIR_WOUNDED") == "" {
		t.Skip("measurement harness: set AIR_WOUNDED=1 to run")
	}
	heavy(t)
	for _, loss := range []float64{0.0, 0.5, 0.8, 1.0} { // thrust remaining 1.0, 0.75, 0.6, 0.5 (one engine gone)
		pressing, limping, ticks, modes := 0, 0, 0, map[string]int{}
		intents := map[string]int{}
		var speed, gee float64
		for seed := uint64(1); seed <= 6; seed++ {
			made, err := (&Air{}).Create(game.Session{Identifier: fmt.Sprintf("hurt%.0f%d", loss*10, seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": false, "weapons": "guns",
					"bots": map[string]any{"ace": 1.0, "pilot": 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)
			var hurt *craft
			for _, slot := range i.slots() {
				if c := i.aircraft[slot]; c != nil && c.brain != nil && c.brain.skill.library >= 4 {
					hurt = c
					break
				}
			}
			if hurt == nil {
				t.Fatal("no ace in the roster")
			}
			for tick := uint64(0); tick < 60*90; tick++ {
				i.Step(tick, nil)
				if !hurt.alive || hurt.model == nil {
					break
				}
				// Thirty seconds in, take one engine — the wound a player's
				// heater leaves — and hold it there against any repair.
				if tick >= 60*30 {
					hurt.model.State.Damage.Engine[0] = loss
					ticks++
					modes[hurt.brain.play]++
					intents[hurt.brain.intent]++
					speed += hurt.model.State.Velocity.Length()
					gee += hurt.brain.g
					if hurt.brain.mode == "limp" {
						limping++
					}
					if hurt.brain.press > 0 || hurt.brain.prey != nil {
						pressing++
					}
				}
			}
			i.Close()
		}
		top, best := "", 0
		for m, c := range modes {
			if c > best {
				top, best = m, c
			}
		}
		share := func(m map[string]int, key string) float64 { return 100 * float64(m[key]) / float64(ticks) }
		fmt.Printf("thrust %.2f: speed %3.0f m/s | g %.2f | pressing %5.1f%% | postures convert %4.1f%% finish %4.1f%% deny %4.1f%% reset %4.1f%% | commonest play %s\n",
			1-loss/2, speed/float64(ticks), gee/float64(ticks), 100*float64(pressing)/float64(ticks),
			share(intents, "convert"), share(intents, "finish"), share(intents, "deny"), share(intents, "reset"), top)
		_ = limping
	}
}

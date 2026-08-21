// Mochi world: TEMPORARY heater-employment probe — where do the machine's
// AIM-9Ms go, and what actually decides the missiles ladder?
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestHeaterEmployment scopes every number to its pairing - pooling lets the
// pilot's spray contaminate the ace's geometry - and separates how well each
// side's rounds arrive from whether heaters decide the fight at all.
func TestHeaterEmployment(t *testing.T) {
	if os.Getenv("AIR_HEATER") == "" {
		t.Skip("measurement harness: set AIR_HEATER=1 to run")
	}
	heavy(t)

	type shot struct {
		tier    string
		tail    float64 // aspect the seeker sees: the victim's attitude, as the decoy roll grades it
		span    float64 // range at release, m
		lit     bool    // victim in burner at release
		second  bool    // the second round of a pair
		closest float64
		decoyed bool
	}
	type record struct {
		shots            []shot
		wins             map[string]int
		missiles, rounds map[string]int // kills by heater, kills by gun
		carriage         []string       // what each side was still lugging when the guns decided it
		envelope, asked  map[string]int // ticks each side sat in a legal launch envelope, and how many it could ask on
		threat           map[string]int // ticks each side had a round in the air tracking it
		blocked          map[string]int // which single condition denied the shot, when only one did
	}

	books := map[string]*record{}
	order := []string{}

	for _, pair := range [][2]string{{"superhuman", "ace"}, {"ace", "pilot"}} {
		name := fmt.Sprintf("%s v %s", pair[0], pair[1])
		book := &record{wins: map[string]int{}, missiles: map[string]int{}, rounds: map[string]int{},
			envelope: map[string]int{}, asked: map[string]int{}, threat: map[string]int{}, blocked: map[string]int{}}
		books[name] = book
		order = append(order, name)

		for seed := uint64(1); seed <= 16; seed++ {
			g := New()
			made, err := g.Create(game.Session{Identifier: fmt.Sprintf("heater%s%d", pair[0], seed),
				Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
				Parameters: map[string]any{"missiles": true, "weapons": "fox2",
					"bots": map[string]any{pair[0]: 1.0, pair[1]: 1.0}}})
			if err != nil {
				t.Fatal(err)
			}
			i := made.(*instance)

			tier := map[int]string{}
			for _, slot := range i.slots() {
				c := i.aircraft[slot]
				if c == nil || c.brain == nil {
					continue
				}
				tier[slot] = pair[1]
				if c.brain.skill.library == skills[pair[0]].library && c.brain.skill.wander == skills[pair[0]].wander {
					tier[slot] = pair[0]
				}
			}

			live := map[uint64]*shot{} // rounds still in the air, by launch number
			last := map[int]uint64{}   // each shooter's previous launch tick
			burst := map[int]uint64{}  // last tick a warhead went off on this slot
			blame := map[int]int{}     // and who shot it
			dead := map[int]bool{}     // deaths already booked
			// The fight ENDS at the first death, as TestLadderDuel scores it: pooling
			// respawn kills measures spawn-camping as if it were doctrine.
			finished := false
			for tick := uint64(0); tick < 60*240 && !finished; tick++ {
				i.Step(tick, nil)
				flying := map[uint64]bool{}
				for _, m := range i.flying {
					if m.radar != nil {
						continue // the AIM-120 path has its own spacing and DLZ; not this rule
					}
					flying[m.number] = true
					target := i.aircraft[m.target]
					if target == nil || target.model == nil {
						continue
					}
					direction, distance := i.bearing(m.position, target.model.State.Position)
					s, known := live[m.number]
					if !known {
						// First sight is the tick after release: the round is
						// still on the shooter's nose, so this IS the release
						// geometry to within a metre.
						s = &shot{
							tier:    tier[m.shooter],
							tail:    direction.Dot(target.model.State.Attitude.Rotate(flight.Vec3{X: 1})),
							span:    distance,
							lit:     target.latest.Reheat > 0.05,
							second:  last[m.shooter] != 0 && tick-last[m.shooter] < 60,
							closest: distance,
						}
						live[m.number] = s
						last[m.shooter] = tick
					}
					if distance < s.closest {
						s.closest = distance
					}
					if distance < missile_fuse {
						burst[m.target], blame[m.target] = tick, m.shooter
					}
					if m.blind > 0 || m.loose {
						s.decoyed = true
					}
				}
				for number, s := range live {
					if !flying[number] {
						book.shots = append(book.shots, *s)
						delete(live, number)
					}
				}
				// Time under threat: does the ace's volume simply keep the
				// machine defensive, and is that where the positional deficit
				// comes from?
				for _, m := range i.flying {
					if m.radar == nil && !m.loose && m.blind <= 0 {
						if name, known := tier[m.target]; known {
							book.threat[name]++
						}
					}
				}
				// How often does the top tier sit in a launch envelope it never
				// asks to use? The missile request rides on b.shoot, which is
				// the GUN decision — so heater geometry outside gun range can
				// be structurally unaskable.
				for _, slot := range i.slots() {
					c, foe := i.aircraft[slot], (*craft)(nil)
					if c == nil || c.brain == nil || !c.alive || c.model == nil {
						continue
					}
					for _, other := range i.slots() {
						if other != slot && i.aircraft[other] != nil && i.aircraft[other].alive && i.aircraft[other].model != nil {
							foe = i.aircraft[other]
						}
					}
					if foe == nil || c.brain.missiles == 0 {
						continue
					}
					b, me := c.brain, &c.model.State
					direction, distance := i.bearing(me.Position, foe.model.State.Position)
					v := foe.model.State.Velocity
					if v.Length() < 1 {
						continue
					}
					aspect := direction.Dot(v.Normalize())
					nose := me.Attitude.Rotate(flight.Vec3{X: 1})
					margin := b.tactics.missile.margin + b.tactics.missile.step*b.skill.discipline
					limit := missile_range * (b.tactics.missile.base + b.tactics.missile.slope*max(0, aspect)) *
						(b.tactics.missile.floor + b.tactics.missile.gain*b.skill.discipline)
					rear := aspect > b.tactics.missile.tail
					near := distance < b.tactics.missile.span && distance < limit
					pointed := nose.Dot(direction) > margin
					if !rear || !near || !pointed {
						// Which ONE condition is denying the shot? Counting only the
						// lone blocker names the binding constraint; a tick failing
						// three of them says nothing about which to relax.
						switch {
						case !rear && near && pointed:
							book.blocked[tier[slot]+" aspect only"]++
						case rear && !near && pointed:
							book.blocked[tier[slot]+" range only"]++
						case rear && near && !pointed:
							book.blocked[tier[slot]+" nose only"]++
						}
						continue
					}
					book.envelope[tier[slot]]++
					if b.shoot {
						book.asked[tier[slot]]++
					}
				}
				// Book the kill the tick a jet stops flying, and credit the
				// weapon: a warhead that went off on it moments ago owns the
				// kill, otherwise the guns do.
				for _, slot := range i.slots() {
					c := i.aircraft[slot]
					if c == nil || c.brain == nil || dead[slot] {
						continue
					}
					if c.model != nil && c.alive {
						continue
					}
					dead[slot] = true
					// air.go stamps Damager for both weapons; the burst clock
					// is only used to say WHICH one finished him.
					killer, known := tier[c.condition.Damager]
					if !known {
						killer = "unattributed"
					}
					if at, hit := burst[slot]; hit && tick-at < 30 {
						book.missiles[killer]++
					} else {
						book.rounds[killer]++
						// The carriage question: a gun fight between a jet that
						// has shot its rack away and one still lugging six.
						victim, shooter := 0, 0
						if c.brain != nil {
							victim = c.brain.missiles
						}
						if k := i.aircraft[c.condition.Damager]; k != nil && k.brain != nil {
							shooter = k.brain.missiles
						}
						book.carriage = append(book.carriage,
							fmt.Sprintf("%s killed %s by gun: shooter had %d aboard, victim %d",
								killer, tier[slot], shooter, victim))
					}
					book.wins[killer]++
					_ = blame
					finished = true
				}
			}
			for _, s := range live {
				book.shots = append(book.shots, *s)
			}
			i.Close()
		}
	}

	// A round "arrives" when it fuses: pursue detonates at closest approach
	// inside the 12 m envelope, so the sampled minimum is the arrival test.
	arrived := func(s shot) bool { return s.closest < missile_fuse }

	table := func(shots []shot, title string, key func(shot) string) {
		groups := map[string][]shot{}
		for _, s := range shots {
			groups[key(s)] = append(groups[key(s)], s)
		}
		names := []string{}
		for k := range groups {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Printf("    %s\n", title)
		for _, name := range names {
			g := groups[name]
			hits, decoys := 0, 0
			var span float64
			for _, s := range g {
				if arrived(s) {
					hits++
				}
				if s.decoyed {
					decoys++
				}
				span += s.span
			}
			fmt.Printf("      %-24s %4d shots | arrived %5.1f%% | decoyed %5.1f%% | mean release %5.0f m\n",
				name, len(g), 100*float64(hits)/float64(len(g)), 100*float64(decoys)/float64(len(g)), span/float64(len(g)))
		}
	}

	for _, name := range order {
		book := books[name]
		fmt.Printf("\n=== %s (16 seeds) ===\n", name)
		tiers := []string{}
		for tier := range book.wins {
			tiers = append(tiers, tier)
		}
		for tier := range book.missiles {
			if !contains(tiers, tier) {
				tiers = append(tiers, tier)
			}
		}
		sort.Strings(tiers)
		for _, tier := range tiers {
			var mine []shot
			for _, s := range book.shots {
				if s.tier == tier {
					mine = append(mine, s)
				}
			}
			hits := 0
			for _, s := range mine {
				if arrived(s) {
					hits++
				}
			}
			rate := 0.0
			if len(mine) > 0 {
				rate = 100 * float64(hits) / float64(len(mine))
			}
			fmt.Printf("  %-11s kills: %d by heater, %d by gun | %3d rounds fired, %4.1f%% arrived | %4d ticks in a legal envelope (%.2f s per fight)\n",
				tier, book.missiles[tier], book.rounds[tier], len(mine), rate, book.envelope[tier], float64(book.envelope[tier])/60/16)
			fmt.Printf("              under threat for %d ticks (%.1f s per fight)\n", book.threat[tier], float64(book.threat[tier])/60/16)
			for _, why := range []string{"aspect only", "range only", "nose only"} {
				fmt.Printf("              denied by %-12s %5d ticks (%.1f s per fight)\n", why, book.blocked[tier+" "+why], float64(book.blocked[tier+" "+why])/60/16)
			}
		}
		for _, line := range book.carriage {
			fmt.Printf("    %s\n", line)
		}
		var top []shot
		for _, s := range book.shots {
			if s.tier == strings_first(name) {
				top = append(top, s)
			}
		}
		if len(top) > 0 {
			fmt.Printf("  -- %s's own shot selection --\n", strings_first(name))
			table(top, "BY ASPECT", func(s shot) string {
				switch {
				case s.tail < 0.3:
					return "a head-on <0.3"
				case s.tail < 0.6:
					return "b beam 0.3-0.6"
				case s.tail < 0.85:
					return "c quarter 0.6-0.85"
				}
				return "d stern >0.85"
			})
			table(top, "BY RANGE", func(s shot) string {
				switch {
				case s.span < 800:
					return "a inside 800 m"
				case s.span < 1500:
					return "b 800-1500 m"
				case s.span < 2200:
					return "c 1500-2200 m"
				}
				return "d beyond 2200 m"
			})
			table(top, "BY PLUME", func(s shot) string {
				if s.lit {
					return "lit (burner)"
				}
				return "cold (military)"
			})
			table(top, "BY PAIR POSITION", func(s shot) string {
				if s.second {
					return "second of pair"
				}
				return "first of pair"
			})
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func strings_first(pairing string) string {
	for n := 0; n+2 < len(pairing); n++ {
		if pairing[n] == ' ' && pairing[n+1] == 'v' && pairing[n+2] == ' ' {
			return pairing[:n]
		}
	}
	return pairing
}

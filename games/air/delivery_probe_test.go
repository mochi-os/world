// Mochi world: the delivery probe — how much of the g the brain asks for reaches the jet
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Measurement only, no assertions: set AIR_POINT=1.
//
// A recorded human fight (01a0b090, 2026-09-17) showed the ace using a flat
// 70-71% of the wing available to it in EVERY play, press included, with the
// 90th percentile at 71-73%. Flat across plays means a ceiling downstream of
// the laws, and the 0.85 aero cap alone would read 85%. This walks the chain
// one link at a time - the law's g, corner discipline, the cap, the stick
// compose() sends, what that stick means to the pitch law, and the load that
// arrives.
//
// What it found (2026-09-18, ace, sub-corner): cap 2.62 g = 85% of the wing;
// the stick asked the law for 2.08 g; 2.11 g arrived = 71%. compose() was
// inverting a simpler law than the one flying - level as cos γ and the bare
// 7.5 g placard, where the law's centre is backed off above 0.15 rad of alpha
// and its ceiling schedules down with weight. With compose() reading the law's
// own range (flight.Model.Envelope) the stick asks for the capped g exactly and
// 94% of it arrives, 79% of the wing; the rest is the law's own lag. That repair
// was DECLINED by the doctrine gates the same day (three of five red: see
// compose() in bot.go), so this probe reads the shortfall again, on purpose.

package air

import (
	"fmt"
	"math"
	"os"
	"sort"
	"testing"

	"world/games/air/aircraft"
	"world/games/air/flight"
)

func TestDeliveryProbe(t *testing.T) {
	if os.Getenv("AIR_POINT") == "" {
		t.Skip("measurement probe: set AIR_POINT=1")
	}
	type sample struct{ pace, law, corner, capped, stick, ceiling, asked, delivered, alpha, wing float64 }
	for _, level := range []string{"pilot", "ace"} {
		b := NewBandit(level, 7, 250000, "", false, true, "fox2", 0)
		b.Spawn(flight.Vec3{X: 1500, Y: 3000}, flight.Vec3{X: -150})
		environment := flight.Environment{Seed: 7, Wrap: 250000}
		player := flight.New(aircraft.Get("fa18c"), environment, flight.World{Sea: sea})
		player.State = flight.Level(player, flight.Vec3{Y: 3000}, flight.Vec3{X: 1}, 140, fuel)
		words := make([]float64, flight.Size)
		var samples []sample
		for tick := uint64(1); tick <= 60*90; tick++ {
			// A slow, tight, level turn: a target that keeps the bandit below
			// corner and pulling, which is where the recorded fight was held.
			rate := 9.81 * math.Sqrt(3.2*3.2-1) / 140
			heading := rate * float64(tick) / 60
			player.State.Velocity = flight.Vec3{X: 140 * math.Cos(heading), Z: 140 * math.Sin(heading)}
			player.State.Position = player.State.Position.Add(player.State.Velocity.Scale(1.0 / 60))
			player.State.Encode(words)
			b.Mirror(words, false, true)
			b.Menace(nil)
			b.Step()
			m := b.craft.model
			speed := m.State.Velocity.Length()
			pace := corner(m)
			if tick%6 != 0 || speed >= pace {
				continue
			}
			d := b.craft.brain.demand
			if d.Capped < 2 {
				continue // not turning: nothing to deliver
			}
			limit := m.Airframe.Limit.Positive
			base, top := m.Envelope(0, false) // the pitch law's own command range: what a stick fraction MEANS
			samples = append(samples, sample{
				pace: speed / pace, law: d.Law, corner: d.Corner, capped: d.Capped, stick: d.Stick,
				ceiling:   top,
				asked:     base + d.Stick*(top-base), // what that stick means to the FCS (fcs.go envelope: demand = level + stick*(ceiling-level))
				delivered: m.Nz(), alpha: m.Alpha() * 180 / math.Pi,
				wing: limit * (speed / pace) * (speed / pace),
			})
		}
		if len(samples) == 0 {
			fmt.Printf("%s: no sub-corner turning samples\n", level)
			continue
		}
		median := func(pick func(sample) float64) float64 {
			values := make([]float64, len(samples))
			for n, s := range samples {
				values[n] = pick(s)
			}
			sort.Float64s(values)
			return values[len(values)/2]
		}
		mass := b.craft.model.Mass()
		fmt.Printf("\n%s: %d sub-corner turning samples, mass %.0f kg, FCS ceiling %.2f g of %.1f (schedule %.3f)\n",
			level, len(samples), mass, median(func(s sample) float64 { return s.ceiling }), b.craft.model.Airframe.Limit.Positive,
			math.Min(1, b.craft.model.Airframe.Limit.Reference/mass))
		fmt.Printf("  speed/corner median %.2f, alpha median %.1f deg\n",
			median(func(s sample) float64 { return s.pace }), median(func(s sample) float64 { return s.alpha }))
		fmt.Println("  stage                         median g   share of wing")
		for _, row := range []struct {
			name string
			pick func(sample) float64
		}{
			{"law commands", func(s sample) float64 { return s.law }},
			{"after corner discipline", func(s sample) float64 { return s.corner }},
			{"after the aero cap", func(s sample) float64 { return s.capped }},
			{"what the stick means to FCS", func(s sample) float64 { return s.asked }},
			{"delivered (nz)", func(s sample) float64 { return s.delivered }},
		} {
			g := median(row.pick)
			share := median(func(s sample) float64 { return row.pick(s) / s.wing })
			fmt.Printf("  %-28s %8.2f   %8.0f%%\n", row.name, g, share*100)
		}
		fmt.Printf("  delivered / capped, median:   %.3f   (1.000 would mean the cap is what arrives)\n",
			median(func(s sample) float64 { return s.delivered / s.capped }))
		fmt.Printf("  stick-implied / capped:       %.3f   (1.000 means the stick asks the law for exactly the capped g)\n",
			median(func(s sample) float64 { return s.asked / s.capped }))
	}
}

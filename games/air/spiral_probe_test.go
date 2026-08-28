// Mochi world: the spiral-deadlock probe (#42)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// spiraler flies the user's standard fox-2 recipe (2026-08-28), the pattern
// that reliably beats the ace: hold the two-circle descending rate fight as an
// equal — co-altitude, corner speed, conceding nothing — and when the bot
// leaves the stalemate low, keep the height it donated, roll onto its high
// six, and convert from lag. The deadlock is a Nash equilibrium: the probe
// never defects from it, so every conversion it scores was paid for by the
// bot's own exit. The pouncer (#64) starts ON the perch; this probe EARNS it,
// which is the state the defect actually lives in.
type spiraler struct {
	phase  string
	centre flight.Vec3
	angle  float64
	roof   float64
	since  uint64
}

func (s *spiraler) fly(me, foe *flight.State, tick uint64) map[string]any {
	const dt = 1.0 / 60
	gap := me.Position.Y - foe.Position.Y
	apart := me.Position.Subtract(foe.Position).Length()
	if s.phase == "" {
		s.phase = "deadlock"
		s.centre = me.Position.Add(foe.Position).Scale(0.5)
		s.roof = me.Position.Y
	}
	// The circle's centre drifts toward the midpoint, so the two circles stay
	// engaged however the bot repositions; the probe's altitude follows the
	// bot's honest rate-fight descent at a capped rate — a mutual spiral is
	// followed, a dive out is not, and the cap is what banks the height.
	mid := me.Position.Add(foe.Position).Scale(0.5)
	toward := mid.Subtract(s.centre)
	toward.Y = 0
	if reach := toward.Length(); reach > 1 {
		s.centre = s.centre.Add(toward.Scale(math.Min(reach, 45*dt) / reach))
	}
	s.roof += clamp(foe.Position.Y-s.roof, -9*dt, 6*dt)
	held := tick - s.since
	switch s.phase {
	case "deadlock":
		if gap > 250 {
			s.phase, s.since = "bank", tick // he left the equilibrium low
		}
	case "bank":
		// The user's "wait a little longer": keep the spiral while the split
		// grows, so the perch is unambiguous before committing the nose.
		if gap > 500 || held > 4*60 {
			s.phase, s.since = "convert", tick
		} else if gap < 150 {
			s.phase, s.since = "deadlock", tick // he came back up: stalemate resumes
		}
	case "convert":
		if gap < 100 && held > 3*60 {
			s.phase, s.since = "deadlock", tick // height spent or he rejoined: back to the equilibrium
		} else {
			return pursue(me, foe) // roll in from above, lag to his six, fire on parameters
		}
	}
	// Deadlock and bank both orbit a ghost on the circle: ~800 m radius at
	// corner-ish pace, at the probe's own capped altitude — never the bot's
	// dived one.
	s.angle += 0.21 * dt * 60 / 60
	ghost := flight.State{}
	ghost.Position = flight.Vec3{X: s.centre.X + 800*math.Cos(s.angle), Y: s.roof, Z: s.centre.Z + 800*math.Sin(s.angle)}
	ghost.Velocity = flight.Vec3{X: -170 * math.Sin(s.angle), Z: 170 * math.Cos(s.angle)}
	data := pursue(me, &ghost)
	data["fire"] = false
	if apart < 500 && gap < -100 {
		// He is converting from above: honest defence, break into him rather
		// than orbiting on rails.
		data = pursue(me, foe)
		data["fire"] = false
	}
	return data
}

// TestSpiralDefection (#42): the ace against the equilibrium-holder. The
// deadlocked descending spiral is a Nash equilibrium — leaving it low donates
// the height differential to the opponent, and the user converts that
// donation reliably (recording 01a0496dbd0a: lag co-altitude at t=96, low at
// t=104, 5,700 to 3,400 ft by t=112, dead at t=157). The gate reads the
// CONSEQUENCES — perch donated and time spent low — not the exit count: a bot
// that dives out once and stays under registers few exits and maximal
// donation, so entry-counting is anti-correlated with holding. Measured
// bands: pre-stack 437,480 m·s donated / low 40.5%% of ticks; with the
// threat-faded stack term 125,934 m·s / 19.7%%. Gates sit between with the
// noise margin on the fixed side (#69 recalibration lesson). The defection
// score tables at first exit stay logged: they name the play that outbid
// holding when this regresses.
func TestSpiralDefection(t *testing.T) {
	heavy(t)
	defections, blunders, total, downed, lost := 0, 0, 0, 0, 0
	donated, windows := 0.0, 0
	modes := map[string]int{}
	for seed := uint64(1); seed <= 12; seed++ {
		g := New()
		made, err := g.Create(game.Session{Identifier: fmt.Sprintf("spiral%d", seed), Game: "air", Mode: "furball", Capacity: 8, Seed: seed,
			Parameters: map[string]any{"missiles": false, "bots": map[string]any{"ace": 1.0}}})
		if err != nil {
			t.Fatal(err)
		}
		i := made.(*instance)
		if _, err := i.Join(game.Player{Identity: "", Name: "human", Slot: 0}); err != nil {
			t.Fatal(err)
		}
		bot := -1
		for slot, a := range i.aircraft {
			if a != nil && a.brain != nil {
				bot = slot
			}
		}
		if bot < 0 {
			t.Fatal("no bot in the session")
		}
		place(i, bot, 0, -1500)
		me := &i.aircraft[bot].model.State
		foe := &i.aircraft[0].model.State
		probe := &spiraler{}
		was, tabled := "", false
		settled := uint64(0) // ticks the fight has continuously qualified as a deadlock
		for tick := uint64(0); tick < 150*60; tick++ {
			data := probe.fly(foe, me, tick)
			i.Step(tick, map[int][]game.Input{0: {{Data: data}}})
			if !i.aircraft[0].alive || !i.aircraft[bot].alive ||
				i.aircraft[0].model == nil || i.aircraft[bot].model == nil {
				break
			}
			total++
			b := i.aircraft[bot].brain
			modes[b.play]++
			gap := foe.Position.Y - me.Position.Y
			apart := foe.Position.Subtract(me.Position).Length()
			// The stalemate must be ESTABLISHED before an exit counts: the
			// merge scramble is not the equilibrium. Co-altitude, mutual
			// range, held continuously for eight seconds.
			if probe.phase == "deadlock" && math.Abs(gap) < 250 && apart > 500 && apart < 2200 {
				settled++
			} else if probe.phase != "deadlock" || math.Abs(gap) > 400 {
				settled = 0
			}
			// A defection: the bot commits to low from inside the established
			// stalemate, where holding is the equilibrium and diving donates
			// the perch.
			if b.play == "low" && was != "low" && settled > 8*60 {
				defections++
				if gap > -100 {
					blunders++ // co-altitude or already beneath: the pure donation. A dive off a held height advantage is a licensed attack, not a defection.
				}
				if !tabled {
					scores := map[string]float64{}
					sim := flight.New(i.aircraft[bot].model.Airframe, i.aircraft[bot].model.Environment, i.aircraft[bot].model.World)
					if b.prey != nil {
						i.choose(bot, i.aircraft[bot], b, sim, b.prey, tick, apart, scores)
						t.Logf("seed %d defection t=%5.1fs gap %+4.0f m range %4.0f m: %v", seed, float64(tick)/60, gap, apart, scores)
					}
					tabled = true
				}
			}
			was = b.play
			if probe.phase == "convert" {
				donated += gap / 60 // metre-seconds of perch the bot donated, per tick
				// The SHOOT-cue analogue: the probe in the bot's rear quarter
				// at seeker range with its nose on — the window the user's
				// spaced 9M pair flies out of.
				if apart > 400 && apart < 2500 && gap > 150 {
					los := me.Position.Subtract(foe.Position).Scale(1 / math.Max(apart, 1))
					tail := me.Velocity
					if tail.Length() > 1 {
						tail = tail.Normalize()
					}
					nose := foe.Attitude.Rotate(flight.Vec3{X: 1})
					if nose.Dot(los) > 0.9 && tail.Dot(los) < -0.3 {
						windows++
					}
				}
			}
		}
		if i.aircraft[bot].kills > 0 {
			downed++
		}
		if !i.aircraft[bot].alive || i.aircraft[bot].model == nil {
			lost++
		}
		i.Close()
	}
	t.Logf("spiral: defections %d (blunders %d) across 12 fights | perch donated %.0f m·s | 9M-quality windows %.1f s per fight | bandit lost %d, probe downed %d | of %d ticks | plays %v",
		defections, blunders, donated, float64(windows)/60/12, lost, downed, total, modes)
	if donated > 250000 {
		t.Errorf("perch donated %.0f m·s across 12 deadlock fights: the Nash-equilibrium defection is back (fixed band ~126k, defect baseline 437k)", donated)
	}
	if share := float64(modes["low"]) / math.Max(float64(total), 1); share > 0.30 {
		t.Errorf("low flown %.0f%% of the deadlock fights: the spiral exit dominates again (fixed band ~20%%, defect baseline 40%%)", share*100)
	}
}

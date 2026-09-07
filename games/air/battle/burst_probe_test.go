// The #103 measurement: what a fusing warhead does as a function of miss
// distance, and whether the damage it deals is felt in the air.
//
// The 2026-09-06 joust (recording 01a078652d8b) put three AIM-9Ms on an ace at
// 7.9, 10.6 and 11.8 m. It logged 8.71 of structural damage, zero engine loss
// and zero fire, and the jet then flew 639 kt, climbed to 20,497 ft and pulled
// 6.8 g — indistinguishable from clean. This probe measures both halves of
// that: the damage a burst deals across the whole 3-15 m band, and the flight
// performance the damaged airframe retains. Measurement only, no assertions:
// the gates live in TestAsternProbe and TestBlast.
package battle

import (
	"fmt"
	"os"
	"testing"

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

// reach flies a model at full military power from trimmed level flight and
// returns the speed it holds after the run: the single number that says
// whether damage is felt. A clean jet and a wounded one diverge here or the
// damage is bookkeeping.
func fly(damage flight.DamageState, seed uint64) (float64, float64) {
	m := flight.New(fa18c.Airframe, flight.Environment{Seed: seed}, flight.World{Sea: 0})
	m.State = flight.Level(m, flight.Vec3{Y: 3000}, flight.Vec3{X: 1}, 200, fa18c.Airframe.Mass.Fuel*0.7)
	m.State.Damage = damage.Copy()
	stick := 0.0
	peak := 0.0
	for i := 0; i < 240*40; i++ {
		// Hold altitude gently so the run measures thrust against drag, not a
		// dive; the pitch gain is small enough never to fight the damage.
		stick = clamp(stick+((3000-m.State.Position.Y)*0.0004-m.State.Velocity.Y*0.004-stick*2)*0.004, -0.4, 1)
		m.Step(flight.Inputs{Throttle: 1, Pitch: stick})
		if v := m.State.Velocity.Length(); v > peak {
			peak = v
		}
	}
	return m.State.Velocity.Length() * 1.94384, peak * 1.94384
}

func TestBurstProbe(t *testing.T) {
	if os.Getenv("AIR_BURST") == "" {
		t.Skip("measurement probe: set AIR_BURST=1")
	}
	// Astern and below, the geometry two of the three recorded fusings flew:
	// the seeker guides to the tailpipe, so this is the honest heater bearing.
	const runs = 400
	clean, cleanPeak := fly(flight.DamageState{}, 1)
	fmt.Printf("clean reference: %.0f kt after 40 s at MIL from 200 m/s (peak %.0f)\n\n", clean, cleanPeak)
	fmt.Println(" miss   kills  connect  frags | thrust  elements   leak   drag | anyEng >.5Eng fire heavy | worst-case speed")
	for _, miss := range []float64{3, 4, 5, 5.4, 5.6, 6, 7, 7.9, 9, 10.6, 11.8, 12.5, 14} {
		kills, connected, frags, fires := 0, 0, 0, 0
		anyThrust, hardThrust, heavy := 0, 0, 0
		thrust, elements, leak, drag := 0.0, 0.0, 0.0, 0.0
		var worst flight.DamageState
		worstSum := -1.0
		for seed := uint64(1); seed <= runs; seed++ {
			m := flight.New(fa18c.Airframe, flight.Environment{Seed: seed}, flight.World{Sea: 0})
			m.State = flight.Level(m, flight.Vec3{Y: 3000}, flight.Vec3{X: 1}, 200, fa18c.Airframe.Mass.Fuel*0.7)
			body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe),
				Damage: &m.State.Damage, Condition: &Condition{Damager: -1}}
			// Body frame is world frame at identity attitude, so the point is
			// the burst offset: astern and a little below.
			point := flight.Vec3{X: -miss * 0.96, Y: -miss * 0.28}
			kill, events, hits := Blast(point, 650, flight.Vec3{}, flight.Quat{W: 1}, body, 0, seed, 3, seed)
			if kill {
				kills++
				continue
			}
			if len(hits) > 0 {
				connected++
				frags += len(hits)
			}
			for _, e := range events {
				if e.Kind == "fire" {
					fires++
					break
				}
			}
			d := &m.State.Damage
			if d.Engine[0]+d.Engine[1] > 0.01 {
				anyThrust++
			}
			if d.Engine[0] > 0.5 || d.Engine[1] > 0.5 {
				hardThrust++
			}
			thrust += d.Engine[0] + d.Engine[1]
			sum := 0.0
			for _, v := range d.Element {
				sum += v
			}
			elements += sum
			if sum > 4 {
				heavy++
			}
			leak += d.Leak
			drag += d.Drag
			if sum+d.Engine[0]+d.Engine[1] > worstSum {
				worstSum = sum + d.Engine[0] + d.Engine[1]
				worst = d.Copy()
			}
		}
		n := float64(runs)
		// Fly the WORST case of the sample: the mean hides whether the model
		// can express damage at all.
		hurt, _ := fly(worst, 1)
		fmt.Printf("%5.1f  %4d/%d  %4d/%d  %5.1f | %6.3f  %8.3f  %5.2f  %5.3f | %4.0f%% %4.0f%% %4.0f%% %4.0f%% | %6.0f kt (%+.0f)\n",
			miss, kills, runs, connected, runs, float64(frags)/n,
			thrust/n, elements/n, leak/n, drag/n,
			float64(anyThrust)/n*100, float64(hardThrust)/n*100, float64(fires)/n*100, float64(heavy)/n*100,
			hurt, hurt-clean)
	}
}

// The #78 gate: fragment damage by burst bearing at the recorded geometry
// (miss 7.8 m, closure 624 m/s — the 2026-08-24 joust fusing that wounded
// nothing). A burst on the tail axis used to funnel every ray into the
// tail-cone Fuselage station, whose strike case carries only drag and a rare
// jam, so the better the stern-chase aim the less the warhead did; fragments
// now transit body stations onto what they shadow, and this gate holds the
// floor: an astern fringe burst must wound what it reaches.
package battle

import (
	"fmt"
	"testing"

	"world/games/air/aircraft/fa18c"
	"world/games/air/flight"
)

func TestAsternProbe(t *testing.T) {
	bearings := []struct {
		name  string
		point flight.Vec3 // body frame, miss 7.8 m
	}{
		{"astern", flight.Vec3{X: -7.8}},
		{"astern-off", flight.Vec3{X: -7.6, Y: 0.9, Z: 1.2}},
		{"ahead", flight.Vec3{X: 7.8}},
		{"abeam", flight.Vec3{Z: 7.8}},
		{"above", flight.Vec3{Y: 7.8}},
		{"quarter", flight.Vec3{X: -5.5, Z: 5.5}},
	}
	for _, b := range bearings {
		connected, kills, strikes := 0, 0, 0
		thrust, wing, other := 0.0, 0.0, 0.0
		burning := 0
		const runs = 300
		for seed := uint64(1); seed <= runs; seed++ {
			m := flight.New(fa18c.Airframe, flight.Environment{Seed: seed}, flight.World{Sea: 0})
			m.State = flight.Level(m, flight.Vec3{Y: 2000}, flight.Vec3{X: 1}, 180, fa18c.Airframe.Mass.Fuel*0.7)
			body := &Body{Airframe: fa18c.Airframe, Parts: Parts(fa18c.Airframe), Damage: &m.State.Damage, Condition: &Condition{Damager: -1}}
			// Attitude identity: the body frame IS the world frame, so the probe
			// point is the burst point.
			kill, events, hits := Blast(b.point, 624, flight.Vec3{}, flight.Quat{W: 1}, body, 0, seed, 3, seed)
			if kill {
				kills++
				continue
			}
			if len(hits) > 0 {
				connected++
				strikes += len(hits)
			}
			_ = events
			thrust += m.State.Damage.Engine[0] + m.State.Damage.Engine[1]
			for e, v := range m.State.Damage.Element {
				surface := -1
				for _, p := range body.Parts {
					if p.Kind == Structure && p.Index == e {
						surface = p.Surface
						break
					}
				}
				if surface >= 0 && fa18c.Airframe.Surfaces[surface].Kind == flight.Wing {
					wing += v
				} else {
					other += v
				}
			}
			if body.Condition.Burning {
				burning++
			}
		}
		mean := 0.0
		if connected > 0 {
			mean = float64(strikes) / float64(connected)
		}
		fmt.Printf("%-10s  kills %3d/300  connected %3d/300  mean fragments %.1f | visible channels: thrust %.3f  wing-elements %.3f  other-elements %.3f  burning %d/300\n",
			b.name, kills, connected, mean, thrust/300, wing/300, other/300, burning)
		if (b.name == "astern" || b.name == "astern-off") && (thrust+wing+other)/300 < 0.5 {
			t.Errorf("%s: a 7.8 m astern fringe burst wounds almost nothing (mean visible damage %.3f): the tail-cone station is armouring the airframe again", b.name, (thrust+wing+other)/300)
		}
	}
}

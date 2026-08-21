// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"testing"

	"world/games/air/flight"
	"world/games/air/round"
)

// TestHeatZone pins the AIM-9M ladder to the physics the cockpit's SHOOT cue
// will lean on (#47): the zone must order with aspect and closure the way
// the seeker's own rules dictate, and the floor must be inside the ceiling.
func TestHeatZone(t *testing.T) {
	// A shooter at 400 kt, 4 km up; targets set 3 km out at 400 kt.
	shooter := round.Target{Position: flight.Vec3{Y: 4000}, Velocity: flight.Vec3{X: 205}}
	place := func(aspect float64) round.Target {
		// aspect: the target's heading relative to the line of sight — 0 flies
		// away (a tail shot), pi flies at the shooter (head-on).
		return round.Target{Position: flight.Vec3{X: 3000, Y: 4000}, Velocity: flight.Vec3{X: 205 * math.Cos(aspect), Z: 205 * math.Sin(aspect)}}
	}
	tail := Heat(shooter, place(0), flight.Vec3{}, 0, 0)
	beam := Heat(shooter, place(math.Pi/2), flight.Vec3{}, 0, 0)
	head := Heat(shooter, place(math.Pi), flight.Vec3{}, 0, 0)
	for name, z := range map[string]round.Zone{"tail": tail, "beam": beam, "head": head} {
		if z.Max <= 0 {
			t.Errorf("%s: no Rmax at all (%+v)", name, z)
		}
		if z.Escape > z.Max || z.Minimum >= z.Max {
			t.Errorf("%s: the ladder is out of order: %+v", name, z)
		}
	}
	// The zone is the SEEKER's, not the kinematics': the acquisition cap is what
	// orders stern > beam > head-on, the same cap acquire() applies to a lock.
	if !(tail.Max > beam.Max && beam.Max > head.Max) {
		t.Errorf("Rmax should order stern > beam > head-on: %.0f / %.0f / %.0f m", tail.Max, beam.Max, head.Max)
	}
	if math.Abs(tail.Max-missile_range) > 50 {
		t.Errorf("a stern shot's Rmax should be the seeker's full reach (%.0f m), got %.0f", missile_range, tail.Max)
	}
	if math.Abs(head.Max-missile_range*0.15) > 50 {
		t.Errorf("a cold head-on nose should be lockable only to the plume floor (%.0f m), got %.0f", missile_range*0.15, head.Max)
	}
	// Lit up, the head-on reach grows: the plume is the beacon.
	if lit := Heat(shooter, place(math.Pi), flight.Vec3{}, 1, 0); lit.Max <= head.Max {
		t.Errorf("a burner-lit head-on nose should be lockable further than a cold one: %.0f v %.0f m", lit.Max, head.Max)
	}
	t.Logf("tail %+v", tail)
	t.Logf("beam %+v", beam)
	t.Logf("head %+v", head)
}

// TestHeatRecordedShots replays twelve recorded launches through the ladder:
// the pilot's six beam shots read inside the zone, the two head-on shots
// outside the seeker's cold-nose reach read as tone only, and four rear-aspect
// shots read as no-escape.
func TestHeatRecordedShots(t *testing.T) {
	type shot struct {
		name                      string
		sx, sy, sz, svx, svy, svz float64
		tx, ty, tz, tvx, tvy, tvz float64
		ax, ay, az                float64
		lit                       float64 // the target's afterburner at launch, from the recording
		least                     float64 // the recorded closest approach, m
	}
	shots := []shot{
		{"own t=39.1", -17382154, 3487, 3133275, 72.5, 128.6, -72.9, -17381537, 5192, 3132945, -107.9, 54.0, -185.7, -29.1, -68.9, 12.4, 1, 131},
		{"own t=41.9", -17382072, 3878, 3133134, -0.4, 147.4, -42.3, -17381900, 5212, 3132487, -139.3, 6.7, -165.3, 1.8, 3.1, -12.6, 0.59, 684},
		{"own t=45.2", -17382196, 4292, 3132924, -64.9, 82.4, -87.8, -17382360, 5121, 3131896, -136.3, -101.7, -150.3, -1.1, -59.0, 40.9, 0.29, 73},
		{"own t=46.6", -17382296, 4383, 3132792, -78.1, 49.5, -100.9, -17382551, 4941, 3131727, -147.6, -141.0, -106.8, -11.5, 5.6, 3.7, 0.02, 29},
		{"own t=51.0", -17382699, 4414, 3132284, -104.8, -33.6, -127.2, -17383244, 4390, 3131105, -194.1, -111.6, -135.2, -29.4, 10.6, 48.5, 0, 451},
		{"own t=54.2", -17383096, 4294, 3131899, -150.0, -37.5, -114.5, -17383936, 4071, 3130932, -209.9, -112.3, 36.8, 26.7, -16.5, 62.8, 0.71, 398},
		{"bandit t=280.7", -17390372, 2064, 3134992, -15.4, -89.0, -121.2, -17391476, 1669, 3134095, -166.2, -13.1, 77.4, 13.5, -4.1, 32.1, 1, 1009},
		{"bandit t=280.9", -17390376, 2046, 3134967, -24.6, -90.5, -123.6, -17391509, 1666, 3134111, -163.9, -14.1, 85.1, 15.1, -3.9, 33.9, 1, 946},
		{"bandit t=358.1", -17392434, 1937, 3136006, -90.9, 18.1, 36.3, -17394459, 1553, 3135617, -222.9, 22.1, 139.6, -1.0, -11.2, 0.9, 0.02, 17},
		{"bandit t=358.3", -17392455, 1940, 3136014, -98.0, 16.1, 34.9, -17394508, 1558, 3135648, -227.3, 20.7, 142.4, 0.1, -8.7, 0.2, 0, 19},
		{"bandit t=361.5", -17392781, 1915, 3136041, -108.2, -27.5, -13.6, -17395214, 1603, 3136090, -220.0, 11.6, 137.6, 5.1, -0.7, -3.3, 0, 338},
		{"bandit t=361.7", -17392805, 1909, 3136038, -113.7, -29.3, -16.0, -17395261, 1605, 3136119, -221.4, 11.6, 138.5, 0.4, -0.1, -0.4, 0, 2},
	}
	flashed, steady := 0, 0
	for _, s := range shots {
		shooter := round.Target{Position: flight.Vec3{X: s.sx, Y: s.sy, Z: s.sz}, Velocity: flight.Vec3{X: s.svx, Y: s.svy, Z: s.svz}}
		target := round.Target{Position: flight.Vec3{X: s.tx, Y: s.ty, Z: s.tz}, Velocity: flight.Vec3{X: s.tvx, Y: s.tvy, Z: s.tvz}}
		z := Heat(shooter, target, flight.Vec3{X: s.ax, Y: s.ay, Z: s.az}, s.lit, 0)
		span := target.Position.Subtract(shooter.Position).Length()
		cue := "tone only"
		switch {
		case span < z.Minimum:
			cue = "BREAK"
		case span <= z.Escape:
			cue = "SHOOT (flash)"
			flashed++
		case span <= z.Max:
			cue = "SHOOT (steady)"
			steady++
		}
		t.Logf("%-15s range %5.0f m | Rmax %5.0f Rne %5.0f Rmin %4.0f | %-14s | recorded closest %4.0f m", s.name, span, z.Max, z.Escape, z.Minimum, cue, s.least)
		switch {
		case s.name == "bandit t=280.7" || s.name == "bandit t=280.9":
			if span <= z.Max {
				t.Errorf("%s: a shot at a cold nose beyond the plume floor read as inside the zone (%.0f m, Rmax %.0f)", s.name, span, z.Max)
			}
		case s.name[:6] == "bandit":
			if span > z.Escape {
				t.Errorf("%s: a rear-aspect shot at a level target that arrived inside 20 m read as escapable (%.0f m, Rne %.0f)", s.name, span, z.Escape)
			}
		default:
			if span > z.Max {
				t.Errorf("%s: a shot the round arrives on at a fixed step read as outside Rmax (%.0f m, Rmax %.0f)", s.name, span, z.Max)
			}
		}
	}
	if steady+flashed < 8 {
		t.Errorf("only %d of the twelve recorded shots read shootable; ten were", steady+flashed)
	}
}

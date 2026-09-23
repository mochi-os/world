// Mochi world: The opponent forecast against the pilot's recorded fights
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package air

import (
	"math"
	"sort"
	"testing"
)

// fights are the pilot's recorded jousts against the ace and when each merged
// (the first 3/9 crossing, measured by the debrief), and whether the pilot
// slowed hard in the 12 s after it. The first four were flown fast, the next
// two slowed at once, and the last flew fast for 18 s and slowed after.
var fights = []struct {
	id    string
	merge float64
	hard  bool
}{
	{"01a0b0904ee17d2793a26044daa99e20", 11.7, false},
	{"01a0bba3fa0f7f3aa15db25aba969df6", 10.9, false},
	{"01a0c91b3caf734ebad4fe28696121d5", 11.3, false},
	{"01a0ca0150ff7abe9741eec06e9e252b", 11.9, false},
	{"01a0ca24ff4f764f99eee4efc65de9fb", 11.6, true},
	{"01a0ca2e8e5c7bf9bbf4852b81a6728b", 11.4, true},
	{"01a0ca40c193704799b5e71b39cb276f", 11.6, false},
}

// along is the pilot's speed change per second along his path at an instant,
// from his velocity half a second before, as the bot's swing reads it.
func along(reel footage, when float64) (float64, bool) {
	_, velocity, _, _, _, _, now := reel.at(1, when)
	_, before, _, _, _, _, then := reel.at(1, when-0.5)
	if !now || !then || velocity.Length() < 1 {
		return 0, false
	}
	return velocity.Subtract(before).Scale(1 / 0.5).Dot(velocity.Normalize()), true
}

// missed replays one fight's pilot through the bot's forecast: every quarter
// second of a window, the track the bot would hold (his velocity; his swing
// from a look half a second earlier, capped at 8 g as the bot caps it; and how
// long his speed had kept falling faster than 2 m/s^2), flown `ahead` seconds
// by evolve(), against where the pilot really went. It returns the median miss
// in metres, and the median of the forecast's speed less his real speed, m/s,
// over the looks where a slowing was believed (NaN when none was).
func missed(t *testing.T, reel footage, from, to, ahead float64, believe bool) (float64, float64) {
	t.Helper()
	var misses, speeds []float64
	for when := from; when <= to; when += 0.25 {
		position, velocity, _, _, _, _, now := reel.at(1, when)
		_, before, _, _, _, _, then := reel.at(1, when-0.5)
		truth, arrival, _, _, _, _, later := reel.at(1, when+ahead)
		if !now || !then || !later {
			continue
		}
		swing := velocity.Subtract(before).Scale(1 / 0.5)
		if swing.Length() > 80 {
			swing = swing.Normalize().Scale(80)
		}
		lasted := 0.0
		for back := 0.0; back <= 20; back += 0.25 {
			if rate, ok := along(reel, when-back); !ok || rate >= -2 {
				break
			}
			lasted = back
		}
		contact := track{position: position, velocity: velocity, swing: swing, lasted: lasted}
		if believe {
			contact.floor = slowest
		}
		guess, speed := evolve(&contact, ahead)
		misses = append(misses, guess.Subtract(truth).Length())
		if contact.floor > 0 && lasted >= lasting && swing.Dot(velocity.Normalize()) < 0 {
			speeds = append(speeds, speed.Length()-arrival.Length())
		}
	}
	if len(misses) == 0 {
		t.Fatalf("no forecast could be checked between %.1f and %.1f s", from, to)
	}
	sort.Float64s(misses)
	sort.Float64s(speeds)
	if len(speeds) == 0 {
		return misses[len(misses)/2], math.NaN()
	}
	return misses[len(misses)/2], speeds[len(speeds)/2]
}

// TestForecastFollowsTheSlowing: stage 11's forecast, which believes a slowing
// that has lasted `lasting`, against the speed-holding arc, on the pilot's own
// tracks in the 12 s after each merge, where the bandit chooses the play the
// rest of the fight follows. It must find the pilots who slowed hard, closer
// by at least a twentieth at 8 and at 12 s (measured when it was built: 10 to
// 63%), and leave the pilots who stayed fast where they were (measured: 0%).
// The speed it forecasts is logged, not judged: it has the slowed-hard pilots
// 60 and 115 kt too slow at 12 s (duel.go slowing).
func TestForecastFollowsTheSlowing(t *testing.T) {
	for _, fight := range fights {
		reel := view(t, fight.id)
		for _, ahead := range []float64{8, 12} {
			held, _ := missed(t, reel, fight.merge, fight.merge+12, ahead, false)
			slowed, speed := missed(t, reel, fight.merge, fight.merge+12, ahead, true)
			change := slowed/held - 1
			t.Logf("%s %2.0f s ahead: %4.0f -> %4.0f m (%+.0f%%), believed speed %+.0f kt", fight.id[:8], ahead, held, slowed, 100*change, 1.944*speed)
			switch {
			case fight.hard && change > -0.05:
				t.Errorf("%s slowed hard after the merge, and the %.0f s forecast only moved %+.0f%% (%.0f -> %.0f m)", fight.id[:8], ahead, 100*change, held, slowed)
			case !fight.hard && change > 0.01:
				t.Errorf("%s stayed fast after the merge, and the %.0f s forecast got %+.0f%% worse (%.0f -> %.0f m)", fight.id[:8], ahead, 100*change, held, slowed)
			}
		}
	}
}

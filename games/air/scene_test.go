// Mochi world: scenes — moments a human pilot created, put back in front of the brain
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Outcome gates on recorded human flying (footage_test.go explains the reader,
// the open-loop limit, and AIR_FOOTAGE). All are from recording 01a0b090
// (joust, ace, 2026-09-17), the fight the human won in 187 s without the ace
// ever producing a gun solution, and each is a decision the brain got wrong
// THEN - so each is expected RED until the stage of
// claude/plans/air-bot-arbiter.md that repairs it lands.

package air

import (
	"fmt"
	"math"
	"testing"
)

const tape = "01a0b0904ee17d2793a26044daa99e20"

// seeds is how many bandits fly each scene. Selection noise and personality
// vary by seed, so a scene passes on a majority, never on one lucky pilot.
const seeds = 8

// majority fails the test unless at least six of the eight seeds pass.
func majority(t *testing.T, what string, passed int, detail []string) {
	t.Helper()
	for _, line := range detail {
		t.Log(line)
	}
	if passed < 6 {
		t.Fatalf("%s: %d of %d seeds", what, passed, seeds)
	}
}

// Threat on the six (t=156-160). The human closed from 770 m and sat 12-21
// degrees off the ace's tail while it flew `rebuild` with its nose 114-158
// degrees away, and killed it with the next burst. The window is FOUR seconds:
// measured, the tape's velocity stops pointing at today's bot after that (it has
// left the recorded track), the brain rightly stops calling him a menace, and
// anything later says nothing about this decision.
//
// Two decisions, two tests, because they belong to different repairs. The
// first is task #1's: no energy recovery in front of a gun.
func TestSceneThreatOnTheSix(t *testing.T) {
	reel := view(t, tape)
	passed := 0
	var detail []string
	for seed := uint64(1); seed <= seeds; seed++ {
		rebuilding := 0
		replay(t, reel, "ace", seed, 156, 4, func(g glimpse) {
			if g.mode == "rebuild" {
				rebuilding++
			}
		})
		ok := rebuilding == 0
		if ok {
			passed++
		}
		detail = append(detail, fmt.Sprintf("seed %d: rebuilding %4.1f s of 4  %v", seed, float64(rebuilding)/60, ok))
	}
	majority(t, "the ace must not recover energy with a gun 770 m behind it and closing", passed, detail)
}

// The second reading of the same four seconds: with the menace registered, it
// must turn with everything the jet affords, and stay out of his sights.
//
// Judged as a SHARE of the affordable turn, never as a g figure. The ace is at
// 186 kt here, where its aero cap allows about 1.6 g, and this test first asked
// for 3: a bar nothing under the cap can reach, which read as "answers a gun
// with an offensive yo-yo at 1.6 g" when 1.6 g was the whole wing. Repaired, it
// says what the four seconds can honestly say: the 4a brain asks for all of the
// turn and is in the taped human's sights for none of it, and the old reflex
// (1.4 g, unloaded) would fail it. It cannot say whether `high` is the right
// play with a gun behind - the tape stops following the bot before that is
// decided, and that question belongs to TestSceneUnderGuns.
func TestSceneThreatIsAnswered(t *testing.T) {
	reel := view(t, tape)
	passed := 0
	var detail []string
	for seed := uint64(1); seed <= seeds; seed++ {
		ticks, share, speed, sighted := 0, 0.0, 0.0, 0
		flown := map[string]int{}
		replay(t, reel, "ace", seed, 156, 4, func(g glimpse) {
			if g.second < 1 {
				return // the picture is still forming
			}
			ticks++
			share += g.asked / math.Max(g.afforded, 1)
			speed += g.speed
			flown[g.mode]++
			if g.faced < 6 && g.distance < 800 {
				sighted++
			}
		})
		mean := share / float64(max(ticks, 1))
		ok := mean >= 0.8
		if ok {
			passed++
		}
		detail = append(detail, fmt.Sprintf("seed %d: asked for %3.0f%% of the turn it could afford at %.0f kt with him on its six, in his sights %.1f s of 3 %v  %v",
			seed, mean*100, speed/float64(max(ticks, 1))*1.944, float64(sighted)/60, flown, ok))
	}
	majority(t, "the ace must turn with all it has against a gun closing from 770 m", passed, detail)
}

// The dump (t=44-58). The human dumped 254 kt down to 138 at 35 degrees of
// alpha, collapsed his radius from 1,667 m to 735 m, and went from head-on to
// dead astern of the ace in fourteen seconds (aspect 7 degrees at t=56-58).
// Asserted on where the HUMAN ends up relative to the ace's tail, which is the
// recorded failure. The weakest of the four as evidence: once the bot departs
// from the taped track the human no longer follows it.
func TestSceneTheDump(t *testing.T) {
	reel := view(t, tape)
	passed := 0
	var detail []string
	for seed := uint64(1); seed <= seeds; seed++ {
		astern := 0
		replay(t, reel, "ace", seed, 44, 14, func(g glimpse) {
			if g.second > 11 && g.trailed < 30 && g.distance < 2000 {
				astern++
			}
		})
		ok := astern < 60
		if ok {
			passed++
		}
		detail = append(detail, fmt.Sprintf("seed %d: human within 30 deg of its tail inside 2,000 m for %4.1f s of the last 3  %v", seed, float64(astern)/60, ok))
	}
	majority(t, "the ace must not end the human's energy dump with him dead astern", passed, detail)
}

// Under guns (t=88-98, from the run of t=84-107). The ace flew `high` for 22.7 s straight while the human
// sat 400-500 m behind it, hitting it five times. Against one constant-arc
// prediction of him the yo-yo always pays inside its twelve-second window;
// against a pilot who adjusts, it never does. Judged only on the ticks where
// the tape still has him on its six inside 600 m - the picture the brain was
// actually handed - and on WHAT IT CHOSE there: an offensive repositioning
// play is the wrong answer to a gun tracking from 450 m. Opened at t=88 because
// a brain started cold at t=84 picks `pitch` instead and the scene proves
// nothing; from t=88 it flies `high` for every tick of the ten seconds, as
// recorded.
func TestSceneUnderGuns(t *testing.T) {
	reel := view(t, tape)
	passed := 0
	var detail []string
	offensive := map[string]bool{"press": true, "lag": true, "low": true, "high": true, "climb": true}
	for seed := uint64(1); seed <= seeds; seed++ {
		tracked, attacking := 0, 0
		chosen := map[string]int{}
		replay(t, reel, "ace", seed, 88, 10, func(g glimpse) {
			if g.trailed < 45 && g.distance < 600 && g.play != "" {
				tracked++
				chosen[g.play]++
				if offensive[g.play] {
					attacking++
				}
			}
		})
		share := float64(attacking) / float64(max(tracked, 1))
		ok := tracked >= 60 && share <= 0.5
		if ok {
			passed++
		}
		detail = append(detail, fmt.Sprintf("seed %d: tracked inside 600 m for %4.1f s, flying an offensive play for %3.0f%% of it %v  %v",
			seed, float64(tracked)/60, share*100, chosen, ok))
	}
	majority(t, "with a gun tracking from its six inside 600 m the ace must mostly fly a defensive play", passed, detail)
}

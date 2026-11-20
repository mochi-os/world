#!/usr/bin/env python3
"""Measured tactics tuning (#143): coordinate descent over the bot brain's
doctrine, one constant at a time, scored by the scenario battery
(games/air/battery_test.go).

Each constant runs at its default and at each candidate (default x --factors);
metric names carry their own direction (up_* rises, down_* falls) and candidates
rank by summed normalized improvement. Kill events are rare, so anything under
~100 --seeds ranks noise.

The driver NEVER edits bot.go. Promotion is a human act, and the ladder tests
(TestBotLadder, TestBotGunnery, TestBotSection) must stay green after the edit.
A constant whose every candidate scores poorly means a MISSING behaviour.

Usage:
  tools/tactics.py                                   # sweep the default constants
  tools/tactics.py --constants drag.pace,spiral.nose # sweep a chosen set
  tools/tactics.py --seeds 200 --out report.json     # the overnight run
  tools/tactics.py --factors 0.9,1.1                 # narrower candidates
"""

import argparse
import json
import re
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# The tunable doctrine: an integer flag (candidates round to whole numbers and
# duplicates collapse) and the reason today's value was picked, shown in the
# report. The values themselves are NOT kept here: the baseline run reports the
# defaults it flew (its BATTERY-DEFAULTS line, read from standard() in
# games/air/bot.go), and the candidates are centred on those. A copy kept here
# drifted - support.share read 0.75 for months after bot.go moved to 0.9.
CONSTANTS = {
    "drag.pace":       (False, "below ~2/3 corner a break neither defeats his solution nor keeps the corner"),
    "drag.span":       (False, "inside 900 m an extension hands him the saddle; the break stays mandatory"),
    "spiral.nose":     (False, "established = his nose committed, not merely pointed this decision"),
    "spiral.span":     (False, "a spiral against a distant attacker donates altitude for nothing"),
    "spiral.floor":    (False, "the descending turn needs altitude to spend before the guard flattens it"),
    "spiral.saddle":   (True,  "one-decision transients belong to the reversal, not a committed spiral"),
    "spiral.hold":     (True,  "a spiral re-decided every cadence is no spiral"),
    "jink.span":       (False, "jinking outside guns range spends the energy the fight is decided by"),
    "jink.base":       (True,  "faster re-rolls smear the track; slower ones are learnable"),
    "jink.spread":     (True,  "the irregularity that keeps the rhythm unlearnable"),
    "high.closure":    (False, "modest overtake is the closure discipline's job, not a vertical excursion"),
    "high.span":       (False, "beyond this the chase needs the knots the yo-yo would spend"),
    "high.tail":       (False, "dead astern is a pure closure problem"),
    "low.near":        (False, "a slight opening is patience, not a cut"),
    "low.far":         (False, "a big opening means he is running, and lagging a runner points behind him"),
    "plan.deficit":    (False, "a REAL energy deficit before denying his rate game"),
    "lead.closure":    (False, "the pull begins ~2 s before the pass"),
    "lead.floor":      (False, "closer than this the merge is already happening"),
    "lead.angle":      (False, "a quarter-circle post-merge angle without giving up the tally"),
    "missile.margin":  (False, "loose noses feed flares at the merge"),
    "missile.tail":    (False, "the disciplined save the shot for rear aspect the flare reaction cannot beat"),
    "missile.span":    (False, "close enough that the motor wins the tail chase"),
    "sandwich.span":   (False, "an attack run, not someone merely pointed this way for a moment"),
    "sandwich.nose":   (False, "a loose 0.8 cone flagged anyone merely flying toward the teammate"),
    "sandwich.weight": (False, "the threatened wingman's problem outranks nearer targets"),
    "support.behind":  (False, "the perch converts the instant the picture changes"),
    "support.above":   (False, "the energy bank the conversion is paid from"),
    "support.share":   (False, "a mate meaningfully closer than me owns the fight"),
    "form.abeam":      (False, "combat spread: mutual lookout without fouling each other's turns"),
    "form.blend":      (False, "station-chasing inside the spread just S-turns forever"),
    "form.burner":     (False, "a rejoin from far behind needs the corner cut in reheat"),
    "press.span":      (False, "the advantage clock runs only inside the fight, not on a transit"),
    "press.hold":      (True,  "five seconds of held angles before patience becomes the finish"),
    "press.loose":     (False, "measured: rounds trace the airframe, a wider gate only sprays"),
    "press.closure":   (False, "the overtake ceiling of the run-in to the finishing gap"),
    "press.gap":       (False, "dispersion is angular: half the range is four times the hit density"),
    "crowd.weight":    (False, "spread the section across a target-rich picture; a perch is parked guns"),
    "sandwich.reach":  (False, "a rescue 20 km out is a lonely transit to a stale fight, not a rescue"),
    "rejoin.span":     (False, "a pair this far apart is two singles; re-form before crossing into a fight"),
    "rejoin.fight":    (False, "a target inside this is my fight now, rejoined or not"),
    "zoom.edge":       (False, "an energy edge worth spending upstairs, not on rate"),
    "rope.edge":       (False, "the climb he cannot follow needs a real bank balance"),
    "rope.hold":       (True,  "three committed seconds - a released rope is just a climb"),
    "bracket.span":    (False, "split the approach while there is still sky to split"),
    "bracket.angle":   (False, "~35 degrees off the direct line: a flank at the merge, not a detour"),
    "wounded.weight":  (False, "finish what is already dying - the smoking bird pulls the eye"),
}

DEFAULT_SWEEP = [
    "drag.pace", "drag.span", "spiral.nose", "spiral.span", "spiral.saddle",
    "jink.span", "high.closure", "plan.deficit", "lead.closure",
    "missile.margin", "sandwich.weight", "support.behind", "form.abeam",
    "press.hold", "press.closure", "press.gap", "crowd.weight",
]

LINE = re.compile(r"BATTERY (\S+)((?: \S+=\S+)+) \((\d+) seeds\)")
DEFAULTS = re.compile(r"BATTERY-DEFAULTS (\{.*\})")


def battery(seeds, overrides):
    """Run the battery once; return ({scenario: {metric: mean}}, {constant: the
    default it ran with})."""
    env = {"AIR_BATTERY": "1", "AIR_SEEDS": str(seeds)}
    if overrides:
        env["AIR_TACTICS"] = json.dumps(overrides)
    run = subprocess.run(
        ["go", "test", "./games/air", "-run", "^TestBattery$", "-v", "-count=1", "-timeout", "120m"],
        cwd=ROOT, env={**dict(__import__("os").environ), **env},
        capture_output=True, text=True)
    if run.returncode != 0:
        sys.exit(f"battery failed:\n{run.stdout[-2000:]}\n{run.stderr[-2000:]}")
    out = {}
    for scenario, pairs, _ in LINE.findall(run.stdout):
        out[scenario] = {}
        for pair in pairs.split():
            name, value = pair.split("=")
            out[scenario][name] = float(value)
    if not out:
        sys.exit(f"no BATTERY lines in output:\n{run.stdout[-2000:]}")
    found = DEFAULTS.search(run.stdout)
    if not found:
        sys.exit(f"no BATTERY-DEFAULTS line in output:\n{run.stdout[-2000:]}")
    return out, json.loads(found.group(1))


def score(baseline, candidate):
    """Summed normalized improvement across every metric; direction from the
    metric's own name. Positive = better than baseline."""
    total = 0.0
    for scenario, metrics in baseline.items():
        for name, base in metrics.items():
            new = candidate.get(scenario, {}).get(name)
            if new is None:
                continue
            scale = max(abs(base), 1.0)
            delta = (new - base) / scale
            total += delta if name.startswith("up_") else -delta
    return total


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--seeds", type=int, default=24, help="seeds per battery run (overnight: 200)")
    parser.add_argument("--constants", help="comma-separated subset (default: a curated sweep)")
    parser.add_argument("--factors", default="0.8,0.9,1.1,1.2", help="candidate multipliers of the default")
    parser.add_argument("--out", help="write the full JSON report here")
    args = parser.parse_args()

    names = args.constants.split(",") if args.constants else DEFAULT_SWEEP
    for name in names:
        if name not in CONSTANTS:
            sys.exit(f"unknown constant {name!r} — known: {', '.join(sorted(CONSTANTS))}")
    factors = [float(f) for f in args.factors.split(",")]

    started = time.time()
    print(f"baseline ({args.seeds} seeds)...", flush=True)
    baseline, defaults = battery(args.seeds, None)
    for name in names:
        if name not in defaults:
            sys.exit(f"the battery reports no constant {name!r}: tactics.py and battery_test.go's knobs disagree")
    for scenario, metrics in sorted(baseline.items()):
        print(f"  {scenario}: " + " ".join(f"{k}={v:.3f}" for k, v in sorted(metrics.items())))

    report = {"seeds": args.seeds, "baseline": baseline, "constants": {}}
    for name in names:
        whole, why = CONSTANTS[name]
        default = defaults[name]
        if whole:
            default = int(default)
        candidates = []
        for f in factors:
            value = round(default * f) if whole else round(default * f, 6)
            if value not in [c for c, _ in candidates] and value != default:
                candidates.append((value, None))
        rows = []
        for value, _ in candidates:
            print(f"{name}={value} ({args.seeds} seeds)...", flush=True)
            result, _ = battery(args.seeds, {name: value})
            rows.append({"value": value, "score": score(baseline, result), "metrics": result})
        rows.sort(key=lambda r: -r["score"])
        report["constants"][name] = {"default": default, "reason": why, "candidates": rows}
        print(f"  {name} (default {default}: {why})")
        for r in rows:
            marker = "+" if r["score"] > 0 else " "
            print(f"   {marker} {r['value']:>8}  score {r['score']:+.3f}")

    print(f"\n{time.time()-started:.0f}s. A positive score is a CANDIDATE, not a promotion:")
    print("promote by editing standard() with a one-line doctrinal justification,")
    print("then keep TestBotLadder, TestBotGunnery, and TestBotSection green.")
    if args.out:
        Path(args.out).write_text(json.dumps(report, indent=1))
        print(f"report: {args.out}")


main()

#!/usr/bin/env python3
"""Measured tactics tuning (#143): coordinate descent over the bot brain's
doctrine, one constant at a time, scored by the scenario battery
(games/air/battery_test.go).

For each constant the driver runs the battery at the default and at each
candidate value (default x the --factors list), compares every metric against
the baseline (metric names carry their own direction: up_* wants to rise,
down_* wants to fall), and ranks candidates by the summed normalized
improvement. Matches parallelize across cores inside the battery; candidates
run sequentially. Overnight scale comes from --seeds: kill events are rare,
so anything under ~100 seeds ranks noise (#138 only resolved because deaths
ran 7:1).

The driver NEVER edits bot.go. Promotion is a human act: a winning number is
proposed to the doctrine (standard() in games/air/bot.go) only with a
one-line justification a pilot would recognise, and the ladder tests
(TestBotLadder, TestBotGunnery, TestBotSection) must stay green after the
edit — the skill ladder is a constraint the tuning respects. A constant whose
every candidate scores poorly is the other, more valuable finding: a MISSING
behaviour, not a wrong number.

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

# The tunable doctrine, mirroring standard() in games/air/bot.go — name,
# default, and the reason today's value was picked (shown in the report so
# the doctrinal-justification guard starts from the incumbent's reason).
# Integer-valued constants (counts, tick holds) are marked so candidates
# round to whole numbers and duplicates collapse.
CONSTANTS = {
    "drag.pace":       (0.68, False, "below ~2/3 corner a break neither defeats his solution nor keeps the corner"),
    "drag.span":       (900,  False, "inside 900 m an extension hands him the saddle; the break stays mandatory"),
    "spiral.nose":     (0.90, False, "established = his nose committed, not merely pointed this decision"),
    "spiral.span":     (1400, False, "a spiral against a distant attacker donates altitude for nothing"),
    "spiral.floor":    (2300, False, "the descending turn needs altitude to spend before the guard flattens it"),
    "spiral.saddle":   (2,    True,  "one-decision transients belong to the reversal, not a committed spiral"),
    "spiral.hold":     (150,  True,  "a spiral re-decided every cadence is no spiral"),
    "jink.span":       (900,  False, "jinking outside guns range spends the energy the fight is decided by"),
    "jink.base":       (40,   True,  "faster re-rolls smear the track; slower ones are learnable"),
    "jink.spread":     (35,   True,  "the irregularity that keeps the rhythm unlearnable"),
    "high.closure":    (90,   False, "modest overtake is the closure discipline's job, not a vertical excursion"),
    "high.span":       (1200, False, "beyond this the chase needs the knots the yo-yo would spend"),
    "high.tail":       (0.85, False, "dead astern is a pure closure problem"),
    "low.near":        (-30,  False, "a slight opening is patience, not a cut"),
    "low.far":         (-140, False, "a big opening means he is running, and lagging a runner points behind him"),
    "plan.deficit":    (400,  False, "a REAL energy deficit before denying his rate game"),
    "lead.closure":    (2.0,  False, "the pull begins ~2 s before the pass"),
    "lead.floor":      (600,  False, "closer than this the merge is already happening"),
    "lead.angle":      (1.3,  False, "a quarter-circle post-merge angle without giving up the tally"),
    "missile.margin":  (0.87, False, "loose noses feed flares at the merge"),
    "missile.tail":    (0.3,  False, "the disciplined save the shot for rear aspect the flare reaction cannot beat"),
    "missile.span":    (2600, False, "close enough that the motor wins the tail chase"),
    "sandwich.span":   (2200, False, "an attack run, not someone merely pointed this way for a moment"),
    "sandwich.nose":   (0.92, False, "a loose 0.8 cone flagged anyone merely flying toward the teammate"),
    "sandwich.weight": (0.3,  False, "the threatened wingman's problem outranks nearer targets"),
    "support.behind":  (1100, False, "the perch converts the instant the picture changes"),
    "support.above":   (500,  False, "the energy bank the conversion is paid from"),
    "support.share":   (0.75, False, "a mate meaningfully closer than me owns the fight"),
    "form.abeam":      (1500, False, "combat spread: mutual lookout without fouling each other's turns"),
    "form.blend":      (1200, False, "station-chasing inside the spread just S-turns forever"),
    "form.burner":     (3000, False, "a rejoin from far behind needs the corner cut in reheat"),
    "press.span":      (1500, False, "the advantage clock runs only inside the fight, not on a transit"),
    "press.hold":      (300,  True,  "five seconds of held angles before patience becomes the finish"),
    "press.loose":     (1.0,  False, "measured: rounds trace the airframe, a wider gate only sprays"),
    "press.closure":   (45,   False, "the overtake ceiling of the run-in to the finishing gap"),
    "press.gap":       (250,  False, "dispersion is angular: half the range is four times the hit density"),
    "crowd.weight":    (1.0,  False, "spread the section across a target-rich picture; a perch is parked guns"),
}

DEFAULT_SWEEP = [
    "drag.pace", "drag.span", "spiral.nose", "spiral.span", "spiral.saddle",
    "jink.span", "high.closure", "plan.deficit", "lead.closure",
    "missile.margin", "sandwich.weight", "support.behind", "form.abeam",
    "press.hold", "press.closure", "press.gap", "crowd.weight",
]

LINE = re.compile(r"BATTERY (\S+)((?: \S+=\S+)+) \((\d+) seeds\)")


def battery(seeds, overrides):
    """Run the battery once; return {scenario: {metric: mean}}."""
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
    return out


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
    baseline = battery(args.seeds, None)
    for scenario, metrics in sorted(baseline.items()):
        print(f"  {scenario}: " + " ".join(f"{k}={v:.3f}" for k, v in sorted(metrics.items())))

    report = {"seeds": args.seeds, "baseline": baseline, "constants": {}}
    for name in names:
        default, whole, why = CONSTANTS[name]
        candidates = []
        for f in factors:
            value = round(default * f) if whole else round(default * f, 6)
            if value not in [c for c, _ in candidates] and value != default:
                candidates.append((value, None))
        rows = []
        for value, _ in candidates:
            print(f"{name}={value} ({args.seeds} seeds)...", flush=True)
            result = battery(args.seeds, {name: value})
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

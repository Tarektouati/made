#!/usr/bin/env python3
"""Turn the files written by scripts/bench.sh into one markdown table.

Usage: bench-report.py <out-dir>

Sources, all lower-is-better:
  base.bench.txt / head.bench.txt  go benchmarks, significant when `benchstat` finds
                                   p < 0.05 and the medians differ by >= 2% (a tiny
                                   but consistent shift is significant yet only noise)
  hf-<name>.json                   hyperfine runs, significant when the 95% confidence
                                   intervals of the mean do not overlap and the means
                                   differ by >= 5% (single-digit gaps between identical
                                   binaries are normal run-order noise)
  made-base / made-head            binary sizes, significant when they differ by >= 1%
  base.bench.failed / head.bench.failed
                                   markers left by bench.sh when `go test -bench`
                                   failed on that side; noted under the table
Nothing here fails the build: regressions are only flagged in the table.
"""
import csv
import io
import json
import math
import statistics
import subprocess
import sys
from pathlib import Path

GO_MIN_DELTA = 2.0  # percent

out = Path(sys.argv[1])
rows = []  # (metric, base, head, delta%, verdict) as display strings


def verdict(delta, significant):
    if not significant:
        return "no"
    return "⚠️ **regression**" if delta > 0 else "✅ improvement"


def add(metric, base, head, base_s, head_s, significant):
    delta = (head - base) / base * 100
    rows.append((metric, base_s, head_s, f"{delta:+.1f}%", verdict(delta, significant)))


def fmt_time(seconds):
    for unit, scale in (("s", 1), ("ms", 1e3), ("µs", 1e6), ("ns", 1e9)):
        if seconds * scale >= 1 or unit == "ns":
            return f"{seconds * scale:.3g} {unit}"


def benchstat_csv(*files):
    res = subprocess.run(["benchstat", "-format", "csv", *map(str, files)],
                         capture_output=True, text=True, check=True)
    return list(csv.reader(io.StringIO(res.stdout)))


def has_benchmarks(path):
    return path.exists() and any(l.startswith("Benchmark") for l in path.read_text().splitlines())


# Go micro-benchmarks.
base_txt, head_txt = out / "base.bench.txt", out / "head.bench.txt"
if has_benchmarks(head_txt):
    if has_benchmarks(base_txt):
        for r in benchstat_csv(base_txt, head_txt):
            # data rows: name, base, ci, head, ci, delta ("~" when not significant), p
            if len(r) < 7 or not r[0] or r[0] == "geomean" or not r[1]:
                continue
            try:
                b = float(r[1])
                if not r[3]:  # ran on base only: failed or removed on head
                    rows.append((f"go bench `{r[0]}`", fmt_time(b), "missing", "n/a", "⚠️ **missing on head**"))
                    continue
                h = float(r[3])
            except ValueError:
                continue  # a header row
            delta = (h - b) / b * 100
            add(f"go bench `{r[0]}`", b, h, fmt_time(b), fmt_time(h),
                r[5] != "~" and abs(delta) >= GO_MIN_DELTA)
    else:
        for r in benchstat_csv(head_txt):
            if len(r) >= 2 and r[0] and r[0] != "geomean":
                try:
                    rows.append((f"go bench `{r[0]}`", "n/a", fmt_time(float(r[1])), "n/a", "no base benchmark"))
                except ValueError:
                    continue

# End-to-end timings from hyperfine (schema 2: results[].measurements[].time_wall_clock).
for name, label in (("list", "made -l (1000 tasks)"), ("noop", "made noop"), ("version", "made -V")):
    path = out / f"hf-{name}.json"
    if not path.exists():
        continue
    stats = {}
    for res in json.loads(path.read_text())["results"]:
        t = [m["time_wall_clock"]["value"] for m in res["measurements"]]
        mean, half = statistics.mean(t), 1.96 * statistics.stdev(t) / math.sqrt(len(t))
        stats[res["command"].split()[0].rsplit("-", 1)[-1]] = (mean, half)
    (bm, bh), (hm, hh) = stats["base"], stats["head"]
    add(f"hyperfine {label}", bm, hm, fmt_time(bm), fmt_time(hm), abs(hm - bm) > bh + hh and abs(hm - bm) / bm >= 0.05)

# Binary size.
base_size, head_size = (out / "made-base").stat().st_size, (out / "made-head").stat().st_size
delta = (head_size - base_size) / base_size * 100
add("binary size (stripped)", base_size, head_size,
    f"{base_size / 1024:.0f} KiB", f"{head_size / 1024:.0f} KiB", abs(delta) >= 1)

print("## made benchmarks: base vs head\n")
print("| metric | base | head | Δ% | significant? |")
print("| --- | --- | --- | --- | --- |")
for r in rows:
    print("| " + " | ".join(r) + " |")
print("\nBoth sides ran on the same runner in the same job; lower is better. "
      "Warn only: this never fails the job. Go benchmarks are significant when benchstat finds p < 0.05 "
      f"and the change is {GO_MIN_DELTA:g}% or more, "
      "hyperfine timings when the 95% confidence intervals of the mean do not overlap and differ by 5% or more, "
      "binary size at a change of 1% or more.")
for side in ("base", "head"):
    if (out / f"{side}.bench.failed").exists():
        print(f"\n⚠️ `go test -bench` failed on {side}; some Go benchmark rows may be missing (see the job log).")

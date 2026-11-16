#!/usr/bin/env bash
# Benchmark the working tree (head) against a base git ref and write a markdown
# report. Used by the CI "bench" job and runnable locally:
#
#   scripts/bench.sh [base-ref] [out-dir]     # defaults: origin/main, bench-out
#
# Needs go, hyperfine, benchstat and python3 on PATH. The report is printed and
# saved as <out-dir>/report.md. Both sides run on this machine (Go benchmarks
# interleaved, in alternating order), so only the base-vs-head difference is
# meaningful, never the absolute numbers.
set -euo pipefail

base_ref=${1:-origin/main}
out=$(mkdir -p "${2:-bench-out}" && cd "${2:-bench-out}" && pwd)
head_dir=$(git rev-parse --show-toplevel)
base_dir=$out/base-src

for tool in go hyperfine benchstat python3; do
  command -v "$tool" >/dev/null || { echo "bench: $tool not found on PATH" >&2; exit 1; }
done

git worktree add --detach "$base_dir" "$base_ref" >/dev/null
trap 'git worktree remove --force "$base_dir"' EXIT

# 1. Go micro-benchmarks, in 10 interleaved rounds of -count 1 whose order alternates
# (base head, head base, ...) so runner drift hits both sides equally. A base without
# benchmarks just yields a file with no Benchmark lines. A failing round is not
# fatal, but leaves a <side>.bench.failed marker for the report.
rm -f "$out"/*.bench.failed "$out"/{base,head}.bench.txt
for i in $(seq 10); do
  order="base head"; [ $((i % 2)) -eq 0 ] && order="head base"
  for side in $order; do
    dir=$head_dir; [ "$side" = base ] && dir=$base_dir
    (cd "$dir" && { go test -run '^$' -bench . -count 1 ./... || touch "$out/$side.bench.failed"; }) \
      | tee -a "$out/$side.bench.txt" | grep -v '^ok\|^PASS\|^?' || true
  done
done

# 2. Binaries, built like the CI "build" job; also the binary size data.
for side in base head; do
  dir=$head_dir; [ "$side" = base ] && dir=$base_dir
  (cd "$dir" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out/made-$side" .)
done

# 3. End-to-end timing against a large generated Madefile (1000 tasks plus "noop").
fixture=$out/fixture
mkdir -p "$fixture"
awk 'BEGIN {
  print "# Tasks\n\n### noop\nDoes nothing.\n```bash\ntrue\n```\n"
  for (i = 0; i < 1000; i++)
    printf "### task-%d\nDescription of task %d.\n```bash\necho task %d\n```\n\n", i, i, i
}' > "$fixture/Madefile.md"

hf() { # hf <name> <made args...>
  local name=$1; shift
  (cd "$fixture" && hyperfine -N --warmup 3 --runs 30 --export-json "$out/hf-$name.json" \
    -n base "$out/made-base $*" -n head "$out/made-head $*" >/dev/null)
}
hf list -l
hf noop noop
hf version -V

python3 "$head_dir/scripts/bench-report.py" "$out" | tee "$out/report.md"

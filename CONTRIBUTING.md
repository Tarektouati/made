# Contributing

## Development

This repository's own tasks live in [`Madefile.md`](./Madefile.md): run `made` in
the repo to list them, and `made check` to build, vet, and test.

`made format` runs `gofmt -w .` and `made lint` runs golangci-lint (v2.15.0, the
version CI uses).

## Pushing through no-mistakes (optional)

[`no-mistakes`](https://github.com/kunchenguid/no-mistakes) is an optional local
gate. Pushing to its `no-mistakes` remote instead of `origin` runs an AI-driven
pipeline (review, test, docs, lint, push, PR, CI) in a disposable worktree, and
opens the PR only once every check passes. `made check` remains the baseline
whether or not you use it. Install it by following the upstream
[installation guide](https://kunchenguid.github.io/no-mistakes/start-here/installation/), then:

```bash
no-mistakes init            # once per clone; adds the `no-mistakes` git remote
git push no-mistakes        # from your feature branch
no-mistakes                 # open the TUI for the active run
```

The gate's settings are in [`.no-mistakes.yaml`](./.no-mistakes.yaml) and use the
`made format`, `made lint` and `made check` tasks. no-mistakes reads `commands`
and `document.instructions` from `main`, so a branch cannot change those for its
own run; `auto_fix` and `commit` are read from the pushed branch. Git worktrees of a clone share its remotes, so `init`
is needed once per clone, not per worktree.

## Benchmarks

Every pull request gets a base-vs-head performance table from the `bench` CI job
(step summary and one sticky PR comment; warn only, it never fails the build). To
produce the same report locally, install [`hyperfine`](https://github.com/sharkdp/hyperfine)
and `benchstat` (`go install golang.org/x/perf/cmd/benchstat@latest`), then run:

```bash
made bench              # or: scripts/bench.sh [base-ref] [out-dir], base defaults to origin/main
go test -run '^$' -bench . -count 10 ./...   # just the Go benchmarks
```

The Go benchmarks run in 10 interleaved base/head rounds whose order alternates, so
drift on the runner affects both sides equally. A Go row is flagged only when
`benchstat` finds p < 0.05 and the change is 2% or more; hyperfine timings need
non-overlapping confidence intervals and a 5% change, binary size a 1% change.

# Contributing

## Development

Tool versions (Go, golangci-lint and `made` itself) are pinned in
[`.prototools`](./.prototools) and installed with [proto](https://moonrepo.dev/proto),
so local development, no-mistakes and CI all use the same ones. Install proto by
following its [installation guide](https://moonrepo.dev/docs/proto/install), then,
once per clone:

```bash
proto trust     # .prototools loads its golangci-lint and made plugins from tools/proto/
proto install   # installs the pinned go, golangci-lint and made
```

proto ignores the plugins in an untrusted `.prototools`. CI trusts it automatically.
To bump a tool, edit its version in `.prototools`.

This repository's own tasks live in [`Madefile.md`](./Madefile.md): run `made` in
the repo to list them, and `made check` to build, vet, and test.

`made format` runs `gofmt -w .` and `made lint` runs golangci-lint. `made dist` builds a
release-style binary into `dist/` for `GOOS`/`GOARCH` (default: this machine), as the CI
build job does: `GOOS=windows GOARCH=amd64 made dist`.

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

no-mistakes runs those tasks in its own worktrees under `~/.no-mistakes/worktrees`.
Trust that directory once (`proto trust ~/.no-mistakes/worktrees`) so the pinned
golangci-lint and made from `.prototools` are used there too.

## Benchmarks

Every pull request gets a base-vs-head performance table from the `bench` CI job
(step summary and one sticky PR comment; warn only, it never fails the build). To
produce the same report locally, install [`hyperfine`](https://github.com/sharkdp/hyperfine)
and `benchstat` (`go install golang.org/x/perf/cmd/benchstat@latest`), then run:

```bash
made bench [base-ref] [out-dir]   # defaults: origin/main, bench-out (same as scripts/bench.sh)
go test -run '^$' -bench . -count 10 ./...   # just the Go benchmarks
```

The Go benchmarks run in 10 interleaved base/head rounds whose order alternates, so
drift on the runner affects both sides equally. A Go row is flagged only when
`benchstat` finds p < 0.05 and the change is 2% or more; hyperfine timings need
non-overlapping confidence intervals and a 5% change, binary size a 1% change.

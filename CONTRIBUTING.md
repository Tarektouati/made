# Contributing

## Development

This repository's own tasks live in [`Madefile.md`](./Madefile.md): run `made` in
the repo to list them, and `made check` to build, vet, and test.

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

<!-- made v0.7.0 -->

# Tasks

The development tasks for `made` itself. Run `made` anywhere in the repository to
list them, or `made <task>` to run one.

### say-hello
Prints a friendly greeting.
```bash
echo hello
```

### build
Compiles all packages.
```bash
go build ./...
```

### test
Runs the test suite.
```bash
go test ./...
```

### check
Builds, vets, and tests in one go.
```bash
go build ./... && go vet ./... && go test ./...
```

### format
Formats all Go sources in place.
```bash
gofmt -w .
```

### lint
Runs golangci-lint with the repo's `.golangci.yml`.
```bash
golangci-lint run ./...
```

### dist
Builds a release-style binary into `dist/` for `GOOS`/`GOARCH` (default: this machine). The CI build job runs this task, so keep its body as is.
```bash
os=$(go env GOOS)
arch=$(go env GOARCH)
ext=""
if [ "$os" = windows ]; then ext=.exe; fi
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "dist/made-$os-$arch$ext" .
```

### bench
Benchmarks the working tree against a base ref (default `origin/main`): Go benchmarks (benchstat), CLI timings (hyperfine) and binary size. Needs `hyperfine` and `benchstat` on PATH. Usage: `made bench [base-ref] [out-dir]`.
```bash
scripts/bench.sh "$@"
```

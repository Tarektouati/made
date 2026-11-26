<!-- made v0.7.0 -->

# Tasks

The development tasks for `made` itself. Run `made` anywhere in the repository to
list them, or `made <task>` to run one.

### build
Compiles all packages.
```bash
go build ./...
```

### test
Runs the test suite on every package; arguments are `go test` flags, e.g. `made test -race -count=1`.
```bash
go test "$@" ./...
```

### test-pkg
Runs `go test` with exactly the flags and packages given. Usage: `made test-pkg [flags] [packages]`. The CI test steps run this task, so keep its body as is.
```bash
go test "$@"
```

### check
Builds, vets, and tests in one go.
```bash
made build && go vet ./... && made test
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

### vulncheck
Scans for known vulnerabilities with the pinned govulncheck, for each CI build target (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64). The CI vulncheck job runs this task, so keep its body as is.
```bash
version=v1.10.0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# Build govulncheck for this machine even if GOOS/GOARCH are exported: the scan
# below sets GOOS/GOARCH per target, which must not cross-compile the scanner itself.
env -u GOOS -u GOARCH GOBIN="$tmp" go install "golang.org/x/vuln/cmd/govulncheck@$version"
status=0
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  echo "== $target"
  GOOS=${target%/*} GOARCH=${target#*/} "$tmp/govulncheck" ./... || status=1
done
exit "$status"
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

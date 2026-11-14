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

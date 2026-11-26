# made

[![Go badge](https://img.shields.io/badge/Go-%E2%89%A51.26-00ADD8?logo=go&logoColor=FFFFFF&labelColor=000000)](https://go.dev) [![Homebrew badge](https://img.shields.io/badge/Homebrew-Tarektouati%2Ftap-FBB040?logo=homebrew&logoColor=FFFFFF&labelColor=000000)](https://github.com/Tarektouati/homebrew-tap) [![Release](https://img.shields.io/github/v/release/Tarektouati/made?logo=github&logoColor=FFFFFF&labelColor=000000)](https://github.com/Tarektouati/made/releases)

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE) [![CI](https://github.com/Tarektouati/made/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Tarektouati/made/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/Tarektouati/made.svg)](https://pkg.go.dev/github.com/Tarektouati/made)

A Markdown-driven task runner — think `make` or `just`, but tasks live in plain
Markdown instead of a custom file format. A task is an H3 heading followed by a
fenced code block, so your `Madefile.md` documents your tasks and runs them too.

## What made is not

`made` stays small on purpose: it finds a code block under a heading and runs it.

- **Not a build system.** There are no task dependencies, file targets, or
  up-to-date checks — every run executes the script. Use `make` if you need those.
- **Not a sandbox.** `made` runs the code blocks of the task file as they are, like
  `make` or `just` run their recipes. Only run tasks from files you trust. A
  Madefile found in a parent directory is refused if another user could have
  written it; see the [trust model](SECURITY.md#trust-model).
- **Not a cross-platform shell layer.** Scripts run as written under the fence's
  interpreter; nothing is translated for Windows. See
  [Platform compatibility](docs/installation.md#platform-compatibility).

## Installation

Release binaries cover macOS and Linux (`amd64`/`arm64`); Windows works via
`go install`. Each fence language you use needs its interpreter on `PATH`.

```sh
brew install Tarektouati/tap/made                                                   # Homebrew
curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | sh  # curl
go install github.com/Tarektouati/made@latest                                       # Go ≥ 1.26
```

More: version pinning, install directory, checksums, and shell completion in
[docs/installation.md](docs/installation.md).

**Privacy:** `made` checks for a new release at most once a day with a single GET to
GitHub's `releases/latest` URL, in the background, and prints a one-line notice on
stderr when there is one. No telemetry. Set `MADE_NO_UPDATE_CHECK=1` to turn it off;
see [New-version notice](docs/installation.md#new-version-notice).

## Quick start

Create a starter `Madefile.md` at the root of your repository:

```sh
made init   # writes Madefile.md with one example task, "hello"
```

A task is an H3 heading, an optional description, and a fenced code block:

````markdown
### say-hello
Prints a friendly greeting.
```bash
echo hello
```
````

Then run it:

```sh
made say-hello       # runs the script under the "say-hello" heading
made                 # lists all tasks with descriptions
made hello          # runs the example task from made init
made -f tasks.md ci  # read tasks from a different file
made -f README.md ci # …such as your README
made deploy staging  # pass "staging" to the task as $1
made --version       # print the version
```

## CLI reference

```
made [-f file] [--no-chdir] [-l] [-V] [task [--] [args...]]
made [-f file] --check [--strict] [--host]
made init [--force]
made completion <bash|zsh|fish>
made self-update [vX.Y.Z] [--dry-run]
```

| Flag | Default | Description |
|---|---|---|
| `-f`, `--file` | nearest `Madefile.md` | Markdown file to read tasks from: any readable file but a directory, including a pipe (`-f <(…)`, `-f /dev/stdin`), whose tasks run in the current directory. Without `-f`, `made` uses the `Madefile.md` in the current directory or the closest parent directory, up to the repository root (or `$HOME` or the filesystem root outside a repository; a parent directory's Madefile must be yours and not writable by others; see [How it works](docs/how-it-works.md)) |
| `--no-chdir` | | Run the task in the current directory instead of the task file's directory |
| `--check` | | Check the task file for problems instead of running a task; see [Checking a Madefile](docs/checking.md) |
| `--strict` | | With `--check`, fail on warnings too |
| `--host` | | With `--check`, also check that each task's interpreter is on PATH; see [Checking against this machine](docs/checking.md#checking-against-this-machine) |
| `-l`, `--list` | | List available tasks and exit |
| `-V`, `--version` | | Print the version and exit |
| `-h`, `--help` | | Print usage to stdout and exit |
| `task` | | Name of the task to run (the H3 heading text) |
| `args` | | Passed to the task's script as positional parameters |

Long flags also work with a single dash (`-file`, `-list`).

More: `made init`, `made completion`, `made self-update`, exit codes, and passing
arguments in [docs/cli.md](docs/cli.md).

## Supported languages

The code fence's language selects the interpreter. Matching is case-insensitive.

| Fence language | Interpreter |
|---|---|
| *(none)*, `bash`, `shell` | `bash -e -o pipefail -c` |
| `sh` | `sh -e -c` |
| `zsh` | `zsh -e -o pipefail -c` |
| `fish` | `fish -c` |
| `python` | `python3 -c`, or `python -c` if `python3` is not on `PATH` |
| `python3` | `python3 -c` |
| `node`, `js`, `javascript` | `node -e` |
| `ruby` | `ruby -e` |
| `perl` | `perl -e` |

`bash`, `sh`, and `zsh` tasks run in strict mode and stop at the first failing
command; `fish` tasks don't. More: python fallback, fence info strings, strict mode,
and fish failures in [docs/languages.md](docs/languages.md).

## Example output

Listing the tasks in this repository:

```
$ made
Tasks in Madefile.md:
  build      Compiles all packages.
  test       Runs the test suite on every package; arguments are go test flags, e.g. made test -race -count=1.
  test-pkg   Runs go test with exactly the flags and packages given. Usage: made test-pkg [flags] [packages]. The CI test steps run this task, so keep its body as is.
  check      Builds, vets, and tests in one go.
  format     Formats all Go sources in place.
  lint       Runs golangci-lint with the repo's .golangci.yml.
  vulncheck  Scans for known vulnerabilities with the pinned govulncheck, for each CI build target (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64). The CI vulncheck job runs this task, so keep its body as is.
  dist       Builds a release-style binary into dist/ for GOOS/GOARCH (default: this machine). The CI build job runs this task, so keep its body as is.
  bench      Benchmarks the working tree against a base ref (default origin/main): Go benchmarks (benchstat), CLI timings (hyperfine) and binary size. Needs hyperfine and benchstat on PATH. Usage: made bench [base-ref] [out-dir].
```

Running an unknown task:

```
$ made deploy
made: unknown task "deploy"

Tasks in Madefile.md:
  build      Compiles all packages.
  test       Runs the test suite on every package; arguments are go test flags, e.g. made test -race -count=1.
  test-pkg   Runs go test with exactly the flags and packages given. Usage: made test-pkg [flags] [packages]. The CI test steps run this task, so keep its body as is.
  check      Builds, vets, and tests in one go.
  format     Formats all Go sources in place.
  lint       Runs golangci-lint with the repo's .golangci.yml.
  vulncheck  Scans for known vulnerabilities with the pinned govulncheck, for each CI build target (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64). The CI vulncheck job runs this task, so keep its body as is.
  dist       Builds a release-style binary into dist/ for GOOS/GOARCH (default: this machine). The CI build job runs this task, so keep its body as is.
  bench      Benchmarks the working tree against a base ref (default origin/main): Go benchmarks (benchstat), CLI timings (hyperfine) and binary size. Needs hyperfine and benchstat on PATH. Usage: made bench [base-ref] [out-dir].
```

## Documentation

- [Installation](docs/installation.md) — requirements, platforms, install options, shell completion
- [CLI reference](docs/cli.md) — every flag and subcommand, exit codes, passing arguments
- [Supported languages](docs/languages.md) — interpreters per fence and strict mode for shell tasks
- [How it works](docs/how-it-works.md) — parsing, execution, working directory, task file discovery, signals
- [Checking a Madefile](docs/checking.md) — `made --check` and its rules
- [Common patterns](docs/patterns.md) — chaining tasks, tolerating failures, monorepos, and more
- [Pre-commit and prek](docs/pre-commit.md) — run a task or `made --check` as a git hook
- [Migrating from 0.2](docs/migrating.md) — moving tasks out of `README.md`
- [Contributing](CONTRIBUTING.md) — development tasks and benchmarks
- [Changelog](CHANGELOG.md) — release highlights and links to the full notes
- [Security policy](SECURITY.md) — the trust model, supported versions and how to report a vulnerability

## License

MIT

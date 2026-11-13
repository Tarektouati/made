# made

[![Go badge](https://img.shields.io/badge/Go-%E2%89%A51.26-00ADD8?logo=go&logoColor=FFFFFF&labelColor=000000)](https://go.dev) [![Homebrew badge](https://img.shields.io/badge/Homebrew-Tarektouati%2Ftap-FBB040?logo=homebrew&logoColor=FFFFFF&labelColor=000000)](https://github.com/Tarektouati/homebrew-tap) [![Release](https://img.shields.io/github/v/release/Tarektouati/made?logo=github&logoColor=FFFFFF&labelColor=000000)](https://github.com/Tarektouati/made/releases)

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE) [![CI](https://github.com/Tarektouati/made/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Tarektouati/made/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/Tarektouati/made.svg)](https://pkg.go.dev/github.com/Tarektouati/made)

A Markdown-driven task runner — think `make` or `just`, but tasks live in plain
Markdown instead of a custom file format. A task is an H3 heading followed by a
fenced code block, so your `README.md` documents your tasks and runs them too.

## Requirements

- Go ≥ 1.26 (only when installing with `go install`)
- The interpreter for each fence language you use available on `PATH` (`bash`,
  `python3`, `node`, …)

## Platform compatibility

**macOS and Linux** are fully supported. Release binaries (and the Homebrew cask)
are built for `darwin` and `linux` on `amd64` and `arm64`.

**Windows** builds and runs via `go install`, but it has no POSIX process groups:
cancelling a task (Ctrl-C) kills the interpreter only, so processes spawned by the
script may outlive it.

## Installation

**Homebrew**

```sh
brew install Tarektouati/tap/made
```

The binary is unsigned, so the cask strips the macOS Gatekeeper quarantine
attribute on install; otherwise the first run would be blocked.

**curl**

```sh
curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | sh
```

Installs the latest release for macOS or Linux (`amd64`/`arm64`) into
`/usr/local/bin` after verifying its checksum. Set `MADE_VERSION` to pin a release
and `MADE_INSTALL_DIR` to change the target directory:

```sh
curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | MADE_VERSION=v0.1.0 MADE_INSTALL_DIR="$HOME/bin" sh
```

**go install**

```sh
go install github.com/Tarektouati/made@latest
```

## Quick start

Add a task to your `README.md` — an H3 heading, an optional description, and a
fenced code block:

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
made -f tasks.md ci  # read tasks from a different file
made --version       # print the version
```

## CLI reference

```
made [-f file] [-l] [-V] [task]
```

| Flag | Default | Description |
|---|---|---|
| `-f`, `--file` | `README.md` | Markdown file to read tasks from |
| `-l`, `--list` | | List available tasks and exit |
| `-V`, `--version` | | Print the version and exit |
| `task` | | Name of the task to run (the H3 heading text) |

Running `made` with no task lists the tasks, same as `--list`. When a task runs,
`made` exits with the script's exit code, so it composes with `&&`, CI steps, and
git hooks (see [Pre-commit and prek](#pre-commit-and-prek)). An unknown task prints an error and the task list, and exits with `1`.

## Supported languages

The code fence's language selects the interpreter. Matching is case-insensitive.

| Fence language | Interpreter |
|---|---|
| *(none)*, `bash`, `shell` | `bash -c` |
| `sh` | `sh -c` |
| `zsh` | `zsh -c` |
| `fish` | `fish -c` |
| `python`, `python3` | `python3 -c` |
| `node`, `js`, `javascript` | `node -e` |
| `ruby` | `ruby -e` |
| `perl` | `perl -e` |

A fence with no language defaults to `bash`. An unsupported language (e.g. `yaml`)
is an error rather than being run under bash.

## How it works

**Parsing.** `made` parses the Markdown file with
[goldmark](https://github.com/yuin/goldmark) and walks the top-level blocks in
document order. Each H3 heading starts a task named after its text. Paragraphs
between the heading and the first fenced code block become the task's
description, and that code block becomes its script. Any heading of another level
ends the current task's scope, and an H3 with no code block after it is skipped.
Only top-level blocks count: code inside lists or blockquotes is never a task.

**Execution.** The script is passed inline to its interpreter (`bash -c <script>`,
`node -e <script>`, …). It inherits `made`'s stdin, stdout, stderr, environment,
and working directory — nothing is written to a temporary file.

**Signals.** On `SIGINT` or `SIGTERM`, `made` tears down the whole task, not just
the interpreter: the task runs in its own process group, which is sent `SIGTERM`
and then `SIGKILL` after a 3-second grace period, so grandchildren spawned by the
script are not orphaned. When stdin is an interactive terminal, no separate group
is created — a background group reading the terminal would be stopped by
`SIGTTIN`, and the terminal already delivers Ctrl-C to the whole foreground group.

## Example output

Listing the tasks in this repository:

```
$ made
Tasks in README.md:
  say-hello  Prints a friendly greeting.
  build      Compiles all packages.
  test       Runs the test suite.
  check      Builds, vets, and tests in one go.
```

Running an unknown task:

```
$ made deploy
made: unknown task "deploy"

Tasks in README.md:
  say-hello  Prints a friendly greeting.
  build      Compiles all packages.
  test       Runs the test suite.
  check      Builds, vets, and tests in one go.
```

## Common patterns

**Chain tasks**

Each task is independent; chain them from the shell (or from another task):

```sh
made build && made test
```

**Fail fast in bash**

A `bash` task runs as one `bash -c` script, so a failing command in the middle
doesn't stop it unless you ask:

```bash
set -euo pipefail
go vet ./...
go test ./...
```

**Keep tasks out of the README**

Put tasks in a dedicated file and point `made` at it:

```sh
made -f tasks.md release
```

**Show code samples without creating tasks**

Any H3 followed by a code block is a task. To document code under a heading
without making it runnable, use an H2 or H4 heading, or wrap the example in a
four-backtick fence as the Quick start section above does.

**Tasks in other languages**

````markdown
### stats
Counts Go files in the repo.
```python
import pathlib
print(len(list(pathlib.Path(".").rglob("*.go"))))
```
````

## Pre-commit and prek

This repository ships a hook definition (`.pre-commit-hooks.yaml`) for
[pre-commit](https://pre-commit.com) and [prek](https://github.com/j178/prek), so
a project can run one of its tasks on commit without contributors installing
`made` first. The hook uses `language: golang`: the tool builds `made` from this
repository with `go install` (prek downloads a Go toolchain if none is found;
pre-commit needs Go available).

**pre-commit**

Add the hook to `.pre-commit-config.yaml`, pinning `rev` to a release tag:

```yaml
repos:
  - repo: https://github.com/Tarektouati/made
    rev: v0.1.0
    hooks:
      - id: made
        args: [-f, Madefile.md, check]
```

**prek**

In `prek.toml`:

```toml
[[repos]]
repo = "https://github.com/Tarektouati/made"
rev = "v0.1.0"
hooks = [
  { id = "made", args = ["-f", "Madefile.md", "check"] },
]
```

prek also reads the `.pre-commit-config.yaml` shown above.

**Notes**

The hook sets `pass_filenames: false`, so staged files are never appended to the
command; the task runs as written. `args` are passed to `made`: use `-f` to pick
the task file and end with the task name. The hook fails when the task exits with
a non-zero code. It runs when any file is staged; set `always_run: true` or
`files` in your config to change that.

## Tasks

The tasks below are real — running `made` in this repo executes them.

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

## License

MIT

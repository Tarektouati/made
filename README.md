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
- **Not a cross-platform shell layer.** Scripts run as written under the fence's
  interpreter; nothing is translated for Windows. See
  [Platform compatibility](#platform-compatibility).

## Requirements

- Go ≥ 1.26 (only when installing with `go install`)
- The interpreter for each fence language you use available on `PATH` (`bash`,
  `python3` (or `python` for `python` fences), `node`, …)

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

### Shell completion

`made <TAB>` completes task names (with their descriptions in zsh and fish) from the
task file `made` would use, including one given with `-f`. `made -f <TAB>` completes
file names, and `made completion <TAB>` completes shell names. Homebrew installs the
completions for bash, zsh, and fish automatically. Otherwise, load the script that
`made completion <shell>` prints:

| Shell | Setup |
|---|---|
| bash | add `eval "$(made completion bash)"` to `~/.bashrc` |
| zsh | add `eval "$(made completion zsh)"` to `~/.zshrc`, after `compinit`; or save it as `_made` in a directory on `$fpath` |
| fish | run `made completion fish > ~/.config/fish/completions/made.fish` once |

Release archives also include the scripts, in `completions/`.

To get task names, the scripts run `made [-f file] --list-names`, which prints one
`name<TAB>description` line per task with no header. The flag is meant for the
scripts and is not shown in `made --help`.

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
made init [--force]
made completion <bash|zsh|fish>
```

| Flag | Default | Description |
|---|---|---|
| `-f`, `--file` | nearest `Madefile.md` | Markdown file to read tasks from. Without `-f`, `made` uses the `Madefile.md` in the current directory or the closest parent directory, up to the repository root (or the filesystem root outside a repository; see [How it works](#how-it-works)) |
| `--no-chdir` | | Run the task in the current directory instead of the task file's directory |
| `-l`, `--list` | | List available tasks and exit |
| `-V`, `--version` | | Print the version and exit |
| `-h`, `--help` | | Print usage to stdout and exit |
| `task` | | Name of the task to run (the H3 heading text) |
| `args` | | Passed to the task's script as positional parameters |

Long flags also work with a single dash (`-file`, `-list`).

**`made init`** writes a starter `Madefile.md` (a short header and one `hello`
task) in the current directory and prints `made: created Madefile.md`. It needs no
existing task file. If `Madefile.md`, `madefile.md`, or `MADEFILE.md` already exists
in the current directory, it exits with `1` and leaves the file alone; parent
directories are not checked, so you can create a nested Madefile on purpose. It
takes no top-level flags (such as `-f`, `-l` or `--no-chdir`) and no arguments;
these exit with `2`. `init` is reserved: a task named `init` cannot be
run with `made init`, so give it another name.

**`made init --force`** overwrites the existing file with the starter template
instead, keeping its name (so a `madefile.md` stays `madefile.md`), and prints
`made: overwrote <name>`. If several Madefile names exist, only the one `made`
reads (`Madefile.md`, then `madefile.md`, then `MADEFILE.md`) is overwritten; the
others are named on stderr and left unchanged. Directories with these names are
not Madefiles and are never overwritten. A Madefile name that is a **symlink** is
not overwritten, even with `--force`: `made` exits with `1` and leaves the link and
its target alone. The flag goes after `init`. With no existing Madefile, it behaves
like plain `made init`.

**`made completion <shell>`** prints the completion script for `bash`, `zsh`, or
`fish` (see [Shell completion](#shell-completion)). Like `init`, it needs no task
file, takes no top-level flags, and is reserved: a task named `completion` cannot be run with
`made completion`.

Running `made` with no task lists the tasks, same as `--list`. When a task runs,
`made` exits with the script's exit code, so it composes with `&&`, CI steps, and
git hooks (see [Pre-commit and prek](#pre-commit-and-prek)). A task killed by signal
n makes `made` exit with `128+n`, as shells report it: `137` for `kill -9`, `141` for
`SIGPIPE` (e.g. `made task | head -1`). An unknown task prints an error and the task list, and exits with `1`. If the task's interpreter can't be started, `made` exits with `127` (not on `PATH`) or `126` (found but not executable); see [Supported languages](#supported-languages).

**Passing arguments.** Everything after the task name is passed to the script: `$1`, `"$@"` in shell tasks,
`$argv` in fish, `sys.argv[1:]` in Python, `process.argv.slice(1)` in Node, `ARGV` in
Ruby, and `@ARGV` in Perl. Flags for `made` itself go before the task name; anything
after it, including `-x`-style flags, belongs to the task. A single `--` right after
the task name is optional and dropped (`made test -- -run Foo`).

````markdown
### deploy
Deploys to an environment (default: staging).
```bash
env="${1:-staging}"
echo "deploying to $env"
```
````

```sh
made deploy            # deploying to staging
made deploy production # deploying to production
```

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

A fence with no language defaults to `bash`. An unsupported language (e.g. `yaml`)
is an error rather than being run under bash.

A `python` fence runs under `python` only when `python3` is not on `PATH`, and
`made` doesn't say so. On older systems (e.g. CentOS 7, or macOS before 12.3)
`python` can be Python 2, so a Python 3 script fails there with a Python 2 syntax
error. Use a `python3` fence to require Python 3: it never falls back, and a missing
`python3` is reported as not found.

Only the first word of the fence's info string selects the language. It ends at the
first space, `{`, or `,`, so `bash title="x"`, `bash {1,3}`, `bash{.x}`, and
`bash,title` all run as `bash`. A fence whose info string starts with `{` (e.g.
Pandoc's `{.python}`) is reported as an unsupported language rather than run under
bash.

If a fence's interpreter can't be started, `made` names the interpreter, the task,
and its language, and exits like a shell would: with `127` when the interpreter is
not on `PATH`, or `126` when it is found but can't be executed (it isn't
executable, or its `#!` line points to a missing program).

**Shell tasks fail fast.** `bash`, `sh`, and `zsh` tasks run in strict mode: the task
stops at the first failing command and `made` exits with that command's code, so a
later successful line can't hide an earlier failure. The shell's usual `errexit`
exceptions still apply. For example, a failure in an `if`, `while`, or `until`
condition, in a command negated with `!`, in any command of an `&&`/`||` chain
except the last, or (in `bash`) inside a `$(...)` command substitution doesn't
stop the task. Put such commands on their
own lines, or end the chain with `|| exit 1`. `bash` and `zsh` also enable
`pipefail`, so a failure anywhere in a pipeline counts; `sh` doesn't, because
`pipefail` isn't portable across `sh` implementations. Unset variables are not an
error (no `-u`). `fish` (which has no such option) and the non-shell languages use
their own defaults; they already exit non-zero on an uncaught error. To get the
lenient behavior back, see "Let a shell task continue past failures" under
[Common patterns](#common-patterns).

## How it works

**Parsing.** `made` parses the Markdown file with
[goldmark](https://github.com/yuin/goldmark) and walks the top-level blocks in
document order. Each H3 heading starts a task named after its text, with Markdown
escapes and HTML entities decoded (`&amp;` becomes `&`, `\*` becomes `*`) and
whitespace collapsed to single spaces; that is the name you pass to `made`. Paragraphs
between the heading and the first fenced code block become the task's
description, and that code block becomes its script. Any heading of another level
ends the current task's scope, and an H3 with no code block after it is skipped.
Only top-level blocks count: code inside lists or blockquotes is never a task.

**Execution.** The script is passed inline to its interpreter (`bash -e -o pipefail
-c <script>`, `node -e <script>`, …). The interpreter inherits `made`'s stdin,
stdout, stderr, and environment — nothing is written to a temporary file. The
strict-mode options are passed to the shell rather than prepended to the script,
so line numbers in error messages match the code block. A script whose first
character is `-` or `+` gets a single leading space prepended (except for `python`, where
that would be an indentation error), so the interpreter doesn't read it as an
option; line numbers are unaffected.

Because the script is a single command-line argument, the OS caps its size: 128 KiB
per argument on Linux, about 1 MiB of total arguments on macOS, and about 32K
characters on Windows. A larger task fails before running, with an error giving the
script's size; keep the bulk in a separate file and have the task run it (e.g.
`bash scripts/build.sh "$@"`). For the same reason, other users on the machine can
read the script text with `ps` or `/proc/<pid>/cmdline` while the task runs, so
don't embed secrets in a Madefile; read them from environment variables or files at
run time.

**Working directory.** A task runs in the directory that contains its task file,
not in the directory `made` was started from. Relative paths in a script
therefore resolve the same way wherever you invoke `made`:
`made -f tools/tasks.md build` runs `build` inside `tools/`. Pass `--no-chdir` to
run the task in the current directory instead.

**Finding the task file.** Without `-f`, `made` looks for a file named
`Madefile.md`, `madefile.md`, or `MADEFILE.md` — matched exactly, so macOS and
Linux agree — in the current directory and then in each parent directory, and
uses the first directory that has one. If a directory holds more than one of these
names, `made` uses the first in that order and says so on stderr (`made: using
Madefile.md (also found madefile.md)`).

The search stops after the first directory containing a `.git` entry (a
directory, or a file in worktrees and submodules), so it never leaves the
repository; outside a repository it goes up to the filesystem root. You can
therefore run a repository's tasks from any subdirectory; when the file comes from
a parent directory, `made` prints `made: using <path>` on stderr before running the
task. The nearest Madefile wins and is never merged with one higher up: in a
monorepo, each package can have its own `Madefile.md`, and the root tasks stay
reachable with `made -f ../../Madefile.md`. If no Madefile is found, `made` exits
with an error that suggests `-f` or `made init`; `README.md` is never read unless
you pass `-f README.md`.

An explicit `-f` path is relative to the current directory, read exactly as given,
and never looked up in parent directories. An empty value (for example
`-f "$UNSET_VAR"`) is an error (exit `2`); `made` never falls back to discovery
when `-f` is given.

**Signals.** On `SIGINT`, `SIGTERM`, `SIGHUP` (for example, a closed SSH session or
terminal) or `SIGQUIT`, `made` tears down the whole task, not just the interpreter: the task runs in its own process group, which is sent `SIGTERM`,
and whatever is still running 3 seconds later (even if the interpreter itself
exited at once) is sent `SIGKILL`, so grandchildren spawned by the script are not
orphaned. When stdin is an interactive terminal, no separate group is created — a
background group reading the terminal would be stopped by `SIGTTIN` — so Ctrl-Z
and reading stdin behave as if you had run the script yourself. Ctrl-C and Ctrl-\
do too: the terminal already sends `SIGINT` or `SIGQUIT` to every process of the
task, so `made` only signals the interpreter (`SIGTERM`, then `SIGKILL` after the
grace period) and leaves the rest to shut down on their own, like
`docker compose up` stopping its containers. A `SIGTERM` or `SIGHUP`, which does
not come from the terminal (`kill`, a supervisor, a closed session), still tears
down the whole task: `made` freezes the interpreter and all its descendants with
`SIGSTOP`, sends them `SIGTERM`, and sends `SIGKILL` to whatever outlives the
same grace period. This descendant teardown works on Linux and macOS; on other
Unix systems only the interpreter is signaled. A process that fully detaches
(`setsid`, a double fork whose parent has already exited, or a new process group)
is not reached. If `SIGHUP` is already ignored when `made` starts (as under
`nohup`), it stays ignored and the task keeps running.
After such a teardown `made` exits with `128+n` for signal `n`: `130` for
`SIGINT` (Ctrl-C), `143` for `SIGTERM`, `129` for `SIGHUP` and `131` for
`SIGQUIT`, whatever status the task itself ended with.

## Example output

Listing the tasks in this repository:

```
$ made
Tasks in Madefile.md:
  say-hello  Prints a friendly greeting.
  build      Compiles all packages.
  test       Runs the test suite.
  check      Builds, vets, and tests in one go.
```

Running an unknown task:

```
$ made deploy
made: unknown task "deploy"

Tasks in Madefile.md:
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

**Let a shell task continue past failures**

Shell tasks [stop at the first failing command](#supported-languages). To let one
command fail without stopping the task, append `|| true`:

````markdown
### clean
```bash
rm -r build || true
mkdir build
```
````

To turn strict mode off for a whole task, put `set +e` (and, in `bash`/`zsh`,
`set +o pipefail`) at the top of the script. The task then exits with the last
command's status, so an earlier failure is hidden again. To run every command but
still fail if any of them failed, record the failures instead:

````markdown
### lint-all
```bash
rc=0
golangci-lint run || rc=$?
markdownlint README.md || rc=$?
exit $rc
```
````

To also treat unset variables as errors, opt in with `set -u`.

**Keep tasks in the README**

`made` only discovers `Madefile.md`. To run tasks documented in another file, such
as your README, point `made` at it:

```sh
made -f README.md release
```

The task runs in the directory that holds that file (see
[How it works](#how-it-works)).

**One Madefile per package**

In a monorepo, give each package its own `Madefile.md`. Running `made` inside a
package uses that package's tasks; the root `Madefile.md` stays available with
`-f`:

```sh
cd services/api
made test                      # services/api/Madefile.md
made -f ../../Madefile.md lint # the root tasks
```

**Show code samples without creating tasks**

Any H3 followed by a code block is a task. To document code under a heading
without making it runnable, use an H2 or H4 heading, or wrap the example in a
four-backtick fence as the Quick start section above does.

**Leave a blank line after raw HTML**

Under CommonMark, an HTML block such as `<div>…</div>` runs until the next blank
line. A code block directly after it, with no blank line in between, becomes part
of the HTML, so the H3 above it has no code block and is skipped without a warning.
Keep the blank line:

````markdown
### banner
<div align="center">Build</div>

```bash
echo build
```
````

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

## Migrating from 0.2

Before v0.3.0, `made` read tasks from the nearest `README.md`. It now reads only
`Madefile.md` (see [How it works](#how-it-works)). To migrate, either:

- run `made init` next to your `README.md` to create a `Madefile.md`, then move
  the task sections (the H3 headings and their code blocks) out of `README.md`
  into it, replacing the example `hello` task, or
- keep them where they are and pass the file explicitly: `made -f README.md <task>`
  (for example in scripts, CI, or a pre-commit hook's `args`).

## Development

This repository's own tasks live in [`Madefile.md`](./Madefile.md): run `made` in
the repo to list them, and `made check` to build, vet, and test.

### Benchmarks

Every pull request gets a base-vs-head performance table from the `bench` CI job
(step summary and one sticky PR comment; warn only, it never fails the build). To
produce the same report locally, install [`hyperfine`](https://github.com/sharkdp/hyperfine)
and `benchstat` (`go install golang.org/x/perf/cmd/benchstat@latest`), then run:

```bash
made bench              # or: scripts/bench.sh [base-ref] [out-dir], base defaults to origin/main
go test -run '^$' -bench . -count 10 ./...   # just the Go benchmarks
```

## License

MIT

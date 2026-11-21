# Installation

Requirements, supported platforms, every install method in detail, and shell
completion. [← Back to README](../README.md)

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

## Installing

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
`/usr/local/bin` after verifying its checksum, using `sudo` when that directory is not
writable. If `sudo` is missing or fails (for example with no terminal to prompt on),
it installs into `~/.local/bin` instead. Set `MADE_VERSION` to pin a release
(`v0.6.0`, `0.6.0`, or `latest`) and `MADE_INSTALL_DIR` to change the target
directory; give it as an absolute path (`$HOME/bin`, not a quoted `~/bin`):

```sh
curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | MADE_VERSION=v0.6.0 MADE_INSTALL_DIR="$HOME/bin" sh
```

The checksum is downloaded from the same GitHub release as the archive, so it catches
a corrupted download but not a tampered release. If you need more than that, build
from source with `go install` or verify the release yourself.

**go install**

```sh
go install github.com/Tarektouati/made@latest
```

## Shell completion

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

For the `-f` value, bash and zsh remove its quoting and expand a leading `~`, `$NAME`
or `${NAME}` (not inside single quotes); fish 4 expands it the way fish would for a
run, and older fish removes its quoting and expands only a leading `~`. Completion
never runs a command substitution such as `$(...)` from the command line. A variable
with the same name as a local of the shell's completion system may not expand: in
zsh, for example, `$name`, `$line`, `$i` or `$REPLY`.

All three shells insert a task name quoted, so a name with spaces, quotes or `$(...)`
is one literal word on the command line and never runs anything. bash escapes it
(`with\ space`), or quotes it to match a quote you opened (`'with space'`). The part
you typed may be quoted or escaped too: `with\ s`, `'with s`, `"with s` and `ns\:t`
all complete. The bash script also works in a shell running with `set -e` or `set -u`,
from bash 3.2 on.

To get task names, the scripts run `made [-f file] --list-names`, which prints one
`name<TAB>description` line per task with no header, leaving out tasks with an empty
name. The flag is meant for the scripts and is not shown in `made --help`. It only
reads a regular file, so completing after `-f` on a pipe or `/dev/stdin` fails at
once instead of waiting.

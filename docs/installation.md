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

## Updating

```sh
made self-update            # install the latest release
made self-update v0.6.0     # install a specific release, older ones included
made self-update --dry-run  # show what would happen, change nothing
```

`made self-update` updates a binary installed with `install.sh` or copied from a
release archive. It works like `install.sh`:

1. It reads the latest release tag from where the `releases/latest` page on
   github.com redirects to, without following the redirect (no API token, no rate
   limit), unless you name a version.
2. If `made` is already that release, it prints `made: v0.7.0 is the latest version`
   and exits with `0`. It never installs an older release than the one running
   unless you name it.
3. It downloads `made_<version>_<os>_<arch>.tar.gz` and `checksums.txt` from that
   release and checks the archive's SHA-256 before using it.
4. It writes the new binary next to the old one, runs it with `--version` (it must
   report the expected version), and only then renames it over the old one. The
   rename is atomic, so `made` is always either the old or the new binary, and on
   any failure the old one is left in place.

If `made` is a symlink, the file it points to is replaced and the link is kept.

The checksum comes from the same GitHub release as the archive, as with
`install.sh`, so it catches a corrupted download but not a tampered release. If you
need more than that, build from source with `go install` or verify the release
yourself.

Installs that another tool owns are not replaced, because that would break the
tool's bookkeeping. `made self-update` prints the command to use and exits with `3`:

| Install | Detected by | Command |
|---|---|---|
| Homebrew | the binary is under a Homebrew `Cellar` or `Caskroom` directory | `brew upgrade Tarektouati/tap/made` |
| `go install` | the binary is in `GOBIN` or `GOPATH/bin` (from the environment; `GOPATH` defaults to `~/go`), or was built from the module without the release version stamp | `go install github.com/Tarektouati/made@latest` |
| Development build | the version is `dev`, a pre-release or a pseudo-version such as `v0.6.1-0.20260101000000-abcdef123456` | `go install github.com/Tarektouati/made@latest` |
| Windows (and other platforms without a release archive) | the platform | `go install github.com/Tarektouati/made@latest` |

If the binary's directory is not writable (for example `/usr/local/bin`), it fails
with exit code `4` before downloading anything, and suggests `sudo made self-update`
or re-running `install.sh`; it never installs somewhere else. Other failures
(download, checksum, extraction, the new binary not running) exit with `1`. See the [exit codes](cli.md#subcommands).

The only network requests are plain HTTPS GETs to github.com (and the release
download host it redirects to): the `releases/latest` redirect, `checksums.txt` and
the archive, as `install.sh` makes. Nothing else is sent. A redirect from HTTPS to
plain HTTP is refused. The `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` environment
variables are honoured.

Shell completion offers `self-update` and its `--dry-run` flag.

### New-version notice

`made` tells you when a newer release is out, at most once a day, with one line on
stderr after listing or running tasks:

```
made: v0.7.0 is available (you have v0.6.0); run made self-update
```

The command is the one for your install: `made self-update`,
`brew upgrade Tarektouati/tap/made`, or `go install github.com/Tarektouati/made@latest`.
The notice never changes stdout or the exit code and never delays a task: `made`
shows it from a small cache and refreshes the cache in the background, at most once a
day (once an hour while checks are cut short by `made` exiting), without waiting for
the answer. It stays quiet in CI, when output is piped or
redirected, offline, for development builds, and for the commands scripts and tab
completion use; see [New-version notice](cli.md#new-version-notice).

To turn it off, set `MADE_NO_UPDATE_CHECK=1` (or `NO_UPDATE_NOTIFIER=1`) in your
shell profile.

**Privacy.** The check is one plain HTTPS GET to
`https://github.com/Tarektouati/made/releases/latest`, the same request `install.sh`
makes, with a one-second timeout. No telemetry, no identifiers, and nothing about
your version, system or tasks is sent beyond what any HTTP request carries (your IP
address and Go's default `User-Agent`). The answer is kept in
`made/update-check.json` in your user cache directory.

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

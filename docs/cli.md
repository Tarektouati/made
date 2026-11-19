# CLI reference

The full command-line reference: flags, subcommands, exit codes, and how arguments
reach a task. [← Back to README](../README.md)

## Synopsis

```
made [-f file] [--no-chdir] [-l] [-V] [task [--] [args...]]
made [-f file] --check [--strict] [--host]
made init [--force]
made completion <bash|zsh|fish>
```

| Flag | Default | Description |
|---|---|---|
| `-f`, `--file` | nearest `Madefile.md` | Markdown file to read tasks from. Without `-f`, `made` uses the `Madefile.md` in the current directory or the closest parent directory, up to the repository root (or the filesystem root outside a repository; see [How it works](how-it-works.md)) |
| `--no-chdir` | | Run the task in the current directory instead of the task file's directory |
| `--check` | | Check the task file for problems instead of running a task; see [Checking a Madefile](checking.md) |
| `--strict` | | With `--check`, fail on warnings too |
| `--host` | | With `--check`, also check that each task's interpreter is on PATH; see [Checking against this machine](checking.md#checking-against-this-machine) |
| `-l`, `--list` | | List available tasks and exit |
| `-V`, `--version` | | Print the version and exit |
| `-h`, `--help` | | Print usage to stdout and exit |
| `task` | | Name of the task to run (the H3 heading text) |
| `args` | | Passed to the task's script as positional parameters |

Long flags also work with a single dash (`-file`, `-list`).

## Subcommands

**`made init`** writes a starter `Madefile.md` (a short header and one `hello`
task) in the current directory and prints `made: created Madefile.md`. It needs no
existing task file. If `Madefile.md`, `madefile.md`, or `MADEFILE.md` already exists
in the current directory, it exits with `1` and leaves the file alone; parent
directories are not checked, so you can create a nested Madefile on purpose. It
takes no top-level flags (such as `-f`, `-l` or `--no-chdir`) and no arguments;
these exit with `2`. `init` is reserved: a task named `init` cannot be
run with `made init`, so give it another name.

The generated file starts with `<!-- made vX.Y.Z -->`, which records the version of
`made` that created it. The comment is invisible when the Markdown is rendered, and
it is safe to edit or delete. Local `dev` builds leave it out.

**`made init --force`** overwrites the existing file with the starter template
instead, keeping its name (so a `madefile.md` stays `madefile.md`), and prints
`made: overwrote <name>`. The file is rewritten in place, so if it is a hard link,
every other name for it shows the template too. If several Madefile names exist, only the one `made`
reads (`Madefile.md`, then `madefile.md`, then `MADEFILE.md`) is overwritten; the
others are named on stderr and left unchanged. Directories with these names are
not Madefiles and are never overwritten. A Madefile name that is a **symlink** is
not overwritten, even with `--force`: `made` exits with `1` and leaves the link and
its target alone. The flag goes after `init`. With no existing Madefile, it behaves
like plain `made init`. If `Madefile.md` exists but is not a regular file (a
directory, a broken symlink, or a symlink to a directory), `made init` and
`made init --force` exit with `1` and say what it is; remove it first.

**`made completion <shell>`** prints the completion script for `bash`, `zsh`, or
`fish` (see [Shell completion](installation.md#shell-completion)). Like `init`, it needs no task
file, takes no top-level flags, and is reserved: a task named `completion` cannot be run with
`made completion`.

## Listing and exit codes

Running `made` with no task lists the tasks, same as `--list`. When a task runs,
`made` exits with the script's exit code, so it composes with `&&`, CI steps, and
git hooks (see [Pre-commit and prek](pre-commit.md)). A task killed by signal
n makes `made` exit with `128+n`, as shells report it: `137` for `kill -9`, `141` for
`SIGPIPE` (e.g. `made task | head -1`). An unknown task prints an error and the task list, and exits with `1`. A task whose code block is empty or blank exits with `1` without running anything. If the task's interpreter can't be started, `made` exits with `127` (not on `PATH`) or `126` (found but not executable); see [Supported languages](languages.md).
`-l`/`--list` always lists and ignores any task name after it, even an unknown one:
`made -l build` lists the tasks without running `build`.

A closed stdout or stderr (`made build >&-`) is not reported: the Go runtime reopens
it as `/dev/null` before `made` starts, so the output is discarded and `made` exits
with the task's code. Use `>/dev/null` to discard output on purpose.

The task list aligns descriptions by terminal display width, so CJK and emoji task
names line up. An emoji sequence (a ZWJ sequence such as 👨‍👩‍👧, a skin tone, a
flag) counts as one two-cell emoji, the way terminals that cluster graphemes draw it
(iTerm2, Terminal.app, Ghostty, WezTerm, kitty). Terminals that draw each code point
separately, and ambiguous-width characters, may still be off.

## Passing arguments

Everything after the task name is passed to the script: `$1`, `"$@"` in shell tasks,
`$argv` in fish, `sys.argv[1:]` in Python, `process.argv.slice(1)` in Node, `ARGV` in
Ruby, and `@ARGV` in Perl.

The zero-th element is not the script name: in `sh` and `bash` tasks `$0` is `made`,
Python's `sys.argv[0]` is `-c`, and Ruby's and Perl's `$0` is `-e`. Node's `process.argv`
has no script entry, so `process.argv.slice(1)` is exactly your arguments.

Flags for `made` itself go before the task name; anything
after it, including `-x`-style flags, belongs to the task. A single `--` right after
the task name is optional and dropped (`made test -- -run Foo`). A task whose name
starts with `-` must come after `--` (`made -- -x`); otherwise `made` reads it as one of
its own flags.

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

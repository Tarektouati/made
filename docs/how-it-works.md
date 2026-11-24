# How it works

How `made` parses a Madefile, runs a task, finds the task file, and handles
signals. [← Back to README](../README.md)

**Parsing.** `made` parses the Markdown file with
[goldmark](https://github.com/yuin/goldmark) and walks the top-level blocks in
document order. Each H3 heading starts a task named after its text, with Markdown
escapes and HTML entities decoded (`&amp;` becomes `&`, `\*` becomes `*`) and
whitespace collapsed to single spaces. Inline markup keeps its text
(`` ### Build `all` now `` is the task `Build all now`), and code spans are taken
literally, with no decoding; that is the name you pass to `made`. Paragraphs
between the heading and the first fenced code block become the task's
description, and that code block becomes its script. Any heading of another level
ends the current task's scope, and an H3 with no code block after it is skipped.
When two tasks have the same name, the first one runs; a run or a listing warns on
stderr with the line of each, without changing the exit code. A task whose name is
empty (`###`, or a heading that is only `` ` ` ``) is skipped with a warning.
Only top-level blocks count: code inside lists or blockquotes is never a task.
Files with LF, CRLF or CR-only (classic Mac OS) line endings all work; in a file
with any LF, a lone CR stays part of its line, so a script can contain one.

To keep a hostile or broken Madefile from stalling `made` (or tab completion in a
cloned repository), `made` refuses a file larger than 10 MiB, nesting of lists or
blockquotes deeper than 256 levels, and a file that takes longer than 1.5 s to parse.

**Execution.** The script is passed inline to its interpreter (`bash -e -o pipefail
-c <script>`, `node -e <script>`, …). The interpreter inherits `made`'s stdin,
stdout, stderr, and environment — nothing is written to a temporary file. The
strict-mode options are passed to the shell rather than prepended to the script,
so line numbers in error messages match the code block. A script whose first
character is `-` or `+` gets a single leading space prepended (except for `python`, where
that would be an indentation error), so the interpreter doesn't read it as an
option; line numbers are unaffected. A script containing a NUL byte can't be passed as an
argument, so `made` refuses it with an error giving its line within the script.
A task whose code block is empty or holds only whitespace is refused with an error
too, before anything runs; a later code block under the same heading is still
ignored.

Because the script is a single command-line argument, the OS caps its size: 32 pages
per argument on Linux (128 KiB with the usual 4 KiB pages), about 1 MiB of arguments
plus environment in total on macOS, and about 32K characters for the whole command
line on Windows. The arguments passed to the task count against the same limits, and
so does the environment on Linux and macOS. A task over them fails before running and
`made` exits with `126`. The error blames the script when it alone reaches the cap
on one argument (on Windows, the command line), or on macOS when it is larger than
the arguments and environment together; keep the bulk in a separate file and have
the task run it (e.g. `bash scripts/build.sh "$@"`). Otherwise it says arguments plus
environment (on Windows, the command line) are too large, giving the sizes of the
script, arguments, and environment, never their contents, and the limits of the OS
it runs on. Because the script is an argument, other users on the machine can read
the script text with `ps` or `/proc/<pid>/cmdline` while the task runs, so don't
embed secrets in a Madefile; read them from environment variables or files at run
time.

**Working directory.** A task runs in the directory that contains its task file,
not in the directory `made` was started from. Relative paths in a script
therefore resolve the same way wherever you invoke `made`:
`made -f tools/tasks.md build` runs `build` inside `tools/`. That is the directory
where the file was found (or the directory of the `-f` path as written): if the
Madefile is a symlink, the task runs next to the link, not in the directory of
its target. Pass `--no-chdir` to run the task in the current directory instead.
A task file read from a pipe or a device (`-f /dev/stdin`) has no directory of its
own, so its tasks always run in the current directory.

**Finding the task file.** Without `-f`, `made` looks for a file named
`Madefile.md`, `madefile.md`, or `MADEFILE.md` — matched exactly, so macOS and
Linux agree — in the current directory and then in each parent directory, and
uses the first directory that has one. If a directory holds more than one of these
names, `made` uses the first in that order and says so on stderr (`made: using
Madefile.md (also found madefile.md)`). A name that is present but cannot be used
(a broken symlink, a symlink loop, something other than a regular file, or a name
that cannot be looked up because a directory it is in, or links through, can be
listed but not searched) is skipped with a warning such as `made: skipping
/path/Madefile.md: broken symlink to missing.md`, and the search goes on. A
directory that exists but cannot be listed is skipped the same way. A Madefile
that is found but cannot be read, such as a mode 000 file or a symlink to one, is
not skipped: `made` stops with `made: cannot read Madefile.md: permission denied`
(exit `1`) instead of falling back to a parent's Madefile and running a task you
did not mean to run.

The search starts from the physical current directory, with symlinks resolved
like `pwd -P` and `git` do. After `cd`-ing into a symlinked directory, `made`
therefore walks the parents of the link's target, not of the link, and both
`made: using <path>` and the not-found error show the resolved path.

The search stops after the first directory containing a `.git` entry (a
directory, or a file in worktrees and submodules), so it never leaves the
repository; outside a repository it goes up to the filesystem root, and on Linux
and macOS it also stops after your home directory (`$HOME`, with symlinks
resolved; `$HOME` itself is still searched) when you are below it. You can
therefore run a repository's tasks from any subdirectory; when the file comes from
a parent directory, `made` prints `made: using <path>` on stderr before running the
task. The nearest Madefile wins and is never merged with one higher up: in a
monorepo, each package can have its own `Madefile.md`, and the root tasks stay
reachable with `made -f ../../Madefile.md`. If no Madefile is found, `made` exits
with an error that suggests `-f` or `made init`; `README.md` is never read unless
you pass `-f README.md`.

**A Madefile from a parent directory must be yours.** On Linux and macOS, a
Madefile found in a parent directory is only used if nobody else could have put it
there or changed it, the same rule `git` applies to repositories since
CVE-2022-24765. Otherwise another user who can write to a directory above yours (a
world-writable `/tmp/x`, a shared mount) could have their tasks run by you, or by
root. `made` checks:

- the Madefile, which must be owned by you or root and must not be group- or
  world-writable;
- the directory it was found in, which must be owned by you or root and must not be
  group- or world-writable, unless it has the sticky bit (like `/tmp`): there,
  others cannot rename or remove your file, and a file they put there fails the
  first check;
- when the Madefile is a symlink, every link on the way to the file (each owned by
  you or root, in a directory that passes the rule above), and the final file and
  its directory, by the same two rules. A chain of more than 32 links is refused.

"You" is the effective user. When `made` runs as root through `sudo`, files owned by
the user who ran `sudo` (`SUDO_UID`) count as yours too, so `sudo made install` from
a subdirectory of your project still works. `made` then reads the very file it
checked: if the Madefile was replaced in between (say a directory above it was
renamed and another tree put in its place), it stops with `made: cannot read
<path>: it was replaced after its owner and mode were checked`. A Madefile that fails
the check is not skipped: every command that reads it (running a task, `--list`,
`--check`, `--list-names`) stops with exit `1` and says why, for example

```
made: refusing /srv/shared/Madefile.md found in a parent directory: it is group-writable (mode 0664)
made: hint: if you trust it, make it yours and not writable by others (chmod go-w), or run it deliberately with made -f /srv/shared/Madefile.md
```

Tab completion shows no tasks in that case. A Madefile in the current directory
and a file given with `-f` are never checked, so `-f` is the way to run a parent
Madefile you trust as it is. If your umask is `002` (group-writable files by
default), run `chmod g-w` on the Madefile and its directory, or use `-f`. Windows
has no such check.

The check has known limits:

- Only the owner and the mode bits are read; ACLs are ignored. On macOS, an ACL
  entry (`chmod +a "user:alice allow write" Madefile.md`) can let another user
  change a file whose mode looks safe, and `made` does not see it.
- The directories passed on the way up from the current directory, and the ones
  above each checked directory, are not checked. Someone who can rename one of them
  cannot swap the file while `made` reads it, but can, before you run `made`, put
  another tree in its place whose Madefile is yours and passes the check, such as
  one of your other projects.

An explicit `-f` path is relative to the current directory, read exactly as given,
and never looked up in parent directories. An empty value (for example
`-f "$UNSET_VAR"`) is an error (exit `2`); `made` never falls back to discovery
when `-f` is given. Unlike discovery, which only uses regular files, `-f` reads
anything readable but a directory: a pipe (`made -f <(gen-tasks) -l`, `cat
tasks.md | made -f /dev/stdin -l`) or a device. Opening a named pipe with no writer
waits for one, like `cat`, and the 10 MiB limit still applies. A task read from a
pipe or a device runs in the current directory, as with `--no-chdir`. `--list-names`
(used by tab completion) still only reads a regular file, so completion fails at
once instead of waiting on a pipe or reading the terminal.

**Signals.** On `SIGINT`, `SIGTERM`, `SIGHUP` (for example, a closed SSH session or
terminal) or `SIGQUIT`, `made` tears down the whole task, not just the interpreter: the task runs in its own process group, which is sent `SIGTERM`,
and whatever is still running 3 seconds later (even if the interpreter itself
exited at once) is sent `SIGKILL`, so grandchildren spawned by the script are not
orphaned. When stdin is an interactive terminal, no separate group is created — a
background group reading the terminal would be stopped by `SIGTTIN` — so Ctrl-Z
and reading stdin behave as if you had run the script yourself. Ctrl-C and Ctrl-\
do too: the terminal already sends `SIGINT` or `SIGQUIT` to every process of the
task, so `made` sends nothing for the 3-second grace period and lets the task handle
it, for example finish an `INT` trap; if the task exits by itself, `made` exits with
its status. Whatever is still running after the grace period is torn down like on
`SIGTERM` below. `made` cannot tell Ctrl-C from a `SIGINT` or `SIGQUIT` sent to it
alone while it is in the terminal's foreground (`kill -INT`), so that one also only
tears the task down after the grace period; sent while `made` runs in the background,
it tears the task down at once. A `SIGTERM` or `SIGHUP`, which does not come from the
terminal (`kill`, a supervisor, a closed session), tears down the whole task: `made`
freezes the interpreter and all its descendants with `SIGSTOP`, sends them `SIGTERM`,
and sends `SIGKILL` to whatever outlives the grace period. Once the interpreter has
exited, `made` keeps following the processes it recorded and whatever they start, and
also finds processes of the task orphaned when their parent exited: on Linux `made`
becomes a child subreaper, so they are reparented to it; on macOS it looks in its
process group for processes reparented to `launchd` that started after it. This
descendant teardown works on Linux and macOS; on other Unix systems only the
interpreter is signaled. It is best-effort: a process that leaves `made`'s process
group (`setsid`, `setpgid`, a new session) is not reached, nor, on macOS, an orphan
reparented to something other than `launchd`; and a recorded process that has exited
may have its pid reused by an unrelated process in `made`'s process group. If `SIGHUP` is already ignored when `made` starts (as under
`nohup`), it stays ignored and the task keeps running. Likewise, when `made` starts
with `SIGINT` ignored (a background job of a non-interactive shell, such as
`made a & made b & wait`), `SIGINT` and `SIGQUIT` stay ignored for `made` and the task.
After a teardown `made` exits with `128+n` for signal `n`: `130` for
`SIGINT` (Ctrl-C), `143` for `SIGTERM`, `129` for `SIGHUP` and `131` for
`SIGQUIT`, whatever status the task itself ended with.
A second Ctrl-C (or any second handled signal) during the grace period sends `SIGKILL` right away.

`SIGKILL` sent to `made` itself can't be handled. On Linux the interpreter is still
killed when `made` dies (via `PR_SET_PDEATHSIG`), but processes the script spawned
may survive; macOS has no equivalent, so there the task keeps running.

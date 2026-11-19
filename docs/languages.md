# Supported languages

Which interpreter runs each code fence, and how shell tasks handle failures.
[← Back to README](../README.md)

## Interpreters

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

Interpreters are looked up in the absolute `PATH` entries only. `.`, an empty entry
(a leading, trailing, or doubled `:`), and a relative directory such as `bin` are
skipped, so a `bash` or `node` in the task's directory never runs in place of the one
further along `PATH`. On Windows the current directory is not searched either, unlike
in `cmd.exe`. If no absolute entry has the interpreter, `made` reports it as not on
`PATH` and exits with `127`.

## Shell tasks fail fast

`bash`, `sh`, and `zsh` tasks run in strict mode: the task
stops at the first failing command and `made` exits with that command's code, so a
later successful line can't hide an earlier failure. The shell's usual `errexit`
exceptions still apply. For example, a failure in an `if`, `while`, or `until`
condition, in a command negated with `!`, in any command of an `&&`/`||` chain
except the last, or (in `bash`) inside a `$(...)` command substitution doesn't
stop the task. Put such commands on their
own lines, or end the chain with `|| exit 1`. `bash` and `zsh` also enable
`pipefail`, so a failure anywhere in a pipeline counts; `sh` doesn't, because
`pipefail` isn't portable across `sh` implementations. Unset variables are not an
error (no `-u`). To get the lenient behavior back, see "Let a shell task continue
past failures" under [Common patterns](patterns.md).

The non-shell languages use their own defaults; they exit non-zero on an uncaught
error. `fish` has no strict mode, so it continues past failures (see
[below](#fish-tasks-dont-fail-fast)).

## `fish` tasks don't fail fast

`fish` has no `errexit` option, so a `fish` task runs every line even after a command
fails, and `made` exits with the status of the task's last command. A task with
`false` followed by `echo after` prints `after` and exits `0`, and a failing pipeline
such as `false | cat` passes too. To stop at a failure, guard the command with `or
exit`, on the same line after `;` or on the next line. It exits with the failing
command's status:

```fish
make build; or exit
make test
or exit
```

`or return` does the same at the top level of a task (inside a function, it returns
from the function). `or exit` only sees a pipeline's last command; to check an earlier
one, test `$pipestatus`, e.g. `false | cat; test $pipestatus[1] = 0; or exit 1`.

A `fish` syntax error (e.g. an `if` without its `end`) stops the task before any line
runs, and `fish` exits with `127`: the same code `made` uses when an interpreter is
not on `PATH`. The message tells them apart: `fish` prints its error with a `fish:`
prefix and points at the line, while a missing interpreter gets `made`'s "was not
found on PATH" message.

# Checking a Madefile

`made --check` lints a task file without running anything.
[← Back to README](../README.md)

`made --check` reads the task file the same way a run does (`-f` and discovery
apply) and reports mistakes the parser would otherwise accept silently, without
running anything. Each finding is one line on stdout, followed by a summary:

```
Madefile.md:12: error: duplicate-task: "build" already defined at line 4
Madefile.md:20: warning: extra-code-block: ignored: task "test" runs only its first code block (line 16)
2 problems (1 error, 1 warning)
```

A clean file prints nothing. `made --check` exits with `1` if there is any error,
or with `--strict` any warning, and `0` otherwise. A file that can't be read is
reported on stderr like any other run (exit `1`). `--check` takes no task name and
can't be combined with `-l` or `-V`, and `--strict` and `--host` need `--check`;
these exit with `2`.

## Rules

| Rule | Severity | Reported when |
|---|---|---|
| `duplicate-task` | error | A task has the same name as an earlier one, which is the one `made` runs |
| `unsupported-language` | error | The code block's language isn't one of the [supported languages](languages.md) |
| `empty-name` | error | The heading's name is empty, e.g. `### <br>` (HTML tags are not part of the name) |
| `empty-script` | error | The task's code block is empty or blank |
| `nul-byte` | error | The script contains a NUL byte, which can't be run |
| `script-too-large` | error | The script is too large to pass to its interpreter as a command-line argument: 128 KiB on Linux and macOS, 32,767 UTF-16 characters on Windows (see below) |
| `nesting-too-deep` | error | Lists or blockquotes nest deeper than 256 levels |
| `file-too-large` | error | The file is larger than 10 MiB |
| `interpreter-not-found` | error | Only with `--host`: the task's interpreter (for example `ruby` for a `ruby` block) is not on PATH, or is there but not executable |
| `parse-timeout` | error | Parsing the file took longer than 1.5 s (see [How it works](how-it-works.md)) |
| `name-altered` | warning | The heading has HTML tags or an image, which the task name leaves out (an image becomes its alt text), so the name differs from what it shows. Code spans, emphasis and links keep their text and are not flagged |
| `unreachable-name` | warning | The name is `init` or `completion` (reserved), or starts with `-` (run it as `made -- -x`) |
| `no-code-block` | warning | An H3 heading has no code block before the next heading, so it isn't a task |
| `extra-code-block` | warning | A task has more than one code block; only the first runs |
| `docs-fence` | warning | The task's code block is `markdown`, `md`, `text`, `txt`, `plaintext`, `console` or `output`, so it is probably documentation |

`nesting-too-deep` is found by a quick scan before parsing, which counts each
container marker plus half of the indentation (each nested list level needs at
least two columns). It errs on the strict side and checks every line, including
lines inside code blocks and HTML: a line starting with more than 256 `>`, `-` or
`1.` markers, or a non-blank line indented 514 columns or more, is reported even
when it isn't nested. Blank lines never count, however wide.

`script-too-large` uses the limit of the OS `made --check` runs on. Linux caps a
single argument at 128 KiB (counted in bytes), and macOS is checked against the
same limit so that a Madefile that passes there also runs on Linux, although macOS
itself accepts a larger script. Windows caps the whole command line at 32,767
UTF-16 characters, so a script with many non-ASCII characters fits in more bytes,
and a script just under the limit can still be too long once quoted. The size
reported includes the space `made` adds before a script starting with `-` or `+`.

## Checking against this machine

Without `--host`, the result depends only on the file and the OS, so it is the same
in CI, on a developer machine, and in a pre-commit hook. `--host` adds
`interpreter-not-found`, which looks each task's interpreter up on PATH the way a
run does (a `python` block passes if either `python3` or `python` is found):

```
$ made --check --host
Madefile.md:14: error: interpreter-not-found: "ruby" was not found on PATH (required by task "rb", language "ruby"); install it or change the code fence language
1 problem (1 error, 0 warnings)
```

The result holds only for the PATH `made --check --host` runs with. Run it where
the tasks run, for example as a CI step in the job's image. `--host` and
`--strict` can be combined. An interpreter that is found can still fail to start,
for example through a broken `#!` line; `--host` does not run anything.

To run the check on commit, see [Pre-commit and prek](pre-commit.md).

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
can't be combined with `-l` or `-V`, and `--strict` needs `--check`; these exit
with `2`.

## Rules

| Rule | Severity | Reported when |
|---|---|---|
| `duplicate-task` | error | A task has the same name as an earlier one, which is the one `made` runs |
| `unsupported-language` | error | The code block's language isn't one of the [supported languages](languages.md) |
| `empty-name` | error | The heading's name is empty, e.g. `` ### `build` `` (inline code is not part of the name) |
| `empty-script` | error | The task's code block is empty or blank |
| `nul-byte` | error | The script contains a NUL byte, which can't be run |
| `nesting-too-deep` | error | Lists or blockquotes nest deeper than 256 levels |
| `file-too-large` | error | The file is larger than 10 MiB |
| `name-altered` | warning | Inline markup (code, emphasis, links, HTML) is dropped from the heading, so the task name differs from what it shows |
| `unreachable-name` | warning | The name is `init` or `completion` (reserved), or starts with `-` (run it as `made -- -x`) |
| `no-code-block` | warning | An H3 heading has no code block before the next heading, so it isn't a task |
| `extra-code-block` | warning | A task has more than one code block; only the first runs |
| `docs-fence` | warning | The task's code block is `markdown`, `md`, `text`, `txt`, `plaintext`, `console` or `output`, so it is probably documentation |

To run the check on commit, see [Pre-commit and prek](pre-commit.md).

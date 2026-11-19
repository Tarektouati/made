# Pre-commit and prek

Run a Madefile task, or `made --check`, as a git hook.
[← Back to README](../README.md)

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
    rev: v0.6.0
    hooks:
      - id: made
        args: [-f, Madefile.md, check]
```

**prek**

In `prek.toml`:

```toml
[[repos]]
repo = "https://github.com/Tarektouati/made"
rev = "v0.6.0"
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

**Checking the Madefile**

The `made-check` hook runs [`made --check`](checking.md) on the
repository's root Madefile whenever it is staged (`Madefile.md`, `madefile.md` or
`MADEFILE.md`). Add `args: [--strict]` to fail on warnings too:

```yaml
      - id: made-check
        args: [--strict]
```

Add `--host` to also fail when a task's interpreter is not on PATH (see
[Checking against this machine](checking.md#checking-against-this-machine)). The
hook then fails for any developer who lacks one of the interpreters, so use it only
when everyone has them; otherwise run `made --check --host` in CI instead:

```yaml
      - id: made-check
        args: [--host]
```

The hook checks one file and runs from the repository root, so a nested Madefile
or a different file name needs its own entry, with `-f` naming the file and
`files` matching it:

```yaml
      - id: made-check
        args: [-f, services/api/Madefile.md]
        files: ^services/api/Madefile\.md$
```

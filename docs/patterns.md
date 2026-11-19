# Common patterns

Recipes for everyday Madefiles. [← Back to README](../README.md)

**Chain tasks**

Each task is independent; chain them from the shell (or from another task):

```sh
made build && made test
```

**Let a shell task continue past failures**

Shell tasks [stop at the first failing command](languages.md#shell-tasks-fail-fast). To let one
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

`fish` tasks already continue past failures, so they need neither `|| true` nor
`set +e`. To make a `fish` task stop at a failure, add `; or exit` after the command
(see [`fish` tasks don't fail fast](languages.md#fish-tasks-dont-fail-fast)).

**Keep tasks in the README**

`made` only discovers `Madefile.md`. To run tasks documented in another file, such
as your README, point `made` at it:

```sh
made -f README.md release
```

The task runs in the directory that holds that file (see
[How it works](how-it-works.md)).

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
without making it runnable, use an H2 or H4 heading. A four-backtick fence only helps
under a heading that is not an H3 (as in the [Quick start](../README.md#quick-start) section of the README): directly under
an H3 it is still the first fenced block, so it becomes the task script (a
` ````markdown ` fence fails with `unsupported language "markdown"`).

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

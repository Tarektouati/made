# made

A Markdown-driven task runner — think `make` or `just`, but tasks live in plain
Markdown instead of a custom file format.

Define a task as an H3 heading followed by a fenced code block. The heading is the
task name, prose underneath is its description, and the code block is the script:

````markdown
### say-hello
Prints a friendly greeting.
```bash
echo hello
```
````

Then run it:

```bash
made say-hello       # runs the script under the "say-hello" heading
made                 # lists all tasks with descriptions
made -f tasks.md ci  # read tasks from a different file
```

By default `made` reads tasks from `README.md` in the current directory. Use
`-f`/`--file` to point at another Markdown file.

The code fence's language selects the interpreter: `bash`, `sh`, `shell`, `zsh`,
`fish`, `python`/`python3`, `node`/`js`/`javascript`, `ruby`, and `perl`. A fence
with no language defaults to `bash`; an unsupported language (e.g. `yaml`) is an
error rather than being run under bash.

## Install

```bash
go install github.com/Tarektouati/made@latest
```

## Tasks

The tasks below are real — running `made` in this repo executes them.

### say-hello
Prints a friendly greeting.
```bash
echo hello
```

### build
Compiles all packages.
```bash
go build ./...
```

### test
Runs the test suite.
```bash
go test ./...
```

### check
Builds, vets, and tests in one go.
```bash
go build ./... && go vet ./... && go test ./...
```

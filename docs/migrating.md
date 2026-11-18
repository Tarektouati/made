# Migrating from 0.2

[← Back to README](../README.md)

Before v0.3.0, `made` read tasks from the nearest `README.md`. It now reads only
`Madefile.md` (see [How it works](how-it-works.md)). To migrate, either:

- run `made init` next to your `README.md` to create a `Madefile.md`, then move
  the task sections (the H3 headings and their code blocks) out of `README.md`
  into it, replacing the example `hello` task, or
- keep them where they are and pass the file explicitly: `made -f README.md <task>`
  (for example in scripts, CI, or a pre-commit hook's `args`).

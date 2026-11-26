# Changelog

Highlights from published releases, with links to the complete release notes.
Dates are the publication dates in UTC. Changes since the latest release are in
the [unreleased comparison](https://github.com/Tarektouati/made/compare/v0.7.0...main).

## [0.7.0](https://github.com/Tarektouati/made/releases/tag/v0.7.0) — 2026-11-22

- Added `made self-update` and a cached, nonblocking notice when a newer release is available.
- Pinned the development toolchain with proto and added format/lint tasks.
- Kept Bash completion working with `set -e` and `set -u`.
- Improved errors when task arguments or the environment exceed the OS argument limit.

## [0.6.0](https://github.com/Tarektouati/made/releases/tag/v0.6.0) — 2026-11-19

- Added `--check --host` to report missing interpreters, along with checks for oversized scripts and reserved task names.
- Added task heading line numbers and warnings for empty or duplicate task names.
- Restored explicit `-f` reads from pipes and devices, accepted CR-only files, and improved CLI errors and completion quoting.
- **Behavior change:** tasks with an empty code block are refused.
- Resolved interpreters only from absolute PATH entries.

## [0.5.1](https://github.com/Tarektouati/made/releases/tag/v0.5.1) — 2026-11-19

- Hardened installer cleanup, task-file size limits, and Markdown nesting and parse-time limits.
- Preserved inline text in task names and descriptions while handling control and invisible characters safely.
- Improved signal handling and task process-group cleanup.

## [0.5.0](https://github.com/Tarektouati/made/releases/tag/v0.5.0) — 2026-11-17

- Added `made --check` to inspect a Madefile without running its tasks.
- Recorded the creating made version in the first-line comment from `made init`.

## [0.4.1](https://github.com/Tarektouati/made/releases/tag/v0.4.1) — 2026-11-16

- Improved Unicode task-list alignment, heading escapes, BOM handling, and CRLF scripts.
- Hardened installation, file discovery, and `init --force` against invalid inputs and symlink overwrites.
- Improved interpreter errors, argument-limit errors, and signal exit codes and cleanup.

## [0.4.0](https://github.com/Tarektouati/made/releases/tag/v0.4.0) — 2026-11-15

- Added `made init` and `made init --force` for starter Madefiles.
- Added Bash, Zsh, and Fish completions, shipped in release archives and the Homebrew cask.

## [0.3.0](https://github.com/Tarektouati/made/releases/tag/v0.3.0) — 2026-11-14

- **Breaking:** task discovery moved from `README.md` to `Madefile.md`.
- **Breaking:** shell tasks use strict error handling by default. Commands whose failures are intentional need an explicit `|| true`.
- Passed trailing command-line arguments through to tasks and ran tasks from their task file's directory.
- See the [migration guide](docs/migrating.md) when upgrading from 0.2.

## [0.2.0](https://github.com/Tarektouati/made/releases/tag/v0.2.0) — 2026-11-13

- Added `install.sh` and pre-commit / prek integration.
- Updated dependencies and required Go 1.26 for source builds.

## [0.1.0](https://github.com/Tarektouati/made/releases/tag/v0.1.0) — 2026-11-12

- Introduced the Markdown task runner, language-specific interpreters, version output, and task exit-code propagation.
- Added process-group cancellation, CI coverage, and release archives with Homebrew distribution.

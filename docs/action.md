# GitHub Actions

The root composite action installs a made release, verifies its archive against
the release's SHA-256 checksums, and adds the executable to `PATH` for later steps.
It supports Linux and macOS on X64 and ARM64. Windows fails with a clear error;
this action does not change which release binaries the project publishes.

```yaml
name: CI
on: [push, pull_request]
permissions:
  contents: read
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: Tarektouati/made@v1
        id: made
        with:
          version: "0.7.0"
      # Set up any interpreters, linters or other tools used by your tasks here.
      - run: made ci
      - name: Show the installed version
        env:
          MADE_VERSION: ${{ steps.made.outputs.version }}
        run: printf 'Installed made %s\n' "$MADE_VERSION"
```

The `v1` reference is the action's compatibility tag, not the made binary version.
It requires initial maintainer creation after this action is merged; before that,
use a reviewed full commit SHA containing `action.yml`. A full commit SHA also
lets a workflow pin the action itself after `v1` exists.

## Inputs and output

| Name | Default | Meaning |
|---|---|---|
| `version` | `latest` | A full release version, such as `0.7.0` or `v0.7.0`, or `latest`. Full prerelease/build suffixes are accepted when that release exists. |
| `github-token` | `${{ github.token }}` | Token used to authenticate the `latest` lookup against `api.github.com`, avoiding the lower unauthenticated rate limit. |
| Output `version` | | Resolved installed version, without the leading `v`, such as `0.7.0`. |

Pinned versions do not call the Releases API. `latest` uses the latest published
release returned by GitHub; it does not select a branch or install from source.
The token is never included in archive/checksum download requests. Authenticated
API requests do not follow redirects; public asset downloads follow HTTPS-only
redirects with TLS 1.2 or later. All requests have connection and transfer timeouts
and bounded retries. The installer ignores curl's configuration file.

The installer checks for exactly one valid SHA-256 entry for the selected archive
before extracting only `made`. Files are installed under
`$RUNNER_TOOL_CACHE/made/<version>/<arch>`, where `<arch>` is `amd64` or `arm64`.
Each invocation downloads and verifies the release, including when that directory
already exists. Failed downloads or checksum checks leave an existing executable
untouched and do not publish a path or version output.

Checksums detect damaged or substituted archives relative to the checksum file;
they do not independently authenticate a release whose archive and checksums have
both been replaced. The action does not verify release signatures.

## Runner requirements

GitHub-hosted Linux and macOS runners include the required tools. Self-hosted
runners need Bash, curl, tar, gzip, awk, mktemp, mkdir, install and rm, plus
`sha256sum` or `shasum`. Resolving `latest` also needs `jq`. The runner must provide
writable `RUNNER_TEMP` and `RUNNER_TOOL_CACHE` directories and the `GITHUB_PATH` and
`GITHUB_OUTPUT` environment files. Missing tools produce a named error.

The action installs made only. Set up your project's interpreters and dependencies
before running its tasks. A `ci` task can call other made tasks, as in the
[README example](../README.md#github-actions), so local and CI runs share the same
commands.

## Maintaining the action tag

No workflow creates or moves the major tag automatically. After this action is
merged and the action test matrix passes, a maintainer must create `v1` once at a
reviewed commit containing `action.yml`:

```sh
git fetch origin main --tags
git show <reviewed-commit>:action.yml
git tag -a v1 <reviewed-commit> -m 'made GitHub Action v1'
git push origin refs/tags/v1
```

After a compatible release, inspect its commit and action workflow results, then
move `v1` to that reviewed release commit:

```sh
git fetch origin --tags
git show <release-tag>:action.yml
git tag -fa v1 <release-tag> -m 'made GitHub Action v1'
git push origin +refs/tags/v1:refs/tags/v1
```

The release workflow accepts only tag names matching `v*.*.*`, so creating or
moving `v1` does not start GoReleaser. Keep that guard when changing the release
workflow. A breaking action interface change belongs under a new major action tag.
These commands are maintainer steps; adding the action does not publish a release,
move a tag, or create a Marketplace listing.

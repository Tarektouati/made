# Security policy

## Trust model

`made` runs the code blocks of a task file as scripts, with your permissions. That
is the point of it, as with `make` or `just`: running `made` in a repository runs
that repository's code. Only run tasks from files you trust, and read a Madefile
from an unknown source (`made --list`, or the file itself) before running its
tasks.

What `made` decides for you is which file to read:

- A file given with `-f`, and a Madefile in the current directory, are trusted as
  you chose them, and are not checked.
- A Madefile found in a parent directory is only used on Linux and macOS if it and
  its directory (and, for a symlink, every link of the chain, the final file and
  the directories they are in) are owned by you or root and are not group- or
  world-writable; otherwise `made` refuses it and suggests `-f`. `made` then reads
  that same file, or refuses it if it was replaced after the check. This keeps
  another user who can write to a directory above yours from having their tasks run
  by you or by root.
- Outside a repository, the search for a Madefile stops after your home directory.

The full rule is in [How it works](docs/how-it-works.md) (under "Finding the task file"). Windows has
no ownership check.

Known limits of the check:

- ACLs are ignored: only the owner and the mode bits are read, so on macOS an ACL
  entry (`chmod +a`) that lets another user write the Madefile or its directory is
  not detected.
- Directories above the Madefile's directory (and the ones walked through from the
  current directory) are not checked. A user who can rename one of them cannot swap
  the file while `made` reads it, but can put a different tree in its place
  beforehand, with a Madefile that is yours and passes the check.

## Supported versions

Only the latest release receives security fixes. `made` has a single release line,
and fixes ship as a new release rather than as backports.

| Version | Supported |
|---|---|
| Latest release ([releases](https://github.com/Tarektouati/made/releases/latest)) | ✅ |
| Older releases | ❌ |

To update, run `made self-update`, or use the command for how you installed it
(`brew upgrade Tarektouati/tap/made`, `go install github.com/Tarektouati/made@latest`
or `install.sh`). See [Updating](docs/installation.md#updating).

## Reporting a vulnerability

Please report vulnerabilities privately, not in a public issue, pull request or
discussion. Use GitHub's private vulnerability reporting: open the repository's
**Security** tab and choose **Report a vulnerability**, or go straight to
<https://github.com/Tarektouati/made/security/advisories/new>.

Please include:

- the `made` version (`made --version`) and your OS and architecture;
- the steps or a `Madefile.md` that reproduce the issue;
- what an attacker could do with it (the impact), as far as you know.

## What to expect

`made` is maintained by one person on a best-effort basis, so there are no fixed
response times. Your report will be acknowledged, triaged and, if confirmed,
fixed in a new release. The fix is then disclosed in a
[GitHub security advisory](https://github.com/Tarektouati/made/security/advisories)
(with a CVE where it applies), crediting you unless you'd rather stay anonymous.
Please keep the details private until the advisory is published.

## Automated checks

CI runs [govulncheck](https://go.dev/doc/security/vuln/) on every push and pull
request and once a week (`made vulncheck`). CodeQL code scanning and Dependabot
also run on the repository.

# Security policy

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

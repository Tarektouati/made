# Releasing signed macOS binaries

The release workflow runs on `macos-latest` with the pinned OSS GoReleaser. It
cross-builds the existing Linux and Darwin `amd64`/`arm64` targets. A build
post-hook signs and notarizes each Darwin executable before GoReleaser creates
the archives and `checksums.txt`; Linux executables pass through unchanged.

## Owner configuration

The repository owner must provide a **Developer ID Application** signing
identity, exported with its private key as a password-protected `.p12`, and an
App Store Connect team API key for notarization. An ad-hoc or development
certificate does not satisfy Apple's notarization requirements. No credentials
are created by this repository.

Configure these GitHub Actions secrets before triggering a tagged release:

| Secret | Contents |
|---|---|
| `MACOS_SIGN_P12` | Base64-encoded `.p12` containing the Developer ID Application certificate and private key |
| `MACOS_SIGN_PASSWORD` | Non-empty password protecting the `.p12` |
| `MACOS_SIGN_IDENTITY` | Full signing identity name or its certificate's SHA-1 fingerprint, as shown by `security find-identity -p codesigning -v` |
| `MACOS_NOTARY_KEY` | Base64-encoded App Store Connect API `.p8` private key |
| `MACOS_NOTARY_KEY_ID` | That API key's key ID |
| `MACOS_NOTARY_ISSUER_ID` | That API key's issuer UUID |

The existing `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN` are still used for publication.
The signing secrets are scoped to the GoReleaser step. Missing or empty signing
credentials stop the Darwin build, so the workflow cannot publish an unsigned
replacement release. The same rule applies to local GoReleaser builds with
Darwin targets; ordinary `made check` and `made dist` need no Apple credentials.

See GitHub's [certificate setup guidance](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications)
and Apple's [notarytool API-key guidance](https://developer.apple.com/documentation/technotes/tn3147-migrating-to-the-latest-notarization-tool)
for preparing the owner's credentials.

## Signing and notarization

`scripts/notarize-macos.sh` creates a private directory and temporary keychain for
each Darwin artifact. It imports the certificate, then signs with hardened
runtime, a secure timestamp, and the standalone identifier
`com.github.Tarektouati.made`. `codesign --keychain` selects only that temporary
keychain; the script does not change the default keychain or search list and does
not add relaxed entitlements.

After signature verification, the hook submits a ZIP containing the signed
executable with `notarytool --wait --timeout 20m --output-format plist`. Both a
successful command exit and the exact status `Accepted` are required. Rejection,
timeout, unreadable status or a failed signing/cleanup command stops GoReleaser
before it packages or publishes the release. Native tool output stays in the
private temporary directory; public failure messages identify the stage without
printing credential values.

Each hook deletes its temporary keychain, certificate, API key, ZIP and response
on exit, including errors and handled signals. The workflow's `always()` cleanup
step also removes material left by a forcibly terminated hook. Cleanup is limited
to the per-job signing directory under `RUNNER_TEMP`. The workflow allows 45
minutes for GoReleaser inside a 50-minute job, leaving time for that fallback.

Apple issues online notarization tickets for standalone executables, but does not
support stapling tickets to them. Gatekeeper therefore needs access to Apple's
online ticket when checking this binary. The Homebrew cask retains quarantine
metadata and uses the same signed binary as direct downloads and `install.sh`.
Older release assets are not changed by this workflow update.

Apple documents the requirements in [Notarizing macOS software](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution),
the identifier and signing flags in [Creating distribution-signed code](https://developer.apple.com/documentation/xcode/creating-distribution-signed-code-for-the-mac),
and the standalone-binary ticket limitation in [Customizing the notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow).

## Validation

`made check` includes mock tests for credential failures, signing/verification
order, rejected or unfinished notarization, interruption, secret-free diagnostics,
and cleanup boundaries. Those tests substitute the Apple tools and make no
notarization requests. They cannot validate a real certificate, Apple's service,
or Gatekeeper behavior.

Before relying on the first signed release, the owner must configure the secrets
and validate a real build on a macOS runner. Check both Darwin artifacts, their
accepted notarization results, the signatures after archive extraction
(`codesign -vvv --strict ./made`), and launch behavior after a normal download on
the intended macOS architectures. `codesign -dvv ./made` shows the signing
authority and secure timestamp. The real Apple run remains a required owner-side
validation when reviewing this workflow change.

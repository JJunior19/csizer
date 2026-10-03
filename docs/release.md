# Releasing csizer

Releases are cut from versioned tags. GoReleaser, driven by the Release workflow
in `.github/workflows/release.yml`, builds the binaries, publishes the GitHub
release, and updates the Homebrew cask.

## One-time setup

1. ~~Create the public tap repository `JJunior19/homebrew-csizer` with a README
   so its default branch exists.~~ Done — the tap lives at
   `JJunior19/homebrew-csizer` and its default branch is `master`, which is the
   branch GoReleaser pushes to.
2. Create a personal access token that can push contents to
   `JJunior19/homebrew-csizer`.
3. Store that token as the `TAP_GITHUB_TOKEN` repository secret in
   `JJunior19/csizer`.
4. Create a "Developer ID Application" certificate in the Apple Developer
   portal, import it into Keychain Access, and export it as a `.p12` file with
   a password.
5. Create an App Store Connect API key, which downloads as a `.p8` file.
6. Base64-encode both files: `base64 < Certificates.p12` and
   `base64 < ApiKey_XXXXXXXXXX.p8`.
7. Store the following repository secrets in `JJunior19/csizer`:
   `MACOS_SIGN_P12`, `MACOS_SIGN_PASSWORD`, `MACOS_NOTARY_KEY`,
   `MACOS_NOTARY_KEY_ID`, and `MACOS_NOTARY_ISSUER_ID`.
8. Create an npm automation token and store it as the `NPM_TOKEN` repository
   secret. The Release workflow publishes the `csizer` wrapper and one
   platform package per supported OS and architecture to the npm registry.

The tap repository and the `TAP_GITHUB_TOKEN` secret must exist before the first
release, because GoReleaser pushes the generated cask to the tap. The built-in
`GITHUB_TOKEN` cannot write to another repository.

The macOS binaries are signed and notarized whenever `MACOS_SIGN_P12` is set.
Without it, notarization is skipped, and Homebrew on macOS keeps the quarantine
attribute on the installed binary, so Gatekeeper kills unsigned command-line
binaries on Apple Silicon.

## Cutting a release

1. Confirm the working tree is clean and `make check` passes.
2. Push a versioned tag:

   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```

3. The Release workflow builds macOS and Linux binaries for `amd64` and `arm64`,
   uploads the archives and `checksums.txt` to the GitHub release, pushes the
   updated cask to the tap, and then verifies the Homebrew installation on
   macOS.

## Verifying an installation

```sh
./scripts/verify-homebrew-install.sh v0.1.0
```

Without a version argument, the script only checks that installation and the
version command succeed. It removes any existing installation and tap first, so
it verifies the published cask end to end.

## Installing

```sh
brew install --cask JJunior19/csizer/csizer
```

Installing with the fully qualified name trusts only the `csizer` cask, so no
separate `brew trust` step is needed. The cask links the `csizer` binary into
the Homebrew prefix and supports macOS and Linux. On macOS the released
binaries are signed and notarized, so Gatekeeper accepts them without manual
quarantine removal.

# Releasing csizer

Releases are cut from versioned tags. GoReleaser, driven by the Release workflow
in `.github/workflows/release.yml`, builds the binaries, publishes the GitHub
release, and updates the Homebrew cask.

## One-time setup

1. Create the public tap repository `JJunior19/homebrew-csizer` with a README so
   its default `main` branch exists.
2. Create a personal access token that can push contents to
   `JJunior19/homebrew-csizer`.
3. Store that token as the `TAP_GITHUB_TOKEN` repository secret in
   `JJunior19/csizer`.

The tap repository and the `TAP_GITHUB_TOKEN` secret must exist before the first
release, because GoReleaser pushes the generated cask to the tap. The built-in
`GITHUB_TOKEN` cannot write to another repository.

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
brew install JJunior19/csizer/csizer
```

The cask links the `csizer` binary into the Homebrew prefix and supports macOS
and Linux.

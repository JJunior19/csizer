#!/usr/bin/env bash
set -euo pipefail

# Verifies that csizer installs from the Homebrew tap and that the installed
# binary reports the expected version.
#
# Usage:
#   ./scripts/verify-homebrew-install.sh [vX.Y.Z]
#
# The version argument is optional; when omitted, the script only checks that
# installation and the version command succeed. Override the tap and package
# with CSIZER_TAP and CSIZER_FORMULA.

tap="${CSIZER_TAP:-JJunior19/csizer}"
package="${CSIZER_FORMULA:-csizer}"
expected="${CSIZER_VERSION:-${1:-}}"
expected="${expected#v}"

if ! command -v brew >/dev/null 2>&1; then
  echo "error: Homebrew is required to verify the csizer installation" >&2
  exit 1
fi

echo "Removing any existing ${package} installation and tap"
brew uninstall --force --cask "${package}" >/dev/null 2>&1 || true
brew untap "${tap}" >/dev/null 2>&1 || true

echo "Tapping ${tap}"
brew tap "${tap}"

echo "Trusting ${tap}"
# Homebrew refuses to load casks from non-official taps until they are trusted.
brew trust --tap "${tap}"

echo "Installing ${package}"
brew install "${package}"

if ! command -v csizer >/dev/null 2>&1; then
  echo "error: csizer is not on PATH after installation" >&2
  exit 1
fi

reported="$(csizer version | tr ' ' '\n' | sed -n 's/^version=//p')"
if [[ -z "${reported}" ]]; then
  echo "error: could not parse the 'csizer version' output" >&2
  exit 1
fi

if [[ -n "${expected}" && "${reported}" != "${expected}" ]]; then
  echo "error: expected csizer version ${expected} but the installed binary reported ${reported}" >&2
  exit 1
fi

echo "Verified csizer version=${reported} from ${tap}"

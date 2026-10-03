#!/usr/bin/env bash
set -euo pipefail

# Assembles the publishable npm packages into build/npm: the main "csizer"
# wrapper package plus one binary-only package per supported platform
# (csizer-<os>-<arch>). GoReleaser archives and the Homebrew cask keep serving
# the tar.gz artifacts; these npm packages bundle the binaries so package
# managers resolve the right one without postinstall downloads.
#
# Usage: build-npm-packages.sh <dist-dir> <version>
#   <dist-dir>  GoReleaser build output containing csizer_<os>_<arch>_v1/csizer
#   <version>   Release version without the v prefix, e.g. 0.1.2

usage() {
  echo "usage: $0 <dist-dir> <version>" >&2
  exit 2
}

[[ $# -eq 2 ]] || usage

dist_dir=$1
version=$2

[[ -d "$dist_dir" ]] || {
  echo "error: dist dir not found: $dist_dir" >&2
  exit 1
}

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "error: version must be semver without the v prefix, got: $version" >&2
  exit 1
}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
npm_src="$repo_root/npm"
out_dir="$repo_root/build/npm"

targets=(darwin-arm64 darwin-amd64 linux-arm64 linux-amd64)

for target in "${targets[@]}"; do
  os=${target%-*}
  arch=${target#*-}
  # GoReleaser names build dirs with the Go feature-level suffix for the
  # target (e.g. csizer_linux_arm64_v8.0, csizer_darwin_amd64_v1), so the
  # suffix is matched with a wildcard instead of assumed.
  build_dir=$(find "$dist_dir" -maxdepth 1 -type d -name "csizer_${os}_${arch}*" -print -quit)

  [[ -n "$build_dir" && -x "$build_dir/csizer" ]] || {
    echo "error: executable binary not found for $target: $build_dir/csizer" >&2
    exit 1
  }
  binary="$build_dir/csizer"

  pkg_dir="$out_dir/csizer-$target"
  mkdir -p "$pkg_dir/bin"
  cp "$binary" "$pkg_dir/bin/csizer"
  chmod 755 "$pkg_dir/bin/csizer"

  cat >"$pkg_dir/package.json" <<EOF
{
  "name": "csizer-$target",
  "version": "$version",
  "description": "csizer native binary for $os/$arch",
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "git+https://github.com/JJunior19/csizer.git"
  },
  "os": [
    "$os"
  ],
  "cpu": [
    "$arch"
  ],
  "files": [
    "bin"
  ]
}
EOF
done

# The main wrapper package keeps its source of truth in npm/csizer; the build
# copy gets the release version stamped into its own version and into every
# optional dependency so all packages of one release share the same version.
main_dir="$out_dir/csizer"
mkdir -p "$main_dir"
cp -R "$npm_src/csizer/bin" "$main_dir/bin"

node - "$npm_src/csizer/package.json" "$main_dir/package.json" "$version" <<'EOF'
const fs = require('fs');

const [, , sourcePath, targetPath, version] = process.argv;
const pkg = JSON.parse(fs.readFileSync(sourcePath, 'utf8'));

pkg.version = version;
for (const name of Object.keys(pkg.optionalDependencies ?? {})) {
  pkg.optionalDependencies[name] = version;
}

fs.writeFileSync(targetPath, `${JSON.stringify(pkg, null, 2)}\n`);
EOF

echo "npm packages assembled for version $version:"
ls -d "$out_dir"/csizer*

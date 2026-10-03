#!/usr/bin/env node
'use strict';

// Resolves the platform-specific csizer package and hands execution over to
// the native binary bundled with it. Platform packages are optional
// dependencies, so a missing or skipped package means an unsupported
// platform, and the error should say so instead of failing cryptically.

const { spawnSync } = require('child_process');

const platform = process.platform;
const arch = process.arch; // npm package suffixes use Node arch names: arm64, x64

function fail(message) {
  process.stderr.write(`csizer: ${message}\n`);
  process.exit(1);
}

if (platform !== 'darwin' && platform !== 'linux') {
  fail(`unsupported platform "${platform}"; csizer ships binaries for macOS and Linux only`);
}

if (arch !== 'arm64' && arch !== 'x64') {
  fail(`unsupported architecture "${arch}"; csizer ships arm64 and x64 binaries only`);
}

const packageName = `csizer-${platform}-${arch}`;

let binaryPath;
try {
  binaryPath = require.resolve(`${packageName}/bin/csizer`);
} catch (error) {
  fail(
    `the "${packageName}" package is not installed; ` +
      'reinstall csizer with a supported package manager (npm or pnpm), ' +
      'or install the Homebrew cask instead: brew install --cask JJunior19/csizer/csizer'
  );
}

const result = spawnSync(binaryPath, process.argv.slice(2), {
  stdio: 'inherit',
});

if (result.error) {
  fail(`failed to execute ${binaryPath}: ${result.error.message}`);
}

process.exit(result.status === null ? 1 : result.status);

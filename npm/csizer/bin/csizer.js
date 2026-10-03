#!/usr/bin/env node
'use strict';

// Resolves the platform-specific csizer package and hands execution over to
// the native binary bundled with it. Platform packages are optional
// dependencies, so a missing or skipped package means an unsupported
// platform, and the error should say so instead of failing cryptically.

const { spawnSync } = require('child_process');

const archByNodeArch = { arm64: 'arm64', x64: 'amd64' };
const nodeArch = archByNodeArch[process.arch];
const platform = process.platform;

function fail(message) {
  process.stderr.write(`csizer: ${message}\n`);
  process.exit(1);
}

if (platform !== 'darwin' && platform !== 'linux') {
  fail(`unsupported platform "${platform}"; csizer ships binaries for macOS and Linux only`);
}

if (!nodeArch) {
  fail(`unsupported architecture "${process.arch}"; csizer ships amd64 and arm64 binaries only`);
}

const packageName = `csizer-${platform}-${nodeArch}`;

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

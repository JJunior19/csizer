# Operating csizer

How to install, configure, run, and troubleshoot csizer on a workstation or
server. For the command walkthrough see the [README](../README.md); for
design decisions see [architecture.md](architecture.md).

## Compatibility

| Dimension | Supported | Evidence |
|-----------|-----------|----------|
| Operating systems | macOS and Linux | GoReleaser build matrix |
| Architectures | amd64 and arm64 | GoReleaser build matrix |
| Docker | Any daemon reachable through the standard Docker environment, including `DOCKER_HOST` | `internal/dockerclient` uses `client.FromEnv` |
| Go (building from source) | 1.26.5 | `go.mod` |
| Node (npm wrapper) | 18 or newer | `npm/csizer/package.json` `engines` |
| Package managers | Homebrew, npm, pnpm | Release verification jobs |

## Installation channels

| Channel | Command | Notes |
|---------|---------|-------|
| Homebrew | `brew install --cask JJunior19/csizer/csizer` | Fully qualified name auto-trusts the cask |
| npm | `npm install -g csizer` | Resolves the platform binary at install time; no postinstall scripts |
| pnpm | `pnpm add -g csizer` | Same packages as npm |
| Source | `make build` with Go 1.26.5 | Produces `bin/csizer` |

All channels serve the same release binaries. macOS binaries are signed and
notarized.

Updates follow each channel: `brew upgrade` (the tap updates on every
release), `npm update -g csizer` or `pnpm add -g csizer` (npm packages publish
on every release), or `git pull && make build` for source. Uninstall with
`brew uninstall --cask csizer`, `npm uninstall -g csizer`, or by deleting
`bin/csizer`.

## Configuration

| Variable | Purpose |
|----------|---------|
| `CONTAINERSIZE_DB_PATH` | Override the SQLite database location |
| `CONTAINERSIZE_CONFIG_PATH` | Override the config location |
| `DOCKER_HOST` and standard Docker variables | Select the Docker daemon to observe |

Without overrides, csizer uses its platform default locations and prints the
resolved database path on `csizer init`. If Docker is unavailable, init still
creates the database and prints its path before reporting the Docker error.

## Running the daemon

`csizer daemon` owns collection independently of the foreground CLI: tracked
workloads keep being sampled after `csizer track` exits, and the daemon
combines Docker lifecycle events with a reconciliation pass every minute
(retry every 5 seconds) to repair missed events and container recreation.

Collection is exclusive. A daemon holds a 30-second lease stored in the local
database; a second daemon exits with "another daemon still holds the
collection lease" instead of double-sampling. Run one daemon per machine, per
database. Stop it with Ctrl+C or by sending SIGTERM.

## Data

Everything is stored in a local SQLite database (WAL mode, `0600` file and
`0700` parent permissions on fresh creation). See
[architecture.md](architecture.md) for the schema, pragmas, and sampling
defaults. Workload settings (sampling rates and 30-day raw retention) are
stored per workload; consult `csizer inspect WORKLOAD` for the observations
behind a recommendation.

## Automation

`csizer track`, `list`, `inspect`, and `recommend` accept `--json` and emit a
versioned, stable machine-readable document, so scripts should pin the parser
to the reported schema version instead of the human output.

## Troubleshooting

| Symptom | Cause and fix |
|---------|---------------|
| `Docker: inaccessible` on init | The daemon named by the Docker environment is down or unreachable; fix `DOCKER_HOST` or start Docker, then run `csizer init` again. |
| npm wrapper prints "the csizer-… package is not installed" | The platform is not one of darwin/linux on amd64/arm64, or the install skipped optional dependencies; reinstall, or use the Homebrew cask. |
| Daemon exits mentioning the collection lease | Another daemon owns collection for this database; stop it, or point `CONTAINERSIZE_DB_PATH` at a different database. |
| A recreated container shows under the same workload | Expected: identity survives container recreation; the daemon reconciles it within a minute. |

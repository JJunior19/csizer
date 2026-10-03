# ContainerSize

ContainerSize is a local-first tool for discovering container workloads and, in later phases, recommending CPU and memory settings from collected evidence.

**Status:** Local Docker tracking, background collection, workload analysis, and ECS Fargate recommendations are available. The Homebrew release (v0.1.1) is published with signed and notarized macOS binaries.

## Installation

Install the published binary with Homebrew (macOS and Linux):

```sh
brew install --cask JJunior19/csizer/csizer
```

The fully qualified name trusts the cask automatically, so no separate
`brew trust` step is needed. On macOS the released binaries are signed and
notarized. Alternatively, build from source with Go 1.26.5 using `make build`.

## Objective

ContainerSize aims to turn representative passive observations into reviewable resource recommendations. Every recommendation should explain its evidence window, assumptions, uncertainty, and confidence.

Recommendations are evidence-based estimates, not load-test guarantees. ContainerSize will not generate traffic, prove capacity, replace production monitoring, or guarantee behavior under unseen workloads.

## Current usage

With Go 1.26.5 available, the current code can be checked locally:

```sh
make build
./bin/csizer --version
./bin/csizer init
./bin/csizer docker list
./bin/csizer track backend
# Run this in a second terminal while the workload is active.
./bin/csizer daemon
./bin/csizer list
./bin/csizer inspect backend
./bin/csizer recommend backend --provider ecs-fargate
```

`csizer init` creates or migrates the local SQLite database, prints the absolute path reported by the initialized store, and then checks Docker access:

```text
Database: /Users/alex/Library/Application Support/ContainerSize/containersize.db
Docker: accessible
```

If Docker is unavailable, database initialization still completes and the database path is printed before the command returns the Docker error. Set `CONTAINERSIZE_DB_PATH` and `CONTAINERSIZE_CONFIG_PATH` to override the platform defaults.

The phase-3 schema stores workload identity and settings, historical container instances, tracking-session metadata, raw metric samples, and structures for future container events and minute rollups. The schema does not mean those events or rollups are being collected: this phase only provides migrations and concrete persistence methods for workloads, instances, sessions, and atomic metric-sample batches.

`docker list` reads all containers, including stopped containers, through the Docker SDK and prints only minimal identity and status metadata:

```text
CONTAINER      SERVICE  PROJECT  IMAGE                              STATUS
demo-api-1     api      demo     ghcr.io/example/api:latest         running
standalone-db  -        -        postgres:17                        exited
```

It honors the standard Docker environment, including `DOCKER_HOST`. It does not inspect container environments, logs, mounts, requests, or payloads.

The later tracking and recommendation workflow remains planned:

```sh
docker compose up -d
csizer track backend
csizer list
csizer inspect backend
csizer recommend backend --provider ecs-fargate
```

`csizer track WORKLOAD` resolves a Docker workload by its Compose service, workload key, display name, or container name and stores it locally. It does not start collection itself; run `csizer daemon` separately while the tracked workload is running. `csizer list`, `csizer inspect WORKLOAD`, and `csizer recommend WORKLOAD --provider ecs-fargate` expose persisted tracking, analysis, and the smallest compatible ECS Fargate task size. Add `--json` to these commands for a versioned machine-readable response.

The recommendation remains an evidence-based estimate, not a load-test capacity guarantee. It is based on collected samples and includes confidence, assumptions, and warnings. Homebrew installation is available; npm installation is not available yet.

## Architecture

The design keeps identity and future analysis independent from container runtimes, persistence, provider formats, and output rendering. Moby SDK types remain confined to the Docker adapter, and SQLite migrations are embedded in the binary.

See [docs/architecture.md](docs/architecture.md) for package boundaries, runtime and SQLite plans, and risks.

## Privacy

ContainerSize is local-first. The current `docker list` command reads only the minimal container identity and status metadata shown in its output and sends no remote telemetry. The planned collector will keep resource metrics, minimal technical identifiers, and lifecycle events local by default; it will not collect environment variables, application logs, request content or payloads, or secrets. Any future remote provider exchange must be explicit and limited to required normalized fields.

## Roadmap

Phases 1-9 are complete, and phase 10 (Homebrew and release) is the current work unit. See the [canonical roadmap](docs/roadmap.md) for current progress, acceptance status, and the next work unit.

## Contributing

Use Go 1.26.5 and keep changes as small, reviewable work units. Before proposing a change, run:

```sh
make check
```

Tests should exercise behavior, errors, and output streams. Use table-driven tests when behavior has multiple cases. Do not introduce an interface until a concrete implementation needs the seam.

See [docs/release.md](docs/release.md) for the release process and Homebrew packaging.

# ContainerSize

ContainerSize is a local-first tool for discovering container workloads and, in later phases, recommending CPU and memory settings from collected evidence.

**Status:** Local SQLite initialization and Docker discovery are available through `csizer init` and `csizer docker list`. Tracking, metric collection, retention jobs, statistics, and recommendations are not implemented yet.

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

`csizer track`, `csizer list`, `csizer inspect`, and `csizer recommend` are future roadmap examples and do not exist yet. Homebrew and npm installation are also not available in this phase.

## Architecture

The design keeps identity and future analysis independent from container runtimes, persistence, provider formats, and output rendering. Moby SDK types remain confined to the Docker adapter, and SQLite migrations are embedded in the binary.

See [docs/architecture.md](docs/architecture.md) for package boundaries, runtime and SQLite plans, risks, and the 12 planned work units.

## Privacy

ContainerSize is local-first. The current `docker list` command reads only the minimal container identity and status metadata shown in its output and sends no remote telemetry. The planned collector will keep resource metrics, minimal technical identifiers, and lifecycle events local by default; it will not collect environment variables, application logs, request content or payloads, or secrets. Any future remote provider exchange must be explicit and limited to required normalized fields.

## Roadmap

Embedded SQLite persistence is the current phase. The roadmap continues through collection, daemon reliability, analysis, recommendation, ECS adaptation, CLI workflows, packaging, and hardening. No tracking command, collector, daemon, retention process, provider, or packaging code exists yet.

## Contributing

Use Go 1.26.5 and keep changes as small, reviewable work units. Before proposing a change, run:

```sh
make check
```

Tests should exercise behavior, errors, and output streams. Use table-driven tests when behavior has multiple cases. Do not introduce an interface until a concrete implementation needs the seam.

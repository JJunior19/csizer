# ContainerSize

ContainerSize is planned as a local-first tool that observes container workloads and recommends CPU and memory settings from collected evidence.

**Status:** bootstrap only. The `csizer` executable currently provides help and version output, plus internal platform path resolution. It is not yet usable for container monitoring or recommendations.

## Objective

ContainerSize aims to turn representative passive observations into reviewable resource recommendations. Every recommendation should explain its evidence window, assumptions, uncertainty, and confidence.

Recommendations are evidence-based estimates, not load-test guarantees. ContainerSize will not generate traffic, prove capacity, replace production monitoring, or guarantee behavior under unseen workloads.

## Current bootstrap

With Go 1.26.5 available, the current code can be checked locally:

```sh
go test ./...
make build
./bin/csizer --version
```

The planned quick start, once monitoring exists, is:

```sh
docker compose up -d
csizer track backend
csizer list
csizer inspect backend
csizer recommend backend --provider ecs-fargate
```

The `csizer` commands are roadmap examples and do not exist yet. Homebrew and npm installation are not available in this phase.

## Architecture

The design keeps analysis independent from container runtimes, persistence, provider formats, and output rendering. Interfaces will be added only with their first implementation rather than as empty placeholders.

See [docs/architecture.md](docs/architecture.md) for package boundaries, runtime and SQLite plans, risks, and the 12 planned work units.

## Privacy

ContainerSize is local-first. By default, it collects only resource metrics, minimal technical identifiers, and container lifecycle events. It does not collect environment variables, application logs, request content or payloads, or secrets, and it sends no remote telemetry. Any future remote provider exchange must be explicit and limited to required normalized fields.

## Roadmap

The roadmap progresses through Docker discovery and workload identity, SQLite persistence, collection, daemon reliability, analysis, recommendation, ECS adaptation, CLI workflows, packaging, and hardening. Docker SDK, SQLite, daemon, provider, and packaging code are intentionally outside this bootstrap.

## Contributing

Use Go 1.26.5 and keep changes as small, reviewable work units. Before proposing a change, run:

```sh
make check
```

Tests should exercise behavior, errors, and output streams. Use table-driven tests when behavior has multiple cases. Do not introduce an interface until a concrete implementation needs the seam.

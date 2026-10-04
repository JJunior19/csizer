# ContainerSize

Size your containers with evidence, not guesswork. ContainerSize discovers the
Docker workloads running on your machine, collects resource samples while they
run, and recommends the smallest AWS ECS Fargate task size that fits — all
local-first, stored in SQLite on your machine.

**Status:** tracking, background collection, analysis, and ECS Fargate
recommendations are available. Release v0.1.1 ships signed and notarized macOS
binaries.

## Install

Homebrew (macOS and Linux):

```sh
brew install --cask JJunior19/csizer/csizer
```

npm or pnpm (Node 18+):

```sh
npm install -g csizer
# or
pnpm add -g csizer
```

All channels serve the same release binaries; macOS binaries are signed and
notarized, and npm resolves the platform binary at install time without
postinstall scripts. Prefer building from source? Use Go 1.26.5 and run
`make build`.

## Quick start

From any directory with a running Compose project:

**1. Initialize local storage and check Docker access**

```sh
csizer init
```

```text
Database: /Users/you/Library/Application Support/ContainerSize/containersize.db
Docker: accessible
```

**2. See what Docker is running**

```sh
csizer docker list
```

```text
CONTAINER      SERVICE  PROJECT  IMAGE                       STATUS
demo-api-1     api      demo     ghcr.io/example/api:latest  running
standalone-db  -        -        postgres:17                 exited
```

**3. Track the workload you care about**

Resolve it by Compose service, workload key, display name, or container name:

```sh
csizer track api
```

**4. Start the collector in a second terminal**

The daemon collects samples while the tracked workload is running:

```sh
csizer daemon
```

**5. Get the recommendation**

```sh
csizer recommend api --provider ecs-fargate
```

The recommendation reports the smallest compatible Fargate task size, with the
confidence, assumptions, and warnings behind it. It is an evidence-based
estimate, not a load-test capacity guarantee.

## Commands

| Command | Purpose |
|---------|---------|
| `csizer init` | Create or migrate the local SQLite database; check Docker access |
| `csizer docker list` | List local containers with minimal identity and status |
| `csizer track WORKLOAD` | Track a discovered workload |
| `csizer daemon` | Run continuous local collection |
| `csizer list` | List tracked workloads |
| `csizer inspect WORKLOAD` | Show stored observations for a workload |
| `csizer recommend WORKLOAD --provider ecs-fargate` | Recommend the smallest compatible Fargate task size |
| `csizer version` | Print build version information |

Add `--json` to `track`, `list`, `inspect`, and `recommend` for versioned
machine-readable output.

## Requirements and configuration

- Docker running locally. The standard Docker environment is honored,
  including `DOCKER_HOST`.
- `CONTAINERSIZE_DB_PATH` and `CONTAINERSIZE_CONFIG_PATH` override the
  platform-default locations.
- If Docker is unavailable, `csizer init` still creates the database and
  prints its path before reporting the Docker error.

## Privacy

Local-first by design:

- No remote telemetry. Everything stays in your SQLite database.
- `docker list` reads only the minimal container identity and status metadata
  shown in its output. It does not inspect environments, logs, mounts,
  requests, or payloads.
- Collection never reads environment variables, application logs, request
  content, or secrets. Any future remote exchange will be explicit and limited
  to required normalized fields.

## Project

| Doc | Contents |
|-----|----------|
| [docs/operations.md](docs/operations.md) | Compatibility, configuration, daemon lifecycle, data, troubleshooting |
| [docs/privacy.md](docs/privacy.md) | Data inventory and the local-first review |
| [docs/development.md](docs/development.md) | Setup, layout, and conventions for contributors |
| [docs/architecture.md](docs/architecture.md) | Package boundaries, runtime and SQLite design, risks |
| [docs/roadmap.md](docs/roadmap.md) | Delivery phases, MVP acceptance, and maintenance rules |
| [docs/release.md](docs/release.md) | Release process and packaging |

## Contributing

Use Go 1.26.5 and keep changes small, reviewable work units. Run before
proposing a change:

```sh
make check
```

Tests should exercise behavior, errors, and output streams; prefer
table-driven tests when behavior has multiple cases. Do not introduce an
interface until a concrete implementation needs the seam.

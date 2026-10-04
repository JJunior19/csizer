# Privacy review

Reviewed against v0.1.3 on 2026-10-04 by reading the code paths named below.
This page states what csizer reads, stores, and transmits, and the review
method, so the claim "local-first" is checkable rather than a slogan.

## What csizer reads

| Data | Where | Why |
|------|-------|-----|
| Container identity: names, Compose project and service labels, image reference, state | `internal/dockerclient` via the Docker API | Discover workloads and keep identity stable across container recreation |
| Runtime counters: cumulative CPU and system time, memory usage/cache/working-set/limit, optional PID, network, and block-I/O counters | Docker stats streams read by `internal/collector` | Produce resource samples and recommendations |

The Docker API is the only external interface csizer talks to
(`client.FromEnv`, honoring `DOCKER_HOST`). There is no `net/http` client in
`internal/` or `cmd/`: csizer makes no outbound HTTP requests anywhere.

## What csizer never reads

By construction, the Docker calls used do not access:

- Environment variables of containers
- Application logs or request content and payloads
- Mounted files or secrets
- Anything beyond the listed identity labels and runtime counters

## What csizer stores

Everything goes to one local SQLite database (default path printed by
`csizer init`; override with `CONTAINERSIZE_DB_PATH`): workload identity and
settings, container instances, tracking sessions, metric samples, and
lifecycle events. Fresh databases use `0600` permissions with `0700` parent
directories, and the store requires regular files with protected parents.

## What leaves the machine

Nothing. There is no telemetry, no update check, and no provider call. The
ECS Fargate recommendation is produced by translating measured evidence into
entries of the static Fargate task-size catalog shipped in the binary.

## Review method

- Grepped `internal/` and `cmd/` for HTTP client usage: none.
- Confirmed the Docker client is constructed with `client.FromEnv` only for
  listing, stats, events, and daemon checks (`internal/dockerclient`).
- Confirmed recommendation output adapts stored evidence to the static
  provider catalog (`internal/ecsfargate`).

Any future change that introduces a remote call must update this page in the
same commit and make the exchange explicit and inspectable, per the boundary
in [architecture.md](architecture.md).

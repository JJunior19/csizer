# Developing csizer

How to set up, work in, and verify changes to this repository. For running
csizer see [operations.md](operations.md); for release duties see
[release.md](release.md).

## Requirements

- Go 1.26.5 (from `go.mod`)
- golangci-lint v2.12.2 (CI pins this version)
- Node 22 when touching the npm packaging under `npm/`
- A Docker daemon for the integration paths (`docker list`, collector, daemon)

## Workflow

```sh
make check   # fmt-check, vet, lint, race tests, build — run before proposing a change
```

| Target | Purpose |
|--------|---------|
| `make build` | Build `bin/csizer` |
| `make fmt` | Apply gofmt |
| `make fmt-check` | Fail if anything needs gofmt |
| `make vet` | `go vet ./...` |
| `make lint` | golangci-lint |
| `make test` / `make test-race` | Unit tests, with and without the race detector |

CI runs the same checks on every push and pull request, plus a cross-platform
build matrix (linux/darwin, amd64/arm64). Releases are tag-driven; see
[release.md](release.md).

## Layout

| Path | Responsibility |
|------|----------------|
| `cmd/csizer` | Thin entry point; assembles dependencies only |
| `internal/cli` | Command definitions and output rendering |
| `internal/dockerclient` | Docker adapter; Moby SDK types must not cross this boundary |
| `internal/collector` | CPU-delta and memory semantics; one-shot and batched persistence |
| `internal/identity` | Pure workload identity resolution; no I/O |
| `internal/daemon` | Background collection, lease ownership, reconciliation |
| `internal/storage` | SQLite opening, migrations, repositories |
| `internal/config` | Platform path policy |
| `internal/ecsfargate` | ECS Fargate task-size adaptation |
| `internal/recommendation`, `internal/analysis` | Evidence, confidence, and analysis workflows |
| `migrations/` | Numbered, checksum-verified SQL migrations |
| `npm/` | npm wrapper package; platform packages are generated, never edited |

## Conventions

- Keep `cmd/` thin and Moby types inside `internal/dockerclient`; tests must
  be able to replace Docker and storage through the existing seams without
  importing SDK or database types.
- Do not introduce an interface until a concrete implementation needs the
  seam.
- Tests exercise behavior, errors, and output streams; prefer table-driven
  tests when behavior has multiple cases.
- Schema changes are new numbered SQL files in `migrations/`; every open
  validates names and SHA-256 checksums, so applied migrations are immutable.
- Machine-readable output is versioned: changes add a schema version instead
  of mutating an existing one.
- Keep changes as small, reviewable work units with tests and docs alongside
  the behavior; see the maintenance rules in
  [roadmap.md](roadmap.md).

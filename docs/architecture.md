# ContainerSize architecture

ContainerSize will passively observe container workloads and produce evidence-backed resource recommendations. The current phase provides the CLI foundation, Docker container discovery, pure workload identity resolution, embedded SQLite migrations, concrete storage repositories, version metadata, platform path policy, tests, and project tooling. It does not yet collect container metrics or lifecycle events.

## Boundaries

The executable entry point stays thin: `cmd/csizer` assembles dependencies and delegates CLI behavior to `internal/cli`. `internal/dockerclient` owns Docker construction and maps Moby SDK responses into minimal internal metadata; Moby types do not cross that adapter boundary. `internal/identity` is a pure resolver with no Docker or I/O dependency. `internal/storage` owns SQLite opening, migrations, and concrete persistence behavior. Platform path policy belongs to `internal/config`; build metadata belongs to `internal/version`. Future domain and application packages will own collection and analysis workflows.

The provisional module path is `github.com/jorgeccarhuasaroni/containersize`. Before publication, changing the `module` directive and replacing that prefix in Go imports is sufficient. Keep the path confined to module declarations and imports so the rename remains mechanical.

The Docker SDK is used only for read-only container listing and daemon access checks. The pure-Go SQLite driver is confined to `internal/storage`. No collector, Docker event stream, daemon, retention job, provider adapter, recommendation logic, or packaging wrapper is part of this phase. Interfaces remain at CLI consumer seams so tests can replace Docker and storage lifecycle dependencies without importing SDK or database types.

## Planned ports

The following names describe intended architectural seams, not interfaces that exist today:

| Port | Responsibility |
| --- | --- |
| `ContainerSource` | Discover containers, receive lifecycle events, and read runtime counters. |
| `WorkloadResolver` | Group container instances into stable deployable workloads. |
| `SampleWriter` | Persist validated observation batches without exposing storage details. |
| `ActivityClassifier` | Separate active workload windows from idle or ambiguous periods. |
| `Analyzer` | Convert representative samples into resource recommendations and evidence. |
| `ConfidenceEvaluator` | Grade coverage, recency, variability, and data quality. |
| `ProviderAdapter` | Translate recommendations to provider-specific resource settings. |
| `Renderer` | Emit human-readable and stable machine-readable results. |

## Runtime approach

The daemon will combine lifecycle events with periodic reconciliation. Events provide low-latency updates; reconciliation repairs missed events, daemon restarts, and runtime drift. Observation remains passive: ContainerSize measures workloads that actually occur and does not generate traffic.

SQLite runs in WAL mode with `synchronous=NORMAL`, foreign keys enabled, and a 5000 ms busy timeout. These pragmas are repeated in the escaped file URI DSN so every pooled `database/sql` connection receives them. Existing database paths must be regular files and may not be symbolic links. Their parent directory must not be writable by group or others; existing parent permissions are never changed. Newly created parent directories and database files use `0700` and `0600` permissions respectively.

The top-level `migrations` package embeds numbered SQL files. `schema_migrations` records each version, filename, exact-byte SHA-256 checksum, and application timestamp. Every open validates applied names and checksums. Pending migrations acquire a dedicated connection and `BEGIN IMMEDIATE`, re-read migration state under that writer lock, and apply the SQL and version record in the same transaction.

The initial schema contains `workloads`, `container_instances`, `tracking_sessions`, `metric_samples`, `container_events`, `minute_rollups`, and `workload_settings`. Samples reference sessions rather than duplicating `workload_id`; sessions may optionally reference a container instance. Workload settings default to 2-second active sampling, 10-second idle sampling, 30-day raw retention, a 20-second startup window, no provider, and the `balanced` profile. These are persisted settings only: no retention or rollup job runs in phase 3.

Metric sample batches store CPU cores, optional host CPU percent, memory usage/cache/working-set/limit, optional PID/network/block-I/O counters, and a caller-supplied activity state. A batch uses one database transaction and one prepared insert statement. Validation happens before writes, nullable metrics remain SQL `NULL`, timestamps are UTC RFC3339Nano text, and any row or database error rolls back the full batch. `database/sql` makes the store safe for concurrent callers; collector-phase serialization is intentionally not implemented yet.

Machine-readable output will use a versioned, stable schema. Recommendations will carry evidence windows, sample counts, confidence, assumptions, and warnings so consumers do not confuse estimates with measured load-test capacity.

## Privacy boundary

The current `docker list` command reads only minimal container identity labels and status metadata and sends no remote telemetry. Future collection is planned to keep resource metrics, minimal technical identifiers, and lifecycle events local by default. That collector must not read environment values, container logs, request content or payloads, or secrets, and any future provider request must be explicit and inspectable.

## Key risks

- Passive samples can miss peaks or represent an unrepresentative workload window.
- Container identity and workload grouping vary across Docker, Compose, and orchestrators.
- Counter resets, short-lived containers, throttling, and host pressure can distort analysis.
- SQLite write pressure and retention can affect long-running daemon reliability.
- Stable output and migration compatibility constrain later schema changes.
- Provider resource semantics can diverge from local runtime measurements.

## Delivery roadmap

Architecture records durable technical boundaries and decisions; mutable delivery status belongs in the [canonical roadmap](roadmap.md). Keep each roadmap work unit independently testable and reviewable, with tests and documentation included alongside the behavior it introduces.

# ContainerSize architecture

ContainerSize will passively observe container workloads and produce evidence-backed resource recommendations. The current implementation provides the CLI foundation, Docker container discovery, pure workload identity resolution, version metadata, platform path policy, tests, and project tooling. It does not yet monitor or persist container activity.

## Boundaries

The executable entry point stays thin: `cmd/csizer` assembles dependencies and delegates CLI behavior to `internal/cli`. `internal/dockerclient` owns Docker construction and maps Moby SDK responses into minimal internal metadata; Moby types do not cross that adapter boundary. `internal/identity` is a pure resolver with no Docker or I/O dependency. Platform path policy belongs to `internal/config`; build metadata belongs to `internal/version`. Future domain and application packages will own analysis rules and workflows, while infrastructure packages will contain SQLite and provider details.

The provisional module path is `github.com/jorgeccarhuasaroni/containersize`. Before publication, changing the `module` directive and replacing that prefix in Go imports is sufficient. Keep the path confined to module declarations and imports so the rename remains mechanical.

The Docker SDK is used only for read-only container listing. No SQLite driver, daemon, provider adapter, packaging wrapper, migration, statistics collection, event stream, or speculative empty interface is part of this phase. The CLI owns the small listing and closing interface it consumes so tests can replace the adapter without importing SDK types.

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

SQLite will run in WAL mode behind one batch-writing goroutine. Readers use separate read transactions, while the writer serializes bounded batches to avoid lock contention and unbounded memory growth. Schema migrations and retention policy arrive with the first storage implementation.

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

## Planned work units

Each work unit should remain independently testable and reviewable, with tests and documentation included alongside every behavior it introduces:

1. Bootstrap the architecture, CLI shell, path policy, project documentation, and CI.
2. Implement Docker discovery and stable container and workload identity. **Current.**
3. Add SQLite schema, migrations, WAL configuration, retention, and the single batch writer.
4. Collect resource samples and lifecycle events through the first `ContainerSource` and `SampleWriter` implementations.
5. Add the daemon with restart recovery, lifecycle coordination, and periodic reconciliation.
6. Build workload aggregates and representative-window analysis from persisted samples.
7. Produce CPU and memory recommendations with evidence, confidence, assumptions, and warnings.
8. Translate normalized recommendations into valid ECS Fargate CPU and memory settings.
9. Expose track, list, inspect, and recommend workflows with stable human-readable and machine-readable outputs.
10. Add Homebrew packaging, release automation, and installation verification.
11. Add npm and pnpm distribution wrappers with platform binary resolution and installation verification.
12. Complete operator and contributor documentation, compatibility checks, privacy review, and release hardening.

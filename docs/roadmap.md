# ContainerSize roadmap

**Current state:** Phases 1-10 are complete. Phase 11 (npm and pnpm packaging) is in progress: automation is committed, the first npm release is pending. Last updated: 2026-10-03.

This document is the single source of truth for mutable project progress.

## Statuses

| Status | Meaning |
| --- | --- |
| Complete | Delivered, verified, and committed. |
| Next | The next bounded work unit. |
| Pending | Planned but not started. |
| Partial | Some acceptance evidence exists, but the criterion is not complete end to end. |
| Blocked | Cannot proceed until a named dependency or decision is resolved. |

## Delivery phases

| Phase | Scope | Status | Delivered outcome or target | Commit evidence |
| --- | --- | --- | --- | --- |
| 1 | Architecture and CI | Complete | Modular CLI foundation, platform path policy, project documentation, tests, linting, CI, and cross-platform builds. | `1e143c5`, `b0ff68a`, `5b55bf3` |
| 2 | Docker discovery and identity | Complete | Read-only Docker discovery, stable workload identity, and `csizer docker list`. | `7901d84`, `999c0c0`, `3e74cb6` |
| 3 | SQLite, migrations, and init | Complete | Embedded checksummed migrations, secure SQLite Store and repositories, atomic metric batches, Docker health check, and `csizer init`. | `6e7ddb4`, `237088a`, `0f6bdd1`, `ee67484` |
| 4 | Collector and events | Complete | Decode Docker stats, normalize lifecycle events, and persist observations through the existing batch Store. | `27bcfc6` |
| 5 | Daemon | Complete | Background lifecycle, lease-based ownership, recovery, and periodic reconciliation. | `a94219f` |
| 6 | Aggregates and analysis | Complete | Build workload aggregates and representative-window analysis from persisted samples. | `bb47dfd` |
| 7 | Recommendation and confidence | Complete | Produce CPU and memory recommendations with evidence, confidence, assumptions, and warnings. | `780c294` |
| 8 | ECS Fargate adapter | Complete | Translate normalized recommendations into valid ECS Fargate CPU and memory settings. | `bf96f82` |
| 9 | CLI tracking, query, and outputs | Complete | Expose tracking and query workflows with stable human-readable and machine-readable output. | `8e811dd` |
| 10 | Homebrew and release | Complete | v0.1.1 published from versioned tags with signed and notarized macOS binaries; the tap serves the cask and `brew install --cask JJunior19/csizer/csizer` is verified end to end on macOS. | `0de13f4` |
| 11 | npm and pnpm packaging | Partial | The npm wrapper, platform packages, and release publishing and verification are committed (`ff7b1f6`); the first npm release is pending. | `ff7b1f6` |
| 12 | Documentation and hardening | Pending | Complete operator and contributor documentation, compatibility checks, privacy review, and release hardening. | - |

## Phase 10 (complete) — next work unit: phase 11

- [x] Build versioned release binaries for supported macOS and Linux targets.
- [x] Publish and verify a Homebrew cask that resolves those release binaries. v0.1.1 is live with signed and notarized macOS binaries, and `brew install --cask JJunior19/csizer/csizer` is verified end to end.
- [x] Automate release publication from versioned tags.
- [x] Add installation verification for Homebrew users.

Out of scope for phase 10: npm and pnpm packaging.

## MVP acceptance

| # | Acceptance criterion | Status | Evidence or gap |
| --- | --- | --- | --- |
| 1 | Install `csizer` through Homebrew. | Complete | v0.1.1 serves the cask from `JJunior19/homebrew-csizer`; `brew install --cask JJunior19/csizer/csizer` is verified end to end on macOS. |
| 2 | Install `csizer` through `pnpm add -g`. | Pending | npm and pnpm packaging is phase 11. |
| 3 | Discover Docker containers with `csizer docker list`. | Complete | Docker discovery and the identity resolver are committed. |
| 4 | Resolve a Compose service with `csizer track backend`. | Complete | The tracking command resolves and persists a discovered Docker workload. |
| 5 | Keep collecting after the foreground CLI exits. | Complete | The background daemon owns collection independently of the foreground CLI. |
| 6 | Preserve workload identity when Compose recreates a container. | Complete | The daemon resolves recreated containers to their existing tracked workload. |
| 7 | Show tracked workloads with `csizer list`. | Complete | The CLI lists persisted tracked workloads. |
| 8 | Show historical statistics with `csizer inspect`. | Complete | The CLI exposes persisted aggregate statistics. |
| 9 | Return a valid ECS Fargate recommendation. | Complete | The CLI adapts provider-neutral recommendations to valid ECS Fargate task sizes. |
| 10 | Include evidence, confidence, and warnings in recommendations. | Complete | The recommendation CLI output includes all three. |
| 11 | Persist observations locally in SQLite. | Complete | The collector and background daemon persist metric and lifecycle observation batches locally. |
| 12 | Preserve the local-first privacy boundary. | Complete | Current Docker discovery reads minimal identity/status metadata, sends no telemetry, and documents excluded application data. |
| 13 | Support JSON for important query commands. | Complete | Tracking and query commands emit versioned JSON with `--json`. |
| 14 | Enforce tests and cross-platform checks in CI. | Complete | CI runs formatting, vet, lint, race tests, and Linux/macOS builds. |
| 15 | Create release binaries, a Homebrew cask, and npm packages from a release. | Partial | Tags produce signed and notarized binaries plus the Homebrew cask (v0.1.1); npm packaging is phase 11. |

## Maintenance

- Update progress only after verified behavior is committed.
- Include commit evidence for every completed phase.
- Keep [architecture.md](architecture.md) focused on durable technical decisions.
- Keep the [README](../README.md) to a concise status summary and roadmap link.
- Use future GitHub issues and milestones for executable tasks, and link them back to this roadmap.
- Use `CHANGELOG.md` for released changes only, not planned work or in-progress status.
- Do not add speculative dates or completion percentages.

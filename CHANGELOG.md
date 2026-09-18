# Changelog

All notable changes to Semaphore EX are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions use the
`vX.Y.Z-ex.N` scheme: `X.Y.Z` is the upstream Semaphore UI line the release is
based on, `N` counts fork releases on that line. Release candidates append `-rcM`
and share the section of their final tag.

`task release:notes` copies the section of the tag being released into the GitHub
release, so every release needs its section here before the tag is pushed.

## [Unreleased]

## [v2.20.0-ex.2] - 2026-09-18

### Added

- Task-scoped SSH agents offer the template Repository key alongside the
  Inventory SSH key. Requirements installation and nested Git commands can use
  the Repository identity for private collections, modules and dependencies.
- Additional SSH keys can be selected through project defaults, always-included
  keys, template overrides and authorized per-run overrides. Migration `2.20.71`
  persists these bindings; execution snapshots and project backups retain them.
- Automatic SSH host selection works on local, remote and container runners.
  Host lists are optional below five distinct public-key identities and required
  from five onward; explicit host lists apply at every size.
- Encrypted Terraform HTTP state with workspace aliases, credential bindings,
  append-only versions and durable locks. Migration `2.20.70` adds the backend
  tables; legacy plaintext state requires the documented vault rekey procedure.
- Provider-facing API contracts for environment storage bindings, Terragrunt
  inventories, stable API-token references and permission catalogs.

### Fixed

- Terraform backend aliases are resolved only when backend override is enabled;
  tasks using an external backend retain their configuration.
- API integration fixtures exercise Terraform state and alias operations on
  MySQL, MariaDB, PostgreSQL and SQLite, including persisted results and
  credential rebinding.

### Changed

- Upstream reference generation, documentation navigation, task logging and
  frontend dependency updates are merged while preserving the EX product
  contracts.
- Transitive build and test dependencies of the web frontend are pinned to patched versions
  through an `overrides` block; `npm audit` now reports only the documented inherited Vue 2
  findings. These overrides affect build and test tooling.
- `THIRD-PARTY-LICENSES.md` reflects the current dependency versions.

### Removed

- The unused `qrterminal` and `rsc.io/qr` Go modules.

## [v2.20.0-ex.1] - 2026-09-13

First published release of the fork.

### Upstream base

- Built on upstream `semaphoreui/semaphore` `develop` as of 2026-09-11
  (commit `9b90ea43`, between `v2.20.0-alpha2` and the upcoming `v2.20.0`),
  merged in commit `41965a6f`. Upstream migrations `2.20.4` and `2.20.5` ship as
  local migrations `2.20.68` and `2.20.69`; see `maintenance/migrations.yml`.
- Go 1.26.8 toolchain and container base images; Node 24 frontend build.

### Added

- One full-featured edition without edition selection, licensing or subscription
  quotas. `/api/info` reports `"edition": "enhanced"` together with the core and
  enhanced revisions the binary was built from.
- Runners and placement: project runner registration, lifecycle, health and
  history, reconciliation, tag-based placement, executor images, secure mode.
- Task diagnostics and secrets: task summaries, structured file logs, audit
  webhook export, debug log filtering, Vault and OpenBao runtime secrets, managed
  secret storage, TOTP and LDAP capability lifecycles.
- Workflows: graphical editor with validation, linear and conditional parallel
  runs, artifacts, parameters and overrides, triggers, approvals, reconciliation,
  versions and cross-project references, workflow RBAC and role-based approvals.
- High availability: cluster dashboard, cross-node coordination, task recovery,
  workflow progression and a resilience verification gate that runs in CI.
- Roles and identity: custom project roles, global and template roles, LDAP and
  OIDC group mapping.
- Container executors: Docker and Kubernetes executors with hardening policies.
- Governance and delivery: notification governance, PagerDuty, Opsgenie and
  ServiceNow delivery, signed webhooks.
- Credentials: global credential grants, dispatch-time resolution with audit,
  server-generated SSH keys, template search, per-schedule time zones.
- Policy controls: execution preflight, deployment windows, policy guardrails,
  artifact retention and provenance.

### Packaging

- Container images `ghcr.io/freefair/semaphore-ex` (server) and
  `ghcr.io/freefair/semaphore-ex-runner` for `linux/amd64` and `linux/arm64`,
  published with SBOM and provenance attestations.
- Archives, `deb` and `rpm` packages named `semaphore-ex`; the package conflicts
  with the upstream `semaphore` package because both install `/usr/bin/semaphore`.
  Checksums are signed with the release key in
  `deployment/packaging/semaphore-ex-release.asc`.

### Known inherited risks

- The web frontend uses Vue 2 and Vuetify 2, which are end of life upstream and
  carry open advisories (see `SECURITY.md`). A migration is an upstream project
  and not part of this release.

### Upgrade notes

- Upgrading from upstream Semaphore UI is supported along the shared migration
  history up to upstream `2.20.5`; the fork appends its own migrations after it.
  Take a database backup first. Downgrading back to upstream is not supported.

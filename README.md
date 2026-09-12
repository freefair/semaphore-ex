# Semaphore EX

Semaphore EX is a full-featured fork of [Semaphore UI](https://github.com/semaphoreui/semaphore),
the modern web interface for Ansible, Terraform, OpenTofu, Terragrunt, PowerShell and other
DevOps tools. It ships one edition that contains everything implemented in this repository:
no edition selection, no license activation, no subscription quotas. Features that are
optional or need external infrastructure have ordinary configuration switches instead.

[![Full Product Build](https://github.com/freefair/semaphore-ex/actions/workflows/product_build.yml/badge.svg)](https://github.com/freefair/semaphore-ex/actions/workflows/product_build.yml)
[![Dev](https://github.com/freefair/semaphore-ex/actions/workflows/dev.yml/badge.svg)](https://github.com/freefair/semaphore-ex/actions/workflows/dev.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Quick Start

Run the server with SQLite and log in as `admin` / `changeme` at <http://localhost:3000>:

```bash
docker run -d --name semaphore-ex -p 3000:3000 \
  -e SEMAPHORE_DB_DIALECT=sqlite \
  -e SEMAPHORE_ADMIN=admin \
  -e SEMAPHORE_ADMIN_PASSWORD=changeme \
  -e SEMAPHORE_ADMIN_NAME=Admin \
  -e SEMAPHORE_ADMIN_EMAIL=admin@localhost \
  -v semaphore-ex:/var/lib/semaphore \
  ghcr.io/freefair/semaphore-ex:latest
```

Verify the instance reports the full edition:

```bash
curl -s -c cookie -H 'Content-Type: application/json' \
  -d '{"auth":"admin","password":"changeme","method":"password"}' \
  http://localhost:3000/api/auth/login
curl -s -b cookie http://localhost:3000/api/info | jq '{edition, core_revision, enhanced_revision}'
```

Other installation paths:

- **Docker Compose**: snippets for server, runner and databases live in
  [`deployment/compose`](deployment/compose/README.md).
- **Remote runner image**: `ghcr.io/freefair/semaphore-ex-runner`, configured as described in the
  [runner guide](https://freefair.github.io/semaphore-docs/admin-guide/runners/).
- **Debian/RPM package or binary**: download from
  [GitHub Releases](https://github.com/freefair/semaphore-ex/releases). The package is named
  `semaphore-ex`, installs `/usr/bin/semaphore` and conflicts with the upstream `semaphore`
  package. A sample systemd unit and environment file live in [`deployment/systemd`](deployment/systemd/README.md). Verify the checksums before installing:

  ```bash
  gpg --import deployment/packaging/semaphore-ex-release.asc
  gpg --verify semaphore-ex_<version>_checksums.txt.sig semaphore-ex_<version>_checksums.txt
  sha256sum --check --ignore-missing semaphore-ex_<version>_checksums.txt
  ```

Images are published for `linux/amd64` and `linux/arm64` with SBOM and provenance attestations.
Every release tag is `vX.Y.Z-ex.N`, where `X.Y.Z` is the upstream line the release is based on.

## What the fork adds

All of upstream Semaphore UI, plus the features below. Each feature group has a specification
under [`docs/docs/developer-guide/plans/pro-slices`](docs/docs/developer-guide/plans/pro-slices/README.md).

| Area | Features |
|---|---|
| Runners and placement | Project runner registration, lifecycle, health and history, reconciliation, tag placement, executor images, secure mode |
| Task diagnostics and secrets | Task summaries, structured file logs, audit webhook export, debug log filtering, Vault and OpenBao runtime secrets, managed secret storage, TOTP and LDAP lifecycles |
| Workflows | Graphical editor with validation, linear and conditional parallel runs, artifacts, parameters and overrides, triggers, approvals, reconciliation, versions, cross-project references, workflow RBAC |
| High availability | Cluster dashboard, cross-node coordination, task recovery, workflow progression, resilience gate in CI |
| Roles and identity | Custom project roles, global and template roles, LDAP and OIDC group mapping |
| Container executors | Docker and Kubernetes executors with hardening policies |
| Governance and delivery | Notification governance, PagerDuty, Opsgenie and ServiceNow delivery, signed webhooks |
| Credentials | Global credential grants, dispatch-time resolution and audit, server-generated SSH keys, template search, per-schedule time zones |
| Policy controls | Execution preflight, deployment windows, policy guardrails, artifact retention and provenance |

Backend authorization, role permissions, safety policy and configured enablement stay enforced;
they are security controls, not edition gates.

## Documentation

- [User and administration guide](https://freefair.github.io/semaphore-docs/) (fork of the upstream docs,
  all eleven locales)
- [Changelog](CHANGELOG.md) and [release procedure](maintenance/RELEASING.md)
- [Upstream maintenance policy](maintenance/README.md): how upstream is merged, how migrations and
  exported contracts are protected, and how changes are verified
- API reference: `api-docs.yml` plus the fork additions in `api-docs-ex.yml`

## Relationship to upstream

`develop` is the long-lived fork branch. Upstream `semaphoreui/semaphore` is merged regularly;
published history is never rewritten. Fork code lives in the Enhanced module
(`test/edition-contract/enhanced`), in same-package `*_ex.go` files and in focused UI components,
so upstream files change as little as possible. Bugs that reproduce on the upstream Community
release belong in the [upstream tracker](https://github.com/semaphoreui/semaphore/issues);
everything else goes to [this repository's issues](https://github.com/freefair/semaphore-ex/issues).

## Development

```bash
git clone --recursive git@github.com:freefair/semaphore-ex.git && cd semaphore-ex
task deps          # Go workspace vendoring, frontend packages, goreleaser
task build         # embedded frontend + server binary in bin/semaphore
task test          # Go test suites
(cd web && npm run test:unit)
```

Toolchain versions are pinned in `.github/workflows/product_build.yml` and the Dockerfiles.
`tools/upstream-sync/verify.sh` runs the complete release gate set locally.

## Security

See [SECURITY.md](SECURITY.md) for supported versions, how to report a vulnerability and the
known inherited risks.

## License

MIT. Upstream Semaphore UI is © [Denis Gukov](https://github.com/fiftin); the fork additions are
© freefair. Third-party notices are listed in [THIRD-PARTY-LICENSES.md](THIRD-PARTY-LICENSES.md).

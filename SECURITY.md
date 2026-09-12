# Security Policy

## Supported versions

Semaphore EX is released as `vX.Y.Z-ex.N` tags. Security fixes land on the most recent
release line only; older fork releases and release candidates are not patched.

| Version | Supported |
|---|---|
| latest `vX.Y.Z-ex.N` release | yes |
| earlier fork releases, `-rc` tags, `develop` snapshots | no |

## Reporting a vulnerability

Report vulnerabilities privately, not in public issues:

- GitHub private vulnerability reporting:
  <https://github.com/freefair/semaphore-ex/security/advisories/new>
- E-mail: <security@freefair.io>

Include a description, steps to reproduce, affected version (`semaphore version` or
`/api/info`) and any logs or payloads. We confirm receipt within 7 days and aim to
provide an assessment within 30 days. Please do not disclose the issue publicly until a
fix is released.

## Scope

- The Semaphore EX server and runner binaries, container images and packages published
  from this repository.
- The Enhanced module under `test/edition-contract/enhanced` and the fork-owned code in
  this repository.

Issues in code shared with upstream Semaphore UI are coordinated with the upstream
maintainers; we may forward a report to <security@semaphoreui.com> and ship the fix with
the next fork release. Issues that reproduce only on upstream Community releases should be
reported upstream directly.

## Known inherited risks

The web frontend is built on Vue 2 and Vuetify 2, both end of life upstream. Their open
advisories have no fix inside the 2.x lines; the fix is a migration to Vue 3 and Vuetify 3,
which is an upstream project and not part of the fork. The fork ships these dependencies
knowingly and re-evaluates them at every upstream sync.

| Advisory | Package | Severity | Notes |
|---|---|---|---|
| [GHSA-3jp5-5f8r-q2wg](https://github.com/advisories/GHSA-3jp5-5f8r-q2wg) | vuetify 2.x | high | Prototype pollution; no 2.x fix |
| [GHSA-9w3x-85mw-4fwm](https://github.com/advisories/GHSA-9w3x-85mw-4fwm) | vuetify 2.x | medium | XSS in `VDatePicker`; no 2.x fix |
| [GHSA-5j4c-8p2g-v4jx](https://github.com/advisories/GHSA-5j4c-8p2g-v4jx) | vue 2.x | low | ReDoS in `parseHTML`; no 2.x fix |

All other production dependency advisories known at release time are fixed in the
shipped lockfile. Development-only advisories (build tooling, test runner) do not reach the
shipped artifacts and are tracked through Dependabot.

## Verification at release time

Every release runs the full product gate (Go and frontend tests, reproducible double
build, browser and container smoke tests, HA resilience gate), `govulncheck` on the pinned
Go toolchain, and `npm audit --omit=dev`. Release checksums are signed with the key in
`deployment/packaging/semaphore-ex-release.asc`; container images carry SBOM and
provenance attestations.

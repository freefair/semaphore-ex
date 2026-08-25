# Task: Implement selected enhanced edition slices

**Started:** 2026-08-25
**Last update:** 2026-08-25 20:26

## Scope
Implement the ordered backlog in docs/docs/developer-guide/plans/pro-slices with tests, documentation, review, and atomic commits

## Progress

- 2026-08-25 13:25 — Slice 002 verification: Community artifacts build byte-identically; Chromium renders a functional login page with the community edition marker; manifest, SPDX SBOM, provenance, YAML, and changed workflows validate. Enhanced build selects the clean-room module and reports enhanced metadata, but repeated Vue builds differ because production sourceMappingURL values participate in their own content hashes. Asked Dennis via ntfy whether to disable production source maps or publish them separately.

- 2026-08-25 13:29 — Community server image startup smoke passed: authenticated /api/info returned edition=community, contract_version=1.0.0, implementation_version=community-1, the exact core revision, no enhanced revision, and the all-false Community feature object. OCI labels matched the same identity; the temporary container was stopped.

- 2026-08-25 14:10 — Slice 002 current verification: Core and Community module Go suites pass; docs build passes; Community and clean-room Enhanced artifacts are byte-identical across two builds; Community server and runner containers build and smoke; authenticated API metadata and both browser edition smokes pass; Source Maps are separate and the SPDX SBOM contains exact Production npm packages.

- 2026-08-25 14:14 — Slice 002 verification completed: current root and pro Go suites pass; go vet passes in both modules; focused edition unit tests pass; Community and enhanced artifact directories are byte-identical across repeated builds; production bundles contain no source maps or references; Playwright browser smokes, Community server/runner containers, authenticated /api/info checks, docs build, and focused workflow validation pass. Slice checklist is complete.

- 2026-08-25 14:59 — The external repository access blocker is resolved by scope clarification: no external module access is required or expected.

- 2026-08-25 20:08 — Slice 003 backend first green: isolated /tmp contracts and repository tests pass for immutable per-request snapshots, facade DTO mapping, API denial mapping, dedicated SQL persistence, Community rejection, enhanced lifecycle transitions, background action guarding, data retention, and concurrent resolution. Explicit disabled state now takes precedence over an expired timestamp.

- 2026-08-25 20:18 — Slice 003 verification: Community and clean-room Enhanced full Go suites pass; focused capability packages pass with the race detector; go vet passes; production frontend build and Chromium capability dialog test pass with visual screenshot review; changed frontend files lint clean; docs build passes. Known baseline failures remain unchanged: three frontend unit tests, six global lint errors, and three unrelated documentation anchors.

## Decisions

- **Pin the Docker build toolchain to Go 1.26.5 and Node.js 24.19.0 images by multi-architecture digest.** (2026-08-25): The isolated /tmp Dockerfile proved the Node-based builder can import the pinned Go toolchain. This removes the mutable Alpine Node package from the build and preserves multi-architecture builds.

- **Keep hidden source maps in a separate debug-source-maps artifact and exclude them from served and embedded production assets** (2026-08-25): The user selected option 1B; hidden-source-map removes browser references, while physical separation avoids shipping source content and stabilizes product hashes

- **Generate npm SBOM packages deterministically from the committed package-lock production tree** (2026-08-25): The user selected option 2A; package-lock records exact package versions and its dev marker identifies strictly development-only packages

- **Implement Slice 003 from the public contract and in-repository clean-room fixture only; do not depend on or attempt to inspect the official private module.** (2026-08-25): Dennis confirmed the official pro/enterprise module is unavailable and out of scope; the approved D02 seam can be validated through pro_interfaces, the Community stub, and the checked-in clean-room Enhanced fixture.

- **Use a dedicated capability_test_records table for the Slice 003 lifecycle proof.** (2026-08-25): Dennis selected option B to avoid the risk that a generic OptionsManager namespace becomes accidental long-term domain persistence; the dedicated schema makes lifecycle data ownership and future evolution explicit.

- **Bump the enhanced-module contract to 1.1.0 and resolve worker capability once at execution start.** (2026-08-25): The Slice 003 seam expansion is additive but requires new constructors, so a minor-version signal is accurate. One execution-start snapshot preserves per-operation consistency; queued work resolves later and observes disablement while in-flight work avoids partial policy changes.

## Open

- [ ] GoReleaser v2.11.2 parses the updated metadata fields, but  exits 2 because the pre-existing snapshot.name_template and archives.format_overrides.format properties are deprecated. These are unrelated side defects and remain unchanged.

- [ ] Correction to the preceding entry: the command is GoReleaser check. The shell consumed its formatted command name while recording the note; no project command was run.

- [ ] Decision required: choose whether production source maps are disabled or emitted as separate debug artifacts. Also choose whether the edition SPDX is expanded deterministically from production npm lock dependencies or generated by a new pinned external artifact scanner.

- [ ] Full frontend test baseline has three existing failures in ArgsPicker, YesNoDialog, and Socket. Reproduced unchanged at commit 533b5a06 in a clean Node 24.19.0 container, so Slice 002 did not introduce them.

- [ ] Full actionlint has two existing constant if false findings in community_beta.yml lines 120 and 136. All other changed workflows pass actionlint 1.7.12.

- [ ] Slice 003 is blocked on required Enhanced module access: pro_impl is absent, no local semaphorepro-module clone exists, GH_TOKEN/GITHUB_TOKEN are absent, and gh auth status reports the active Frisch12 keyring token invalid (GitHub API 401).

- [ ] Slice 003 persistence decision pending: A uses the existing SQL-backed OptionsManager under an internal capability-test namespace (recommended, no pre-Slice-004 migration); B introduces a dedicated capability_test_records table and multi-dialect migration.

- [ ] Contract version decision pending: A bump CoreContractVersion from 1.0.0 to 1.1.0 for the additive Slice 003 seam expansion, or B keep 1.0.0 only if Slices 001-005 are one unreleased baseline. No version change or Slice 003 commit until Dennis decides.

## Next session
Slice 001 implemented: versioned module contract, Community compile assertions and black-box contracts, workspace replacement fixture, full Community/enhanced builds, docs and test suites green. Proceed with Slice 002 dual-build verification.
Wait for the source-map decision, implement it, rerun dual Community/enhanced reproducibility, finish container startup smoke, update Slice 002 checklist, build docs, review, and commit.
After Dennis chooses both options, implement them and rerun byte-equivalence, both browser smokes, final Community server/runner container builds, all tests, docs build, review, checklist, and commits.
Commit Slice 002 atomically (nested docs first, then root), then begin Slice 003 capability lifecycle with a failing /tmp contract.
After GitHub authentication is restored, query the private repository default branch/revision, clone it as pro_impl without exposing credentials, inspect its current capability implementation, and write the failing /tmp Slice 003 contract before editing either module.

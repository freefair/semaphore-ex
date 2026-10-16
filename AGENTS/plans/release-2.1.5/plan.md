# Semaphore EX v2.20.0-ex.2.1.5 release

## Scope

Release the verified develop product with all changes since v2.20.0-ex.2.1.4,
including task-dialog usability and isolated HA fault recovery verification.
The upstream base remains the 2.20.0 release line.

## Approach

1. Prepare the versioned CHANGELOG section from the actual commit range and
   refresh third-party notices for the selected full-product dependency graph.
2. Run the release runbook's dependency checks and validate release-note
   extraction. Commit release metadata and push develop normally.
3. Require successful Dev and Full Product Build runs for that exact commit.
4. Create the next unused annotated fork tag and use the existing release
   workflow to build signed packages and multi-architecture server/runner images.
5. Verify signatures, asset coverage, version metadata, image digests and the
   workflow's latest promotion before publishing the verified GitHub release.
6. Remove task-owned local verification fixtures and archive the task journal.

## Decisions

Continue the established numeric patch sequence with 2.1.5. No upstream merge or
product feature changes are required for this release. A Beta dry run adds a
second packaging cycle without testing a changed release mechanism, so use the
ordinary final-release workflow and its exact-commit CI gate.

The license collector's default GOWORK=off selects compatibility scaffolding;
include the selected Enhanced workspace graph when checking shipped dependencies.
Keep dependency versions unchanged unless a verified release blocker requires
an explicitly scoped repair.

The required vulnerability scan found reachable advisories in the pinned Go
toolchain and Go modules. Upgrade only the affected versions to their verified
patched releases, align active package/image/HA build pins, and repeat the scans
and affected checks before release CI. This prerequisite preserves application
behavior and avoids shipping known reachable vulnerabilities. Historical release
metadata keeps its original versions.

Release/build outcomes and active next steps belong in the task journal rather
than this plan.

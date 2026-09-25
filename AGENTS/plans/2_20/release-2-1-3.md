# Release v2.20.0-ex.2.1.3

## Scope and authorization

Publish the current develop branch and build a new Semaphore EX release, as
requested by Dennis. The source contains inventory host browsing (61399073),
the verified upstream host-mapping merge (39c16390), and the task-dialog fixes
since v2.20.0-ex.2.1.2. Preserve unrelated untracked work and published history.

## Decision

Continue the existing release sequence with v2.20.0-ex.2.1.3. Use the existing
tag-driven final release workflow, reusing exact-commit Dev and Full Product
Build results. A manual Beta build would repeat packaging without replacing
those gates and is unnecessary because release tooling is unchanged here.

## Execution

1. Verify origin, unpublished commits, retained merge verification and the
   latest published version. Add and validate the CHANGELOG release section.
2. Run the prescribed vulnerability and production dependency checks; compare
   third-party license coverage to the released dependency set.
3. Commit release metadata and push develop normally after rechecking origin.
   Publish the already-reviewed product Wiki changes independently.
4. Wait for Dev and Full Product Build on the exact pushed SHA. Diagnose and
   fix in-scope failures, then require fresh green evidence before tagging.
5. Push the new immutable annotated EX tag. Wait for signed packages, server
   and runner images, and promotion of verified image digests to latest.
6. Verify draft release metadata, assets and checksum signature; publish the
   release after the required workflow succeeds. Retain evidence, update the
   task journal and clean only artifacts created for this release.

## Existing limitation

The separate external Claude source review was previously rejected by automatic
approval review. The user's publication request authorizes the product push;
it does not override that source-transmission rejection. Do not retry it without
the requested payload-specific permission.

## CI repair

The first candidate's Dev integration jobs fail while Dredd parses the required
host-history query parameters because the specification has no example values.
Add `x-example` values to both API fragments. Give all three inventory-host read
transactions independent fixtures, including a completed membership snapshot
and recorded host execution, and verify their returned identities. This retains
HTTP coverage instead of skipping the new endpoints or weakening validation.

Validate schema rejection before the change, successful transaction compilation
after it, and the complete SQLite Dredd suite with the CI-pinned Node 24.19.0 and
Go 1.26.8 toolchains. The next pushed commit requires fresh exact-head CI.

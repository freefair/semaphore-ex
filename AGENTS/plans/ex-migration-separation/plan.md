# Independent upstream and EX migrations

## Scope

Separate all EX migrations from upstream for fresh databases. Existing database
adoption and old mixed-history compatibility are explicitly excluded by the user. No live
instance changes or release publication are part of this work.

## Decision

Use separate registries, SQL directories, and history tables. Keep the upstream
registry and filenames on upstream's canonical identities. Give EX its own
sequence with upstream-anchored `X.Y.Z-exA.B.C` identities. Interleave each EX
step after its exact upstream anchor and before the next upstream step. Undo
the resulting dependency order in reverse. Reject missing anchors before executing. Release versions are independent from both
schema sequences.

A shared numeric tail would continue to mix identities. Adding release suffixes
to the shared registry would still couple ownership and ordering. Separate
histories make equal version numbers independent by construction.

## Implementation order

1. Add namespace-aware migration descriptors and a separate EX registry.
2. Move fork SQL to the EX directory without mixing it with canonical upstream
   SQL; restore upstream migrations currently mapped to local IDs 66–69.
3. Route SQL lookup and applied-history operations by namespace, and implement
   explicit apply/rollback target semantics and combined HA schema identity.
4. Add the credential Boolean correction as an EX migration.
5. Update maintenance inventories and checks to enforce the separation, including
   future upstream identity collisions, plus migration/SQL tests and CLI docs.
6. Verify fresh installs, repeated startup, explicit targets, rollback/reapply,
   independent identical identities, Boolean behavior, and PostgreSQL/SQLite;
   run available project gates and security review.

## Verification

Use disposable local databases only. Preserve enabled/disabled credential
semantics and test invalid Boolean conversion values. Existing unrelated
task-group/cancellation work remains in place and is excluded from this commit
unless a migration integration change is required by the separation.

## Delivered behavior

Upstream and EX use independent registries, embedded SQL directories, and physical
history tables (`migrations` and `ex_migrations`). The shared execution plan
interleaves EX migrations at their upstream anchors and reverses that order for
rollback. PostgreSQL and MySQL/MariaDB serialize this plan across HA nodes.
The credential enablement correction is `2.20.5-ex1.4`.

The independent task-group work retains the pending `2.20.5-ex1.3` migration in
the working tree; that migration and its implementation are excluded from this
change. Existing database adoption remains a separate decision.

The isolated commit tree passes the complete `db`, `db/sql`, and
`tools/upstreamcheck` suites, Enhanced SQL and HA suites, maintenance quick gates,
and `go vet` for the changed core packages. Fresh-database migration matrices and
cross-connection migration-lock tests pass on PostgreSQL 17.11, MySQL 8.4.11,
and MariaDB 10.11.19; SQLite runs in the SQL suite. Retained verification evidence
is under `/tmp/semaphore-credential-boolean/`.

The architecture decision and upstream maintenance instructions are maintained in
the [Wiki ADR](https://github.com/freefair/semaphore-ex/wiki/ADR-Independent-Migration-Histories)
and [maintenance guide](https://github.com/freefair/semaphore-ex/wiki/Upstream-Maintenance).

# Upstream synchronization 2026-10-19

## Scope

Merge upstream 58a596ed20b724af67c468588edcda12d1779549 into product develop at
3c8706bd941c180a6f09603d15212454c0cb6ca3, preserving published history, EX
contracts and unrelated fork-history-rebuild work. Publish product and related
Wiki changes through ordinary pushes after verification. No releases/deployments.

## Approach

1. Retain fetched assessment and recovery ref. Preserve unrelated files by checksums.
2. Resolve group role mapping changes against existing immutable EX role identities,
   directory ownership, permission unions/denies and last-admin protection.
3. Integrate workflow surveys with existing typed parameters, immutable versions,
   run snapshots and cross-project permission/secret boundaries.
4. Integrate runner connection deadlines while preserving HA and task lifecycle.
5. Preserve shipped migration bytes. Assess additive anchored EX compatibility for
   upstream's later edits to 2.20.13; register canonical 2.20.14 with EX role identity
   compatibility. Update reviewed seam/migration inventories, never blindly replace.
6. Review relevant Wiki contracts and document decisions in a new ADR and guides.
7. Run focused regressions and database matrix, Claude consultation and dedicated
   Terra review, desktop/mobile browser QA, then the full retained verification.
8. Compare fresh remote tips, commit/push normally, verify exact-head Dev and Full
   Product Build CI, repair in-scope failures, clean task-owned QA and record results.

## Alternatives

Replacing EX implementations with upstream null seams would lose shipped behavior;
retaining EX without implementing new seams would silently ignore upstream behavior.
Select focused compatibility adapters and explicit observable regressions instead.
Keep Wiki as the sole maintained English documentation source; discard only the
incoming docs gitlink from the merge result, not any existing user directory.

## Toolchains and evidence

Use official SHA256-verified Node 24.19.0/npm 11.17.0 and GOTOOLCHAIN=go1.26.9.
Evidence root: /tmp/semaphore-sync-20261019. Dedicated Terra handles security-related
integration/reviews as required by repository policy; primary integrates and verifies.

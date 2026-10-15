# Monorepo upstream integration

## Goal

Merge upstream `0f07f813e516f9352a1b2c829c26c21fca19f829` into the existing
`develop` lineage at `fa17385e28afdd2baba9f56c3d1ca149fea33573`, preserving
EX execution, authorization, migration and documentation contracts. Publish the
verified product and related Wiki updates using ordinary pushes.

## Approach and alternatives

Use the repository's retained preflight, regular merge, full gates and exact-head
CI. A rebase would rewrite published history and is outside this request.
Preserve the shipped upstream 2.20.11 SQL and add its later monorepo column through
an anchored EX migration, following ADR 0037's existing changed-migration policy.
Rewriting the shipped migration would invalidate ledger checksums and existing
schema history. Keep the Wiki as the documentation source instead of restoring
the upstream documentation submodule.

## Implementation order

1. Retain exact refs, source hashes, upstream changes and conflict preview. Preserve
   unrelated fork-history-rebuild plans and the unfinished LDAP task journal.
2. Create a local recovery branch. Merge the recorded upstream SHA. Resolve API
   repository fields, template SQL validation and TaskForm state/loading conflicts
   while retaining EX permission, preflight and transactional behavior.
3. Add independent monorepo schema migration and focused behavior regressions;
   review shared-checkout branch semantics in local, remote and container paths.
   Update exported contracts only after implementation and regression review.
4. Document compatibility decisions in a Wiki ADR and repository/template guides.
5. Run focused tests, full retained gates, desktop/mobile browser verification,
   Claude review and dedicated Terra security review. Integrate in-scope fixes.
6. Commit verified changes, fetch and compare origin, push normally, read back
   remote heads and require Dev and Full Product Build success for the exact head.
7. Clean task-owned disposable runtime resources; retain evidence and recovery
   branch. Record completion in automation memory.

## Evidence

`/tmp/semaphore-full-sync-20261014/` retains the assessment, reviews, gates,
browser evidence and publication checks. A command-scoped Git exclusion avoids
staging 119 unrelated untracked plan files; their checksums are compared at the end.

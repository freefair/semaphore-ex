# Resolve the reported sync follow-ups

## Authorized scope

The user requested fixes for the follow-ups named in the completed sync report.
Start from published develop `91384008a94ff1bcb3d085b7242b7c3a4d678f37`.

| Backlog item | Required outcome |
| --- | --- |
| c1022563 | Template-scoped readers/runners can use the UI without project-wide resource grants; preserve backend authorization. |
| cddb7248 | Inventory API schemas describe all implemented workspace inventory types. |
| f1597645 | Non-file inventories do not cause unused repository credentials to be resolved or forwarded, including historical snapshots. |
| a4519ff3 | Reproduce shallow go-git update behavior and fix unnecessary re-cloning if confirmed; otherwise retain evidence of correct behavior. |
| c6e1a2da | A local filesystem repository cannot retain an ineffective shared-checkout flag. |
| ab4465d3 | Intentional preparation cancellation produces accurate lifecycle/log output. |
| e6436ce0 | Verify mixed-version runner handling of monorepo policy and enforce a compatible boundary if older runners can ignore it. |
| c779a0d6 | Inspect current dependency alerts, verify affected dependencies and apply compatible fixes with focused validation. |

## Plan and alternatives

1. Reproduce each finding and trace current contracts, callers and existing tests.
   Security investigation and implementation use dedicated Terra agents under the
   repository policy; independent review precedes final verification.
2. Prefer narrow corrections at existing authorization, serialization and lifecycle
   boundaries. Broad role grants would expose unrelated resources; indiscriminate
   dependency upgrades and blanket data rewrites would change scope unnecessarily.
3. Write reproducing tests in temporary storage before executable changes, then
   retain appropriate regressions in the owning packages. Exercise valid controls
   and alternate paths. Treat unconfirmed hypotheses as investigations, not assumed bugs.
4. Update API contracts and English Wiki documentation/ADRs. Keep changes reviewable
   by finding or tightly coupled boundary.
5. Run focused checks, complete product gates, desktop/mobile browser QA and Claude
   review. Commit completed changes, publish by ordinary pushes, and verify exact-head CI.
6. Close only backlog entries whose stated outcome is verified; record any concrete
   unresolved blocker. Clean up task-owned resources and preserve recovery refs.

## Working state

Preserve the 119 unrelated files under `AGENTS/plans/fork-history-rebuild/` and the
unfinished LDAP/backend-diagnostics journal. This follow-up has a separate scoped
CLI journal under this plan directory; it stays ignored by Git. General execution
evidence is retained under `/tmp/semaphore-followups-20261015/`. Security-plugin
artifacts use the plugin-managed storage returned by its tools.

Existing authority covers local disposable QA, Claude consultation, verified commits
and ordinary publication. Runtime deployments, history rewrites and unrelated work
remain outside this task.

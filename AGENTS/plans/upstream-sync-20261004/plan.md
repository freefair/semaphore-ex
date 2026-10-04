# Upstream sync 2026-10-04

## Objective and authorization

Merge recorded upstream 7102353a6fb75a5e19dbbeda3f4eb1c929deab11 into develop at 2a4f0213be8855a06975a8e0bba7c7b2fbcb3dff, preserve full-product contracts, verify and publish normally. User explicitly authorized full sync, push and Claude consultation.

## Approach

Use a normal merge with a local recovery branch. Rebase or reconstruction would rewrite published history and is outside scope. Fresh public HTTPS fetch substitutes for failed SSH-agent authentication without changing remotes; retained assessment follows those fetched refs.

1. Preserve unrelated 119-file rebuild plan and existing separate backend/LDAP journal; retain assessment under /tmp/semaphore-full-sync-20261004.
2. Resolve resource/API/storage audit conflicts against EX authorization, atomic persistence and secret lifecycle contracts. Dedicated Terra handles security-relevant integration and review.
3. Resolve task/runner audit conflicts against SQL-authoritative HA lifecycle and redaction. Integrate workflow identity variables in all active execution paths, preserving immutable run identity.
4. Retain upstream SQL identities and existing EX ledger. Review changed exported DB shapes and methods, updating inventories only after behavioral checks.
5. Keep documentation solely in Wiki; adapt relevant upstream docs there. Retain recovery points and avoid helper/tooling edits.
6. Run focused regressions, full retained verification (Go 1.26.8, Node 24.19.0), final Terra and independent Claude reviews, and any changed-surface browser checks.
7. Freeze verified source, commit merge, fetch and compare recorded origin, push normally, read back remote SHA and inspect exact-head CI. Retain evidence and restore unrelated files with checksum equality.

## Decisions

No new migration is incoming. Keep full-feature product catalog, configured enablement and permissions independent of historical Pro metadata. No deployment, release tag, history rewrite or backend-diagnostics scope expansion.

## Documentation evidence

The incoming upstream docs gitlink `6155c2385ca75a0450b083a68b012c58c6824ade` is not advertised/retrievable from the public documentation repository (`upload-pack: not our ref`). Review uses the available audit documentation at `da772a0` and source/behavioral evidence for workflow variables. This does not restore the retired docs submodule. Compatibility decisions are documented in Wiki ADR 0033.

## Compatibility resolution

Resource and task audit records follow active EX handlers and durable task creation. Preserve the existing HTTP 200 schedule-update response and its body; the incoming test is adapted to that established contract. Workflow identity goes through shared execution preparation, and optional metadata lookup failures do not mutate a running task. Storage and dependent credential deletion is transactional. Task control events follow persisted changes; started local tasks keep completion emission at their owner to avoid a second event from another node. Completion reasons remain optional and process-local as upstream implements them; no new durable cause schema is introduced.

The independent custom-role task-group permission omission is parked as backlog 71952c28. The validated pre-sync role mask is preserved. No visible UI source changes require new browser verification in this sync.

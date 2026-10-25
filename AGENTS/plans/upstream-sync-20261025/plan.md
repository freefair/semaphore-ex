# Upstream Redis Sentinel compatibility

## Objective
Merge recorded upstream 484c2f2656c61cb91a2317f8c14776eb6a7f63bc into verified product develop, publish normally, and verify exact-commit CI. Preserve unrelated work and recovery refs. No release, deployment, provider changes or history rewrite.

## Approach and alternatives
1. Preserve premerge product head, assess incoming seams, read HA slice contracts.
2. Merge the recorded SHA. Adopt Sentinel configuration and one shared Redis client per node in the selected Enhanced module; preserve SQL-backed task state, schedule/workflow ownership and task fencing; retain bounded Redis audit leases. Keeping independent clients would miss upstream's shared lifecycle contract; replacing SQL authority with Redis would violate EX contracts.
3. Keep retired docs/pro_impl/docsref absent. Do not adopt the incoming commercial-only manual stand; use task-owned synthetic QA and existing product test infrastructure.
4. Cover new factory wiring, Sentinel credentials/TLS, lifecycle ownership and validation with focused regression tests. Record behavioral references in maintenance/contracts.yml only after tests prove behavior.
5. Run actual Redis/Sentinel failover and cross-node diagnostics/event checks, desktop/mobile browser QA where visible diagnostics change. Review final immutable diff with Claude and dedicated Terra.
6. Use Go 1.26.9 and Node 24.19.0 task-locally. Run all retained verification gates. Update Wiki HA/configuration/runbook/status and ADR.
7. Check origin tips, commit and push develop/Wiki master normally, read back SHAs, wait for Dev and Full Product Build on the exact head; repair in-scope failures. Clean task-owned fixtures and archive journal.

## Ownership
Evidence and disposable fixtures: /tmp/semaphore-sync-20261025. The 119 existing AGENTS/plans/fork-history-rebuild files are unrelated and checksum-preserved. Terraform provider remains read-only.

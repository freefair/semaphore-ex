# Private Galaxy dependency compatibility

## Scope
Merge upstream `ffaf288d009dfdd5716e5872dbf28cdd9e7243f8` into the existing `develop` lineage, preserving task-scoped SSH selection, host mappings, credential redaction and container execution. Publish with a normal push after verification. Existing backend diagnostic proposals are publication-only; their implementation stays separate.

## Approach
1. Retain fetched preflight evidence and the pre-merge recovery branch.
2. Merge the exact assessed upstream commit. Combine the SSH test imports; preserve both test suites and quoted host-policy paths.
3. Trace the Galaxy credential environment through local and remote runners and the separate Docker/Kubernetes bundle path. Correct only confirmed integration gaps, with failing reproductions and focused regressions.
4. Obtain dedicated Terra security review and an independent Claude consultation. Review the final diff against both parents.
5. Update the English product Wiki with the reviewed behavior and compatibility decision.
6. Run all retained local gates with the configured CI toolchain. No UI change is expected; browser verification applies if visible behavior changes.
7. Fetch origin immediately before ordinary product and Wiki pushes, verify remote SHA equality, and capture one CI snapshot in accordance with the standing no-wait preference.
8. Restore unrelated planning files with checksum verification; retain recovery points and verification evidence, and clean only task-owned artifacts.

## Options
- Adopt the incoming code without tracing execution paths: smallest textual diff, but container bootstrap and task identity handling are separate EX integrations.
- Preserve the upstream implementation and add narrow compatibility hooks where required: selected, because it retains upstream behavior while covering every supported executor.
- Replace the credential subsystem: unnecessary scope and merge burden; excluded.

## Evidence
Assessment, focused test output and review artifacts are retained under `/tmp/semaphore-full-sync-20261003`. The open LDAP/backend project journal remains untouched; this automation records its continuity separately.

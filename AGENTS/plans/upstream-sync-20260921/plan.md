# Upstream SSH Generation Compatibility Sync

## Scope

Merge application upstream `e95560fd81a6dc510957c5c74aff07f656bb1ccc` into
`develop` at `ef98c4bc2525d47380ae3a6fd17d5b4d91b1f93e`. Documentation upstream
`18e66e93b9f746c5e637e9c617b07941d1e1d5b0` is already contained in docs `main`
at `920bfa5b7f0e2dfcbcb78b61cc3dbef02be4264f`. Publication requires approval.

## Approach and alternatives

Use a regular merge and preserve both parent histories. Replacing the fork's
generated-key implementation wholesale would lose explicit rotation, algorithm,
fingerprint, audit, and response-redaction contracts. Keeping only the fork side
would miss upstream interoperability fixes. Reconcile the overlapping feature
against Slice 062, retaining the existing focused implementation and UI.

## Execution

1. Retain the fetched assessment, migration decisions, seam patches and conflict
   previews; preserve both pre-merge heads on `codex/pre-sync-20260921` branches.
2. Resolve key service/API and UI conflicts, review automatically merged key
   model/storage/encryption changes, and add focused behavioral regressions.
3. Adopt `deps:image` in both Dockerfiles while preserving full-product build
   selection and source metadata. Keep all shipped migrations unchanged.
4. Record compatibility decisions and update the maintenance documentation.
5. Run focused checks, desktop/mobile browser verification, dedicated Terra
   security review, and the complete retained verification runner. Reproduce
   frontend baseline failures at the exact assessed upstream commit.
6. Review both parent diffs, commit verified work, and request publication of
   the concrete result. Publish docs before root, recheck origins, then verify
   required workflows on the exact published commits after approval.

## Evidence and unrelated state

Evidence is retained under `/tmp/semaphore-full-sync-20260921/`.
The unrelated untracked `fork-history-rebuild` directory is temporarily retained
there with explicit user approval and must be restored after sync checks.
Recovery branches and retained verification evidence remain available.

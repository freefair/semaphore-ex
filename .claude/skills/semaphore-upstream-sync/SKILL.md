---
name: semaphore-upstream-sync
description: Merge upstream into the freefair/semaphore-ex full-product fork while preserving Enhanced contracts. Use for sync assessments, merge conflict resolution, normal pushes, and post-update CI verification in this repository.
---

# Semaphore Upstream Sync

Maintain one upstream-compatible, full-featured product through regular upstream merges into the long-lived fork. Preserve published history and shipped migration identities.

## Required Sources

Resolve the repository root with Git; verify the checkout, branches, and remotes before acting.
Read repository `AGENTS.md`, `.claude/CLAUDE.md`, and root `CLAUDE.md`, plus:

- `maintenance/README.md` for the repository-owned assessment, inventories, and gates.
- `docs/docs/developer-guide/plans/pro-slices/upstream-maintenance.md` for the merge procedure and conflict-sensitive contracts.
- `docs/docs/developer-guide/plans/pro-slices/README.md` and `STATUS.md` for the complete selected slice inventory and current state.
- `docs/docs/developer-guide/adr/0010-ship-one-full-featured-product.md` for product invariants.

Repository policy and the maintained runbook are authoritative. For each conflict, read the slice specifications whose paths or dependencies overlap it. Infer behavior from those contracts, not an inaccessible commercial repository.

## Scope and Authorization

Distinguish assessment-only requests from an actual sync. A sync request authorizes preparing merges and scope-preserving compatibility fixes. Obtain publication approval for the concrete verified result unless already authorized in the conversation.

Routine syncs use merges and ordinary fast-forward pushes. Rebases, other history rewrites, force-pushes, and deletion of recovery branches or restored stashes require separate explicit authorization. A failed normal push is a reason to reassess concurrent work, not to force it.

Keep helper/tooling changes in their own explicitly requested scope. Preserve unrelated tracked/untracked work and review unpublished commits before including them.

## Assessment and Merge Workflow

1. Verify root `develop`: `origin` is `freefair/semaphore-ex`, `upstream` is `semaphoreui/semaphore`. Verify docs `main`: `origin` is `freefair/semaphore-docs`, `upstream` is `semaphoreui/semaphore-docs`. Reconcile unexpected branches, ancestry, or local/origin divergence before merging.
2. Use `GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes'` for network Git commands.
3. From the root, run the repository-owned preflight with a new evidence directory:

   ```bash
   bash tools/upstream-sync/preflight.sh --fetch --output /tmp/semaphore-assessment .
   ```

   Omit `--fetch` only when deliberately assessing recorded local refs. Read root/docs exact SHAs, source fingerprints, seam/schema diffs, incoming migration decisions, and merge conflict previews. The preview never chooses a semantic resolution.
4. For an actual update, create a clearly named local `codex/` recovery branch at each pre-merge head. Preserve unrelated working state without staging it. Retain recovery points until cleanup is authorized.
5. If docs upstream has commits not already included, merge its recorded SHA into docs `main` using `git merge --no-ff --no-commit <recorded-docs-upstream-sha>`. Resolve, verify, and commit. Publish docs normally once authorized and read back its exact remote SHA.
6. If root upstream has commits not already included, merge its recorded SHA into `develop` using `git merge --no-ff --no-commit <recorded-root-upstream-sha>`. Point the docs submodule at the verified published docs commit. An already-contained upstream needs no artificial merge; a docs pointer update can be an ordinary commit.
7. Resolve conflicts against the relevant slice contracts and current upstream behavior. Run focused checks for each manual resolution. Review the final result against both pre-merge parents and run the complete retained gates before committing the merge and any separate compatibility changes.

The skill-local `scripts/preflight.sh REPOSITORY_DIR` remains an optional read-only identity/slice diagnostic. It is not the retained assessment gate and does not replace the repository-owned tools. If required tooling is missing, report that gap rather than falling back to a rebase.

## Semantic Conflict Decisions

- **Migration ownership:** Read `maintenance/migrations.yml` and the migration policy. Keep every shipped numeric ID and SQL file unchanged. Append newly mapped upstream migrations at the local tail and record ownership/upstream identity. Review changes to applied upstream SQL as new corrective migrations. The upstream/fork `2.20.2` and `2.20.3` collisions are ownership decisions, not Git conflicts that a history strategy can solve.
- **Exported seams:** Use `maintenance/contracts.yml` and the seam diffs. Implement required behavior in the selected Enhanced module at `test/edition-contract/enhanced`, maintaining the corresponding root null contract where appropriate. Embedded null stores can satisfy an interface while inheriting a placeholder; exercise new/changed methods with observable behavior tests before updating the inventory. Preserve migration and contract baselines rather than regenerating them blindly.
- **Durability and security:** Preserve upstream concurrency/finalization changes, authoritative SQL lifecycle transitions, HA fencing, permissions, credential redaction, and fail-closed configuration handling.
- **Feature boundaries:** Prefer the existing Enhanced module and focused UI components with narrow props/events. Keep shared-file edits small; broad refactoring and unrelated fixes belong in separate work.
- **Product identity:** Preserve ordinary enablement flags, permissions, safety policies, and lifecycle controls. Keep subscription, quota, edition, upgrade, and external commercial-module behavior absent. Keep gitless build contexts independent of `.git` and external module checkouts.

For security-sensitive conflicts and the final tracked diff, use the repository-required dedicated Terra review. The primary agent inspects the evidence and verifies integrated fixes.

## Completion Gates

From the repository root, use a new evidence directory:

```bash
bash tools/upstream-sync/verify.sh --output /tmp/semaphore-gates
```

The full runner builds the embedded frontend first, checks inventories, runs root/Enhanced Go tests and vet, compiles Dredd hooks, runs the complete frontend suite, builds the product, checks both Dockerfiles, and builds/checks documentation. Retain logs, exit codes, exact source fingerprints, and checksums. Quick mode is an intermediate check, not the completion gate.

Reproduce frontend failures on the exact assessed upstream SHA before classifying them as baseline failures; retain failed results and reproduction evidence. Perform desktop/mobile browser verification of visible areas changed by conflict resolution, including permissions and absence of commercial upgrade surfaces.

Keep documentation's serial eleven-locale build as the default. The bounded parallel build is an opt-in with at most two workers. Preserve translated security pages and canonical fallback checks. If changing concurrency is explicitly in scope, use the docs benchmark tool to measure elapsed time and process-tree peak memory under comparable cache conditions, retaining generated-page verification.

## Publication and Completion

Fetch the relevant `origin` immediately before each push and compare its tip with the assessment's recorded SHA. If it advanced, incorporate the new work and repeat affected verification. Preserve existing publication authorization for the agreed scope.

Publish docs before the root submodule pointer. Use ordinary pushes from the verified branches:

```bash
# Inside the docs submodule:
GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes' git push origin main:main
# Inside the application root:
GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes' git push origin develop:develop
```

Read remote SHAs back and require equality with the corresponding local heads. Wait for required workflows on those exact commits, inspect failures, and fix in-scope root causes before reporting completion. Treat Dependabot update failures as separate scope unless they break the selected build.

Keep journals, generated knowledge mirrors, credentials, caches, and build output out of commits. Retain verification evidence and recovery points; perform cleanup only within the user's authorization.

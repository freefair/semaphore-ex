# Audit browser upstream compatibility

## Scope and recorded inputs

Merge upstream `02c46adf57e4439860730d17e697b93133e07a90` into product `develop` at `5709164f4b0628fd1d6d923c9ce71749b2c42604`, preserving published history and full-product behavior. Publication is explicitly authorized. Retained assessment: `/tmp/semaphore-full-sync-20261012/assessment`.

## Approach and alternatives

Keep the upstream audit screen and paging implementation, adding the missing filter/export implementation through the existing Enhanced module. This retains upstream UI compatibility while delivering the new seam without commercial gates. Leaving the null controller selected would disable promised functionality; moving the entire feature into shared code would enlarge future conflicts. Neither is selected.

1. Preserve a local recovery branch. Merge the exact assessed SHA without committing; resolve `api/router.go`, `api/user.go`, and `web/src/App.vue` with existing EX factories/permissions plus the incoming audit route, flag and navigation entry. Keep the retired docs submodule absent.
2. Implement filtered SQL queries and CSV/JSONL export through `test/edition-contract/enhanced`, matching the UI query contract. Keep admin and same-origin checks, strict query validation, bounded/cancellable reads and safe download handling. Adapt the matching null factory and enable the implemented feature. Remove the new screen's commercial notice and gating without changing ordinary permissions.
3. Retain canonical migration `2.20.11` and append its reviewed checksums to `maintenance/migrations.yml`. Update only changed declarations in `maintenance/contracts.yml`, with behavioral test references. Preserve existing baselines.
4. Exercise focused API, SQL, permission, export, migration and frontend contracts. Request independent Claude review and dedicated Terra security work/review. Run the full retained gate runner on stable source; test changed UI at desktop/mobile sizes against disposable local infrastructure.
5. Update the Wiki Audit Log, maintenance/status pages and an ADR covering filter/export compatibility. Check both parent diffs, commit with configured signing, fetch origin immediately before ordinary pushes, and verify exact remote heads and CI.

## Preservation and evidence

Unrelated untracked fork-history-rebuild files and the separate LDAP journal remain outside this task. The automation memory tracks progress. No shared environment, history rewrite, release tag or helper-script change is included. Temporary movement of unrelated plans for strict fingerprint checks is checked with SHA256 manifests and reversed. Keep verification evidence and recovery branches.

## Integration review decision

The published EX `/api/audit/events` endpoint returns legacy `db.Event` rows under delegated global audit permission. The new upstream endpoint would shadow or replace that contract. Preserve the existing endpoint unchanged and mount the new canonical event browser at `/api/audit-log/events` and its `/export` child; update only the new UI requests. This keeps both event schemas and access policies intact. The Wiki documents their separate coverage.

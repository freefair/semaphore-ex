# Inventory and repository upstream integration

## Goal

After completing the pending monorepo merge, integrate recorded upstream
`c170fce81b68e6ffb517a9084d6cf656661ebd01` by a second regular merge and complete
the already authorized full sync and ordinary product/Wiki publication.

## Approach and decisions

Use the existing develop lineage, retained assessment and new recovery ref.
Keep the full-product README and absent docs submodule; adapt relevant English
documentation into the Wiki. Preserve EX task SSH routing, checkout leases,
immutable snapshots, permission checks and secret handling while accepting
upstream browse-only shallow clones, per-task static inventories and selective
Ansible unsafe tags. A blanket upstream-side conflict choice would lose EX
behavior; rebasing would rewrite published history.

## Implementation order

1. Reconcile current origin and retained upstream refs after finishing the prior
   merge. Run a new preflight with reviewed source staged and unrelated work excluded.
2. Merge the recorded upstream SHA. Keep EX integrations in repository browsing,
   command Git cloning, and inventory SSH setup. Preserve the full-product README
   and the deliberate removal of the docs gitlink.
3. Verify static inventory paths in local and container execution. Add a regression
   for a static inventory carrying a legacy repository association; apply static
   path precedence consistently if reproduction confirms the mismatch.
4. Sync the public API bundle and affected Wiki descriptions. Preserve fork role
   and audit contracts when upstream docs describe different implementation details.
5. Run incoming focused tests, including the real installed Ansible loader, then
   the complete retained gate runner. Review the final diff with Claude and Terra.
6. Browser-check inventory type switching, desktop/mobile behavior and affected
   task controls on the final product. Commit verified changes, ordinary pushes,
   remote SHA readback and exact-head Dev/Full Product Build verification.

## Evidence

Retain logs under `/tmp/semaphore-full-sync-20261014/` and the existing Oct 15
assessment directory. Preserve all unrelated fork-history plan files and the
unfinished LDAP journal. The autonomy update is committed separately from product
integration. Routine authorized work continues without another approval round.

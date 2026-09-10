# Repeatable Upstream Maintenance

Implement the requested five maintenance improvements, then adopt regular upstream
merges as repository policy. Keep the existing full-product implementation and
all shipped migration IDs/SQL unchanged.

1. Add `maintenance/migrations.yml` with explicit fork/upstream identities,
   checksums, registration state, and collision mappings. Enforce append-only
   evolution against a reviewed baseline. A ledger preserves deployed history;
   a new runtime version syntax would require a separate migration-engine change.
2. Add a compiler-derived exported contract inventory and implementation-origin
   checks under `tools/upstreamcheck`. Review intentional aliases separately from
   missing behavior. Require behavioral test references for changed callables.
3. Extend the existing preflight as repository-owned tooling under
   `tools/upstream-sync`, with exact refs, isolated merge previews, seam/schema
   diffs, and retained gate evidence. Integrate checks into the existing product
   workflow. Keep semantic merge resolutions and publication explicit.
4. Document future changes through Enhanced implementations and focused UI
   components; retain shared-file structure during syncs.
5. Benchmark all eleven documentation locales serially and with bounded
   concurrency, accounting for warm caches. Retain canonical security fallbacks
   and verify generated output before selecting a default.

After verification, update `CLAUDE.md`, `.claude/CLAUDE.md`, and the upstream
maintenance runbook with the regular-merge policy and executable commands.
The user approved implementation and removal of the previous sync's temporary
branches/stashes. Publishing the new maintenance commits remains a final gate.

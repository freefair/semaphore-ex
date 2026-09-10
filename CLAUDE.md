# Project Working Agreement

## Product Model

This branch ships one full-featured product edition containing every feature implemented by this
branch. It does not ship or support a separate Community edition, edition selection, commercial
subscriptions, license activation, or subscription quotas. The product build always selects the
clean-room implementation; Community stubs may remain only as unwired compatibility scaffolding
where removing them would unnecessarily obstruct upstream merges.

Every implemented feature is included without entitlement checks, but inclusion does not force the
feature to be enabled. Features that are optional, noisy, or require external infrastructure may
have ordinary configuration flags so an operator can disable their behavior and hide their UI.
Edition and subscription gates must not control those flags. Backend authorization, role
permissions, safety policy, configured enablement, and operational lifecycle states remain enforced
independently; they are security and runtime controls rather than product-edition gates.

## Autonomy

During execution of the approved enhanced-edition slice plan, resolve local, reversible implementation and design details with best engineering judgment. Continue without asking for routine preferences. Ask Dennis only when work is blocked by missing authority or information, an irreversible or destructive action, or a choice that materially changes the approved product scope.

The local QA server, database, and test data were created by Codex for this plan. They are Codex-owned disposable infrastructure and may be migrated, rebuilt, reset, seeded, restarted, or stopped without asking Dennis. This authorization does not extend to remote or shared environments.

Keep enhanced-edition work upstream-compatible. Prefer implementing existing interfaces and extension seams; leave Community behavior and shared UI untouched wherever possible. UI changes must be the smallest integration needed for the selected slice and should reuse existing routes, views, and components instead of redesigning shared surfaces.

Keep docs/docs/developer-guide/plans/pro-slices/STATUS.md up to date.

## Security Execution

Run security scans, security-focused investigation, security-relevant implementation, security reviews, and security-fix verification through a dedicated `gpt-5.6-terra` sub-agent when delegation is available. The primary agent integrates the resulting evidence and runs non-security release gates.

## Upstream Maintenance

Use regular upstream merges into the long-lived `develop` branch and the docs
fork's `main` branch. Preserve published fork history. A sync request authorizes
preparing the merge and scope-preserving compatibility fixes; obtain publication
approval before pushing. History rewrites and force-pushes require a separate,
explicit request and are not the routine update procedure.

Follow `docs/docs/developer-guide/plans/pro-slices/upstream-maintenance.md` and
`maintenance/README.md`. Run the repository-owned `tools/upstream-sync/preflight.sh`
with retained evidence before merging. Capture exact refs, review local changes,
and merge the reviewed upstream SHA. Publish verified docs before the root pointer.
Use ordinary pushes and stop to reassess if the recorded remote tip has changed.
This repository policy supersedes older rebase instructions in personal sync skills.

Preserve shipped migration IDs and SQL. Append new local migrations and record
ownership and upstream identity in `maintenance/migrations.yml`. Validate against
the prior reviewed ledger; an upstream change to applied SQL needs a new corrective
migration. Let the incoming assessment expose collisions; resolve their semantics
explicitly rather than accepting a whole side of the merge.

Review every exported seam change with `maintenance/contracts.yml`. Implement new
behavior explicitly in the selected Enhanced module, add behavioral regressions,
and retain references to them. An embedded Community method satisfying an interface
is not proof of implemented behavior. Intentional shared aliases and unwired
scaffolding require a recorded rationale.

For future product changes, prefer `test/edition-contract/enhanced` and focused UI
components behind existing interfaces, props, and events. Shared-file edits must
be the smallest integration needed. Keep refactors and unrelated fixes outside
upstream syncs; document any shared-file change and its contract coverage.

Run `tools/upstream-sync/verify.sh` and retain failed as well as successful results.
Build the embedded frontend before Go verification and keep source stable throughout
the run. Classify baseline failures only with exact-upstream reproduction. Verify
changed UI in the browser and wait for the required workflows on the exact pushed
head. Preserve recovery branches and stashes until their deletion is authorized.

Use the serial docs build by default. Parallel locale builds are opt-in and capped
at two workers; raising that bound requires memory and elapsed-time measurements.
Verify the canonical security fallbacks against generated HTML in either mode.

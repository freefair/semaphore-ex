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

Keep [Wiki documentation](https://github.com/freefair/semaphore-ex/wiki/Implementation-Status) up to date.

## Security Execution

Run security scans, security-focused investigation, security-relevant implementation, security reviews, and security-fix verification through a dedicated `gpt-5.6-terra` sub-agent when delegation is available. The primary agent integrates the resulting evidence and runs non-security release gates.

## Upstream Maintenance

### Independent migration histories

Upstream and EX SQL migrations use separate source directories, registries, and
database history tables. Upstream retains its original migration identities; EX
has an independent sequence with upstream-anchored `X.Y.Z-exA.B.C` identities.
Each EX step runs after its upstream anchor and before the next upstream version;
rollback reverses the same dependency order. Development targets fresh databases. Legacy EX
database adoption, history conversion, and compatibility with the old mixed
history are outside the current implementation scope. Existing deployed
databases are not modified as part of development verification.

Direct communication with Dennis is in German; technical documentation remains in English.

Maintain the project-specific sync skill in
`.claude/skills/semaphore-upstream-sync/`. The Codex discovery entry under
`.agents/skills/` links to that same source. Keep skill instructions, metadata,
and helpers in this repository so policy changes and the skill stay aligned.

Use regular upstream merges into the long-lived `develop` branch.
Maintain English docs in the product Wiki repository on `master`; there is no
separate docs fork to merge. Preserve published fork history. A sync request authorizes
preparing the merge and scope-preserving compatibility fixes; obtain publication
approval before pushing. History rewrites and force-pushes require a separate,
explicit request and are not the routine update procedure.

Follow [Wiki documentation](https://github.com/freefair/semaphore-ex/wiki/Upstream-Maintenance) and
`maintenance/README.md`. Run the repository-owned `tools/upstream-sync/preflight.sh`
with retained evidence before merging. Capture exact refs, review local changes,
and merge the reviewed upstream SHA. Maintain related documentation independently in the Wiki.
Use ordinary pushes and stop to reassess if the recorded remote tip has changed.
This repository policy supersedes older rebase instructions in personal sync skills.

Keep upstream migration IDs canonical and register new upstream SQL only in the
upstream registry/directory. EX migrations belong only to their independent
registry, SQL directory, and history table. Record qualified source ownership in
`maintenance/migrations.yml`; same version numbers across namespaces are distinct.
Development verifies fresh schemas, not conversion of old mixed histories.

Review every exported seam change with `maintenance/contracts.yml`. Implement new
behavior explicitly in the selected Enhanced module, add behavioral regressions,
and retain references to them. An embedded Community method satisfying an interface
is not proof of implemented behavior. Intentional shared aliases and unwired
scaffolding require a recorded rationale.

For future product changes, prefer `test/edition-contract/enhanced` and focused UI
components behind existing interfaces, props, and events. Shared-file edits must
be the smallest integration needed. Keep refactors and unrelated fixes outside
upstream syncs; document any shared-file change and its contract coverage.
Follow `maintenance/source-boundaries.md`: use same-package `_ex.go` files when
public API identity prevents a module move; keep authoritative integration calls
in their original execution order. Extend `api-docs-ex.yml` for fork-only API
paths and definitions and run the bundled-spec checks. Preserve the upstream
locale dictionaries and add fork messages under `web/src/lang/enhanced`.

Run `tools/upstream-sync/verify.sh` and retain failed as well as successful results.
Build the embedded frontend before Go verification and keep source stable throughout
the run. Classify baseline failures only with exact-upstream reproduction. Verify
changed UI in the browser and wait for the required workflows on the exact pushed
head. Preserve recovery branches and stashes until their deletion is authorized.

Releases follow `maintenance/RELEASING.md`: a `vX.Y.Z-ex.N` tag verifies successful
Dev and Full Product Build runs for that exact commit, then builds signed packages,
server images and runner images in parallel. Promote latest only after all builds
succeed. Beta dry runs are optional packaging diagnostics, not ordinary release
prerequisites. Never tag a plain
`vX.Y.Z`; it collides with upstream tags.

Publish documentation as directly readable Markdown with no dedicated docs UI or
compilation step. The product Wiki is the sole maintained English source, with no
local checkout, submodule or docs pipeline. Incoming upstream docs
are reviewed and adapted to its Markdown contract; do not restore a second docs
repository, translation tree or site build.

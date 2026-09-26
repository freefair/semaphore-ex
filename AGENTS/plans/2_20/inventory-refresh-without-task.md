# Inventory refresh without a task run

Status: implementation proposal; user requirements below are agreed, implementation is not started.
This document specifies a replacement for explicit refresh execution, not a request to run a refresh or deploy a change.

## Contents

- [Outcome and agreed requirements](#outcome-and-agreed-requirements)
- [Verified starting points](#verified-starting-points)
- [Architecture decision record](#architecture-decision-record)
- [Resolution and cache lifecycle](#resolution-and-cache-lifecycle)
- [API, identity, and user experience](#api-identity-and-user-experience)
- [Implementation sequence](#implementation-sequence)
- [Acceptance and verification](#acceptance-and-verification)
- [Open decisions and dependencies](#open-decisions-and-dependencies)
- [References](#references)

## Outcome and agreed requirements

Users refresh an inventory directly and see its current hosts without creating a playbook task, opening the normal task dialog, or waiting for the normal task queue.
Refresh progress and errors belong to the inventory and must not change a template's execution history, last-run status, deployment versions, schedules, notifications for task completion, or workflow triggers.
The objective is materially shorter refresh preparation and an accurate execution history, not merely renaming the existing task.

The agreed resolution strategy is:

1. Reuse an existing compatible cached environment when available.
2. Otherwise attempt minimal inventory loading with Ansible's built-in parsers and no additional third-party plugins.
3. If additional declared dependencies are needed, build a reusable environment, install them, and retry resolution.
4. Associate the successful environment with the inventory and reuse it on later refreshes.

Load Ansible packages directly from Python instead of launching `ansible-inventory` as a second process.
Retain automatic inventory membership updates during ordinary Ansible task runs; those still are real tasks.
Use the shared [dependency cache plan](dependency-cache.md), including per-inventory offline-update fallback with a visible notice.

## Verified starting points

| Existing area | Relevant behavior |
|---|---|
| `web/src/components/enhanced/InventoryRefresh.vue` | Selects an Ansible template, constructs `params.inventory_refresh=true`, and opens `NewTaskDialog`. |
| `db/inventory_hosts_ex.go` | Inventory snapshots and host projections carry a task ID; `Task.IsInventoryRefresh` detects the current special task mode. |
| `db_lib/inventory_resolver_ex.go` | Bundled Python wrapper launches `ansible-inventory --list`, emits bounded name/group events, and exits before `ansible-playbook` in refresh mode. |
| `services/tasks/inventory_hosts_ex.go` | Ingests resolver events through the task runner and the inventory repository. |
| `api/projects/inventory_hosts_ex.go` | Host and snapshot visibility is checked through the originating task and its template/workflow permissions. |
| `test/edition-contract/enhanced/db/sql/inventory_hosts.go` | Publishes inventory snapshots and obtains execution history from structured Ansible results. |
| `services/tasks/runner_placement.go` and `db/Runner.go` | Existing refresh capability protects against legacy runners ignoring the flag and running a playbook. |

The earlier [inventory host browser plan](inventory-host-browser.md) deliberately chose the task lifecycle to reuse context and scheduling.
The user's current request supersedes that choice for new explicit refreshes.
Keep the original plan as historical rationale; do not interpret its task requirement as an obstacle to the new design.

## Architecture decision record

### ADR-INVENTORY-01: a dedicated inventory operation and reusable resolver context

Status: proposed; removal of the normal task run is an agreed requirement.

Introduce a persisted `InventoryRefreshOperation` with its own identifier, lifecycle, permissions, bounded diagnostics, executor assignment, heartbeat/lease, and cancellation.
A refresh cannot be represented by a `db.Task` with a different label.
Use a separate bounded worker queue or equivalent operation dispatcher, so a long-running playbook does not occupy the only scheduling slot available to refreshes.
This still permits resource limits and short waits when refresh capacity itself is exhausted; “no task queue” does not mean unlimited execution.

Execute the Python resolver in an isolated process on a worker with the required repository access, runtime, network reachability, and credentials.
A `.venv` isolates packages, not privileges; executable plugins must not run inside the API server process or gain broader access than a task would receive.
The direct Python API removes the extra CLI subprocess and full hostvars JSON round trip, not the need for execution context.

Persist an inventory resolution profile containing repository/source and revision policy, working directory/configuration, runtime/image, executor selection, declared dependency files, and references to variables and credentials.
A selected existing template can supply the initial profile, but saving and using the profile must not start that template.
Capture the effective non-secret configuration and authorized references per refresh attempt, while resolving secret values only at execution time.
Do not silently choose between conflicting template contexts that share one inventory.

| Alternative | Benefit | Cost / decision |
|---|---|---|
| Keep a hidden normal task | Reuses existing scheduling | Still queues as a task and risks task side effects; rejected. |
| Parse all inventories manually | Simple for a narrow static subset | Drifts from Ansible semantics and misses executable sources; rejected. |
| Load plugins in the API server | Removes dispatch | Couples untrusted code and runtime dependencies to server/HA processes; rejected. |
| Dedicated operation with Ansible Python adapter | Independent UX, lifecycle, and reusable environment | Requires operation dispatch and snapshot provenance changes; recommended. |

Use a version-tested wrapper around `DataLoader`, `InventoryManager`, and the required variable/configuration loaders.
Ansible's Python API is internal; support must be proved against the shipped/configured Ansible versions rather than inferred from one import example.

## Resolution and cache lifecycle

1. Authorize refresh execution and resolve the inventory's explicit profile.
   Coalesce equivalent concurrent requests or assign ordered generations; record who requested the operation.
2. Acquire the source revision and effective context without generating a fake playbook run or depending on a playbook file existing.
   Keep repository updates in a private checkout/materialization so concurrent tasks are unaffected.
3. If the profile references a compatible cached environment, perform the dependency freshness check required by its declarations and policy, then resolve directly in that environment.
   Changed dependencies or runtime identity invalidate compatibility; an environment reference is not an unconditional reuse instruction.
4. With no compatible environment, try only the explicitly enabled built-in static parsers.
   Set plugin/configuration search paths so auto-discovery cannot accidentally execute repository plugins during this minimal attempt.
5. Detect sources requiring scripts, collections, or additional Python packages before execution where possible.
   On a genuine missing-dependency result, build a `.venv` plus separate collection installation directory using the declared requirements, and retry once.
   Do not guess package names from arbitrary import failures or repeatedly reinstall on authentication, syntax, or remote API failures.
6. Validate complete host/group membership and publish the new snapshot atomically.
   Treat an empty successful inventory differently from a failed or partially parsed inventory.
   Never publish hosts parsed from only a subset of configured inventory sources as a complete success.
7. On success, publish/associate the environment cache revision under the shared cache policy and finalize the operation.
   On failure, keep the previous successful membership and environment references and expose the current failure.

“Without plugins” means without additional plugins; built-in YAML/INI inventory parsers are themselves Ansible plugins.
The fast path must preserve applicable variable precedence, configuration, vault handling, source directories, and group membership semantics.
A static-looking source that depends on additional variable plugins must not yield a falsely successful incomplete resolution.
Dynamic sources still contact their authoritative APIs on each refresh; dependency caching is not permission to return an old host list as newly refreshed.

The `Continue if update check fails` control applies only to dependency update checks.
If enabled, a complete compatible cached environment can resolve the inventory despite package-source unavailability, with a notice.
It does not mask failure to reach the dynamic inventory's own API or failure to obtain required credentials.

For local, Docker, and Kubernetes execution, keep the same logical resolver contract and capability negotiation.
A Docker-only execution policy must remain Docker-isolated; a separate operation cannot silently route it into a less isolated local process for speed.
Decide whether persistent resolver workers or per-operation containers best satisfy that policy using measured cold/warm costs.

## API, identity, and user experience

Proposed API shape: start, inspect, and cancel a project/inventory-scoped refresh operation, plus read its resolution profile and latest successful snapshot.
Exact paths and response types must follow existing router conventions and be documented in `api-docs-ex.yml`.
Return a real operation ID, never a task ID disguised as an operation ID.

Suggested states are queued, preparing, resolving, succeeded, failed, and cancelled.
Expose source revision, start/end time, freshness/fallback notice, host count, and sanitized diagnostics.
Do not expose raw hostvars, credential values, package-source tokens, or unfiltered plugin output.

Extend snapshot provenance to explicitly identify either a normal task attempt or an independent refresh operation.
Update repository contracts, authorization, retention, API fields, and UI links together; making `task_id` nullable alone is insufficient.
Capture inventory/profile/template provenance independently of current mutable settings.
Refresh snapshots are readable through the inventory/profile permission model while retaining any restrictions inherited from referenced sources.
Existing task-backed snapshots retain their current visibility and execution links.

Prevent older operations or stale workers from replacing a newer published generation.
Reject events after cancellation or lease loss using an attempt/fencing identity.
API-server restarts and worker failure must reconcile operation state without leaving infinite spinners or publishing partial snapshots.

The inventory page provides one refresh action once its profile is configured.
First use may configure missing context; subsequent refreshes do not reopen the template/task dialog.
Show preparation only when needed, resolution progress, last successful refresh, cache notice, and retry/cancel actions.
Keep refresh audit history separate from the host's execution history and the template's task statistics.
Existing refresh tasks are retained as historical records; this plan does not authorize deleting or rewriting them.
Reject or explicitly deprecate new legacy `inventory_refresh` task submissions rather than silently changing the response from a task to an operation.

## Implementation sequence

1. **Domain and contracts:** introduce profile/operation/provenance interfaces and selected Enhanced implementations; add independent EX migrations and migration ownership entries.
   Extend `db/inventory_hosts_ex.go` and the host repository contracts with explicit origin identity.
2. **Operation lifecycle:** add project-scoped API/service operations with authorization, durable dispatch, leases, cancellation, capability negotiation, and restart recovery.
   Reuse executor primitives where possible without calling `TaskPool.AddTask` or constructing a normal task bundle identity.
3. **Resolver adapter:** replace the `ansible-inventory` subprocess path in `db_lib/inventory_resolver_ex.go` with the tested direct Python adapter.
   Add strict minimal/full resolution modes, explicit dependency classification, and profile-based source/configuration handling.
   Preserve ordinary Ansible-run membership collection through the same resolver behavior.
4. **Environment reuse:** integrate the shared cache contracts and per-inventory settings; persist a cache reference after successful resolution.
   Cover source updates, missing plugins, open version ranges, offline policy, and runtime incompatibility.
5. **Snapshot and authorization integration:** update `services/tasks/inventory_hosts_ex.go`, `api/projects/inventory_hosts_ex.go`, and `test/edition-contract/enhanced/db/sql/inventory_hosts.go` without weakening existing task visibility.
   Define retention for independent operations and keep execution history based on actual execution evidence.
6. **UI:** replace the `NewTaskDialog` flow in `web/src/components/enhanced/InventoryRefresh.vue`; integrate profile settings and progress in `InventoryForm.vue`, `InventoryDetails.vue`, and the host browser.
   Use fork-owned English/German messages and focused components.
7. **Compatibility and documentation:** keep older snapshots readable, require resolver-operation capability, document legacy request behavior, extend API specs and `maintenance/contracts.yml`, and prepare product Wiki guidance plus the accepted ADR.

## Acceptance and verification

- [ ] Refresh creates zero normal task rows and never invokes `ansible-playbook`.
- [ ] Refresh does not change template last-run status, build/deployment versions, task statistics, task notifications, autoruns, or workflow triggers.
- [ ] Refresh proceeds while normal playbook task slots are occupied, subject only to its own bounded capacity.
- [ ] A minimal static inventory resolves without installing extra dependencies or launching `ansible-inventory`.
- [ ] A declared collection/Python dependency is installed once and reused on later compatible refreshes.
- [ ] Known cached contexts go directly to the environment path rather than repeating an expected minimal-mode failure.
- [ ] Syntax, authentication, and source connectivity errors are reported without an installation/retry loop.
- [ ] Multiple inventory sources, vaults, custom variable behavior, groups, empty inventories, and partial parse failures preserve Ansible semantics.
- [ ] Automatic collection during normal tasks still works, including runs with a host limit; membership is not reduced to executed hosts.
- [ ] Offline dependency fallback is configurable and visible; dynamic inventory API failure still fails the refresh and preserves the previous snapshot.
- [ ] Older generations, cancelled attempts, and lease-expired workers cannot publish or overwrite a newer result.
- [ ] API-server failover, worker death, permission changes, and legacy runner dispatch have explicit tested outcomes.
- [ ] Docker/Kubernetes policies remain enforced; cross-project cache and inventory access remain denied.
- [ ] Browser verification covers first-time setup, one-click warm refresh, progress, cancellation, failure/retry, fallback notice, and absence from task history.
- [ ] Measure cold and warm source acquisition, dependency checks, environment setup, resolution, and total latency against the existing task-based flow on equivalent fixtures.

Use focused Go tests, resolver subprocess fixtures with real supported Ansible versions, frontend tests, and actual browser/executor checks.
Do not claim a speed improvement from removal of a task row or a subprocess alone.

## Open decisions and dependencies

- This plan depends on the cache contracts and Ansible environment adapter, not on completion of every Terraform/Python cache adapter.
- Profile defaults and migration from multiple conflicting template contexts require explicit UX design; no arbitrary template is selected.
- Determine supported runtime versions and the required resolver worker placement/capacity from deployed execution policies before implementation.
- Select operation retention and capacity limits from measured behavior and product requirements; no numeric defaults are invented here.
- The core conflict is resolved by the current request: new explicit refreshes replace the old task-based approach; normal Ansible task collection remains.

## References

- [Ansible Python API](https://docs.ansible.com/projects/ansible/latest/dev_guide/developing_api.html): direct access is possible but internal APIs have no external compatibility guarantee.
- [Ansible inventory manager source](https://github.com/ansible/ansible/blob/devel/lib/ansible/inventory/manager.py): implementation reference; use the supported release's source for compatibility tests.
- [Python virtual environments](https://docs.python.org/3/library/venv.html): package isolation and runtime/path compatibility requirements.

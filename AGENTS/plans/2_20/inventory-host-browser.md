# Inventory details and host execution history

## Requested outcome

Inventory references open a real detail page. The detail page shows the hosts
contained in that inventory. A project Hosts view supports searching and grouping
by inventory, with a chronological task history for each host, task trigger
information, and access to task output.

## Investigation

- `web/src/components/ObjectRefsView.vue` generates
  `/project/:projectId/inventories/:inventoryId`.
- `web/src/router/index.js` only registers `/project/:projectId/inventory`.
  There is no inventory detail route to render the reference destination.
- `web/src/views/project/Inventory.vue` presents configuration and edit actions,
  without a host view.
- The selected Enhanced implementation already persists structured Ansible host
  and result events in `task__summary_event`. Its repository is
  `test/edition-contract/enhanced/db/sql/ansible_task.go`.
- Tasks retain inventory overrides and user, schedule, and integration references.
  Existing task details and the task log dialog provide reusable presentation.
- Inventory files and scripts are resolved in a runner context. A history of
  executed hosts is not a complete inventory membership snapshot: limits,
  playbook patterns, skipped execution, and never-run hosts differ.

## Accepted scope

Dennis approved autonomous implementation on the existing develop checkout:
update inventory membership during every normal Ansible run and offer an
explicit refresh action using the selected template's execution context.
The prior session is closed. Its Dev and Full Product Build workflows for
`2f65cb48` completed successfully; no inherited CI fix is required.

No release, push, or deployment is included in this request.

## Architecture decision: resolve inventories in the execution context

Status: accepted for this implementation.

Inventory configuration alone does not establish membership. Files, inventory
directories, scripts, plugins, vaults and environment variables are interpreted
by Ansible on the selected executor. Executed host results cannot substitute for
that membership because playbook patterns and task limits exclude valid hosts.

The implementation runs a bundled resolver before the playbook, using the same
environment, repository, inventory and vault context. Explicit refresh uses the
normal task admission and runner lifecycle with `params.inventory_refresh=true`,
and exits after resolving membership. The wrapper is bundled into Docker and
Kubernetes task payloads, so those executors run the same logic inside their
execution environment. Local execution uses the same wrapper.

Alternatives considered:

- Parse INI/YAML on the API server: inexpensive, but cannot correctly resolve
  dynamic plugins, scripts, vaults, runner-local files or template variables.
- Infer hosts from prior playbook output: needs no extra inventory command, but
  excludes never-executed hosts and cannot establish complete membership.
- Resolve automatically plus explicit refresh: selected because both current
  membership and task execution evidence remain accurate and independently useful.

Membership is persisted per task assignment. Host rows can arrive out of order;
publication requires the expected number of distinct rows. Successful and failed
terminal snapshots are immutable, and failures retain the previous successful
membership. An explicit runner capability prevents legacy runners from ignoring
the refresh flag and executing a playbook. It is rechecked at dispatch.

The inventory result projection contains names and groups, never hostvars or
raw inventory diagnostics. Frames are bounded and require one JSON document.
The existing task execution authority still applies to authored inventories,
plugins, playbooks and dependencies; these observations are not an authorization
source. Project host queries additionally enforce template and workflow read
permissions. Cross-project workflow inventories are not attributed to a consumer
project; that requires a separate provenance model and is outside this view.

Task history joins actual structured Ansible host results to the captured
inventory identity. Older reviewed runs are supported through their immutable
execution snapshots. Current template settings never reconstruct old identity.
Retention of tasks also retains or removes their linked membership snapshots;
this view is an execution-backed inventory browser, not a separate CMDB.

## Implementation order

### 1. Inventory resolution and historical identity

- Add focused inventory host snapshot types and repository contracts under `db/`
  and `pro_interfaces/`, with the implementation in the selected Enhanced module.
- Add an independent EX migration and update `maintenance/migrations.yml`.
  Store host names, group membership, resolution time, inventory identity, and
  execution-context provenance. Keep variable values and credentials out of the
  host snapshot API and logs.
- Capture the inventory identity used by a run; do not reconstruct historical
  membership from a template's current inventory selection.
- Resolve with Ansible in the same repository, variables, credentials, and runner
  context as execution, using focused additions around `db_lib/AnsibleApp.go`
  and `services/tasks/local_executor_inventory.go`.
- If explicit refresh is selected, reuse the runner scheduling and assignment
  lifecycle for an inventory-resolution operation that does not run a playbook.
  Define its operation type explicitly and retain normal execution permissions.
- Keep snapshot publication atomic and safe across concurrent runners and HA
  servers. Show resolution failure or missing data explicitly; do not substitute
  an empty inventory for a failed resolution.

### 2. Project host queries and task history

- Add paginated, project-scoped host and inventory snapshot queries in Enhanced
  `db/sql`, with exact host matching and escaped literal search.
- Join structured task host results to tasks for execution history. Do not infer
  successful execution from inventory membership or arbitrary log text.
- Preserve inventory context when identical host aliases occur in multiple
  inventories. The UI must expose that context instead of silently merging them
  into one physical machine.
- Apply existing task/template visibility permissions to history and output.
  Add authenticated routes through a focused EX route registration file.
- Reuse the existing task origin information and log output authorization path.
  Historical tasks without host evidence remain explicitly unavailable for host
  attribution; do not fabricate a backfill.

### 3. User interface

- Add a focused inventory detail view and register the existing broken plural
  URL as a supported destination or alias in `web/src/router/index.js`.
- Link inventory names in `web/src/views/project/Inventory.vue` to details and
  provide a direct path from the inventory edit dialog to its hosts.
- Add `web/src/views/project/Hosts.vue` and focused components under
  `web/src/components/enhanced/`. Integrate the Hosts entry in `web/src/App.vue`.
- Provide host search, inventory filtering, optional grouping, pagination,
  last resolution time, and distinct loading, empty, unavailable, and error states.
- Host details show task, execution time, status, trigger, and a task output
  action using `TaskLink` and the existing dialog. Label whole-task output
  accurately; host-specific results and whole-task logs are different views.
- Add fork-owned English and German messages in `web/src/lang/enhanced/`.

### 4. Documentation and verification

- Record the chosen snapshot/refresh design in an ADR in the product Wiki.
- Extend `api-docs-ex.yml` and the shipped Swagger fragment, and update
  `maintenance/contracts.yml` for exported seams.
- Add behavioral regressions for resolution, membership versus execution,
  effective inventory selection, duplicate host aliases, pagination, permissions,
  and failure states. Use synthetic inventories and credentials for verification.
- Verify the broken-link flow, inventory host contents, search, grouping, host
  history, trigger presentation, and task log interaction in the in-app browser.
  Exercise desktop and narrow viewport layouts, refresh failures, and hosts with
  no task history.
- Run focused Go and frontend tests, frontend build, API bundle checks, and the
  repository maintenance verification required by `CLAUDE.md`. Read the diff and
  retained results before committing completed implementation work.

## Source reference

Ansible's inventory command supports inventory sources, extra variables, and a
playbook directory. Export mode is not an exact representation of resolved
runtime inventory and should not stand in for it:
https://docs.ansible.com/projects/ansible-core/2.20/cli/ansible-inventory.html

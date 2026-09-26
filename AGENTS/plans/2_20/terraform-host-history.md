# Terraform resources in host execution history

Status: implementation proposal; the user approved the direction, implementation is not started.
This plan adds execution evidence to Semaphore EX; it does not authorize Terraform runs against infrastructure.

## Contents

- [Outcome and scope](#outcome-and-scope)
- [Verified starting points](#verified-starting-points)
- [Architecture decision record](#architecture-decision-record)
- [Capture and identity model](#capture-and-identity-model)
- [Host association and user experience](#host-association-and-user-experience)
- [Implementation sequence](#implementation-sequence)
- [Acceptance and verification](#acceptance-and-verification)
- [Open decisions and dependencies](#open-decisions-and-dependencies)
- [References](#references)

## Outcome and scope

Users can inspect a host and see which Terraform runs affected its associated resources, which actions were only planned, what completed, and what failed.
Resources with no proven host association remain visible in their task's resource history.
The feature must not imply that all Terraform resources are hosts or that Terraform necessarily connects to the managed VM itself.

The agreed direction is to capture resources and actual actions per run, then link them to hosts using evidence.
Names and IP addresses alone are not sufficient to silently equate resources with inventory hosts.
Inventory membership remains different from execution evidence.

Terraform is the primary requested implementation.
The existing application family also includes OpenTofu and Terragrunt; preserve their behavior and design an engine adapter boundary rather than hardcoding Terraform assumptions into the host model.
For the proposed complete feature, validate event capture for supported OpenTofu and Terragrunt execution paths as separate adapters, including unit identity for Terragrunt.
Do not infer that approval of those tools for caching by itself proves their event formats are compatible.
Expose capability/coverage explicitly until each adapter is verified.

The [dependency cache plan](dependency-cache.md) is independent of history capture.
The [inventory refresh plan](inventory-refresh-without-task.md) changes snapshot provenance; align shared identities without requiring a refresh operation to create a task.

## Verified starting points

| Existing area | Relevant behavior |
|---|---|
| `db/Template.go` | `IsTerraform()` covers Terraform, OpenTofu, and Terragrunt. |
| `db_lib/TerraformApp.go` | Plan/apply execute through shared command and logging mechanisms; plan no-change detection currently listens for human-readable output. |
| `services/tasks/local_executor.go` | Dispatches the Terraform-family applications and performs their preparation. |
| `db/TerraformInventoryState_pro.go` | Stored Terraform state metadata includes an optional task ID. |
| `test/edition-contract/enhanced/db/sql/terraform_inventory.go` | Persists and retrieves inventory state versions. |
| `services/tasks/terraform_backend_ex.go` | Resolves configured internal-backend credentials for child processes. |
| `test/edition-contract/enhanced/db/sql/inventory_hosts.go` | Host task history selects actual structured Ansible `host_summary`/`task_result` events. |
| `api/projects/inventory_hosts_ex.go` | Filters host history through template/workflow task permissions. |
| `web/src/components/enhanced/HostTaskHistory.vue` | Shows chronological tasks and links to complete task output. |

Source inspection found logs and state storage, but no Terraform-resource-to-inventory-host projection in this path.
No live instance or historical state was inspected for this plan.

## Architecture decision record

### ADR-HOSTS-01: resource execution evidence with explicit host associations

Status: proposed; the resource-first approach is agreed.

Persist normalized resource events linked to an immutable execution context, and maintain separately authorized, versioned associations between resource identities and inventory host identities.
Keep planned intent, observed apply progress, terminal results, and current resource attributes as separate concepts.
This supports resources such as VMs, networks, and DNS records without manufacturing a host for each one.

| Alternative | Benefit | Cost / decision |
|---|---|---|
| Parse human-readable logs | Can attempt historical reconstruction | Formatting/version fragility and weak outcome guarantees; not the authoritative capture path. |
| Infer actions from state differences alone | Reuses internal state history | Misses precise execution outcomes and is unavailable for arbitrary external backends; use only as supplementary evidence. |
| Treat every planned action as an execution | Easy association | Reports unapproved, cancelled, or failed work as completed; rejected. |
| Structured event capture plus explicit identity mapping | Accurate phases, outcomes, and provenance | Requires ingestion and mapping contracts; recommended. |

Capture during execution on the actual executor so external state backends are supported.
Do not require the Semaphore internal Terraform backend, a second infrastructure run, or downloading arbitrary external historical states.
Historical runs lacking reliable evidence remain explicitly uncovered; a log link is not a fabricated structured backfill.

## Capture and identity model

### Execution evidence

Define a versioned normalized event envelope containing project, task, attempt, execution assignment, sequence/deduplication identity, engine/version, phase, timestamp, resource address, provider identity, action, and outcome.
For Terragrunt include the unit/root identity; identical resource addresses in different units are not the same resource.
Retain the user/schedule/integration/workflow trigger and repository revision through the existing immutable task context.

Use structured plan data for planned actions and machine-readable apply events for started/completed/errored actions.
Expose replacements as a coherent action while retaining create/delete phase evidence where emitted.
Treat no-op, refresh/read observations, import, moved addresses, and destroy distinctly where supported.
Do not turn a completed provider operation into proof that every downstream effect or provisioner succeeded.
An apply can partially succeed before the overall task fails; preserve both facts.

Inspect the current confirmation flow before selecting flags or plan artifacts.
Do not bypass confirmation, add an extra apply, change user CLI arguments silently, or retain text-based no-change detection after replacing its input with JSON.
If a saved plan becomes part of the design, review its relationship to the existing approval contract explicitly and protect it as sensitive temporary data.
Keep normal readable task output by rendering structured events appropriately, rather than exposing a raw JSON stream as the user log.

Track capture completeness independently of task outcome: complete, partial, or unavailable.
A missing parser event, unknown future event type, disconnected runner, or truncated stream cannot become a fabricated success.
Unknown additive event fields should be tolerated according to the source format; unsupported major formats require an explicit coverage result.
Replayed frames must be idempotent, and late events from an old assignment must not modify the current attempt.

### Resource identity and attribute handling

A resource address is unique only within an engine/root/state context, not globally.
Use a stable scoped identity containing project, logical state/backend identity, workspace, root/unit identity, and resource address, with provider object identity where available.
Capture endpoint/account/region distinctions through non-secret canonical identifiers or opaque authorized references.
Do not use credential values or credential-bearing backend URLs as identifiers.

Provider IDs may arrive only after create and may disappear on destroy.
Retain pre-action identity for destroyed/replaced resources and distinguish object generations when identifiers are reused.
Handle moved addresses as explicit identity transitions rather than creating false unrelated host histories.
Current template configuration must not rewrite the historical resource context.

When extra host identity attributes are needed, use a narrowly defined provider adapter or explicit mapping rather than persisting full state or plan values.
Plan/state JSON can contain sensitive values, including values not safe merely because a sensitivity flag is absent.
Project only approved fields in the executor, bound payload size, and avoid raw plan/state/hostvars in events, logs, diagnostics, and cache artifacts.

## Host association and user experience

An inventory host currently has a project/inventory/host-alias identity, not a universal physical-machine identity.
Retain that scope and allow one resource to be associated with several inventory aliases only through explicit evidence or configuration.
Duplicate host aliases across inventories must remain distinct.

Provide two association mechanisms:

1. An explicit authorized mapping between a scoped resource and an inventory host.
2. A verified provider adapter that compares stable scoped object identifiers already available on both sides.

The current inventory snapshot transports only host names and groups.
Automatic provider-ID matching therefore needs an explicit, narrowly allowlisted identity extension or another declared mapping source; it cannot work merely by having a VM ID on the Terraform side.
Do not expand that extension into unrestricted hostvars collection.
Make the association source, scope, and effective revision inspectable.
Manual association of old resource events is labeled as a later mapping decision, never presented as evidence captured during the original run.

Names/IPs may support a suggested match for user confirmation, but do not silently merge identities based on them.
Ambiguous resources remain unlinked, with the reason visible.
Infrastructure API endpoint, hypervisor/node, managed VM, and related resource are distinct relationships; preserve that distinction in labels.

Extend the Hosts history to show the engine, task/trigger/time, affected resources, planned versus applied action, resource outcome, overall task status, association provenance, and capture coverage.
Use a resources section in task details for all captured resources, including those not linked to any host.
Links to task output keep existing authorization checks.
A host link never grants access to an otherwise hidden template/workflow or resource event.
Pagination must remain stable when Ansible and Terraform events share a chronological view.

Example acceptance story: a Terraform run plans a VM replacement, completes creation but fails a later step.
The linked host shows the replacement intent and the observed completed phase alongside the failed overall task; an unapproved plan shows only intent.
A DNS-only resource remains inspectable in that run without inventing an executed host.

## Implementation sequence

1. **Discovery and fixtures:** verify command/event capabilities for supported engines and confirmation flows; capture redacted fixtures for plan-only, apply, failure, cancellation, replacement, and multi-unit execution.
   Select the first real provider adapter only after the relevant host-side identity source is confirmed.
2. **Contracts and storage:** add resource context/event/association/coverage types under `db/` and `pro_interfaces/`, with selected Enhanced repositories and services.
   Add separate EX migrations, indexes for project/task/resource/host lookup, attempt fencing, and retention; update `maintenance/migrations.yml`.
3. **Executor capture:** add focused Terraform-family adapters near `db_lib/TerraformApp.go` and integration points in `services/tasks/`.
   Reuse structured transport patterns without spoofing Ansible events; support local, Docker, Kubernetes, and remote runner delivery.
4. **Identity and mapping:** implement explicit mappings and the agreed provider identity adapter, including the minimal inventory identity extension if required.
   Add authorization, ambiguity handling, provenance, and deletion/replacement behavior before exposing automatic links.
5. **Queries and UI:** extend `api/projects/inventory_hosts_ex.go` and the Enhanced history repository with a generalized history contract.
   Update `web/src/components/enhanced/HostTaskHistory.vue`, the host browser, and task summary components to show resources, phases, and coverage without duplicating existing task output UI.
6. **Compatibility and docs:** preserve existing Ansible history and old runs with absent resource evidence; capability-gate old runners and unsupported engine formats.
   Update `api-docs-ex.yml`, bundled API docs, `maintenance/contracts.yml`, English/German fork messages, and product Wiki guidance with an accepted ADR.

## Acceptance and verification

- [ ] A linked VM resource exposes its Terraform task and actual observed actions in the host history.
- [ ] Plan-only or unapproved work is labeled as planned and never counted as a successful infrastructure modification.
- [ ] Partial apply success, resource failure, cancellation, no-op, import, move, replacement, and destroy have truthful distinct representations.
- [ ] Provider ID appearing after create and old identity surviving destroy/replacement are handled without losing history.
- [ ] Same resource addresses in different states/workspaces/units/projects do not collide.
- [ ] Host-side identifiers are present and authorized before any automatic match is accepted; duplicate names/IPs cannot silently merge hosts.
- [ ] Explicit mappings work for providers without an automatic adapter, with clear mapping provenance.
- [ ] Unlinked and non-host resources remain visible in task details.
- [ ] An external backend works without copying raw state into Semaphore or requiring an internal-backend alias.
- [ ] Existing confirmation semantics and command execution counts are unchanged; structured output remains readable and no-change handling still works.
- [ ] Event replay, disconnect, runner reassignment, unknown format, and truncated output give idempotent or explicitly partial coverage.
- [ ] Hidden tasks/resources stay hidden through host queries, mapping APIs, pagination, and task output links.
- [ ] Payload/log fixtures demonstrate that secrets and unapproved attributes never enter the history projection.
- [ ] Existing Ansible history remains correct alongside Terraform-family events and independent inventory refresh snapshots.
- [ ] Browser verification covers linked/unlinked resources, empty/partial history, planned/applied distinctions, permissions, and task links.
- [ ] Tool-specific fixtures and executor checks verify every engine advertised as supported; shared application dispatch is not evidence of compatibility.

Use deterministic local fixtures and disposable provider scenarios for verification rather than running against an assumed production/test environment.
Run focused Go/frontend suites, API contracts, appropriate build/lint/typecheck checks, and actual browser verification after implementation.

## Open decisions and dependencies

- Choose the initial provider identity adapter and the source of matching host identifiers; no provider or target infrastructure was selected in the conversation.
- Finalize the non-secret logical state identity for externally configured backends and Terragrunt units.
- Confirm the supported engine/version matrix and any limitations of multi-unit structured output before promising identical coverage.
- Keep the first delivery centered on resource history and explicit host links; broader CMDB reconciliation, discovery outside Semaphore, and retrospective log mining require separate scope.
- Coordinate snapshot-origin contracts with the inventory refresh plan; dependency caching does not gate this feature.

## References

- [Terraform machine-readable UI](https://developer.hashicorp.com/terraform/internals/machine-readable-ui): structured resource progress and outcomes during execution.
- [Terraform JSON format](https://developer.hashicorp.com/terraform/internals/json-format): planned changes, resource addresses, prior/planned values, and moved-address information.
- [Terraform show](https://developer.hashicorp.com/terraform/cli/commands/show): JSON representations can expose sensitive values and require controlled handling.

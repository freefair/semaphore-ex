# Dependency caches across task executions

Status: implementation proposal; user requirements below are agreed, implementation is not started.
This document plans changes to Semaphore EX and does not authorize deployment, infrastructure provisioning, or publication.
The `2_20` directory follows the existing planning structure and does not assign a release version.

## Contents

- [Outcome and agreed requirements](#outcome-and-agreed-requirements)
- [Verified starting points](#verified-starting-points)
- [Configuration and failure semantics](#configuration-and-failure-semantics)
- [Architecture decision record](#architecture-decision-record)
- [Dependency adapters](#dependency-adapters)
- [Implementation sequence](#implementation-sequence)
- [Acceptance and verification](#acceptance-and-verification)
- [Open decisions and dependencies](#open-decisions-and-dependencies)
- [References](#references)

## Outcome and agreed requirements

Repeated executions reuse downloaded dependencies and compatible installed environments, including executions in separate Docker containers.
Caching reduces repeated preparation without silently changing dependency requirements, execution isolation, or task results.

The agreed scope includes Ansible, Terraform, OpenTofu, Terragrunt, and partial support for Python tasks.
An inventory refresh uses the same cache infrastructure without creating a task; see [the inventory refresh plan](inventory-refresh-without-task.md).
The independent [Terraform host history plan](terraform-host-history.md) does not require caching to work.

1. A task template or inventory can enable `Use cache`.
2. Each cache configuration can independently enable `Continue if update check fails`.
3. The lifecycle is restore, check/init/install, execute, then publish the cache after successful completion.
4. Requirements hashes detect changed declarations, not newly available dependency versions.
5. Open version ranges without a governing lockfile must be resolved against their package sources on each preparation, even when installed versions still satisfy the range.
6. A source availability failure may use a complete, compatible existing cache when the configuration permits it.
   Emit a visible informational log entry; the failed update check alone does not fail the task in this mode.
7. Missing or incompatible dependencies, invalid requirements, failed installations, and execution errors remain failures.
8. Environments are reusable only within their runtime compatibility and authorization boundaries.

## Verified starting points

These observations come from source inspection, not runtime benchmarks.

| Existing area | Relevant behavior |
|---|---|
| `db_lib/AnsibleApp.go` | Galaxy installation uses requirements hashes in the repository/template internal directory; changed files trigger installation, unchanged files skip it. |
| `db_lib/LocalApp.go` | `LocalApp.InstallRequirements` and the installation/running argument types provide existing preparation boundaries. |
| `db_lib/TerraformApp.go` | Shared Terraform-family execution handles init, workspace selection, plan, and apply. |
| `db_lib/ShellApp.go` | Python currently runs through the shell application; its requirements installation method is a no-op. |
| `services/tasks/local_executor.go` | Preparation invokes application installers and has Terraform-specific init handling. |
| `test/edition-contract/enhanced/services/tasks/docker/executor.go` | Docker creates a task bundle volume and removes it during cleanup; it is not a persistent dependency cache. |
| `test/edition-contract/enhanced/services/tasks/k8s/manifest.go` | Kubernetes volumes are subject to explicit execution policy; new persistence cannot bypass that policy. |
| `pro_interfaces/workflow_file_artifact.go` | Existing artifact contracts demonstrate checksums, revision conflicts, leases, and bounded transfer, but are tied to workflow/task identities. |

Reuse suitable underlying mechanisms, not fake workflow runs or task IDs to satisfy artifact APIs.

## Configuration and failure semantics

Persist explicit Boolean settings and return `false` explicitly in JSON APIs.
Proposed field names are `use_cache` and `continue_if_cache_update_check_fails`; finalize naming against adjacent template/inventory APIs.
The second setting is editable only while caching is enabled, and the effective policy is captured at execution start.
Template settings govern task caches; inventory settings govern independent refresh caches.
Sharing compatible blobs does not cause one consumer to inherit another consumer's fallback policy.

Default values are a product decision still to be confirmed before implementation.
Recommended defaults are caching disabled for existing configurations and fallback enabled when a user first opts into caching, matching the requested availability behavior.
Treat these as proposed defaults, not a recorded user decision.

| Situation | Fallback enabled | Fallback disabled |
|---|---|---|
| Cache disabled | Use ordinary preparation; fallback setting has no effect. | Same. |
| Cache valid, update check succeeds | Reconcile to the resolved dependencies and run. | Same. |
| Source unreachable, cache complete and compatible | Log the failed check and cache revision used; continue. | Fail preparation with an actionable reason. |
| Source unreachable, cache absent/incomplete/incompatible | Fail preparation; required dependencies are unavailable. | Same. |
| Invalid requirements or unsatisfiable dependency set | Fail preparation. | Same. |
| Integrity check, installation, backend init, or execution fails | Preserve the last published cache and report the actual failure. | Same. |

Distinguish a package-source availability failure from a checksum/signature failure or a rejected credential.
The proposed fallback classification includes transport unavailability, timeouts, and temporary source service outages.
Do not turn all installer nonzero exits into permission to continue.
Cache integrity and consumer access checks remain mandatory even during offline fallback.

Record last successful update check separately from last use, failed check, and cache publication time.
An offline fallback does not advance the successful-check timestamp or label the dependencies as newly checked.
If a cache upload fails after a successful task, the proposed behavior is a notice and unchanged task success; the previous cache remains authoritative.

## Architecture decision record

### ADR-CACHE-01: immutable dependency revisions with tool-specific adapters

Status: proposed.

Use a shared cache service with application adapters for discovery, dependency resolution, compatibility checks, restore, and export.
Persist immutable manifests and artifacts, materialize a private writable environment for each consumer, and atomically publish a new successful revision.
An unchanged successful dependency set may retain its existing revision without uploading identical content.

The manifest identifies project and cache owner, adapter/schema version, dependency declarations and included files, selected package versions, source identities and immutable revisions, artifact digests, runtime compatibility, and successful verification timestamps.
Use non-secret source identifiers; credentials and credential-bearing URLs never enter keys, manifests, or logs.
Include the effective package-source configuration and authorized access domain when deciding whether reuse is permitted.

Runtime compatibility includes OS, architecture, interpreter/ABI, tool versions, relevant system libraries, execution image digest where applicable, and the stable installation path for an installed environment.
Do not use an inventory ID or a requirements hash as the entire cache key.
A cached resolved manifest is evidence of the last resolution, not a user lockfile that freezes an otherwise open range.

The original snapshot stays immutable while init/install and the task run against a private copy.
Publish only allowlisted dependency content after the consumer succeeds; do not export arbitrary post-task working directories.
Concurrent publishers use compare-and-swap or equivalent revision checks; readers lease exact revisions so eviction cannot remove in-use content.
Task cancellation, process loss, partial uploads, and failed executions leave no published partial revision.

| Alternative | Benefit | Cost / decision |
|---|---|---|
| Shared writable dependency directory | Simple local reuse | Concurrent installers and task code can corrupt other runs; rejected. |
| Archive the entire task directory | Broad apparent reuse | Includes state, credentials, generated configuration, and unrelated execution output; rejected. |
| Immutable dependency artifacts plus private environments | Explicit ownership, concurrency, reproducibility | Requires manifests, adapters, and transfer lifecycle; recommended. |
| Separate cache implementation for every application | Tool-specific freedom | Duplicates policy, transfer, retention, and UI behavior; rejected. |

Use a storage interface that works without a single API server's local filesystem being authoritative.
Local runner materializations are disposable accelerators; another runner can rebuild or restore a compatible cache.
Cross-runner cache transport must be settled before claiming distributed reuse is delivered.
Evaluate existing blob primitives versus a configured shared filesystem or object store; choose the concrete backend after inspecting deployment constraints.
Do not introduce a mandatory new external service merely to enable local caching.

Keep caches project-scoped and at least as restricted as the producing dependency sources and execution context.
Do not enable automatic cross-project or unrestricted cross-template reuse of executable artifacts.
Apply quotas, bounded extraction, integrity checks, last-used retention, administrative invalidation, and cleanup of failed staging revisions.

## Dependency adapters

| Adapter | Reusable content | Preparation contract |
|---|---|---|
| Ansible | Collections, roles, Python package artifacts, compatible `.venv` | Resolve declared Galaxy/Python dependencies, install missing or changed selections, verify imports and collection availability. |
| Terraform | Provider packages and clean module source artifacts | Keep backend/workspace init; honor provider locks; check unlocked providers and mutable module sources independently. |
| OpenTofu | Its supported provider and module artifacts | Implement and test against OpenTofu capabilities; do not assume all Terraform flags or cache semantics match. |
| Terragrunt | Clean downloaded module sources and underlying engine dependencies | Identify each unit/source and selected engine; regenerate execution configuration and preserve unit isolation. |
| Python | Declared package artifacts and compatible `.venv` | Introduce an explicit dependency preparation path and run the script with that interpreter. |

Never cache Terraform/OpenTofu state, saved plans, backend credentials, `.env` files, SSH material, full process environments, or generated Terragrunt execution configuration.
Neither `.terraform/` nor `.terragrunt-cache/` is safe to archive wholesale.
The Terraform dependency lockfile governs providers, not every module source; a provider lock must not suppress checks for an unlocked module.
For mixed locked/unlocked inputs, refresh only the unlocked selections while preserving locked selections and validating their checksums.
Define this per adapter instead of applying an unconditional `init -upgrade` that rewrites locked selections.

For unchanged requirements such as `package > 16`, resolve newly available permitted versions rather than accepting an installer's “already satisfied” result as an update check.
Record exact selected versions and immutable VCS commits where available.
Do not rely on a requirement-file hash to detect moved Git references or newly published packages.

A Python `.venv` is generally not portable between arbitrary paths and runtimes; retain installed environments only at compatible stable locations.
Otherwise restore verified wheels/source artifacts and rebuild the environment in the target runtime.
The initial Python scope is declared requirements files with recursively included requirements and constraints; other lockfile formats need explicit adapters.
Arbitrary package installation inside scripts and operating-system package management are outside automatic Python cache support.

## Implementation sequence

1. **Contracts and persistence:** define cache policy, manifest, revision, lease, and failure categories under `db/` and `pro_interfaces/`, with selected Enhanced services and repositories under `test/edition-contract/enhanced/`.
   Add independent EX migrations and register ownership in `maintenance/migrations.yml`; preserve upstream migration identities.
   Extend `db/Template.go`, `db/Inventory.go`, export/import paths, and effective execution snapshots using minimal shared-file integrations.
2. **Storage and lifecycle:** implement restore/stage/publish/lease/evict semantics, bounded transfer, crash recovery, and configured retention.
   Choose and document the HA storage backend before implementing transport-dependent behavior.
3. **Ansible and Python:** replace hash-only freshness decisions with explicit dependency resolution; add Python preparation without changing unrelated shell applications.
   Integrate through `db_lib/LocalApp.go`, focused adapter files, `db_lib/AnsibleApp.go`, `db_lib/ShellApp.go`, and `services/tasks/local_executor.go`.
4. **Terraform family:** add separate capability-tested adapters around `db_lib/TerraformApp.go` for Terraform, OpenTofu, and Terragrunt.
   Prove offline reuse through each tool's supported mechanisms; merely ignoring a failed online init is not an implementation.
5. **Executor integration:** restore before initialization and export after successful completion for local execution and separate Docker containers.
   Integrate Kubernetes under its existing volume/network policies as required by the project's executor support; show a precise unsupported capability until a path is implemented.
   Negotiate runner cache capability so an old runner cannot silently ignore enabled policy.
6. **Configuration and UX:** add the two settings to `web/src/components/TemplateForm.vue` and `web/src/components/InventoryForm.vue` through focused Enhanced components.
   Show cache hit/miss, update-check outcome, resolved revision, fallback notice, and invalidation action with existing permission checks.
7. **Contracts and documentation:** update `api-docs-ex.yml`, the bundled API specification, `maintenance/contracts.yml`, and export/import tests.
   Publish user guidance and the accepted ADR in the product Wiki when implementation is authorized for publication.

## Acceptance and verification

Behavioral tests and actual executor checks are required for implementation; this planning-only change does not run application suites.

- [ ] Second execution reuses verified dependency content and still performs the required init/check behavior.
- [ ] Two separate Docker executions reuse content after the first container and its task volume are removed.
- [ ] Every named adapter has its own cold, warm, changed-dependency, and offline scenarios.
- [ ] Unchanged open-range requirements discover a newly published compatible version in a controlled package-source fixture.
- [ ] Locked versions remain fixed; unlocked module updates are still detected beside a provider lockfile.
- [ ] Offline fallback enabled gives a notice and successful execution with complete compatible dependencies; disabled fails preparation.
- [ ] Offline fallback never accepts an incomplete, incompatible, inaccessible, or corrupt cache.
- [ ] Installer failures, invalid constraints, backend errors, and task failures are not swallowed as failed update checks.
- [ ] Failure after preparation does not publish the staged revision; successful unchanged runs avoid redundant uploads.
- [ ] Concurrent tasks, API-server failover, worker loss, revision conflicts, eviction, and cancellation preserve valid readers and published revisions.
- [ ] Archive and manifest checks demonstrate that secrets, state, plans, and generated execution configuration are absent.
- [ ] Python uses the prepared interpreter and handles runtime changes with a rebuild rather than a broken relocated `.venv`.
- [ ] Browser verification covers both controls, persisted `false`, hit/miss/fallback displays, permissions, and error states.
- [ ] Benchmarks report cold/warm preparation, update-check latency, transferred bytes, and total execution time for each executor; no unmeasured speed claim is used as acceptance evidence.

Run focused Go and frontend tests, relevant builds/lint/typechecks, API contract checks, and executor integration checks after implementation.
Keep the source stable during verification and preserve current-run evidence.

## Open decisions and dependencies

- Select the authoritative shared cache backend and configuration after deployment requirements are known; no target environment is assumed by this plan.
- Confirm defaults for both UI settings and numeric quota/retention limits.
- Establish the supported tool/runtime capability matrix from the actual shipped images and configured executors; do not guess version numbers.
- Resolve tool-specific offline initialization and mixed lockfile/module behavior with fixtures before finalizing adapter APIs.
- Share the cache service with inventory refreshes; a refresh operation must not need a synthetic task to obtain or publish a cache.
- Defer arbitrary user-defined cache directories and additional package managers until their content/export contracts are specified.

## References

- [Python virtual environments](https://docs.python.org/3/library/venv.html): installed environments contain runtime/path assumptions and are generally not movable.
- [pip upgrade behavior](https://pip.pypa.io/en/stable/development/architecture/upgrade-options/): an ordinary installation can retain already-satisfied dependencies.
- [Terraform plugin cache](https://developer.hashicorp.com/terraform/cli/config/config-file): package caching does not replace dependency selection and its cache is not guaranteed concurrency-safe.
- [Terraform dependency lockfile](https://developer.hashicorp.com/terraform/language/files/dependency-lock): provider selections and checksums have defined scope.
- [OpenTofu plugin management](https://opentofu.org/docs/cli/plugins/): validate the OpenTofu adapter against its own supported mechanisms.
- [Terragrunt provider caching](https://www.gruntwork.io/blog/terragrunt-faster-runs-with-the-runner-pool-and-provider-caching): version-specific caching capabilities need integration checks against supported executors.

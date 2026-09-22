# Terraform state locking and graceful cancellation

## Scope and accepted decisions

Implement on `develop` and release `v2.20.0-ex.2.1.1`.
Restore the backend's default locking for init, plan and apply.
On cancellation, request graceful Terraform shutdown once and wait for process exit without automatic SIGKILL.
A stuck provider can therefore keep the task in `stopping`; preserving state persistence and lock ownership takes precedence over a bounded stop time.
The downstream Ansible version bump and deployment are separate operator work.

## Managed task groups — revised approved requirements

The initial free-text/global-group prototype was rejected and remains unpublished.
Replace it with first-class project-owned groups managed from the project sidebar.
A group can be shared with explicitly selected projects; there is no global flag or global namespace.
Only templates select groups, through searchable existing records. Per-task membership input is not supported.

Each group defines a concurrency limit and runner policy. Template membership may contain multiple groups.
Every group's restrictions apply cumulatively. Runner candidate definitions are intersected; an empty intersection is a visible UI/API validation error, with no override/selection escape hatch.
Temporary runner unavailability is a waiting condition, distinct from contradictory definitions.
Creation, modification, deletion and sharing are protected by explicit project permissions.
Consumers can use granted groups. Editing, deletion and sharing require the corresponding permission in the owning project, including when initiated from a consuming project's list.

### Shared group editing clarification

Keep owned and explicitly shared groups in the management list. Load the caller's roles for visible owning projects in `web/src/views/project/TaskGroups.vue`; use owning-project permissions for each action and pass the owning project to `TaskGroupForm.vue` and delete requests. Preserve the existing owner-scoped API authorization. Verify consumer-only denial and owner-authorized writes in API tests, UI permission/endpoint regressions, and the local browser preview.

Hiding all shared-group actions is simpler but blocks authorized owners working from a consuming project. Granting consumer write authority would violate ownership. Owner-scoped actions are selected because they preserve both access and the security boundary.

All group capacities are checked atomically with SQL-authoritative task start.
A short dispatcher guard transaction serializes admission only; running unrelated groups remain parallel.
No waiting task holds partial reservations. Starting, running, confirmation and stopping tasks consume capacity.
Uncertain runner recovery cannot release capacity while an execution may still be alive.
Group deletion and grant revocation must preserve referenced templates, queued tasks and active execution invariants.

### Implementation order

1. Add the managed group schema in the independent EX sequence (`2.20.5-ex1.3`) with group records, project grants, template ID bindings and immutable task membership IDs.
2. Add persistence, policy intersection, atomic capacity checks, safe updates/deletion and focused multi-database/HA tests.
3. Add permission-catalog entries, project group CRUD/sharing/search APIs, template selection validation and import/snapshot protections.
4. Build the sidebar management page, group editor, searchable template selector and concrete validation errors using existing Vue/Vuetify components.
5. Show the revised UI to Dennis and obtain the requested UI approval before publication.
6. Add matching Terraform-provider resources/data sources/template group attributes after the API is stable; preserve unrelated provider WIP.
7. Complete documentation, review and release verification below, then publish the requested server release and coordinate the provider release.

The original locking/graceful-stop fix remains in scope. The old preview is not the accepted design.

## Alternatives

- Keep disabling locking: rejected because independent runners and workstation applies can write the same state concurrently.
- Increase the generic 15-second timeout: rejected because no fixed timeout guarantees state persistence and unlock.
- Graceful Terraform-specific cancellation: selected by the user; preserve existing cancellation behavior for other application types.

## Implementation order

1. Add regression fixtures for default locking, explicit CLI overrides, pending cancellation, cancellation during init/plan/apply, and delayed lock release.
2. Remove the three injected `-lock=false` flags in `db_lib/TerraformApp.go`.
3. Add focused `db_lib/terraform_command_ex*.go` helpers for one interrupt and process-exit waiting, and pass the stop channel through `LocalAppInstallingArgs` from `services/tasks/local_executor_ex.go`.
4. Make Terraform init input polling cancellation-aware and synchronized so stopping during a confirmation prompt cannot strand the waiter.
5. Record the decision in the product Wiki ADR and describe the shipped behavior in `CHANGELOG.md`.
6. Verify package behavior, real Terraform state locking/unlocking, platform compilation, maintenance checks and the product release gates. Obtain a read-only Claude review.
7. Commit and push the reviewed fix, await green workflows on that exact SHA, complete the signed release dry run, then tag and publish the requested version. Verify signed assets and published images.

## Acceptance

### Docker execution verification

UI and push approval are granted. Before publication, build the current working-tree executable for Linux and run an isolated Docker Compose stack with two server processes, PostgreSQL, Redis and two real remote runners. Reuse the existing runtime image only as the tool/runtime layer and replace its Semaphore executable with the candidate; release image packaging remains a separate gate.

Exercise real shell/Terraform fixture processes through HTTP task creation: single shared state serialization, overlapping multiple-group memberships, capacity two with a third queued run, independent groups progressing, group runner intersection, template/inventory runner tags, inactive candidates, stop and subsequent admission. Submit through both server endpoints and observe persisted assignments, execution markers and terminal results. Run SQL admission regressions against PostgreSQL and MySQL as well as SQLite. Keep compose/configuration and synthetic fixtures isolated, retain failure/success evidence and remove only task-owned containers, volumes and images afterwards. Fix demonstrated defects with focused regressions before repeating the scenarios.

- init, plan and apply respect backend locking unless an operator explicitly overrides it.
- A stopped Terraform operation receives one graceful interrupt; its provider children are not terminated by Semaphore's generic stop policy.
- Task execution remains active until Terraform exits, even beyond 15 seconds, and does not advance to another stage after cancellation.
- init and workspace setup also observe pending cancellation.
- Existing Ansible and shell cancellation stays unchanged.
- Release is published only after the required evidence is green.

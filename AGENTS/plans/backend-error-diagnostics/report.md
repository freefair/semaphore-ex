# Backend error diagnostics assessment

## Result

The LDAP failure is part of a widespread backend diagnostics problem. This assessment identifies **25 finding groups**, including **10 independently reproduced cases**. Product source is unchanged; these are findings, not completed fixes.

The most consequential patterns are:

- Internal failures represented as invalid user input, missing resources, revision conflicts or authorization failures.
- Original causes discarded before any operator-visible diagnostic is recorded.
- Required database writes ignored while the outer operation reports success.
- Background workers ignoring the only error that could explain their lack of progress.

## Scope and evidence

Reviewed source: `152a50ca8e6c44273a505e006977e918c184d48f`, local `develop`, 2026-10-02.

- Parsed **679 non-test Go files** across the checkout, including compatibility implementations and tooling. No credentials or environment files were read.
- Enumerated **65 packages and 608 build-selected Go files** in the root and Enhanced modules on the current host. Of those, 588 are backend/CLI source; the remainder are 10 tool files, 9 test-harness files and one web embedding file.
- Swept API error writers, discarded error branches, ignored call results, compound error guards, and operation-to-caller propagation. Manually traced the finding groups below. A syntax candidate is not a confirmed defect: the initial scan produced 2,235 candidates, many of which are normal named returns, discarded non-error results or cleanup.
- Executed synthetic HTTP/controller, LDAP/TLS, notification-worker, SQL and restore reproductions. Ten cases reproduced the described defects. The tests assert current broken behavior; a passing characterization test does not mean the product is fixed.
- The initial TLS reproduction was blocked by the sandbox's listener restriction. The same test passed outside the sandbox against a disposable localhost TLS server. The listener and worker were closed, and the test database was in-memory.
- A requested Claude CLI second opinion could not run: `Not logged in · Please run /login`. There is no independent reviewer sign-off.

This is a source-wide pattern assessment with targeted failure injection, not an exhaustive execution of every error path. No real LDAP instance, remote database, Redis, Vault, Docker daemon or Kubernetes cluster was contacted. No UI, deployment or release claim is made. Platform-specific source was included in the syntax sweep, but other target platforms were not built.

Evidence labels: **R** = reproduced against current code; **S** = statically confirmed at the cited boundary and traced callers. Priorities rank remediation urgency within this assessment; they are not security vulnerability ratings.

## Findings

### F01 — P1 — Shared HTTP writer turns internal errors into empty HTTP 400 responses [R]

`api/helpers/write_response.go:35-56` handles known not-found, conflict and validation types, then treats every other error as a bad request. It logs the original error and a stack but writes **no response body**. It has no request context and attaches no request ID to the log. There are 216 `WriteError(w, err)` call sites in the root API alone; this count describes exposure to the helper, not 216 independently confirmed bugs.

Reproduction: a synthetic database disk-full error produces `400` with `body=""`. A caller cannot tell it from malformed input. A real invocation path is repository branch listing (`api/projects/repository.go:84-115`), which forwards Git/credential errors into this helper.

Fix direction: central classification of validation versus internal/dependency failures, a safe response with request ID, and a correlated operator diagnostic retaining the cause. Do not mark arbitrary `error` values as HTTP 400.

### F02 — P1 — Request decoding drops the field and reason [R]

`api/helpers/helpers.go:24-31` discards `json.Decoder` errors and emits an empty HTTP 400. The same pattern appears as generic text in `api/projects/project.go:161-173`, `api/projects/templates_ex.go:119-131,222-234`, `api/global_credentials.go:302`, and `api/notification_governance.go:365`.

Reproduction: `{"port":"wrong-type"}` decoded into an integer field produces `400` with an empty body. Syntax errors, type errors and missing required application values cannot be distinguished by clients.

Fix direction: safe field path, expected type or syntax location, stable validation code and request ID; omit supplied values and raw bodies.

### F03 — P1 — LDAP loses both technical cause and setup stage [R]

`services/identity/ldap_client.go:96-106,141-143,170,196-202,351-353` replaces connect, bind and search errors with `ErrLDAPProviderUnavailable`, preserving only a broad operation label. `api/ldap.go:113-117` discards the returned readiness result on failure; `api/ldap.go:256-259` maps provider failures, referrals and duplicate identities to the same 503 code without logging their cause. The existing UI (`web/src/components/LdapCapabilityPanel.vue:1000`) further describes all of them as an inability to reach the provider securely.

Reproductions:

- A self-signed TLS endpoint becomes exactly `LDAP provider unavailable: connect`; `errors.As` cannot recover the certificate error.
- A service-bind failure with readiness code `service_bind_failed` becomes only `{"error":"LDAP_PROVIDER_UNAVAILABLE"}`, with no log output from the controller.

Fix direction: preserve the cause; distinguish connect/TLS, service bind, search, user bind, referral and duplicate identity. Return safe detailed diagnostics and failed readiness to the admin test UI. Keep public login presentation independently controlled.

### F04 — P1 — Admin error writers discard unknown causes without logging [R/S]

Three controller paths were reproduced with injected database failures:

| Boundary | Observed response | Log output from boundary |
|---|---|---|
| `api/global_credentials.go:256-296` | 500, `Global credential service failure` | None |
| `api/audit_webhook.go:258-274` | 500, `Audit webhook operation failed` | None |
| `api/notification_governance.go:427-440` | 503, `Notification governance is unavailable` | None |

Additional static examples: `api/options.go:32,63`, `api/user_options.go:29,67`, `api/user.go:133`, `test/edition-contract/enhanced/api/roles.go:92,312`, `test/edition-contract/enhanced/api/policy_guardrail_controller.go:475-486` and `test/edition-contract/enhanced/api/projects/deployment_window_controller.go:415-427`. These hide the underlying failure behind a generic body or bare status. Some write an audit outcome, which records that an operation failed but not why.

Fix direction: migrate each boundary to the shared diagnostic contract; retain endpoint-specific categories while recording unknown causes with request context.

### F05 — P1 — Audit webhook service destroys causes before the controller sees them [S]

`test/edition-contract/enhanced/services/server/audit_webhook_svc.go:103,137,150,163,171,203,224,228,259,271,663,674` constructs fresh strings such as `load audit webhook configuration`, `store audit webhook credential` and `deliver audit webhook test` without wrapping the failing database, encryption or delivery operation. Adding logging only to the controller would still lose the actual reason.

Its background loop (`:395-425`) returns silently on configuration/claim failures and ignores `processDelivery` and `failClaimedDelivery` errors. Metrics describe failure counts, not causes.

Fix direction: retain causal errors in the service; log bounded operation diagnostics for worker failures and distinguish delivery failure from failure to record delivery state.

### F06 — P1 — Notification worker drops operational errors entirely [R/S]

`test/edition-contract/enhanced/services/server/notification_dispatcher.go:127` ignores `DispatchOnce(ctx)`. That method returns claim, lookup and state-write database errors (`:145-157`). No caller records them. Reproduction: the outbox claim fails with an injected database error; the loop emits no log, and `Close()` returns nil.

Adapter diagnostics are also too coarse: Opsgenie (`test/edition-contract/enhanced/services/server/opsgenie.go:103-108,143-148`) discards transport causes and response-read errors; PagerDuty and ServiceNow similarly reduce failures to dispatch outcomes. Their result model needs enough safe dependency detail to explain retries/permanent failures without storing response payloads.

Fix direction: handle worker errors explicitly; include delivery/destination IDs, operation, dependency classification and safe status/result code.

### F07 — P1 — Global credential resolution repeatedly erases the reason [S]

`test/edition-contract/enhanced/services/server/global_credential_external_adapter.go:51-69` records parse failures only as `adapter.invalid=true`. Its resolver (`:80-101`) uses the same `global credential provider unavailable` for invalid configuration, missing provider, missing bootstrap environment value, an external read error and an empty result.

`test/edition-contract/enhanced/services/server/global_credential_runtime.go:145-170` replaces decryption/provider errors again with generic material-resolution failures. Authorization queries (`:123-133`) also become policy reasons. The task caller (`services/tasks/TaskRunner.go:406`, `api/runners/runners.go:425`) ignores the returned resolution error and marks the task generically blocked.

Fix direction: carry a safe diagnostic through configuration, provider resolution, authorization lookup and task dispatch; expose the operation and reason to operators instead of only the blocked outcome.

### F08 — P2 — Vault/OpenBao retains categories but loses actionable details [S]

`test/edition-contract/enhanced/services/server/vault_runtime.go:642-681,710-725` maps transport, response-read and decode errors to `SecretProviderError{Category, Operation}` without a cause. HTTP responses are grouped and their precise status is lost. DNS, refused connections and several other transport failures become `unavailable`; TLS errors become only `tls`.

`api/projects/secret_storages_ex.go:20-22` drops the error entirely and returns only the health DTO. If provider setup fails before meaningful health is produced, the response offers no explanation of that error.

Fix direction: extend safe diagnostic information while preserving the existing useful category/operation distinction.

### F09 — P1 — Database failures are reported as optimistic-lock conflicts [R/S]

`db/sql/docker_execution_policy.go:58-67` maps **every failed initial INSERT** to `ErrDockerExecutionPolicyRevisionConflict`. The Kubernetes equivalent is `db/sql/kubernetes_execution_policy.go:57-66`. `test/edition-contract/enhanced/db/sql/policy_guardrail.go:107-108` does the same for a draft INSERT. Several `RowsAffected()` errors are also conflated with a legitimate mismatch.

Reproduction: an isolated in-memory SQLite database has no `docker_execution_policy` table. The underlying SQL failure says `no such table`; `SaveDockerExecutionPolicy` returns only `Docker execution policy revision conflict`.

Fix direction: translate only verified uniqueness/expected-revision conflicts; retain other SQL failures. Separate failure to read an affected-row count from an actual zero-row update.

### F10 — P1 — Database lookup failures become not-found or invalid-selection errors [S]

Examples: `db/sql/task_group_catalog_ex.go:113,167,205` and `db/sql/template_task_groups_ex.go:48,57`. Failed SQL reads become `ErrNotFound`, `group runner is not available` or `task group selection is invalid`. `test/edition-contract/enhanced/db/sql/terraform_inventory.go:160-167` also converts storage errors to a lock error.

Fix direction: distinguish no rows from database failures and legitimate lock contention; preserve cause and operator context.

### F11 — P1 — Session database outages look like invalid sessions [S]

`api/auth.go:43-48` and `services/session_svc.go:46-51` return `(nil, false)` for every `GetSession` error, without logging. Missing/expired sessions and an unavailable database are indistinguishable to callers.

Fix direction: retain non-enumerating authentication behavior where required, but distinguish operational failure internally and provide correlated operator evidence. Cookie rejection alone is not a defect.

### F12 — P1 — Capability resolution loses the underlying failure [S]

`api/capabilities.go:43-49` logs safe event fields and a fixed message but omits the error. The delegated middleware (`:73-83`) substitutes an unavailable capability snapshot without a diagnostic. `api/projects/secret_storages_ex.go:59-73` maps non-policy failures to `CAPABILITY_PROVIDER_ERROR` without logging.

Fix direction: retain the failed dependency/operation alongside the deliberate fail-closed capability decision.

### F13 — P1 — Audit failure diagnostics themselves discard causes [S]

`services/audit/facade.go:41-46,65,80-105` replaces validation, webhook preparation, database and file errors with strings such as `audit persistence failed`. The caller often logs only `event.SafeFields()` and a fixed sentence, for example `api/ldap.go:223`, `api/enhanced_audit.go:65,152`, and `api/helpers/event_log.go:51-59`.

Several Enhanced paths ignore audit errors outright, including cross-project template controllers and workflow approval/artifact code. This is distinct from `services/audit/recorder.go:49-67`, which already logs the cause and event identifiers and should be used as a positive reference.

Fix direction: preserve database and file-sink causes separately, attach event/request IDs, and explicitly handle failures in callers that currently ignore them.

### F14 — P1 — Project restore can report success after data was not restored [R/S]

`services/project/restore.go:552,557,566,842` ignores failures creating integration matchers, extract values and aliases, then returns success. The initial duplicate-project lookup (`:725-734`) also proceeds after a query failure. Backup construction drops some missing reference lookups (`services/project/backup.go:307-546`), including role references (`:462`).

Reproduction: inject a failed `CreateIntegrationAlias`; `BackupFormat.Restore` returns a created project, nil error and no log.

Fix direction: propagate required-write failures with entity/index context and report partial completion explicitly. Review transactional boundaries separately; do not silently redesign restore semantics in the diagnostic patch.

### F15 — P1 — Terraform backend failures are hidden by bare statuses or generic text [S]

`test/edition-contract/enhanced/api/terraform.go:49-63,87-96,114` drops alias lookup, credential-decryption, state-read and encryption causes behind 404/503. The admin inventory path (`test/edition-contract/enhanced/api/projects/terraform_inventory.go:246`) similarly hides a state-decryption failure.

`services/tasks/terraform_backend_ex.go:21,35,46,60-64` replaces alias query, configuration and credential errors before task dispatch.

Fix direction: retain the HTTP backend protocol and authentication boundaries, but supply safe operation diagnostics to operators and authorized management views.

### F16 — P2 — Runner administration and reconciliation obscure failures [S]

`api/runners.go:85,178`, `api/runners/runners.go:628-690,1039`, and `api/runners_ex.go:181,224` return not-found, generic conflict, empty 500 or `Unknown error` for underlying service/database failures. Enhanced project-runner paths (`test/edition-contract/enhanced/api/projects/projects.go:61,95,117,169,231,362`) similarly aggregate causes; create failures become HTTP 400 and middleware lookup failures become 404.

Fix direction: classify lookup versus persistence versus stale-session failures, retaining runner/session/attempt context in diagnostics.

### F17 — P2 — Workflow and cross-project authorization hide infrastructure failures [S]

`test/edition-contract/enhanced/services/server/cross_project_template_svc.go:27,49,59,69,72` collapses database errors into `ErrNotFound`. Workflow definition normalization (`test/edition-contract/enhanced/services/server/workflow_definition_svc.go:46,65,93,177`) does the same. Approval identity resolution (`test/edition-contract/enhanced/services/server/workflow_svc.go:2131,2137`) becomes `workflow approval actor is not eligible`; preflight may replace resolution failures with hidden-reference findings (`:414,476,479,522`).

Concealing inaccessible resources is deliberate and can remain appropriate. The defect is the lack of a separate operator-visible cause for infrastructure failures.

Fix direction: preserve authorization policy while retaining failure category and safe diagnostic context internally.

### F18 — P1 — HA initialization and heartbeat failures can be silent [S]

`test/edition-contract/enhanced/services/ha/ha.go:46,76,92,96,126,130,231,235` returns nil after cluster identity or Redis option errors, indistinguishable from intentionally disabled services. `test/edition-contract/enhanced/services/ha/registry.go:118` ignores heartbeat/database refresh errors; `NodeCount` (`:103`) reports zero on query failure. The transport ping path in `test/edition-contract/enhanced/services/ha/ws_broadcaster.go:71` closes the subscription without retaining its underlying cause.

Other broadcaster paths already report degraded health and log publish/reconnect errors. Preserve that working behavior.

Fix direction: make initialization failure distinct from configured disablement, and record heartbeat/registry failures with node and dependency context.

### F19 — P1 — Failure-state writes can fail silently [S]

Examples:

- LDAP failed readiness: `test/edition-contract/enhanced/pkg/features/ldap.go:358,371`.
- LDAP group reconciliation decision/history: `test/edition-contract/enhanced/pkg/features/ldap_group_mapping.go:136,140,168,318` in that directory.
- Secret-sync status: `services/server/secret_storage_svc_ex.go:153,170`.
- Workflow-trigger invocation/result persistence: `test/edition-contract/enhanced/services/server/workflow_trigger_svc.go:473-533,651-738`.
- Workflow reconciliation status: `test/edition-contract/enhanced/services/server/workflow_svc.go:3274` in that directory.

The primary operation can fail while its visible state/history fails to update, leaving a misleading previous state and no explanation of the failed update.

Fix direction: report primary and status-write failures separately; retain appropriate retry behavior and existing lease/fencing guarantees.

### F20 — P2 — Task preparation drops prerequisite causes [S]

`services/tasks/TaskRunner.go:302` logs only `Failed to load host mappings for task dispatch`. Inventory, repository and vault credential lookups in `services/tasks/TaskRunner_ex.go:113,120,131,149` return fresh unavailable strings. `services/tasks/task_ssh_bindings_ex.go:67` replaces a decryption cause with a key-ID-only error. Execution preflight and snapshot parsing also replace validation/encoding errors with broad messages (`services/tasks/execution_preflight.go:452,487,495`, `services/tasks/execution_preflight_token.go:66,74,78`).

Fix direction: retain prerequisite operation and cause; render safe actionable task diagnostics tied to task/project IDs.

### F21 — P2 — Docker/Kubernetes cleanup and stop outcomes lose technical reasons [S]

Enhanced Docker executor cleanup (`test/edition-contract/enhanced/services/tasks/docker/executor.go:695-709`) emits telemetry and generic log messages, dropping the client error. Stop/remediation paths (`:634,640`, `test/edition-contract/enhanced/services/tasks/docker/remediation.go:49-143`) compress different failures into daemon-unavailable or still-running evidence. Kubernetes cleanup (`test/edition-contract/enhanced/services/tasks/k8s/executor.go:382,395`) and ignored log-stream results (`:195,293`) have similar gaps.

The Docker and Kubernetes clients themselves often wrap causes correctly. The loss occurs when those errors become lifecycle outcomes.

Fix direction: retain the lifecycle safety result while also recording operation/resource identity and a safe technical reason. Do not replace stop confirmation with an optimistic cancelled state.

### F22 — P2 — Read operations may return incomplete success [S]

`api/projects/tasks_ex.go:193` breaks pagination on a query error and returns the already collected tasks as an apparently normal result. Notification routing preview skips any destination lookup error (`test/edition-contract/enhanced/services/server/notification_governance_svc.go:301`). `db/Task.go:209,214` silently returns no incoming version when task/template lookups fail.

Fix direction: surface failed completeness checks or return an explicitly marked partial result. Do not equate an unavailable lookup with an empty result.

### F23 — P2 — Invalid filters are silently ignored or turned into defaults [S]

`api/projects/inventory_hosts_ex.go:46-47,122` ignores integer parsing errors. For example, a nonnumeric inventory filter becomes zero rather than a field-validation error. `api/helpers/query_params.go:48` silently ignores invalid owner IDs and can select the unowned-only behavior instead.

Fix direction: reject malformed supplied filters with field-level diagnostics; distinguish omission from invalid input.

### F24 — P2 — CLI/import and Ansible paths confuse unavailable input with absence [S]

`cli/cmd/project_import.go:77` suppresses directory-walk errors, so inaccessible paths are skipped without explaining why. `db_lib/AnsibleApp.go:159-162` treats every `os.Stat` error as a missing requirements file and skips installation successfully, including permission and I/O errors.

`services/export/TaskStageResult.go:76-81` prints a parse failure without its cause and proceeds to store the parsed map, allowing the later write result to replace the decode failure.

Fix direction: treat only `IsNotExist` as optional absence, propagate other I/O errors, and stop or explicitly report incomplete imports.

### F25 — P2 — Stored-data decode failures are mislabeled as user/domain errors [S]

Examples include `test/edition-contract/enhanced/db/sql/policy_guardrail.go:897,900`, `test/edition-contract/enhanced/services/server/deployment_window_governance_svc.go:116`, and policy service stored-source parsing. They map invalid persisted data to `ErrInvalidOperation` or similar broad values. Template survey decoding (`db/Template.go:559`, `db/sql/template.go:493`) logs identifiers and a hint but omits the parser reason.

Fix direction: distinguish corrupted/incompatible stored data from rejected current input; retain parser location/type and resource ID without logging full stored JSON.

## Examples deliberately not counted as defects

- Naked `return` in a function with a named `err` result propagates that error; the syntax scan alone cannot call it swallowed.
- `_, err := ...` discards a non-error result, not the error.
- Hash writes, valid primitive JSON marshaling and documented cleanup/rollback discards are not equivalent to ignoring required writes.
- `api/runners/runners.go:418` looks suspicious in isolation, but `collectTaskAccessKeys` logs the decryption cause with IDs at `:459,476,490,506,520,537`.
- OIDC/TOTP generic external responses are not automatically defects. The TOTP and OIDC mapping default branches log unexpected causes (`api/totp.go:525`, `api/oidc_group_mapping.go:316`); OIDC login and legacy LDAP paths also contain cause-aware logging.
- Schedule and workflow-trigger schedulers and runner reconciliation have multiple working cause-aware log paths. They do not need blanket replacement.
- The newer audit recorder uses structured error logs. Its void `Record` signature does not itself imply an ignored storage failure.

## Repair order

1. Establish the shared diagnostic and HTTP classification contract (F01/F02); implement the LDAP admin test end to end (F03), including frontend display and browser verification.
2. Fix false success and silent processing: restore (F14), worker loops (F05/F06/F18) and failed status writes (F19).
3. Correct false domain classifications at their source (F09-F12/F17), then migrate generic controller boundaries (F04/F15/F16).
4. Preserve safe dependency detail through credentials, Vault and task lifecycle adapters (F07/F08/F20/F21).
5. Close partial-result, input and stored-data diagnostics (F22-F25), and verify each area with failure injection.

The proposed contract and trade-offs are in `decision.md`. Implementation needs separate focused commits and behavior tests; a single global `log.Error(err)` addition would leave causes already destroyed and could expose sensitive values.

## Open work

- The LDAP setup problem is diagnosed in the source, but not repaired or deployed. The actual cause on the user's instance remains unknown because instance/version and live diagnostics were not provided.
- All 25 finding groups require remediation or an explicitly documented intentional fallback with an operator diagnostic.
- No full product build, full regression suite or browser QA was run: executable product source was unchanged.
- Independent Claude review remains unavailable until the CLI is authenticated.

# Task-scoped repository SSH agent

## Scope

Implement `AGENTS/tasks/2026-09-18-task-agent-offers-repository-key.md` on
`feat/task-agent-repository-key`, then release the next verified version on the
existing `v2.20.0-ex.N` line.
The inspected remote has only `v2.20.0-ex.1`; the intended release is
`v2.20.0-ex.2`, subject to another tag check before publication.

The user expanded the implementation scope to additional SSH identities selected
for templates/tasks/runs and project defaults or always-applied identities.
The task agent must make these usable by dependency installers, not only the
initial Repository clone.
The existing authenticated runner-dispatch API is the preferred credential
resolution boundary; a direct container-agent API is an alternative requiring
a new run-scoped capability and explicit network reachability.
The user approved keeping the protected container credential bundle and excluded
repository-specific key selection on a shared Git host from this delivery.
Each additional SSH identity must support a list of hosts (for example one key
for github.com and gitlab.com, another for bitbucket.com and redmine.com).
The user approved automatic client-side host selection, not enforcement against
task-controlled clients. With fewer than five distinct agent identities, host
lists are optional and the common agent may offer the keys normally. From five
distinct identities onward, host routing is required. Count Repository and
Inventory identities too and deduplicate by public-key identity. Explicit host
lists apply at every size. SSH server authentication-attempt limits remain
server-configurable; five is a product threshold, not an SSH guarantee.

## Implementation order

1. Reproduce the missing repository identity with an isolated regression test
   before changing executable source.
2. Extend the task SSH lifecycle in `services/tasks/local_executor*.go` and, if
   necessary, `pkg/ssh/agent.go`.
   Combine the inventory and template repository SSH identities in one task
   agent, preserve inventory authentication, and keep clone authentication
   unchanged.
3. Expose the task socket to application and requirements environments.
   Verify remote-runner hydration through `services/runners`.
4. Extend the container task plan to provide the same identity set without
   introducing host mounts or widening executor policy.
   Reuse the approved protected credential bundle outside the workspace.
   Extend persisted key bindings, API validation and configuration UI for the
   expanded scope after those decisions are resolved; include execution
   snapshots, permission checks and assignment fencing in the design.
5. Verify key combinations, application environments, real nested Git over a
   disposable SSH server, container behavior, and teardown after success,
   failure and cancellation.
6. Update task/key-store documentation, ADR, product status and changelog.
   Obtain independent Claude review and dedicated Terra security review, then
   run the repository release gates against a stable source tree.
7. Publish docs before the root submodule pointer, integrate the reviewed
   feature branch into `develop`, and require green exact-commit CI.
   Run the signed release dry run, verify its signature, then tag and publish
   `v2.20.0-ex.2` with archives, deb/rpm packages and checksums.

## Decisions and alternatives

- A shared task agent meets the specified identity-set and lifecycle contract.
  Reusing only the clone agent would couple independent clone and task
  lifetimes; two task sockets would make identity selection ambiguous.
- Private keys stay out of workspace files and environment variables.
  Public identity selectors may be needed so `IdentitiesOnly=yes` selects the
  intended inventory or repository key from the shared agent.
- Container execution already transfers private inventory credentials through
  a protected read-only bundle at `/semaphore/bundle`, outside `/workspace`.
  Reusing this boundary avoids changing Docker/Kubernetes transport and policy.
  A fully fileless credential transport would be a larger architectural change.
  The user approved this existing transport. Host socket mounts and broader
  Kubernetes permissions are not part of the design.
- Multiple deploy keys on one Git host require selection before authentication:
  an SSH login with the wrong repository's accepted key does not retry another
  key after Git rejects repository authorization.
  Repository-specific routing on the same host is explicitly out of scope.
- Keep host selection explicit and value-free in persisted bindings. A client
  SSH configuration can select a key by hostname, while a generic signing agent
  cannot independently prove that hostname. Strong destination enforcement
  needs authenticated host-key bindings and a compatible agent, and cannot be
  claimed while task code can read private keys from its credential bundle.
- Preserve unrelated untracked fork-history plans.
  Use a clean isolated verification checkout if the full gate rejects those
  unrelated files; do not stage them or weaken the gate.

## Additional-key configuration implementation

1. Add a shared value-free binding DTO `{access_key_id, hosts}`. Host lists
   contain exact canonical hostnames; repository/path routing and wildcard
   patterns are outside this first delivery. Hosts may be empty for the small
   agent case; the runtime validates the effective, deduplicated identity set.
2. Persist `default_ssh_keys` and `always_ssh_keys` on projects and nullable
   `ssh_keys` on templates and tasks. `null` inherits the parent selection;
   `[]` replaces it with an empty selection. Union project always bindings
   afterward. Reject conflicting keys for the same explicitly routed host.
   Use bounded JSON columns and a shared scanner/valuer rather than a new
   separately editable policy resource: this retains existing CRUD permissions
   and transactional persistence. Validate referenced key ownership and SSH type.
3. Include resolved value-free bindings in immutable execution snapshots,
   validate them in preflight and resolve current key material at dispatch.
   Keep project defaults and always bindings in the project that owns the
   selected template, including cross-project execution.
4. Add a reusable Enhanced key/host-list editor to project settings, template
   configuration and the new-task dialog. Show inheritance and always-applied
   bindings explicitly. Preserve existing permission-controlled save flows.
5. Extend local/remote hydration and container bundles with the selected keys,
   and implement the approved automatic host selection. A Repository hostname
   can supply its implicit route; an Inventory key needs an explicit host list
   when routing is required because its managed hosts cannot generally be
   inferred. Explicit routes take precedence over legacy fallback selection.
   Preserve explicit administrator/task SSH overrides and document that they
   bypass generated routing. Test inheritance, empty overrides, conflicts, denied references,
   snapshots, key rotation, real host selection and container execution.

The local review gate includes preservation of configured SSH host verification
and generated SSH configuration, Git dependencies authorized by either selected
identity, and explicit Ansible configuration precedence. Agent initialization
must complete before its value is returned to a caller. Regressions for these
contracts are required before committing the local implementation.

Retain failing and passing regression evidence in the task journal while work
is active, and report the published release tag and verified assets.
Clean up disposable servers, credentials, containers and temporary tooling.

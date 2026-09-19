# Task SSH agent offers the repository key

**Date:** 2026-09-18
**Status:** Implemented and regression-verified — signed release verification in progress
**Requested by:** a consuming deployment that runs Ansible and Terraform pipelines on a local runner
**Size:** M

## Goal

Every task gets an SSH agent that offers the **template repository's access
key**, whatever the app (Ansible, Terraform/OpenTofu/Terragrunt, shell, …) and
whether or not the inventory has an SSH key. A git call the task starts itself
— `ansible-galaxy collection install git+ssh://…`, `terraform init` with a
`git::ssh://` module source, a submodule, a nested `git clone` — then
authenticates with the same key Semaphore used to clone the repository.

Consumers can then drop the host-level workaround they use today: a private
key written onto the runner host plus an `/etc/ssh/ssh_config.d` drop-in that
points git at it. That key lives outside Semaphore's Key Store, cannot be
rotated or scoped per project, and is invisible in the UI.

## Current behaviour (measured in code, `develop` at 050c5c20)

- The repository key is used only for Semaphore's own clone:
  `db_lib/GoGitClient.go:42-52` installs `Repository.SSHKey` with
  `db.AccessKeyRoleGit`, and `pkg/ssh/agent.go` `GetGitEnv()` hands the socket
  to that one git process. Nothing the task starts afterwards sees it.
- The task's own agent is built from the **inventory** key only:
  `services/tasks/local_executor.go:1071`

  ```go
  func (t *LocalExecutor) getSSHAgentEnv() string {
      if t.Inventory.SSHKey.Type == db.AccessKeySSH && t.Inventory.SSHKeyID != nil && t.sshKeyInstallation.SSHAgent != nil {
          return fmt.Sprintf("SSH_AUTH_SOCK=%s", t.sshKeyInstallation.SSHAgent.SocketFile)
      }
      return ""
  }
  ```

  Used at `local_executor.go:865` and `:921` (requirements install, e.g.
  `ansible-galaxy`) and `local_executor_ex.go:149` (the task's own
  environment). A Terraform template has no inventory SSH key, so its tasks
  get no agent at all.
- The task environment is assembled from scratch (`db_lib/LocalApp.go`), so
  the runner process's own `SSH_AUTH_SOCK` never reaches a task either.
- Container execution: `services/tasks/container_task_plan.go:336` drops
  `SSH_AUTH_SOCK` from the passed environment and `:469` re-exports
  `${workspace}/.semaphore/ssh-agent.sock` when it exists, with
  `GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes …'`. The same key set must be
  available through that socket.

Observed symptom on the consumer side, same task run and the same key:
Semaphore's clone of the project repository succeeds, `ansible-galaxy
collection install git+ssh://…` fails with `Permission denied (publickey)`.

## Scope

1. The task agent holds the repository key when `Repository.SSHKey.Type` is
   `ssh`, in addition to the inventory key when that one exists. `pkg/ssh`
   `Agent` already takes a list of `AgentKey`s, so this is one agent with up
   to two keys, not two sockets.
2. `SSH_AUTH_SOCK` for that agent is set for every app, including
   Terraform/OpenTofu/Terragrunt and shell templates, and for the
   requirements-install step.
3. Container executor: the workspace socket offers the same key set.
4. Remote runners: the repository key already travels to the runner
   (`services/runners/executor_factory.go:81`); the agent is built there the
   same way.
5. The agent's lifetime is the task's: created at task start, socket removed
   at task end, including on failure and cancellation.
6. Docs: the task-environment / access-key documentation states that the
   repository key is offered to the task through `SSH_AUTH_SOCK`.

## Not in scope

- No new key type, no new template field and no global credential. If a
  per-template opt-out turns out to be needed, it is a follow-up issue, not
  part of this change.
- No change to how Semaphore's own clone authenticates.
- The private key is never written to disk in the workspace and never put
  into an environment variable. Agent only.
- Inventory repositories (`Inventory.Repository.SSHKey`) are not added in this
  change; name it in the follow-up if it matters.

## Expanded implementation scope

The user additionally requires multiple SSH-key bindings for tasks/runs and
project defaults or always-applied keys, so dependency installers can access
private repositories beyond the template's primary Repository.
This supersedes the original restriction on adding configuration fields when
those fields are required for the expanded key selection.

The user also requested evaluating runtime Keystore access through an API.
The existing authenticated runner-dispatch API already resolves task-bound
credentials; a direct container-agent API would need a short-lived run-scoped
capability and permitted network access to Semaphore.
The user approved retaining the protected container credential bundle outside
the workspace and excluded repository-specific key selection on a shared Git
host from this delivery. Each additional SSH key must support a list of hostnames.
Automatic client-side selection is approved; enforcement against task-controlled
clients is outside scope. Host lists are optional below five distinct task-agent
identities and required from five onward, counting Repository and Inventory keys
and deduplicating public-key identities. Explicit host lists apply at every size.

## Acceptance criteria

- An Ansible template whose repository uses SSH key K and whose inventory has
  **no** SSH key: a playbook task running `ssh-add -l` lists K's fingerprint;
  `ansible-galaxy collection install git+ssh://<host>/<repo>.git` from a
  requirements file succeeds against a host that accepts only K.
- The same with an inventory SSH key I: `ssh-add -l` lists both I and K;
  Ansible still connects to the managed hosts with I.
- A Terraform template whose repository uses K: `terraform init` with a
  `git::ssh://` module source succeeds, and `ssh-add -l` in a local-exec lists
  K.
- A shell/bash template: `ssh-add -l` lists K.
- Container execution: the same `ssh-add -l` checks hold inside the container.
- Repository with a `none` key: no agent is created and `SSH_AUTH_SOCK` is not
  set, exactly as today.
- After the task ends (success, failure, cancel) the socket file no longer
  exists.

## Tests

- Unit: the function that assembles the agent keys returns
  {repo}, {inventory, repo}, {inventory}, {} for the four combinations of
  key types; a `none`/`login_password` repository key contributes nothing.
- Unit: the environment builder sets `SSH_AUTH_SOCK` for Ansible, Terraform
  and shell apps when the agent exists and omits it otherwise.
- Integration: a task run against a local SSH server that accepts only K
  performs a nested `git ls-remote` successfully; the same run with the
  change reverted fails. This is the test that must fail when the feature is
  lost in a future upstream merge.

## Delivery

- Branch `feat/task-agent-repository-key`, Conventional Commits.
- Released as the next `-ex.N` on the current line (after `v2.20.0-ex.1`), with
  `.deb`/`.rpm` and the checksum list like the existing releases.
- Report back: the release tag. The consumer then bumps its pinned server
  version and removes its host-level git key.

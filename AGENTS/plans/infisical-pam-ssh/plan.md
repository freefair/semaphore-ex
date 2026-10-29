# Infisical PAM SSH Integration

## Goal

Tasks reach SSH targets through Infisical PAM when a project opts in. The
Infisical gateway injects the target credential; neither Semaphore nor the
runner holds the target password, key or certificate. Projects without an
Infisical storage are unaffected.

## Research Basis (Infisical CLI v0.44.10, `main`, 2026-10-28)

- `infisical ssh` is removed; SSH access exists only as Infisical PAM.
- `infisical pam access` rejects machine identities. `pam agentic access`
  accepts them but exposes unauthenticated loopback ports and announces them in
  an agent-oriented Markdown file.
- Client protocol (MIT-licensed code outside `ee/`):
  1. Machine identity login, e.g. `POST /api/v1/auth/universal-auth/login`.
  2. `POST /api/v1/pam/accounts/access` with `{path, duration, reason}` returns
     a session: session id, gateway id, optional direct address, relay host,
     relay mTLS client certificate/key and server chain, gateway mTLS client
     certificate/key and server chain.
  3. Dial the direct address, or the relay (TLS, default port 8443, mTLS).
  4. Inside that transport, gateway mTLS with ALPN `infisical-pam-proxy`; the
     gateway certificate carries `urn:infisical:gateway:<id>`. Routing is
     derived from the client certificate.
  5. The stream is a plain SSH connection. The gateway accepts any client
     authentication, presents an ephemeral host key, and authenticates to the
     target with the account credential.
  6. End with ALPN `infisical-pam-session-cancellation`, falling back to
     `POST /api/v1/pam/sessions/<id>/end`.
- The accessible-accounts response carries no target host, so host-to-account
  mapping is explicit configuration.
- PAM is licensed (`backend/src/ee`); a local self-hosted instance cannot be
  used for end-to-end verification without a license.

## Accepted Limitations

- The gateway does not verify the target host key
  (`ssh.InsecureIgnoreHostKey()`), and its own host key is ephemeral. PAM
  routes therefore disable client host-key checking. Dennis accepted this and
  reports it to Infisical.
- WinRM has no client-side PAM proxy and is out of scope.

## Design

| Option | Pro | Contra |
|---|---|---|
| Wrap `infisical pam agentic access` | Uses a supported command | Open loopback ports, Markdown port discovery, CLI in every runner image |
| Native client + per-task broker socket + `ProxyCommand` (chosen) | No TCP listener, file-permission protection like the task SSH agent, lazy sessions, no extra binary | Undocumented session API can change |

- **Storage:** new secret storage type `infisical` with site URL, optional CA,
  universal-auth client id, client secret through the existing credential
  source (local, env, file), PAM session duration, and host mappings
  `host -> folder/account`.
- **Broker:** the local executor starts one broker per task on a Unix socket in
  the task's private SSH directory (0700). It logs in with the machine
  identity, creates a PAM session lazily on the first connection to a host,
  reuses it for the task, and ends every session when the task finishes.
  Tokens and session certificates stay in runner memory.
- **Tunnel:** the routing `ssh_config` gets one `Host` block per mapped host
  with `ProxyCommand "<semaphore> infisical-pam-connect --socket <s> --host %n"`
  and host-key checking disabled for that block only. The subcommand splices
  stdio to the broker socket.
- **Conflicts:** a host mapped to PAM and to an explicit task SSH route or host
  mapping is rejected, as existing routing conflicts are.
- **Scope of execution:** local executor (server-local and remote runners).
  Container tasks are a later slice; until then they do not apply PAM routes
  and the task log says so.
- **Failure policy:** approval, MFA or missing reason fail the connection with a
  categorized error; nothing waits interactively.

## Slices

1. `pkg/infisical`: API client (login, access, end), transport (direct/relay,
   gateway mTLS, identity check), with tests against fake API, relay and
   gateway servers.
2. Broker and `infisical-pam-connect` subcommand, with an end-to-end test
   through a fake gateway to an in-process SSH server.
3. Storage type, validation, API fragment in `api-docs-ex.yml`, contracts.
4. Executor integration in routing (`_ex.go`), conflict checks, cleanup.
5. UI: focused component in `SecretStorageForm.vue` for the new type; fork
   locale messages.
6. Wiki page and ADR; Implementation-Status.
7. Terra security review, Claude review, `tools/upstream-sync/verify.sh`,
   browser check of the form.

## Verification Without a Licensed Instance

Fake Infisical API (`httptest`), fake relay and gateway with a test CA, and an
in-process `golang.org/x/crypto/ssh` server cover the protocol. A run against a
real licensed Infisical instance remains open until one is available.

## Security Review Outcome

- Gateway identity: the gateway certificate must name the session's gateway;
  sessions without a gateway id are rejected (stricter than the CLI).
- Same-user local tasks can reach each other's broker sockets. Not mitigated:
  local tasks already share the service user's configuration, keys and SSH
  agent sockets; separate runners or container executors provide isolation.
- Relay and gateway addresses from the configured API are not filtered against
  private ranges, because gateways normally run in private networks.

## Code Review Outcome

- Routes are reloaded on the node that answers the runner poll (HA).
- Runners acknowledge support with `X-Runner-Infisical-Pam: 1`; routed tasks
  fail on runners without it. Container runners get neither routes nor secret.
- Task-private ControlPath (short `/tmp/sxcp*` directory, forced through the
  wrapper and Ansible environment); `sftp`/`scp` receive `-F routing.conf`.
- Independent cleanup budgets per session, broker reply deadline and
  cancellation in `Connect`, unusable sessions are ended, renewal margin is at
  most a quarter of the session duration.

## Follow-up: Task-Private ControlPath and Execution Review

- Every local task gets the private `/tmp/sxcp*` control path directory
  (`local_executor_ssh_control_path_ex.go`), not only PAM tasks. It is chowned
  to `process.user` when configured and filtered out of container task
  environments. Wiki ADR-0051.
- The execution review lists one `secret_storage` reference per routing
  storage with all routes (`ssh: host → account, …`, or the host count above
  the 2 KiB display bound). Invalid routing is a denial
  (`infisical_pam_invalid`), a container executor image a warning
  (`infisical_pam_skipped`); both codes are in the audit allowlists. Routes and
  credential state join the reference digest. Cross-project workflow nodes
  hide routes and validation details.
- Codex review: process-user ownership, cross-project disclosure and the
  reference budget were fixed with regressions.
- Terra review, documented and not changed: same-UID tasks can still reach
  each other's control sockets (ADR-0050/0051 boundary; an `ansible.cfg`
  `ControlPath` only matters through that boundary); same-project members can
  already read the routes through `GET /secret_storages`; routes are loaded
  live at dispatch like host mappings and credentials, so revocation applies
  immediately.

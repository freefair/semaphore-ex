# Global secrets for tasks and global Infisical

Requested 2026-11-15: pass individual secrets (passwords, license keys) to any task,
configured once globally, with Infisical as a source and Infisical PAM configured
globally instead of per project.

## Starting point

- Variable Group secrets and Vault/OpenBao references are project-scoped.
- Global Credentials (Slice 060/061) exist with project grants, immutable versions,
  audit and dispatch-time resolution, but only workflow secret parameters can bind
  them; actorless tasks are always denied (`actor_required`).
- Global external providers come from `global_credential_providers` in the
  configuration (Vault/OpenBao only). The UI offers a hardcoded provider list.
- Infisical exists only as a project storage for PAM SSH routing.

## Decisions

1. Variable Groups gain **global credential references**: name, target (extra
   variable or environment variable) and a credential granted to the project.
   Bindings are computed when the task's variable groups are loaded and resolved by
   the existing resolver at dispatch. The task row stays value-free.
2. Actorless tasks (schedules, integrations) may consume a credential only when the
   project grant carries the new `consume_unattended` operation (bit 4). Default off.
   Manual and workflow starts keep the user permission checks.
3. Infisical connections are configured in the UI as **global secret storages**
   (Administration). A global Infisical storage holds URL, machine identity client
   ID, client secret (database, environment variable or file, as for project
   storages), optional organization slug and CA. Its client secret is a project-less
   access key so the existing runner key channel carries it.
4. Global credentials gain the external provider `infisical`, referencing a global
   Infisical storage plus project ID, environment slug, secret path, secret name and
   optional version (`GET /api/v4/secrets/{name}`).
5. A global Infisical storage can carry PAM routes and a list of granted projects.
   Tasks of granted projects load these routes together with project storages; a
   host routed twice fails the task as today.

## Order

1. Part 1: references, grant operation, executor env delivery, UI, tests.
2. Part 2: global storages (Infisical), provider resolution, UI.
3. Part 3: global PAM routes and grants, task loading, preflight.
4. Gates, Terra review, Codex review, browser verification, Wiki and ADR, release.

## Out of scope

- Infisical as a value source for project storages.
- Backups: global credential references are not exported; restore reports them.

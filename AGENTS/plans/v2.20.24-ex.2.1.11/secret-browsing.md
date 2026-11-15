# Secret browsing and complete secret-store workflows

## Goal

Nobody copies IDs, slugs or paths out of a secret store into Semaphore EX. Once a
connection exists and its test succeeds, every place that refers to something inside
it offers a searchable picker with human names. Global and project secret stores
offer the same store types and the same workflow. Typing stays available as a
fallback when a machine identity may read but not list.

## Workflow inventory (state before this plan)

| Workflow | Where | Typed by hand today |
|---|---|---|
| Global Infisical connection | Administration > Global secret storages | organization slug; scope project ID, environment slug, folder |
| Global Vault / OpenBao | configuration file only | everything; token in an environment variable |
| Global credential from a store | Administration > Global credentials | Infisical project ID, environment slug, folder, secret name; Vault mount, path, field |
| Scope hint in the credential form | same | shows raw project UUIDs |
| Project Vault / OpenBao storage | Key Store > Storages | mount, namespace |
| Project Infisical storage | Key Store > Storages | organization slug; PAM route account path; usable for SSH routing only, not as a value source |
| Key Store key from a storage | Key Store > key | mount, secret path, field, version ("values are not browsed") |
| Storage sync mapping | Key Store > Storages > sync | mount, remote path, remote field |
| Variable Group bound to a storage | Variable Groups | secret key prefix |
| DVLS storage | Key Store > Storages | vault ID (GUID) |

## Decisions

- **Browsing returns names, never values.** Infisical lists secret keys with
  `viewSecretValue=false` and the server drops any value field. Vault/OpenBao field
  names require reading the secret; the server reads it, returns only the key names
  and discards the values. Every browse request is authorized like a change of the
  store or reference it serves and is audited without values.
- **One browse model for all store types**: `test`, then a tree of nodes
  (`kind`: project, environment, mount, folder, secret, field) loaded lazily by
  parent, each with a display name and the identifier stored in the reference.
- **Global Vault/OpenBao connections move into the database** like Infisical
  (token, AppRole or Kubernetes; secret as a project-less access key). Providers
  from the configuration file keep working and appear read-only in the list.
- **Infisical becomes a value source for projects**: a project Infisical storage
  can back Key Store keys, as Vault/OpenBao storages do, besides its PAM routes.
- **Display names are resolved live** from the connection and cached per request;
  references keep storing stable identifiers. When a name cannot be resolved the UI
  shows the identifier with a warning, never a blank.

## Implementation slices

1. **Browse backend** (`pkg/secretbrowse` client side, Enhanced services, API):
   - Infisical: identity details (test), projects with environments, folders,
     secret keys.
   - Vault/OpenBao: token lookup (test), mounts from `sys/internal/ui/mounts`,
     KV v1/v2 listing, field names.
   - Routes for global storages and for project storages, permission-checked,
     value-free responses, tests against protocol fakes.
2. **Global Vault/OpenBao connections** in `global_secret_storage` with type,
   auth fields and runtime resolution through the existing KV client; configuration
   file providers listed read-only.
3. **Picker component** (`SecretLocationPicker`): searchable cascading selects with
   a breadcrumb, lazy loading, inline errors next to the picker, manual-entry
   fallback. Used by:
   - global credential reference and connection scopes,
   - Key Store key from a storage,
   - storage sync mappings,
   - Variable Group secret key prefix,
   - Infisical PAM routes (account picker if the API lists PAM accounts),
   - DVLS vault selection.
4. **Test connection** buttons in both storage dialogs and lists, result next to
   the button.
5. **Project Infisical as value source** for Key Store keys, including runtime
   resolution, preflight and redaction.
6. **Names instead of identifiers** in scope hints, reference summaries and lists.
7. Docs (Wiki, ADR), browser verification of every workflow above on desktop and
   mobile, Terra security review, release.

## Definition of Done

A slice or the whole plan is done only when every applicable point holds and is
evidenced (command output, screenshot, review file).

### UX (knowledge topic `ux-design-laws`, rule IDs in brackets)

- No workflow asks for an identifier, slug or path that the connection can list:
  every such field is a searchable picker with human names [K2 recognition over
  recall, E3 the system carries the complexity]. Manual entry remains as a
  clearly secondary fallback when listing is denied [I3].
- Names, not identifiers, in every hint, summary, list and chip; identifiers
  only in a tooltip or secondary line [V2 mental model, K1].
- Terms follow the store's own vocabulary (Infisical project/environment/folder,
  Vault mount/path/field) [V1, V2].
- Test connection answers within 400 ms with a visible pending state and reports
  the identity or a concrete, actionable error next to the button [I2, K5, M4].
- Errors and empty states sit next to the control that caused them and say what
  to do next (missing list permission, no KV v2 mount, empty folder) [P1, M4, V3].
- One primary action per dialog; destructive actions apart from frequent ones
  [P6, I1]; targets at least 24x24 px.
- Pickers support keyboard and search, keep the chosen path visible as a
  breadcrumb, and remember the last connection within a dialog [K2, E2, I1].
- Desktop and phone width verified in the browser per workflow; no horizontal
  page scroll; long names wrap or truncate with full text in a tooltip.
- Checklist of `ux-design-laws` section 11 walked for each new or changed dialog.

### UX review

- Codex reviews every new and changed dialog and page against `ux-design-laws`
  (findings with rule IDs, severity order from the topic). Screenshots of desktop
  and mobile are part of the review input.
- All findings of severity "task not solvable" or higher are fixed; the rest are
  fixed or listed with reasoning in the final report.

### Engineering

- Browse responses never contain secret values (tests assert it); browse routes
  are permission-checked like the change they serve and audited without values.
- Unit tests for every new client method, service and API route; frontend unit
  tests for the picker; protocol fakes for Infisical and Vault/OpenBao.
- `tools/upstream-sync/verify.sh` 13/13 green; Dredd run against a live server for
  changed API paths (the local gate only compiles hooks).
- Terra security review of the range; all highs fixed or documented in the ADR.
- Wiki pages and ADR updated; CHANGELOG section; CI green on the exact head before
  tagging.

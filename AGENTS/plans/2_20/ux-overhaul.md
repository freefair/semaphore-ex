# UX overhaul of the fork-owned frontend

Status: plan; policy changes are in place (ADR 0061, ADR 0062), implementation is not started.
Phase 1 starts after Dennis' go. Dennis decided D1 to D5 on 2026-11-14; see [Decisions](#decisions).

## Contents

- [Outcome](#outcome)
- [Verified starting points](#verified-starting-points)
- [Cross-cutting patterns](#cross-cutting-patterns)
- [Phase 1: security and correctness](#phase-1-security-and-correctness)
- [Phase 2: administration area](#phase-2-administration-area)
- [Phase 3: authentication and group mappings](#phase-3-authentication-and-group-mappings)
- [Phase 4: roles and permissions](#phase-4-roles-and-permissions)
- [Phase 5: core flows](#phase-5-core-flows)
- [Phase 6: language, accessibility and remaining rules](#phase-6-language-accessibility-and-remaining-rules)
- [Acceptance and verification](#acceptance-and-verification)
- [Decisions](#decisions)

## Outcome

A first-time administrator completes each of the seven assessed tasks without documentation:
connect the company directory, grant directory groups project permissions, create and change a custom role,
run a playbook and schedule it, enforce two-factor login, onboard and offboard a user with an audit answer,
and receive a failure e-mail or learn exactly what is missing for it.
Today three of these fail and two succeed only partly.

## Verified starting points

The assessment of 2026-11-13 is kept in `AGENTS/tmp/ux-assessment-20261113/` (git-ignored):
`ux-assessment.html` with 23 findings ranked S1 to S4, participant notes, 2FA screenshots,
and the measurements behind ADR 0062.

| Finding | Evidence | Origin |
|---|---|---|
| Password change keeps existing sessions | `api/users.go:274` calls `SetUserPassword` only; TOTP paths already use `…AndRevokeSessions` (`db/Store_ex.go:15-17`); replay showed the old session returning 200 | Upstream handler |
| No user deactivation, delete states no consequences | Users view offers Edit and Delete only | Upstream |
| Admin audit log empty while activity feed has events | `/api/audit-log/events` returned `[]`, `/api/audit/events` 41 rows on the same instance; list in `test/edition-contract/enhanced/db/sql/audit.go:31` | EX |
| TOTP enrollment is QR-only | `web/src/components/TotpEnrollmentPanel.vue:103-108` | EX |
| "Test alerts" reports success without a delivery path | Participant A7 | Upstream |
| New schedule defaults to `* * * * *`, enabled | Participant A4 | Upstream |
| Settings inside System Information | `web/src/components/EnhancedSystemInfoPanel.vue`, `SystemInfoDialog.vue:73`; upstream dialog has no inputs | EX |
| Group mappings in three places; `/role-mappings` lists OIDC only | `web/src/views/RoleMappings.vue:142` | Mixed |
| LDAP errors lose their cause and render at the card top | `services/identity/ldap_client.go:96-98`, `api/ldap.go:256-259`, `web/src/components/LdapCapabilityPanel.vue:4-12` | EX |
| Unlinked directory users block every group apply | `pro_interfaces/ldap_group_mapping.go:221-256` | EX |
| `ldaps://localhost` fails in the server, `127.0.0.1` works | Fixture replay; standalone client with the same library succeeded | Unknown |

## Cross-cutting patterns

Build these first in Phase 2 and use them everywhere afterwards, so every later fix lands in the same shape.

- **Action feedback:** every save, apply, test and delete reports success or failure within 400 ms (I2) next to the control that triggered it (P1, K5). One component, one convention; no card-level error slot shared by unrelated actions.
- **Error content:** a human sentence stating what failed and what to do; the machine code only as secondary detail (I3). No raw codes as the only text.
- **Destructive actions:** state the consequences in the confirmation (M4); offer the non-destructive alternative where one exists.
- **Settings vs. information:** settings live on their own page; information views hold no inputs and may link to the setting (V2).
- **Defaults:** safe and useful defaults (K7, E3); credential pickers default to "None" where none is needed.

## Phase 1: security and correctness

Security-relevant implementation and verification run through the dedicated Terra sub-agent (project `CLAUDE.md`, Security Execution).

1. **Revoke sessions on password change.** Changing a user's password, by the user or an admin, ends that user's other sessions; the editing session stays. Reuse the revocation pattern of the TOTP store methods.
2. **Deactivate users.** A deactivated user cannot log in, existing sessions and API tokens stop working, and all created objects remain. Delete explains what happens to the user's objects. Check first whether the upstream model already carries a disabled state.
3. **Admin audit log.** Find why the audit log list returns no events while events are recorded; fix and cover with a regression test that writes an event through the real path and lists it.
4. **Truthful test alerts.** "Test alerts" reports per channel whether it was sent, skipped or failed, and says when no channel or no recipient is configured.
5. **TOTP enrollment.** Show the secret as text with copy beside the QR code; recovery codes get copy and download, an "I have stored these codes" confirmation, and a warning that restarting enrollment invalidates them. Verify errors appear at the code field.
6. **Schedule default.** A new schedule starts disabled or with a non-repeating placeholder that cannot be saved unchanged; the friendly picker is the default view.
7. **Guest role.** The built-in Guest role loses `workflow.approve` (D2). Existing Guest assignments lose it through a migration; roles that should approve use an explicit role.

## Phase 2: administration area

1. One **Administration** area reachable from the sidebar, which stays visible on admin pages.
2. Sections, each its own route:
   - Users
   - Roles and permissions
   - Authentication: local login and password policy, two-factor policy (today "TOTP rollout"), LDAP providers, OIDC providers
   - Group mappings: one list for LDAP and OIDC providers
   - Notifications: channels with their state. Channels stay configured in the server configuration (D4); the UI states for each channel whether it is configured, names the configuration keys to set, and links to the Wiki page.
   - Audit: audit log, audit webhooks
   - Runners and cluster
   - Global credentials
   - API tokens
   - System information: read-only, linking to the matching setting
3. Move every editable control out of `EnhancedSystemInfoPanel.vue` and `SystemInfoDialog.vue` into these sections; the dialog keeps only information.
4. Implement the cross-cutting feedback and error patterns as shared components.

## Phase 3: authentication and group mappings

1. **LDAP setup as steps:** Configure → Test → Activate, with a visible step state and the 15-minute readiness window shown as a countdown in the Activate step.
2. **LDAP diagnostics:** the server logs the underlying error per stage with provider ID and without credentials; the API returns distinct codes for unavailable, referral and duplicate identity; the UI shows the failed stage and the reason in words. Investigate the `localhost` failure on the way.
3. **Directory presets:** "Active Directory" and "OpenLDAP" fill filters, identity and attribute fields; the immutable-ID format follows the preset.
4. **Continuation references:** LDAP search ignores continuation references when the result already identifies exactly one user; referrals are still never followed, and a referral result code still fails (D3).
5. **Group mappings:** pick a group by searching the directory by name instead of typing an immutable ID; the ID is stored and shown as detail. Built-in roles are offered for project scope. Errors appear in the mapping dialog.
   - Group apply skips directory users without a linked account and lists them as a notice; it no longer blocks (D1).
   - Login requires group membership (D1): a directory user whose groups match no enabled mapping of that provider cannot log in and sees a plain message to contact the administrator. Activating a provider requires at least one enabled mapping. A linked user who no longer matches any mapping is denied at the next login, loses managed grants on reconcile, and has open sessions revoked. Local login, including the recovery administrator, is unaffected.
6. **Lifecycle wording:** "Disabled / Shadow / Selected users / Active" get one line of explanation each, shown with the options.

## Phase 4: roles and permissions

1. One roles page per scope, linked from both the project team page and Administration; remove the duplicate entry points.
2. Permission names identical in checkbox, chip and member row; chips show every granted permission.
3. Member role assignment states whether it adds or replaces; saving gives feedback.
4. An "effective permissions" view per member.
5. Internal role IDs only as secondary detail.
6. **Per-resource permissions (D5):** replace `project.resources.view` and `project.resources.manage` with view, create, update and delete per resource type: templates, inventories, repositories, access keys, variable groups, schedules and integrations. Access keys additionally separate "use in a task" from managing them. A migration maps every existing role to the equivalent set, so no assignment gains or loses access. Enforcement stays in the backend for every endpoint; the UI only reflects it.

## Phase 5: core flows

These are upstream surfaces that the fork now owns (ADR 0062).

1. **Template creation:** empty required pickers offer inline creation; repository and inventory credentials default to "None"; "Ansible prompts" reads as "Ask at launch"; advanced sections collapsed by default.
2. **Execution review:** shows the credentials actually selected; explain or drop "provisional".
3. **Inventory picker:** fix the duplicate entry (one inventory rendered twice).
4. **Schedule dialog:** "New schedule" title for new ones; live "Next run" preview; the timezone banner disappears once a timezone is set.
5. **Users:** create flow without the silent switch to "Edit User"; fix the "User undefined saved" toast; initial password handling states how the user receives it.
6. **Activity feed:** human-readable entries with user names and a user filter; no raw JSON rows.
7. **Project settings:** sections with clear save scope.
8. **Task log:** collapse the repeated "No requirements.yml" lines; the summary tab names the failure reason.

## Phase 6: language, accessibility and remaining rules

1. Terminology pass across the UI: one name per concept ("Variable Groups" vs "Environment", "Key Store" vs "Access Key"), no system jargon without explanation, fix "Enter to your account" and the duplicate "Sign in" labels, locale-consistent date pickers.
2. Check the rules the assessment did not cover: WCAG contrast, keyboard operation, screen-reader names and `aria-live` for status changes, `prefers-reduced-motion`, target sizes and spacing (I1), icon clarity (P5), `autocomplete` attributes (I4).

## Acceptance and verification

- Each phase: unit tests for changed behavior (Vitest, Go with testify), the complete retained gates (`tools/upstream-sync/verify.sh`), and browser verification of the changed areas.
- Permission changes: verify with separate test users for no access, read access and write access on each affected resource type and endpoint.
- Each phase that touches a task area: rerun the matching assessment task with a context-free participant on a disposable local instance; the phase is accepted when that task succeeds without workarounds.
- End of the overhaul: all seven assessment tasks succeed.
- Wiki: update Implementation-Status and the affected feature pages per phase.

## Decisions

Decided by Dennis on 2026-11-14:

| ID | Decision |
|---|---|
| D1 | Group apply skips unlinked directory users instead of blocking. Without a matching group a directory user gets no access. |
| D2 | The built-in Guest role loses "approve workflows". |
| D3 | Continuation references are ignored when exactly one user is identified; referrals are never followed. |
| D4 | The mail server stays in the server configuration; the UI states what is missing and where to set it. |
| D5 | Project permissions are split per resource type and action, choosing the safest model over the simplest. |

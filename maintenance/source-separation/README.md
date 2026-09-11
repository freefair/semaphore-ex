# Further Fork Source Separation

## Scope and result

This follow-up starts at `28f60c458440bbba8e8399654472a98ff76e3829` on the locally adopted `develop`.
The comparison uses the same upstream commit, `9cd15a7604eb3486134eaa32944d5dcf16a24655`, as the history review.
It adds ordinary local commits; it does not rewrite the reconstructed ancestry again.
Publication remains separately authorized.
The backend, UI and public Swagger changes are recorded in `34513a6c`, `38913eb1` and `c82195b9`; the accompanying docs change is `d14cf8e`.

| Textual overlap | Original fork | First separation | This separation |
| --- | ---: | ---: | ---: |
| Changed lines in upstream-owned text paths | 38,502 | 15,983 | 14,294 |
| Reduction from original | — | 58.5% | 62.9% |
| Touched shared text paths | 219 | 220 | 220 |

This pass removes another 1,689 shared changed lines, or 10.6% of the first pass's remainder.
The metric counts additions plus deletions with `git diff --no-renames --numstat`; it is not a count or probability of future merge conflicts.
It still includes 1,467 lines deleting whole commercial UI/edition CI files.
Restoring those deletions would change the selected product.

## Additional boundaries

- Thirteen focused Vue components own workflow settings, node artifacts and approval policy, run artifact metadata, SSH key generation/rotation, runtime provider/key-reference fields, role assignment, deployment overrides and search rendering.
- Eleven explicit state/watch modules preserve fresh component state, property order and watcher bodies. Existing upstream fields, hooks and conditional host wiring stay visible.
- The task form's complete fork-only preflight save override and header constants live in its existing Enhanced option module.
- Project/global permission additions and role descriptors have a dedicated module; existing exports, bit values and descriptor order remain unchanged.
- Identity route registrations and cohesive identity/artifact constructor bundles have same-package Go boundaries. Router selection, middleware, audit wiring and constructor order remain at their reviewed points.
- Complete protected user/membership transactions and the start-claim operation move together, preserving locks, CAS, rollback and caller outcomes. Runner normalization and template policy-field preparation move into the existing `_ex.go` files.
- The embedded Swagger surface now has its own `api-docs-ex.yml`: seven definitions and four paths move behind references without replacing its narrower API with the root specification.

## Remaining shared code

| Surface | Concrete reason for retaining the current integration |
| --- | --- |
| Root API entry | References introduce public paths/definitions; modifications to existing upstream request/response contracts remain in place. Removing those entries requires a different composition format or generated public entry. |
| Embedded Swagger entry | Its tag, eleven fragment references, and changes to the existing task endpoint remain the public entry. The shipped resolver loads the local fragment. |
| Configuration schema | Its `$id` names an upstream-hosted schema. Relative external references would use that identity as their base, requiring an explicitly published fork schema identity and consumer review. |
| Workflow editor/run | Existing graph actions, validation/save gates, upstream approval controls, status mapping and lifecycle requests change upstream behavior. Extracting the whole screen would hide later upstream changes. New settings and artifact markup now have narrow component boundaries. |
| Other shared UI | Existing fields and buttons gain permission/disabled conditions; existing lifecycle methods start/cancel requests and timers. These are host integration changes around extracted options/components. |
| Router | Shared controllers and middleware still need the selected dependencies. Public/authenticated/admin router construction and mixed upstream registrations determine which middleware and route precedence apply. Fork-only identity registrations and cohesive construction blocks are extracted. |
| Template SQL | Visibility precedes candidate search, Unicode matching precedes pagination, and hydration follows selection. Helpers already own candidate search and policy fields; changing the host sequence would change visible results. |
| Runner registration/dispatch | The remaining host paths interleave transport requests, parsing, identity/fence installation, error responses and persistence. Normalization/policy helpers are isolated; the conditional transitions remain visible. |
| Task/schedule execution | Claim outcomes control the existing deferred cleanup and requeue flags. Admission, lease release/block/complete and task creation still form one ordered lifecycle. |
| Shared types and interfaces | New serialized fields and method signatures must be visible to existing callers and implementations; Go cannot add struct fields from another source file. |
| CI/build wiring | Selected-product dependencies, workspace configuration, pinned toolchain and matrix invocation remain entry-point configuration. Changing existing helper behavior or introducing a composite action is separately scoped tooling work. |
| Shipped migrations and removed commercial surfaces | These encode existing schema/product history. Their bytes/identities and selected product semantics remain outside a source-movement refactor. |

The [per-path table](shared-files.yml) retains the earlier ownership decision and records the before/after line counts.
These are reviewed present boundaries, not a claim of a mathematically minimal diff.
Further architectural changes should be justified by the specific remaining dependency, not a broad category label.

## Verification

The results below describe the source-separation checkpoint. The subsequent
[frontend test repair](../frontend-tests.md) resolves its six normal frontend failures
in a separately authorized tooling/test change.

- Maintenance inventories/checker tests, root and Enhanced Go tests, both vet runs, API bundle, Dredd compilation, product build and Dockerfile checks pass.
- MySQL 8.4, MariaDB 10.11 and PostgreSQL 12.22 migration matrices pass; SQLite coverage runs in the Go suite.
- Eleven-locale serial documentation build and translated security/canonical fallback tests pass.
- Changed frontend source lint passes. Structural comparison confirms state/watch property order and bodies for all eleven hosts. The public Swagger expanded value is identical.
- Browser checks cover desktop 1440px and mobile 390px, actual field changes and event-driven requests, artifact expiry/download denial, generated-key result/rotation, AppRole fields, role assignment, read-only workflow settings and Swagger paths/schemas. No page errors or unhandled fixture endpoints remain in the final successful runs.
- Exact-baseline browser comparisons preserve the observed layout. Approval, key form, run artifact and initial Swagger screenshots match at the measured pixel threshold; artifact-editor differences are below 0.001%. Search has a small visual-state difference with matching layout.
- The normal frontend suite reports **250 passing / 6 failing**: the three earlier failures plus three new rendered-component tests affected by the existing Babel/CommonJS template-export problem. It remains a failed gate.
- With diagnostic `VUE_CLI_TEST=1`, the current suite reports **253 passing / 3 failing**. The exact starting source reports **249 passing / 3 failing** under that same environment; ArgsPicker, YesNoDialog and Socket are the remaining failures in both. No permanent test-tooling fix is included without the requested separate scope approval.
- A retained isolated candidate HA run passes all seven scenarios and 21 assertions, with no duplicate logical work, eight accepted writes retained, and continuous audit evidence. Earlier candidate and exact-baseline runs both fail the node-kill assertion with extra runner attempts and no generation-two terminal state. This is not specific to the extraction; its root cause remains unresolved and requires separate investigation. The additional exact-baseline capture also passes all seven scenarios; [HA observations](ha-investigation.md) distinguish the four runs. The passing retry does not erase those failures.

## Separate findings

`SecretSourceToggle` declares a disabled prop without applying it to its buttons.
Mobile WorkflowEditor overflow also reproduces in the exact baseline.
The fixture-based template table has no visible data columns despite populated parent headers in both builds; this limits the integrated highlight visual check and is not established as a production regression.
The docs build retains existing broken-link warnings; generated security fallback checks pass.
These findings are recorded for separate prioritization rather than silently changed here.

Raw logs, screenshots, source fingerprints and failed diagnostics are retained locally in
`AGENTS/plans/fork-source-separation/evidence/` (ignored in Git).
The retained `full-gates` directory binds the starting HEAD and staged working diff; it is evidence for the reviewed source snapshot, not a published release artifact.

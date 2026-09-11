# Fork-Owned Source Boundaries

## Quick Start

Use the root Go workspace and installed frontend dependencies.
After changing API fragments, generate the standalone specification and run its checks:

```bash
go run ./tools/openapibundle
go test ./tools/openapibundle -count=1
go run ./tools/upstreamcheck -mode check -base-ref origin/develop
```

The generated `.dredd/api-docs.bundled.yml` is ignored.
The Task Dredd targets generate it before invoking Dredd.
Direct Dredd invocations must generate it first.

## Ownership and Integration

| Change | Preferred location | Shared integration retained |
|---|---|---|
| Replaceable module behavior | `test/edition-contract/enhanced` | Public factories and interfaces match the compatibility `pro` module |
| Root public types and package-dependent functions | Focused `_ex.go` and `_ex_test.go` files in the same package | Existing types keep their identity; existing callers keep their order |
| Normalized Ansible summaries | Enhanced `db/sql/ansible_task.go` and `db/factory/ansible_task.go` | Task persistence and scoped HTTP consumers use the selected factory |
| Summary rendering | `web/src/components/EnhancedTaskSummary.vue` | `TaskLogView.vue` selects the component; upstream `AnsibleStageView.vue` remains compatibility source |
| Independent Vue options and state | `web/src/lib/enhanced` option objects and state factories | Hosts explicitly spread options at the original position; lifecycle hooks and shared state dependencies remain visible |
| New UI behavior | Focused components with narrow props/events | Existing permission and lifecycle controls remain authoritative |
| Fork locale messages | `web/src/lang/enhanced/{en,de}.js` | Locale setup combines upstream and fork dictionaries |
| Governance and identity route registration | `api/routes_governance_ex.go` and `api/routes_identity_capabilities_ex.go` | The authenticated router supplies existing middleware closures in their original registration order |
| Fork-only API definitions and paths | `api-docs-ex.yml` | `api-docs.yml` retains reference entries and changes to existing upstream contracts |
| Embedded Swagger additions | `web/public/swagger/api-docs-ex.yml` | The public Swagger entry retains references and its narrower API surface; resolve fragments in the shipped Swagger UI |
| Fork-only build targets | `Taskfile.ex.yml` | The flattened include keeps public task names unchanged |

Same-package files reduce textual overlap without changing dependency direction.
A module move is appropriate only when the existing replaceable interface supports it.
Keep variables, initialization, build constraints, compiler directives, and complete `iota` groups intact when extracting declarations.
Shared DTO fields and changes to upstream control flow remain integration points.
A complete fork-owned transaction or claim operation may move into a focused helper when it preserves locks, compare-and-set predicates, rollback, error handling and the surrounding execution order.
Assess each block by its inputs, outputs and side effects; a security or persistence label alone does not determine its location.

The Vue option files are explicit property spreads, not mixins with implicit lifecycle merging.
A method that closes over module-local state remains with that state unless a separately reviewed component design establishes a new boundary.
State factories return fresh objects on every call; retain state/watch property order when introducing spreads.
Workflow settings, node artifacts and approval policy, run artifact metadata, generated SSH key dialogs, provider authentication, global role assignments, deployment overrides and template search use focused components under `web/src/components/enhanced`.
Components emit explicit updates before dirty/normalization notifications; the host retains persistence, permission decisions and lifecycle coordination.
Keep existing upstream controls in slots when new content surrounds them.
The template search component exposes `focus()` so the host shortcut and Escape behavior retain their existing contract.
Prefer focused components for new rendering; avoid copying an entire upstream screen to avoid a small integration change.
The commercial-surface gate follows the selected UI import graph and rejects unresolved or computed imports so unwired compatibility files do not stand in for selected behavior.

## API Documents

Dredd 13.1.2 cannot consume the external schema references used by the authored split.
`tools/openapibundle` expands only the repository's two authored documents into one file and preserves internal references, including recursive schemas.
It rejects missing references, duplicate mapping keys, unsupported external references, expansion cycles and output paths that overwrite authored inputs, including symlink aliases.
No network access is part of bundling.

The split preserves the expanded API value, including path order and existing descriptions.
Review both files together when upstream changes an existing definition used by a fork path.
The fragment file is an ownership boundary, not a separate API version.

The shipped Swagger document uses the same relative-reference boundary in its own directory.
Its fragment references the local public entry for existing parameters and definitions; it is not replaced with the broader root document.
Validate expanded values and resolve the public paths and schemas in the shipped Swagger UI after changing either public document.

Keep `config.schema.yaml` self-contained while it advertises the upstream `$id`.
Relative external references would resolve against that upstream schema identity; an independently published fork schema needs an explicit identity and consumer compatibility review.

## Maintenance Rule

Regular updates merge upstream into the long-lived fork.
History reconstruction is a separately authorized exception and is not part of routine syncs.
Review new exported seams for inherited placeholders and require observable behavior tests before updating their inventory.
Migration IDs and applied SQL remain unchanged by file separation; the ownership ledger remains the current migration policy.
A runtime migration namespace needs its own design and upgrade verification.

Reduced shared-line counts indicate less textual overlap; semantic conflicts still require explicit review.
Keep source extraction, helper changes and compatibility repairs independently reviewable.

# Fork Maintenance Contracts

These reviewed YAML inventories protect the long-lived full-product fork. They
are repository-owned and travel with every clone. They are verification inputs,
not a second runtime migration engine or an automatic conflict resolver.

## Quick Start

For a fresh clone, configure the product upstream and activate the product branch:

```bash
git switch develop
git remote add upstream git@github.com:semaphoreui/semaphore.git
```

For an existing checkout, verify these remote identities and branch names first.
Run preflight with `--fetch` once to populate upstream refs; its retained report
makes missing or unexpected configuration explicit.

Install the repository's Go and frontend dependencies, build the embedded frontend
(`npm --prefix web ci && npm --prefix web run build`) before running the assessment. Use the toolchain versions in `go.mod`
and the product workflow.

```bash
go run ./tools/upstreamcheck -mode check -base-ref origin/develop
bash tools/upstream-sync/preflight.sh --fetch --output /tmp/semaphore-assessment .
bash tools/upstream-sync/verify.sh --quick --output /tmp/semaphore-quick-checks
```

Each output directory must be new. Assessment fetches only when requested. It
captures product local, origin, upstream and merge-base SHAs; source status;
upstream seam/schema patches; full binary patches and fingerprints of staged/unstaged source; fork delta summaries; incoming migration decisions;
and merge-tree conflict previews. Merge-tree writes its objects into the evidence
directory, leaving repository objects, indexes, worktrees, and branches untouched.
Assessment requires new source files to be reviewed and staged, and rejects source/HEAD changes during capture.
The checker uses Go export data and therefore needs dependencies and the embedded
frontend available. Run it with the committed workspace enabled.

## Project Skill

The canonical `semaphore-upstream-sync` skill lives in
[`../.claude/skills/semaphore-upstream-sync/`](../.claude/skills/semaphore-upstream-sync/SKILL.md),
including its UI metadata and optional read-only diagnostic. Edit and commit
that source alongside changes to the maintenance policy. The relative symlink
at `.agents/skills/semaphore-upstream-sync` exposes the same source to Codex
without a separate copy. Both entries travel with a clone.

An existing personal installation may link to this directory for compatibility;
it is not the source of truth. Preserve an existing real directory outside skill
discovery before replacing it with a link. If the checkout moves, update that
personal link; the repository's relative discovery link remains portable.

The skill's optional diagnostic does not replace the repository-owned retained
assessment and verification commands above.

## Migration Ownership

`migrations.yml` records every SQL migration family, whether it is registered,
its owner, original upstream ID when applicable, and exact SHA-256 checksums of
all forward/rollback/dialect files. `upstream-adapted` entries explain why their
local form differs. Historical unregistered SQL is recorded without activating it.

Upstream and EX are independent runtime namespaces:

| Scope | Registry | SQL directory | History table | Target |
|---|---|---|---|---|
| Upstream | `db.GetMigrations` | `db/sql/migrations` | `migrations` | `X.Y.Z` |
| EX | `db.GetEXMigrations` | `db/sql/migrations_ex` | `ex_migrations` | `X.Y.Z-exA.B.C` |

Upstream migration identities match upstream. New upstream migrations are added
under their original identity and never consume EX sequence numbers. EX files
use `vX.Y.Z-exA.B.C.sql`/`.err.sql` names; `X.Y.Z` is the upstream dependency and the numeric EX suffix orders EX steps at that anchor. The versioned ownership ledger records
fully qualified source paths and rejects cross-namespace ownership or files.

Execution interleaves the two independent registries by exact upstream anchor:
`2.20.2`, `2.20.2-ex2.2.1`, `2.20.2-ex2.2.2`, then `2.20.3`.
EX suffix components are compared numerically, so `.10` follows `.2`.
Every EX anchor must exist in the upstream registry; unknown anchors and duplicate
identities fail before execution. A failed step prevents dependent/later steps.
PostgreSQL and MySQL/MariaDB serialize the entire interleaved plan with one
database migration lock; SQLite is a single-host store. HA schema identity
contains both registry heads.

An apply target ends the plan at that identity, including earlier EX dependencies.
An upstream target such as `2.20.2` stops before that anchor's EX steps. Rollback
uses the exact reverse plan: later upstream steps are undone before earlier EX
steps, and an anchor is undone only after all its EX dependents are undone.

This development layout targets fresh databases. It provides no automatic
conversion or adoption of the earlier mixed migration history. Existing database
transition is a separate operator decision and is not inferred from table names.

`-base-ref` checks immutable entries within the separated ledger. The explicit
format-1 to format-2 source-layout transition verifies moved fork SQL checksums
and restores upstream identities; it does not migrate database records. The first ledger is checked
against the immutable product baseline recorded in `tools/upstreamcheck/baseline.go`; subsequent checks compare
against the prior reviewed ledger. The CI gate uses the push predecessor or PR
base, so changing a SQL checksum and its ledger entry together still fails.

```bash
go run ./tools/upstreamcheck -mode incoming -incoming-ref upstream/develop
go run ./tools/upstreamcheck -mode migrations > /tmp/migration-candidate.yml
```

Candidates deliberately use `owner: REVIEW`. They neither replace the ledger nor
choose a migration identity. Review new entries and retain existing entries verbatim.

## Exported Module Contracts

`contracts.yml` inventories public declarations in the compatibility `pro` module,
`pro_interfaces`, and the root `db` package. It includes public type shapes,
interface methods, constants, functions, exported receiver methods, and the selected
implementation's signature and origin. Private controller types remain behind
their inventoried public factory/interface contracts. Receiver-method origin tracking
applies to replaceable `pro` types. For core `db` and `pro_interfaces`, the inventory
records package-level declarations and type shapes, including interface method
signatures; concrete core receiver methods are not enumerated separately.

The checker derives method origins from Go's method sets. A promoted method from
`community-pro` remains visible as `community` even when the Enhanced type compiles.
Function-valued aliases are also identified from their declarations. A new export,
removed export, signature change, or implementation-origin change requires review.

Community origin is not automatically a defect: shared parsers and synchronized
task-state factories are functional implementations. Task-summary persistence is
owned by the selected Enhanced SQL module and tested there.
Unselected Terraform-state and external-secret-provider scaffolding remains unwired.
Legacy email verification intentionally denies access. Every retained Community
alias and missing legacy concrete type has a specific rationale in the inventory.

For a new or changed callable/interface, implement its selected behavior and add
`behavior_tests` references using `repository/path_test.go::TestName`. The checker
validates real test declarations, and the normal module suites execute them. A
reference alone does not establish behavioral coverage: reviewers inspect assertions
and verify that losing the implementation makes the regression fail.

All six delay-store methods reference the direct durable/scoped contract test and
existing restart/stop regressions. The maintenance checker itself tests promotion,
new exports, schema changes, and migration identity collisions.

```bash
go run ./tools/upstreamcheck -mode contracts > /tmp/contracts-candidate.yml
```

Review the candidate diff; retain rationale and behavioral references. No command
silently approves a new placeholder or rewrites the reviewed inventory.

## Retained Verification Evidence

```bash
bash tools/upstream-sync/verify.sh --output /tmp/semaphore-release-checks
```

Stage reviewed new source files first, including new docs files; untracked source
is outside a Git HEAD/diff fingerprint. Existing unrelated journals and generated
knowledge mirrors remain excluded. The runner records root/docs HEADs, separate
working-diff hashes, per-gate logs, exit codes, timings, and a checksum manifest.
Each run writes product artifacts to its own evidence directory, keeping source-map outputs reusable across repeated runs.
It resolves and records the baseline SHA before any gate, and checks that neither HEAD nor source diff changes during verification.
Test processes disable Git commit signing through a command-scoped configuration entry so disposable fixtures do not depend on keychain prompts.

The frontend build precedes Go compilation because `api/public` is embedded.
Independent gates continue after failures, and the final exit code stays nonzero.
The complete frontend suite's known upstream failures remain failed results;
maintainers must retain an exact-upstream reproduction and explicitly classify them.
The script does not turn a failed test into an automatic pass.

Browser verification of changed UI, semantic conflict review, security review when
applicable, and exact-head remote CI results remain explicit maintenance steps.
Ordinary verification never pushes, merges, rebases, or modifies SQL state.

## Releasing

See [RELEASING.md](RELEASING.md) for the tag scheme, the release gates, artifact
verification and the location of the signing key.

## Fork-Owned Source Boundaries

See [source boundaries](source-boundaries.md) for the Enhanced module, same-package
Go files, focused UI components, locale additions, and API bundling workflow.

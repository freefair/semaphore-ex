# Fork Maintenance Contracts

These reviewed YAML inventories protect the long-lived full-product fork. They
are repository-owned and travel with every clone. They are verification inputs,
not a second runtime migration engine or an automatic conflict resolver.

## Quick Start

For a fresh clone, configure the two upstream remotes and activate the fork branches:

```bash
git switch develop
git remote add upstream git@github.com:semaphoreui/semaphore.git
GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes' git submodule update --init docs
git -C docs remote add upstream git@github.com:semaphoreui/semaphore-docs.git
git -C docs switch main
```

For an existing checkout, verify these remote identities and branch names first.
Run preflight with `--fetch` once to populate upstream refs; its retained report
makes missing or unexpected configuration explicit.

Install the repository's Go and frontend dependencies, build the embedded frontend
(`npm --prefix web ci && npm --prefix web run build`), and initialize the docs
submodule before running the assessment. Use the toolchain versions in `go.mod`
and the product workflow.

```bash
go run ./tools/upstreamcheck -mode check -base-ref origin/develop
bash tools/upstream-sync/preflight.sh --fetch --output /tmp/semaphore-assessment .
bash tools/upstream-sync/verify.sh --quick --output /tmp/semaphore-quick-checks
```

Each output directory must be new. Assessment fetches only when requested. It
captures root/docs local, origin, upstream, and merge-base SHAs; source status;
upstream seam/schema patches; full binary patches and fingerprints of staged/unstaged source; fork delta summaries; incoming migration decisions;
and merge-tree conflict previews. Merge-tree writes its objects into the evidence
directory, leaving repository objects, indexes, worktrees, and branches untouched.
Assessment requires new source files to be reviewed and staged, and rejects source/HEAD changes during capture.
The checker uses Go export data and therefore needs dependencies and the embedded
frontend available. Run it with the committed workspace enabled.

## Migration Ownership

`migrations.yml` records every SQL migration family, whether it is registered,
its owner, original upstream ID when applicable, and exact SHA-256 checksums of
all forward/rollback/dialect files. `upstream-adapted` entries explain why their
local form differs. Historical unregistered SQL is recorded without activating it.

The two identities are deliberately separate:

| Logical upstream identity | Shipped local identity | Reason |
|---|---|---|
| `2.20.2` | `2.20.66` | Fork `2.20.2` already configures capabilities |
| `2.20.3` | `2.20.67` | Fork `2.20.3` already adds runner names |

The runtime registry and deployed `migrations` table keep their existing numeric
IDs. New fork migrations receive the next free local tail ID and `owner: fork`.
New upstream migrations receive a new local tail ID and an explicit `upstream_id`,
including when their original number collides with a shipped fork migration.
Register them once in `db/Migration.go`; preserve ordering and all dialect gates.
A change to already applied upstream SQL becomes a new corrective local migration,
not a rewrite of a shipped file.

`-base-ref` makes old ledger entries append-only. The first ledger is checked
against the original SQL at product commit `7472edea`; subsequent checks compare
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
their inventoried public factory/interface contracts.

The checker derives method origins from Go's method sets. A promoted method from
`community-pro` remains visible as `community` even when the Enhanced type compiles.
Function-valued aliases are also identified from their declarations. A new export,
removed export, signature change, or implementation-origin change requires review.

Community origin is not automatically a defect: shared task-summary repositories,
parsers, and synchronized task-state factories are functional implementations.
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

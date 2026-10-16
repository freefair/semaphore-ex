# HA recovery CI repair

## Objective

Make the required HA fault gate reliably recover a task after its owner node is
killed, preserving generation fencing and terminal completion.

## Evidence

Full Product Build 38869498481 passed the product build and smoke tests but failed
the node_kill scenario: three attempts instead of two and no observed terminal
generation-two task. The preceding commit passed this gate; only UI files changed.
The current report does not record attempt outcomes or the terminal wait error.

## Plan

1. Reproduce with the existing harness, isolated Docker Compose resources, and
   locally built images. Retain the environment for targeted, non-secret SQL
   inspection if it fails.
2. Trace task ownership, assignment generations, runner reports, and completion.
   Write a reproducing regression test in temporary storage before source edits.
3. Apply the smallest root-cause fix in the affected runtime or harness. Add safe
   report diagnostics only where needed; preserve fault-gate assertions/timeouts.
4. Run focused tests, broader affected checks, and the complete HA gate. Review
   the diff, record results, commit verified work, and clean task-owned fixtures.

## Decision record

Use the existing full topology rather than mock the fault initially: the failure
crosses node recovery and runner dispatch, and the report alone cannot identify
the responsible component. This costs a container build but preserves production
behavior. Increasing timeouts or accepting extra generations would hide the
reported failure and is not a repair.

### Preserve the injected node failure during runner recovery

The unchanged local gate and a minimal two-container reproduction confirm that
`docker compose start runner` starts stopped dependencies, including the killed
owner. This violates the scenario boundary and introduces an unintended owner
rejoin/lease takeover while recovery is still running. Two unchanged full runs
passed despite this violation, consistent with timing-dependent behavior.

Start the runner with `up --detach --no-deps --no-recreate`. The existing
container and identity remain intact and the failed server stays stopped.
Assert owner downtime immediately after runner startup and after recovery, before
the deliberate owner restart. Preserve every existing assertion and timeout.
Record actual task status, assignment generation, attempt outcomes, and the
terminal wait error in the report instead of claiming terminal success on failure.
The product images and runtime source remain unchanged.

The independent review identified that combined stdout/stderr could turn a
Compose warning into a false nonempty service listing. Keep successful stdout
separate for machine parsing and retain both streams in command failure errors.
A reproducing test failed before this change and passes after it. The review's
nullable-outcome concern does not apply: the migration declares outcome and
generation NOT NULL.

## Verification

- Minimal Docker reproduction: original command restarts the owner (assertion
  fails); corrected command leaves it stopped.
- Regression test fails with a source overlay removing `--no-deps`, and passes
  with the implementation.
- `go test ./test/ha-resilience/... -count=1` and `go vet
  ./test/ha-resilience/...` pass.
- Nine HA wrapper/workflow Bats checks pass.
- Three corrected full HA runs pass all seven scenarios, including a final run
  after stdout/stderr separation. The task succeeds at generation two, the owner
  remains stopped, and uniqueness/audit checks pass.
- Final independent Claude review has no actionable findings.
- The generic local verify wrapper stops at its untracked-source preflight;
  unrelated `AGENTS/plans/fork-history-rebuild/` remains untouched. Focused
  verification above is complete; full product validation runs in CI.

## Open

- Verify required workflows on the published commit.

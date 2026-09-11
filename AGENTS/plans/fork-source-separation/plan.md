# Further Fork Source Separation

## Objective

Reduce the remaining upstream-owned diff from local develop `28f60c458440bbba8e8399654472a98ff76e3829` against the recorded upstream `9cd15a7604eb3486134eaa32944d5dcf16a24655`, preserving the full product and all existing behavior.
Implement as scoped follow-up commits on local develop; publication remains a separate pending authorization.
The original history and the first reconstructed review branch remain recovery points.

## Approach and Tradeoffs

Moving every modified upstream function wholesale would create replacement copies and obscure future upstream changes.
Instead extract coherent fork-specific rendering and orchestration behind explicit props/events and narrow backend calls.
Keep changed upstream behavior visible where no independent contract can preserve the authoritative execution boundary.
Do not restore subscription files or alter shipped migrations to improve a line metric.

1. Review all remaining shared paths, using the existing ownership inventory as input rather than proof that every retained hunk is minimal.
2. Extract workflow settings, node configuration, run details and other substantial fork-only UI sections into focused components. Preserve v-model updates, permission/disabled states, emitted events and CSS behavior.
3. Extract additional backend route construction, registration and fork-specific processing behind focused helpers. The required Terra agent owns security-sensitive Go changes and verifies middleware, transaction, admission and fencing order.
4. Review remaining constants, schema additions and build integration. Further helper/tooling redesign and runtime migration namespacing remain separate scope.
5. Run targeted behavior/structural checks after each extraction; compare desktop/mobile rendering and interactions against the exact starting UI. Run complete retained gates on the final source, including database/HA gates where affected.
6. Record before/after metrics and residual hunks with concrete reasons. Commit source and current architecture/runbook documentation. Retain failures and baseline evidence; publication remains separate.

## Validation and Ownership

The main agent owns UI, documentation, metrics and integration.
The dedicated Terra agent owns Go backend source/tests and security review.
Browser plugin is not available; use the repository-installed Playwright with isolated synthetic fixtures and the existing local QA policy.
Baseline frontend results are 249 passing tests and three known failures in ArgsPicker, YesNoDialog and Socket; mobile WorkflowEditor overflow also exists at the starting commit.
These remain failed or known findings, never implicit passes.

## Completion Criteria

Review every remaining shared path and implement each identified, scope-preserving extraction with adequate evidence.
Residual statements describe the actual dependency or integration constraint; category membership alone is insufficient.
Measure changed lines and actual integration structure separately. Do not claim zero future conflicts or a mathematically proven minimum.

## Reviewed Outcome

The follow-up source is committed in `34513a6c`, `38913eb1` and `c82195b9`.
See `maintenance/source-separation/README.md` and its per-path table for the complete result and residual boundaries.
Raw evidence is retained locally under `evidence/`, including failed diagnostics.
The current normal frontend gate still fails; the separate test-tooling correction has not been authorized.
Candidate HA has a passing seven-scenario run and retained failures that also reproduce on the exact baseline images.
Publication remains pending.

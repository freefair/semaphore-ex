# Release throughput

## Objective and boundaries

Reduce repeated release work while preserving the existing correctness and
security gates. The user explicitly requested this pipeline change during the
v2.20.0-ex.2.1.2 run. That immutable tagged run continues unchanged; it is not
cancelled, restarted or retagged. No upstream synchronization is permitted.

## Measured baseline

For commit 02d45cfedc9813ca6a7fcae8035941b2f201b6a5:

- Develop Full Product Build: 1,150 seconds for artifacts, HA in parallel.
- Mandatory Beta dry-run: full-product gate 967 seconds, signed packages 1,310
  seconds, then images 1,007 seconds. Server and runner builds are serial.
- Final tag runs the same full-product gate, package build and image build again.

These are observed run durations, not promised future timings.

## Implementation sequence

1. Add a small release CI verifier under `tools/release/`, with deterministic
   fixture tests. Require current successful Dev and Full Product Build push runs
   for the exact release commit in this repository on develop. Reject failed,
   cancelled, pending, unrelated and stale run attempts and API errors. A dedicated
   Terra agent implements/reviews this security-relevant gate.
2. Replace the reusable full-build call in `product_release.yml` and
   `product_beta.yml` with this verifier. Keep the normal develop CI unchanged.
   Missing evidence must fail visibly; there is no skip/force input.
3. Run final signed packages and versioned image builds after the lightweight
   gate in parallel. Use two calls to a reusable image workflow so server and
   runner build independently and return separate unambiguous image digests.
   Keep existing Dockerfiles, platforms, signing, SBOM and inline cache policy.
   Install only GoReleaser and go-task in package jobs, omitting unused Swagger
   and GolangCI-Lint installations from the broad task deps target.
4. Promote latest from the produced versioned image digests only after packages
   and both image builds succeed. Do not rebuild images for promotion.
5. Make the Beta dry-run an explicit packaging/signing diagnostic rather than a
   mandatory step in every ordinary release. Update maintenance/RELEASING.md,
   the project sync skill release guidance where applicable, and the Wiki ADR and
   release guide so future agents follow the single-build path.
6. Wire the lightweight gate fixture tests into the existing product CI job.
   Expose a read-only manual gate check without inputs for cheap validation.
   Verify gate rejection cases, validate workflow syntax/expressions, request an
   independent review, and measure the changed workflow structure against the
   baseline. Continue finishing the already-running v2.20.0-ex.2.1.2 release.

## ADR: reuse exact-commit CI evidence

Status: accepted for implementation following the user's request.

The tag workflow validates successful exact-commit push runs instead of repeating
tests and HA checks. Workflow identity, repository, event, branch, commit and the
latest attempt are part of the decision. Release artifacts are built once with
their final tag; tests and binaries are not accepted from another commit or a PR.

A full build-artifact promotion architecture could also share application binaries
between GoReleaser and Docker builds, but changes image inputs and build contracts.
It is deferred in favour of removing measured redundant gates and serial work
without changing shipped runtime contents or current Docker cache governance.

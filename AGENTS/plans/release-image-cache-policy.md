# Release image cache policy

## Goal

Keep server and runner caches independent, stop Docker builds from consuming the
GitHub Actions cache quota, and remove the separate cache-upload failure after an
otherwise successful image publication.

## Decision

Use inline cache metadata in the existing published images. Import the server
cache from the server's `latest` image and the runner cache from the runner's
`latest` image. Manual Beta runs import only because they do not publish images.

Separate GHA scopes would prevent cache-index collisions but retain shared quota
pressure and an additional upload. Separate registry cache images would preserve
intermediate stages but add storage and retention policy. Inline caching avoids
both at the cost of less reuse of intermediate build stages.

## Changes

1. Update the two image steps in `product_release.yml` and `product_beta.yml`.
2. Document the policy, cold-cache fallback and tradeoffs in the release runbook
   and ADR 0014; update the slice status.
3. Verify locally, review, commit and publish docs before the root pointer.
4. Verify the exact pushed heads and run the manual Beta dry run. Preserve all
   existing releases and tags; this change needs no new product version.

## Verification

- Run actionlint and compare the parsed workflow contracts, including Beta
  dry-run export omission and unchanged build/push failure handling.
- Use an isolated local registry and independent BuildKit builders to demonstrate
  inline export, remote warm-cache reuse, image isolation, missing/unavailable
  cache fallback, and hard failures for broken builds or image publication.
- Run the documentation checker and the serial build with the real Pages URL and
  base path, then verify navigation and security fallbacks.
- Obtain direct Claude peer review and the repository-required Terra security
  review. Retain evidence; remove only task-owned fixtures when finished.

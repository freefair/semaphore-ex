# Publish the Git SSH repair

## Scope

Publish a regular Semaphore EX release containing the Git SSH fix.
The user explicitly excludes deployment to their server or runner instances.
The latest remote release and final EX tag are `v2.20.0-ex.2`; the user selected `v2.20.0-ex.2.1` for this release.

## Plan

1. Add the `v2.20.0-ex.2.1` changelog section and verify extracted release notes; extend the final-release tag filter to accept one, two or three numeric EX components as requested.
2. Verify current remote branch tips and push docs `main` before application `develop`.
3. Require successful Dev and Full Product Build workflows on the exact release commit.
4. Run the documented Full Product Beta dry run, inspect package artifacts and verify checksum signatures.
5. Run the documented dependency checks and verify the license inventory.
6. Tag the verified commit, push the final tag, and wait for Full Product Release.
7. Verify signed release assets and both container images, then publish the GitHub release draft.
8. Report the release URL, package/image versions, and verification evidence; clean task-owned local artifacts.

## Decision

Use the established tag-driven signed release workflow from `maintenance/RELEASING.md`.
Publishing a development binary would bypass the package, signature, container and product gates.
The ordinary release includes the already-integrated upstream cancellation fixes since EX.2, described in the changelog.

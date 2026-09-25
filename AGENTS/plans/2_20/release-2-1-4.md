# Release v2.20.0-ex.2.1.4

## Scope

Publish the reviewed SSH mapping and inventory-refresh corrections from
a88ee175 and 7667978c as the next stable patch after v2.20.0-ex.2.1.3.
Dennis requests publication. Preserve unrelated work and published history.

## Approach

Use the existing final tag workflow and exact-commit CI gate described in
maintenance/RELEASING.md. A Beta rehearsal would repeat unchanged packaging
and is unnecessary. The already completed full local gates and Claude review
remain evidence for the unchanged application source.

1. Add the versioned CHANGELOG section and validate release-note extraction.
2. Check the pinned Go vulnerability scan, production dependency audit and
   unchanged third-party license coverage.
3. Commit only release metadata and push develop after checking origin.
4. Require successful Dev and Full Product Build runs on the release commit.
5. Create and push the immutable annotated EX version tag. The existing workflow
   builds signed packages and both images, then promotes exact digests to latest.
6. Verify workflow completion, draft notes, assets, checksum signature and image
   versions. Publish the final GitHub release and read it back.
7. Clean task-owned downloads and test resources; retain evidence and archive
   the task journal.

## Boundaries

This release changes metadata only. It introduces no additional upstream sync,
dependency upgrades, application changes, helper edits or deployment actions.
The Wiki already documents the shipped behavior and remains its English source.

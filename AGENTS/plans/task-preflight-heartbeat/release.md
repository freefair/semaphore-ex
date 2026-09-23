# Release v2.20.0-ex.2.1.2

## Scope and authority

The user requested publication of the completed task-dialog fix and approved
`v2.20.0-ex.2.1.2`. Publication includes the ordinary develop push, release tag,
GitHub release and versioned/latest server and runner images through existing CI.
The user explicitly excludes upstream synchronization: fetch only origin when
checking publication races; perform no upstream fetch, merge, rebase or sync.
Preserve the already-integrated develop lineage and unrelated untracked evidence.
The reported Global credential service failure remains uninvestigated on the
deployed instance and is not represented as fixed by this release.

## Sequence

1. Review the exact task-dialog patch with Claude using the user's explicit
   approval to transmit that diff. Preserve the completed dedicated security
   review, frontend tests, affected backend tests and in-app browser evidence.
2. Add and validate the changelog section. Use a clean temporary verification
   clone of the local candidate for the full repository gate, so unrelated
   untracked history-rebuild evidence remains in the user's checkout.
3. Run pinned-toolchain vulnerability checks and the production npm audit.
   Verify dependency/license inputs are unchanged since the previous release.
4. Publish the verified develop commit with a normal push after checking origin.
   Require successful Dev and Full Product Build runs for that exact SHA.
5. Dispatch Full Product Beta on develop as a dry run, wait for success, download
   its signed snapshot artifact and verify its checksum signature.
6. Publish the prepared execution-review Wiki updates independently, preserving
   the user-managed sidebar. Create and push only the approved release tag.
7. Wait for Full Product Release, verify draft notes, signatures, packages and
   both image tags, publish the final GitHub release and verify its public state.
8. Remove task-owned temporary clones, downloaded artifacts and test resources;
   retain evidence logs and record final release and workflow identities.

## Verification-copy decision

A local disposable clone permits the required clean-source checks without moving,
staging or deleting unrelated untracked files. It does not introduce a product
branch or modify the main checkout's source. This is verification isolation only.

## Review follow-up decision

Refresh scheduling uses the response's HTTP Date and review expires_at, both from
the server, rather than the browser clock. Invalid or insufficient timing metadata
does not start a tight retry loop; server-side expiry validation remains authoritative.
Background refresh keeps the existing review visible while disabling Run. Changed
fingerprints display the existing plan-changed message. A fresh 409 response resets
the expiry timer using that response's server date.

The existing ItemFormBase load-error handling, visible denial rendering, sole
NewTaskDialog consumer and locale fallback address the review's contextual questions.
The existing legacy capability-unavailable behavior remains unchanged. Exact runner
load stays in the digest: changing placement semantics is outside this heartbeat fix.

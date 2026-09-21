# Keep documentation exclusively in the GitHub Wiki

## Scope

Remove the product repository's local `docs` checkout and submodule registration.
The published Wiki remains independent and unchanged; the user maintains its sidebar.

## Plan

1. Confirm the Wiki checkout has no unpublished file changes.
2. Remove the docs CI job, task and checker, and local Wiki requirements from maintenance scripts.
3. Replace active local documentation references with published Wiki links.
4. Remove the docs checkout and submodule registration, check script syntax and commit the removal.

## Decision

The Wiki is the sole documentation source. Keeping a product submodule adds an
unwanted checkout and pinned commit to maintain. Product builds and maintenance
therefore operate without a local documentation repository or docs pipeline.

## Follow-up: retire docs CI and rename Wiki pages

Delete the obsolete Markdown workflow in the retired docs repository. Rename
Wiki pages to concise subject titles and update all page links, including sidebar
targets, while preserving the user-maintained sidebar structure. Use a temporary
Wiki checkout only. Check collisions and link targets, publish, then delete the
temporary checkout. Product CI duration and other jobs are outside this request.

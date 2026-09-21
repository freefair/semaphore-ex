# Move English documentation to the product Wiki

## Scope

Use `freefair/semaphore-ex.wiki.git` as the sole English documentation source.
Retarget the product docs submodule, remove the old Pages publication, and use
only the authenticated Codex in-app browser for UI interactions.

## Plan

1. Move the existing English Markdown pages and assets to the Wiki with working links.
2. Remove duplicate body titles and the custom sidebar; use GitHub page titles and native navigation, plus the full Contents page.
3. Remove reference generators and their build/pipeline dependencies. Keep all reference content as directly edited Markdown and a read-only check.
4. Replace obsolete commercial-edition planning with the current single-product model.
5. Publish the Wiki, then its product pointer and integration. Inspect the rendered result in the in-app browser.

## Decisions

The Wiki itself is canonical. A second maintained repo or synchronized mirror
would contradict the user's request. Plain Markdown needs neither reference
generation nor a site build. The old docs repository remains historical provenance;
its Pages publication is disabled. English content alone is migrated.

## Validation

Check links, anchors and assets without modifying the docs. Confirm no body H1
or docs-generation task remains. Inspect Wiki rendering and native page navigation.

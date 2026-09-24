# Isolate rerun parameters

## Scope and sequence

On the inspected develop checkout, isolate TaskForm's editable parameters from
its sourceTask. Leave workflow editors and unrelated fork-history work unchanged.

1. Reproduce source mutation during initialization and nested-list edits in
   the existing task preflight tests. Cover replacing the source and absent params.
2. Copy the source parameters after spreading the source task in TaskForm.vue.
3. Run frontend regression/build checks, inspect the actual rerun in the in-app
   browser, and obtain an independent Claude review.
4. Commit locally and clean up test-owned resources. No push or deployment.

## ADR: copy the JSON parameter tree at the form boundary

Status: accepted.

Task parameters are JSON API data. Copy their complete tree on assignment,
following the existing JSON-copy convention in the workflow editor. This isolates
both scalar option edits and nested arrays without adding a dependency or changing
Vue/browser requirements. A shallow object spread would leave arrays shared;
cloning the entire task would unnecessarily expand the change to other fields.
Missing or null parameters become a fresh empty object. Defaults and reactive
preflight handling continue to operate on the form-owned copy.

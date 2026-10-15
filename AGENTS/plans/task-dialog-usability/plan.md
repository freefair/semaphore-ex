# Task dialog usability

## Scope and sequence

1. Widen `NewTaskDialog.vue` and arrange `TaskForm.vue` inputs alongside the
   execution review on desktop, stacking at narrow widths. Preserve full titles.
2. In `ExecutionPreflightReview.vue`, present reference names and bindings in
   aligned rows. Omit the routine provisional-placement warning, preserve all
   denials and other findings, and show provisional state only when supplied by
   the placement. Remove static explanatory footer text.
3. Remove the generic SSH-key description from task/template forms; keep actual
   inherited key state and validation. Give the two Terraform auto-approve
   controls distinct labels using the enhanced English/German dictionaries.
4. Verify rendered states and interactions in a disposable local frontend
   harness with fixture responses, run focused unit tests, lint and frontend build.
   Do not execute real tasks, deploy, push, or alter the unrelated untracked plan.

## Decision record

Status: accepted.

Merely increasing width would retain the long vertical form and duplicate-looking
Terraform controls. Collapsing the review would hide information needed before
execution. Use a responsive two-column task layout with an always-visible review;
use a reference table to preserve each distinct binding without long inline chains.
The API contract remains unchanged: filter only the informational/routine warning
with code `provisional_placement`, never a denial with that code. Show placement
provisional state inline instead. Existing readiness and re-review behavior stay
unchanged. Terraform labels distinguish the default value from permission to
change it at task start, without changing either stored Boolean.

## Verification

Regression coverage: routine-warning suppression, retention of real warnings and
denials, conditional provisional state, all reference bindings, full template
title, and independent Terraform default/override controls. Browser verification:
Ansible task dialog, Terraform template creation, concrete denial, desktop and
narrow layout, input changes causing a fresh review. Use actual Vue components
and fixture API responses; no claim of backend or deployed-instance validation.

## Verified result

- Five new regression cases pass, including independent Terraform controls in
  English and German; 23 existing execution/preflight cases pass (28 total).
- Targeted ESLint and production frontend build pass.
- Codex in-app browser: desktop and 390 × 844 task layouts, full title, scrollable
  review with fixed actions, Dry Run refresh, independent Terraform switches,
  and no-candidate denial with disabled Run verified using fixture responses.
- Reference rows use two columns, with the resource type below its name, so
  binding targets have enough width. Narrow headings stack the task action below
  the complete template name.
- The maintenance wrapper refuses untracked files, including the unrelated
  `fork-history-rebuild` plan. It was not bypassed; full-product gates and deployed
  backend execution are outside the completed local frontend verification.
- Existing VAlert Boolean/string and Terraform field-translation warnings remain
  separate follow-up work. No backend behavior or API payload was changed.

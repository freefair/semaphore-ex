# Direct Terraform task confirmation

## Scope and sequence

Work on the inspected develop checkout. Preserve unrelated fork-history work.
No deployment or publication is included.

1. Reproduce lost Terraform options in TaskForm and confirmation/progress races
   in the runner API, including another HA node persisting the decision.
2. Initialize editable task fields reactively in TaskForm.vue, following the
   existing TaskParamsForm pattern, so review and start use current input.
3. Preserve confirmed/rejected decisions when an assigned runner reports an
   older waiting-confirmation snapshot. Keep runner identity/generation fences.
4. Cover plan-only, auto-approve, manual confirmation/rejection and stop;
   verify actual components in the in-app browser with synthetic local data.
5. Run applicable verification, request independent Claude review, commit the
   verified changes, and clean up task-owned temporary resources.

## ADR: preserve input and asynchronous control decisions

Status: accepted.

Vue 2 does not observe properties added to an existing object by assignment.
TaskForm initializes params after observation; the cached review signature can
therefore retain the initial params even after a checkbox changes. Initialize
all editable fields in a fresh object, reusing TaskParamsForm's established
pattern. Rebuilding a payload only at submission would bypass the reviewed
payload contract and leave the displayed review stale.

Runner progress and server control decisions travel independently. An in-flight
waiting-confirmation report from the same assignment must not overwrite a
confirmed/rejected decision or cause emergency termination. Normalize only
these stale reports to the server decision, including a bounded retry after a
cross-node conditional-update conflict. Disabling transition or assignment
checks would weaken the lifecycle contract; changing Terraform CLI flags would
not address either root cause.

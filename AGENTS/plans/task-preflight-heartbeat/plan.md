# Reliable task-template starts

## Scope

Fix repeated stale execution reviews when a healthy remote runner polls between
preview and confirmation. Work on the inspected `develop` checkout and preserve
unrelated untracked fork-history work. No deployment or publication is included.

## Implementation order

1. Reproduce a heartbeat-only change using the task preflight fixture, with the
   regression authored in temporary storage before changing executable source.
2. In `services/tasks/execution_preflight.go`, bind placement review to effective
   runner availability instead of the raw heartbeat timestamp. Preserve runner
   configuration, capacity and placement-change detection.
3. Add adjacent regression coverage for healthy heartbeat refresh, actual
   availability changes and reviewed enqueue. Security-related implementation
   and evidence are handled by the dedicated Terra agent required by CLAUDE.md.
4. Load and display the review automatically when the task dialog opens, as
   requested by the user. Refresh it after input changes, discard obsolete
   responses, and disable Run until the current review is ready. Keep one Run
   action and retain explicit acknowledgement of genuine server-side drift.
   Change `web/src/lib/enhanced/task-form{,-state}.js`, the Enhanced dialog
   options, TaskForm/NewTaskDialog, and a default-off save-disabled prop on
   EditDialog. Add localized loading/error states and focused lifecycle tests.
   Verify the actual components through the in-app browser using isolated
   synthetic API fixtures, without starting a task on shared infrastructure.
5. Run focused regression checks, independent review and applicable repository
   gates. Record the decision in an ADR for the product Wiki and retain evidence.
6. Commit verified changes, without pushing. Archive the task journal and clean
   up task-owned temporary infrastructure.

## ADR: compare effective heartbeat state

Status: accepted.

The current placement digest includes `Runner.Touched`, which is updated by every
poll. A healthy runner therefore invalidates an otherwise identical signed review.
Use the existing `Runner.IsOnline(now, offlineTimeout)` semantics so a refreshed
heartbeat preserves the binding while an offline transition remains meaningful.

Retaining the timestamp preserves the reported failure. Ignoring placement
changes altogether would hide meaningful drift. Effective liveness is the narrow
correction and reuses the dispatch model without adding a new timing policy.

The user selected automatic visible review, followed by one Run click. An optional
review or silent automatic confirmation would change that requested interaction.

## ADR: load the task review automatically

Status: accepted, following the user's explicit request.

Load the review after initial form data and defaults are available. Refresh after
input changes with a short debounce, and fence responses by request generation
and serialized input signature. Disable Run while loading, on errors or denials,
and during submission. Submit the displayed review and the same input payload on
the first Run click. Genuine server-side drift still requires another Run click.
Dispose the form on dialog close so obsolete requests cannot revive its review.

Keeping the first Run click as a preview action preserves the reported interaction
problem. Optional review and automatic stale-plan retries would change the requested
contract. Workflow dialogs retain their existing behavior. The shared EditDialog
only gains an optional save-disabled prop, preserving other consumers' defaults.

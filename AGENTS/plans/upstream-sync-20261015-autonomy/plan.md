# Autonomous sync authorization

## Goal

Make the existing full-sync-and-push authorization effective throughout the workflow and future scheduled runs, then resume the pending upstream sync.

## Approach

1. Align root `CLAUDE.md`, the canonical sync skill, and its discovery prompt with the explicit autonomous authorization. Keep assessment-only mode and genuine authority boundaries intact.
2. Update the existing automation prompt, preserving its schedule, model, project and status. Read back the saved configuration.
3. Record the decision in the Wiki maintenance runbook and ADR. Validate the skill and compare all entry points against the same authorization scenarios.
4. Diagnose the local browser-fixture guard false positive using synthetic input only. Complete browser verification and the existing merge, then assess and integrate newer upstream work with the prescribed gates.
5. Publish verified product and Wiki commits by ordinary pushes and check exact-head CI. Preserve unrelated work and recovery refs.

## Options and decision

Repeated per-step approval adds no authority after an explicit full-sync-and-push request and interrupts unattended runs. Globally disabling command or credential guards would exceed this project-specific request. Use persistent, scoped authorization in the repository and automation instead, with safe diagnosis of technical failures and genuine tool-level approval boundaries preserved.

## Verification

Validate skill metadata and instructions. Read the saved automation configuration and confirm only its prompt changed. Exercise authorization scenarios for resumed QA, ordinary publication, assessment-only work, concurrent remote advancement, and unrelated destructive work. Complete the product's existing verification requirements before publication.

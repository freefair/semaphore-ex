# Container workflow output transport

## Goal

Deliver workflow outputs from supported Docker and Kubernetes executors through
the existing `OutputsProvider` boundary without emitting output values in task
logs or exposing runner paths to task containers.

## Design

1. Add a fixed `collect-outputs` container stage. It reads only the bounded
   workflow-output file and writes no diagnostic data to stdout or stderr.
2. Docker transfers fixed output files through an allowlisted exec stage after a
   successful task stage and before container cleanup. Archive members and byte
   bounds are checked on the runner before collector validation; task images need
   no Python interpreter for this transfer. The file-copy API was rejected because
   Docker cannot copy from the existing tmpfs workspace.
3. Kubernetes uses the configured helper image as a restricted collector
   sidecar. The task container exits naturally; the runner proves that exact
   container's terminal state through the UID-bound Pod API, then retrieves
   outputs through the helper's fixed collector stage and acknowledges through
   a sidecar-only control volume which the task container cannot mount.
4. The Kubernetes client checks the immutable pod UID and task container name
   before exec. The job keeps token automount disabled and retains its existing
   restricted security context.
5. Both executors implement `tasks.OutputsProvider`; the runner persists their
   validated output documents through the existing optional interface.

## Validation

Focused Docker and Kubernetes tests cover successful retrieval, bounded data,
identity checks, acknowledgement, cancellation, and preservation of task exit
status. Existing container-plan tests cover the callback bundle and private
workspace preparation.

## ADR notes for the product Wiki

The runner retrieves output documents through fixed file paths for Docker and
a fixed helper-stage allowlist for Kubernetes, independently of task logs. Docker
keeps its disposable task container alive for bounded archive retrieval. Kubernetes needs a runner-only `pods/exec`
permission and verifies the Pod UID immediately before every retrieval stage;
the task Pod remains tokenless and restricted. A sidecar was selected over a
same-container handshake because arbitrary task code runs as the same UID and
can forge any shared-workspace marker. The helper sidecar has a read-only
workspace mount and its own control volume, so only the runner can acknowledge
completion after validating the transferred document.

Raw Terraform transport is capped at 4 MiB, sixteen times the 256 KiB public
output document budget. This lets the collector filter large sensitive values
before enforcing the public limit. A capture failure or overflow removes the
private raw file, reports only a generic diagnostic, and never changes a
successful Terraform task result.

Raw Terraform transport is capped at 4 MiB, sixteen times the 256 KiB public
output document budget. This lets the collector filter large sensitive values
before enforcing the public limit. A capture failure or overflow removes the
private raw file, reports only a generic diagnostic, and never changes a
successful Terraform task result.

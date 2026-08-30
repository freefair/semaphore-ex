package docker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// RemediateDockerReconciliation is the sole runner-side interpreter for the
// admin's fixed cleanup action. Every branch re-inspects the exact immutable
// daemon identity; a changed identity or label is unresolved, never removed.
func (p *Provider) RemediateDockerReconciliation(ctx context.Context, command db.DockerReconciliationRemediationCommand) db.DockerReconciliationRemediationResult {
	result := p.remediateDockerReconciliation(ctx, command)
	if command.Target == db.DockerReconciliationRemediationTargetCandidate {
		state := db.DockerTelemetryOrphanUnresolved
		if result.Status == db.DockerReconciliationRemediationSucceeded {
			state = db.DockerTelemetryOrphanRemoved
		}
		p.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryOrphan, OrphanState: state, Count: 1})
	} else if command.Target == db.DockerReconciliationRemediationTargetQuarantine {
		state := db.DockerReconciliationQuarantine
		if result.Status == db.DockerReconciliationRemediationSucceeded {
			state = db.DockerReconciliationAbsent
		}
		p.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryReconciliation, ReconciliationState: state, Count: 1})
	}
	return result
}

func (p *Provider) remediateDockerReconciliation(ctx context.Context, command db.DockerReconciliationRemediationCommand) db.DockerReconciliationRemediationResult {
	result := db.DockerReconciliationRemediationResult{CommandID: command.CommandID, Fingerprint: command.Fingerprint, Status: db.DockerReconciliationRemediationErrored, Evidence: db.DockerReconciliationEvidenceIdentityMismatch}
	if command.Validate() != nil || command.RunnerID != p.effectiveRunnerID() || command.SessionID != p.DockerReconciliationSession().SessionID {
		return result
	}
	if command.Target == db.DockerReconciliationRemediationTargetQuarantine && !matchesQuarantineFingerprint(command) {
		return result
	}
	if command.Target == db.DockerReconciliationRemediationTargetCandidate && command.CandidateResource == db.DockerReconciliationCandidateVolume {
		return p.remediateVolume(ctx, command, result)
	}
	if command.Target != db.DockerReconciliationRemediationTargetCandidate && command.Target != db.DockerReconciliationRemediationTargetQuarantine {
		return result
	}
	state, err := p.client.InspectContainer(ctx, command.DaemonID)
	if err != nil {
		result.Evidence = db.DockerReconciliationEvidenceDaemonUnavailable
		return result
	}
	if !state.Exists {
		if command.Target == db.DockerReconciliationRemediationTargetCandidate {
			// An orphan candidate has no task tuple to fall back to. Absence is
			// therefore ambiguous (the daemon object may have been replaced), not
			// proof that this specific candidate was cleaned up.
			return result
		}
		result.Status, result.Evidence = db.DockerReconciliationRemediationSucceeded, db.DockerReconciliationEvidenceAlreadyAbsent
		return result
	}
	if state.ID != command.DaemonID || !matchesRemediationLabels(state.Labels, command) {
		return result
	}
	if state.Running {
		if err = p.client.StopContainer(ctx, command.DaemonID, 10*time.Second); err != nil {
			if killErr := p.client.KillContainer(ctx, command.DaemonID); killErr != nil {
				result.Evidence = db.DockerReconciliationEvidenceDaemonUnavailable
				return result
			}
		}
		for attempt := 0; attempt < 3; attempt++ {
			state, err = p.client.InspectContainer(ctx, command.DaemonID)
			if err != nil {
				result.Evidence = db.DockerReconciliationEvidenceDaemonUnavailable
				return result
			}
			if !state.Exists {
				result.Status, result.Evidence = db.DockerReconciliationRemediationSucceeded, db.DockerReconciliationEvidenceAlreadyAbsent
				return result
			}
			if state.ID != command.DaemonID || !matchesRemediationLabels(state.Labels, command) {
				return result
			}
			if !state.Running {
				break
			}
			if attempt == 2 {
				break
			}
			select {
			case <-ctx.Done():
				result.Evidence = db.DockerReconciliationEvidenceDaemonUnavailable
				return result
			case <-time.After(100 * time.Millisecond):
			}
		}
		if state.Running {
			result.Status, result.Evidence = db.DockerReconciliationRemediationPending, db.DockerReconciliationEvidenceStillRunning
			return result
		}
	}
	if err = p.client.RemoveContainer(ctx, command.DaemonID); err != nil {
		result.Status, result.Evidence = db.DockerReconciliationRemediationPending, db.DockerReconciliationEvidenceStillRunning
		return result
	}
	result.Status, result.Evidence = db.DockerReconciliationRemediationSucceeded, db.DockerReconciliationEvidenceRemoved
	return result
}

func matchesQuarantineFingerprint(command db.DockerReconciliationRemediationCommand) bool {
	key := command.Quarantine
	if key == nil {
		return false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s", command.Target, command.DaemonID, key.RunnerBoot, key.ProjectID, key.TaskID, key.Generation, key.Resource)))
	return fmt.Sprintf("%x", sum[:]) == command.Fingerprint
}

func (p *Provider) remediateVolume(ctx context.Context, command db.DockerReconciliationRemediationCommand, result db.DockerReconciliationRemediationResult) db.DockerReconciliationRemediationResult {
	resources, err := p.client.ListManagedResources(ctx, command.RunnerID)
	if err != nil {
		result.Evidence = db.DockerReconciliationEvidenceDaemonUnavailable
		return result
	}
	for _, resource := range resources {
		if resource.Kind != ManagedVolume || resource.ID != command.DaemonID {
			continue
		}
		if resource.CreationIdentity == "" || resource.CreationIdentity != command.CandidateIdentity || !matchesRemediationLabels(resource.Labels, command) {
			return result
		}
		used, usedErr := p.client.VolumeReferenced(ctx, command.DaemonID)
		if usedErr != nil {
			result.Evidence = db.DockerReconciliationEvidenceDaemonUnavailable
			return result
		}
		if used {
			result.Status, result.Evidence = db.DockerReconciliationRemediationPending, db.DockerReconciliationEvidenceVolumeInUse
			return result
		}
		if removeErr := p.client.RemoveVolume(ctx, command.DaemonID); removeErr != nil {
			result.Status, result.Evidence = db.DockerReconciliationRemediationPending, db.DockerReconciliationEvidenceVolumeInUse
			return result
		}
		result.Status, result.Evidence = db.DockerReconciliationRemediationSucceeded, db.DockerReconciliationEvidenceRemoved
		return result
	}
	// Candidate identity is daemon evidence, not a name lookup. A missing
	// volume could be a replacement between scans, so it stays quarantined.
	return result
}

func matchesRemediationLabels(labels map[string]string, command db.DockerReconciliationRemediationCommand) bool {
	if labels["io.semaphore.managed"] != "v1" || labels[labelExecutor] != "docker" || labels["io.semaphore.runner-id"] != strconv.Itoa(command.RunnerID) {
		return false
	}
	if command.Target == db.DockerReconciliationRemediationTargetCandidate {
		return true
	}
	key := command.Quarantine
	return key != nil && labels[labelRunnerBoot] == key.RunnerBoot && labels[labelProjectID] == strconv.Itoa(key.ProjectID) && labels[labelTaskID] == strconv.Itoa(key.TaskID) && labels["io.semaphore.assignment-generation"] == strconv.Itoa(key.Generation) && labels[labelResource] == string(key.Resource)
}

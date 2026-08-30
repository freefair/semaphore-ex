package docker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

type reconciliationTuple struct {
	boot       string
	projectID  int
	taskID     int
	generation int
	resource   db.DockerReconciliationResource
}

func (t reconciliationTuple) targetKey(name string) string {
	return db.DockerReconciliationScanTarget{TargetBoot: t.boot, ProjectID: t.projectID, TaskID: t.taskID, Generation: t.generation, Resource: t.resource, ContainerName: name}.ScanKey()
}

// ScanDockerReconciliation turns the narrow daemon inventory into one exact,
// bounded scan batch. It never chooses tuple identities: the server-issued
// target list is the only source for readiness-relevant observations.
func (p *Provider) ScanDockerReconciliation(ctx context.Context, session db.DockerReconciliationSession) ([]db.DockerReconciliationObservation, db.DockerReconciliationScanComplete, []db.DockerReconciliationOrphanCandidate, error) {
	if session.ValidatePublic() != nil || session.RunnerID != p.effectiveRunnerID() || len(session.ScanTargets) > maxDockerReconciliationPageSize {
		return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("invalid Docker reconciliation scan session")
	}
	for _, target := range session.ScanTargets {
		if target.Validate() != nil {
			return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("invalid Docker reconciliation scan target")
		}
	}
	// Exact target readiness is O(page size): inspect only server-issued names.
	// Orphan inventory is deliberately not part of this path; a full daemon list
	// per page would turn N pages into O(N×resources) work and can never be a
	// prerequisite for dispatch safety.
	resources := make([]ManagedResource, 0, len(session.ScanTargets))
	for _, target := range session.ScanTargets {
		state, err := p.client.InspectContainer(ctx, target.ContainerName)
		if err != nil {
			return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("inspecting Docker reconciliation target: %w", err)
		}
		if !state.Exists {
			continue
		}
		resources = append(resources, ManagedResource{Kind: ManagedContainer, ID: state.ID, Name: state.Name, Labels: state.Labels, Running: state.Running, StateKnown: true})
	}
	observations, complete, candidates, err := reduceDockerReconciliationScan(session, resources, time.Now().UTC())
	if err == nil {
		p.recordReconciliationTelemetry(observations, candidates)
	}
	return observations, complete, candidates, err
}

func (p *Provider) recordReconciliationTelemetry(observations []db.DockerReconciliationObservation, candidates []db.DockerReconciliationOrphanCandidate) {
	counts := make(map[db.DockerReconciliationState]int64)
	for _, observation := range observations {
		counts[observation.State]++
	}
	for state, count := range counts {
		p.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryReconciliation, ReconciliationState: state, Count: count})
	}
	if len(candidates) > 0 {
		p.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryOrphan, OrphanState: db.DockerTelemetryOrphanDetected, Count: int64(len(candidates))})
	}
}

func reduceDockerReconciliationScan(session db.DockerReconciliationSession, resources []ManagedResource, observedAt time.Time) ([]db.DockerReconciliationObservation, db.DockerReconciliationScanComplete, []db.DockerReconciliationOrphanCandidate, error) {
	if session.ValidatePublic() != nil || observedAt.IsZero() || len(session.ScanTargets) > maxDockerReconciliationPageSize {
		return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("invalid Docker reconciliation scan input")
	}
	targets := append([]db.DockerReconciliationScanTarget(nil), session.ScanTargets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].ScanKey() < targets[j].ScanKey() })
	targetByKey := make(map[string]db.DockerReconciliationScanTarget, len(targets))
	for _, target := range targets {
		if target.Validate() != nil {
			return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("invalid Docker reconciliation scan target")
		}
		if _, duplicate := targetByKey[target.ScanKey()]; duplicate {
			return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("duplicate Docker reconciliation scan target")
		}
		targetByKey[target.ScanKey()] = target
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		if resources[i].ID != resources[j].ID {
			return resources[i].ID < resources[j].ID
		}
		return resources[i].Name < resources[j].Name
	})
	matches := make(map[string][]ManagedResource, len(targets))
	candidates := make([]db.DockerReconciliationOrphanCandidate, 0)
	for _, resource := range resources {
		tuple, reason := parseManagedReconciliationTuple(resource, session.RunnerID)
		if reason != "" {
			candidates = appendCandidate(candidates, orphanCandidate(resource, reason, observedAt))
			continue
		}
		if resource.Kind == ManagedVolume {
			// The server's exact target protocol intentionally has no volume tuple.
			// Preserve the volume as an auditable, non-destructive candidate instead.
			candidates = appendCandidate(candidates, orphanCandidate(resource, db.DockerReconciliationCandidateExtra, observedAt))
			continue
		}
		key := tuple.targetKey(resource.Name)
		if _, expected := targetByKey[key]; !expected {
			candidates = appendCandidate(candidates, orphanCandidate(resource, db.DockerReconciliationCandidateConflicting, observedAt))
			continue
		}
		matches[key] = append(matches[key], resource)
	}
	observations := make([]db.DockerReconciliationObservation, 0, len(targets))
	for index, target := range targets {
		sequence := target.Sequence
		if sequence <= 0 {
			// Legacy zero-valued test fixtures remain deterministic. Server-issued
			// target pages always carry a non-zero durable sequence.
			sequence = int64(index + 1)
		}
		matching := matches[target.ScanKey()]
		observation := db.DockerReconciliationObservation{Sequence: sequence, RunnerID: session.RunnerID, RunnerBoot: target.TargetBoot, ProjectID: target.ProjectID, TaskID: target.TaskID, Generation: target.Generation, Resource: target.Resource, ContainerName: target.ContainerName, ObservedAt: observedAt, QuarantineStatus: db.DockerReconciliationQuarantineNone, Remediation: db.DockerReconciliationRemediationNone}
		switch len(matching) {
		case 0:
			observation.State = db.DockerReconciliationAbsent
		case 1:
			observation.ContainerID = matching[0].ID
			if !matching[0].StateKnown {
				observation.State = db.DockerReconciliationUnknown
			} else if matching[0].Running {
				observation.State = db.DockerReconciliationRunning
			} else {
				observation.State = db.DockerReconciliationExited
			}
		default:
			observation.State = db.DockerReconciliationQuarantine
			observation.Reason = "duplicate"
			observation.QuarantineStatus = db.DockerReconciliationQuarantinePending
			observation.Remediation = db.DockerReconciliationRemediationInspect
			observation.RemediationReason = "duplicate"
			when := observedAt
			observation.QuarantinedAt = &when
		}
		if observation.Validate() != nil {
			return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("invalid reduced Docker reconciliation observation")
		}
		observations = append(observations, observation)
	}
	complete := db.DockerReconciliationScanComplete{SessionID: session.SessionID, Fence: session.Fence, ScanCursor: session.ScanCursor, CoveredTargets: targets}
	if len(observations) > 0 {
		complete.HighestSequence = observations[len(observations)-1].Sequence
	}
	if complete.Validate() != nil {
		return nil, db.DockerReconciliationScanComplete{}, nil, fmt.Errorf("invalid reduced Docker reconciliation completion")
	}
	return observations, complete, candidates, nil
}

func parseManagedReconciliationTuple(resource ManagedResource, runnerID int) (reconciliationTuple, db.DockerReconciliationCandidateReason) {
	if resource.Kind != ManagedContainer && resource.Kind != ManagedVolume || !validManagedIdentity(resource.ID) || !validManagedIdentity(resource.Name) {
		return reconciliationTuple{}, db.DockerReconciliationCandidateMalformed
	}
	labels := resource.Labels
	if labels["io.semaphore.managed"] != "v1" || labels[labelExecutor] != "docker" || labels["io.semaphore.runner-id"] != strconv.Itoa(runnerID) {
		return reconciliationTuple{}, db.DockerReconciliationCandidateMalformed
	}
	projectID, ok := canonicalPositiveLabel(labels[labelProjectID])
	if !ok {
		return reconciliationTuple{}, db.DockerReconciliationCandidateMalformed
	}
	taskID, ok := canonicalPositiveLabel(labels[labelTaskID])
	if !ok {
		return reconciliationTuple{}, db.DockerReconciliationCandidateMalformed
	}
	generation, ok := canonicalPositiveLabel(labels["io.semaphore.assignment-generation"])
	if !ok || db.ValidateDockerRunnerBoot(labels[labelRunnerBoot]) != nil {
		return reconciliationTuple{}, db.DockerReconciliationCandidateMalformed
	}
	var kind db.DockerReconciliationResource
	switch labels[labelResource] {
	case string(db.DockerReconciliationResourceTask):
		kind = db.DockerReconciliationResourceTask
	case string(db.DockerReconciliationResourceHelper):
		kind = db.DockerReconciliationResourceHelper
	default:
		return reconciliationTuple{}, db.DockerReconciliationCandidateMalformed
	}
	return reconciliationTuple{boot: labels[labelRunnerBoot], projectID: projectID, taskID: taskID, generation: generation, resource: kind}, ""
}

func canonicalPositiveLabel(value string) (int, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed > 0 && strconv.Itoa(parsed) == value
}

func validManagedIdentity(value string) bool {
	return value != "" && len(value) <= db.MaxRunnerContainerIdentityLength && strings.TrimSpace(value) == value
}

func orphanCandidate(resource ManagedResource, reason db.DockerReconciliationCandidateReason, observedAt time.Time) db.DockerReconciliationOrphanCandidate {
	kind := db.DockerReconciliationCandidateContainer
	if resource.Kind == ManagedVolume {
		kind = db.DockerReconciliationCandidateVolume
	}
	identifier, name := resource.ID, resource.Name
	if !validManagedIdentity(identifier) {
		identifier = "invalid"
	}
	if !validManagedIdentity(name) {
		name = ""
	}
	identity := resource.ID
	if resource.Kind == ManagedVolume {
		identity = resource.CreationIdentity
	}
	return db.DockerReconciliationOrphanCandidate{Resource: kind, Identifier: identifier, Name: name, Identity: identity, Reason: reason, ObservedAt: observedAt}
}

func appendCandidate(candidates []db.DockerReconciliationOrphanCandidate, candidate db.DockerReconciliationOrphanCandidate) []db.DockerReconciliationOrphanCandidate {
	for _, existing := range candidates {
		if existing.Resource == candidate.Resource && existing.Identifier == candidate.Identifier && existing.Name == candidate.Name && existing.Reason == candidate.Reason {
			return candidates
		}
	}
	if len(candidates) >= maxDockerReconciliationPageSize {
		return candidates
	}
	return append(candidates, candidate)
}

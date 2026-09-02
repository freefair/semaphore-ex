package tasks

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// RunnerPlacementCandidate combines persisted runner data with authoritative
// in-flight assignment load from the task pool.
type RunnerPlacementCandidate struct {
	Runner       db.Runner
	RunningTasks int
}

type evaluatedRunnerPlacement struct {
	candidate  RunnerPlacementCandidate
	evaluation db.RunnerPlacementEvaluation
}

func runnerPlacementPolicy(
	template db.Template,
	inventory db.Inventory,
) ([]string, db.RunnerTagMatchMode, *string) {
	tags := template.EffectiveRunnerTags()
	matchMode := template.EffectiveRunnerTagMatchMode()
	legacyTag := template.RunnerTag
	if len(tags) == 0 && legacyTag == nil && inventory.RunnerTag != nil {
		tags = db.NormalizeRunnerTags([]string{*inventory.RunnerTag})
		legacyTag = inventory.RunnerTag
		matchMode = db.RunnerTagMatchAll
	}
	return tags, matchMode, legacyTag
}

func (p *TaskPool) validateExecutorImageCompatibility(task db.Task, template db.Template) error {
	inventory := db.Inventory{}
	inventoryID := template.InventoryID
	canOverride, err := template.CanOverrideInventory()
	if err != nil {
		return err
	}
	if canOverride && task.InventoryID != nil {
		inventoryID = task.InventoryID
	}
	if inventoryID != nil {
		inventory, err = p.store.GetInventory(template.ProjectID, *inventoryID)
		if err != nil {
			return err
		}
	}
	tags, matchMode, _ := runnerPlacementPolicy(template, inventory)
	projectRunners, err := p.store.GetRunners(
		template.ProjectID, false, db.RunnerFilterIgnoreTags, nil,
	)
	if err != nil {
		return err
	}
	globalRunners, err := p.store.GetAllRunners(
		false, true, db.RunnerFilterIgnoreTags, nil,
	)
	if err != nil {
		return err
	}
	matching := 0
	for _, runner := range append(projectRunners, globalRunners...) {
		if !runnerTagsMatch(runner, tags, matchMode) {
			continue
		}
		matching++
		if runner.SupportsExecutorImage() {
			return nil
		}
	}
	if matching > 0 {
		return db.ErrExecutorImageIncompatible
	}
	return nil
}

// DecideRunnerPlacement returns a deterministic, redacted decision for all
// candidates. Project runners precede global runners; within the same scope,
// lower assignment load and then stable runner ID win.
func DecideRunnerPlacement(
	projectID int,
	requestedTags []string,
	matchMode db.RunnerTagMatchMode,
	candidates []RunnerPlacementCandidate,
	now time.Time,
	offlineTimeout time.Duration,
	executorImages ...*string,
) db.RunnerPlacementDecision {
	tags := db.NormalizeRunnerTags(requestedTags)
	var executorImage *string
	if len(executorImages) > 0 {
		executorImage = executorImages[0]
	}
	if matchMode != db.RunnerTagMatchAny {
		matchMode = db.RunnerTagMatchAll
	}

	evaluated := make([]evaluatedRunnerPlacement, 0, len(candidates))
	for _, candidate := range candidates {
		evaluated = append(evaluated, evaluateRunnerPlacement(
			projectID, tags, matchMode, candidate, now, offlineTimeout, executorImage,
		))
	}
	sort.SliceStable(evaluated, func(i, j int) bool {
		left, right := evaluated[i], evaluated[j]
		if placementScopeRank(left.evaluation.Scope) != placementScopeRank(right.evaluation.Scope) {
			return placementScopeRank(left.evaluation.Scope) < placementScopeRank(right.evaluation.Scope)
		}
		if left.candidate.RunningTasks != right.candidate.RunningTasks {
			return left.candidate.RunningTasks < right.candidate.RunningTasks
		}
		return left.candidate.Runner.ID < right.candidate.Runner.ID
	})

	decision := db.RunnerPlacementDecision{
		RequestedTags:  tags,
		MatchMode:      matchMode,
		RequestedImage: executorImage,
		ResolvedImage:  executorImage,
		Evaluations:    make([]db.RunnerPlacementEvaluation, 0, len(evaluated)),
	}
	for _, item := range evaluated {
		decision.Evaluations = append(decision.Evaluations, item.evaluation)
		if decision.SelectedRunnerID == nil && item.evaluation.Eligible {
			id := item.candidate.Runner.ID
			decision.SelectedRunnerID = &id
			decision.SelectedName = item.candidate.Runner.Name
			decision.SelectedScope = item.evaluation.Scope
		}
	}
	if decision.SelectedRunnerID != nil {
		decision.ReasonCode = db.RunnerPlacementReasonSelected
		decision.Reason = fmt.Sprintf(
			"selected %s runner #%d by scope, current load, and stable runner id",
			decision.SelectedScope, *decision.SelectedRunnerID,
		)
		return decision
	}
	decision.ReasonCode = db.RunnerPlacementReasonNoCandidate
	decision.Reason, decision.ActionHint = rejectedPlacementSummary(tags, evaluated)
	return decision
}

func evaluateRunnerPlacement(
	projectID int,
	requestedTags []string,
	matchMode db.RunnerTagMatchMode,
	candidate RunnerPlacementCandidate,
	now time.Time,
	offlineTimeout time.Duration,
	executorImage *string,
) evaluatedRunnerPlacement {
	runner := candidate.Runner
	scope := db.RunnerPlacementGlobal
	if runner.ProjectID != nil {
		scope = db.RunnerPlacementProject
	}
	evaluation := db.RunnerPlacementEvaluation{
		RunnerID: runner.ID, RunnerName: runner.Name, Scope: scope,
		AcceptedCriteria: make([]string, 0, 7), RejectedCriteria: make([]string, 0, 7),
		AcceptedReasonCodes: make([]db.RunnerPlacementReasonCode, 0, 7),
		RejectedReasonCodes: make([]db.RunnerPlacementReasonCode, 0, 7),
	}
	criterion := func(ok bool, accepted string, acceptedCode db.RunnerPlacementReasonCode, rejected string, rejectedCode db.RunnerPlacementReasonCode) {
		if ok {
			evaluation.AcceptedCriteria = append(evaluation.AcceptedCriteria, accepted)
			evaluation.AcceptedReasonCodes = append(evaluation.AcceptedReasonCodes, acceptedCode)
		} else {
			evaluation.RejectedCriteria = append(evaluation.RejectedCriteria, rejected)
			evaluation.RejectedReasonCodes = append(evaluation.RejectedReasonCodes, rejectedCode)
		}
	}
	criterion(runner.ProjectID == nil || *runner.ProjectID == projectID,
		"project scope accepted", db.RunnerPlacementReasonScopeAccepted,
		"different project", db.RunnerPlacementReasonDifferentProject)
	criterion(runner.Active, "active", db.RunnerPlacementReasonActive,
		"inactive", db.RunnerPlacementReasonInactive)
	criterion(runner.IsRegistered(), "registered", db.RunnerPlacementReasonRegistered,
		"not registered", db.RunnerPlacementReasonNotRegistered)
	criterion(runner.IsOnline(now, offlineTimeout), "heartbeat accepted", db.RunnerPlacementReasonOnline,
		"offline", db.RunnerPlacementReasonOffline)
	criterion(runner.MaxParallelTasks <= 0 || candidate.RunningTasks < runner.MaxParallelTasks,
		"capacity available", db.RunnerPlacementReasonCapacityAvailable,
		"at capacity", db.RunnerPlacementReasonCapacity)
	criterion(runnerTagsMatch(runner, requestedTags, matchMode),
		"tag policy matched", db.RunnerPlacementReasonTagMatched,
		"tag policy did not match", db.RunnerPlacementReasonTagMismatch)
	if executorImage != nil {
		criterion(runner.SupportsExecutorImage(),
			"executor image compatible", db.RunnerPlacementReasonImageSupported,
			"executor image unsupported", db.RunnerPlacementReasonImageUnsupported)
	}
	evaluation.Eligible = len(evaluation.RejectedCriteria) == 0
	return evaluatedRunnerPlacement{candidate: candidate, evaluation: evaluation}
}

func runnerTagsMatch(runner db.Runner, requested []string, mode db.RunnerTagMatchMode) bool {
	if len(requested) == 0 {
		return runner.IsDefault
	}
	tags := db.NormalizeRunnerTags(runner.Tags)
	matched := 0
	for _, requestedTag := range requested {
		for _, runnerTag := range tags {
			if requestedTag == runnerTag {
				matched++
				break
			}
		}
	}
	if mode == db.RunnerTagMatchAny {
		return matched > 0
	}
	return matched == len(requested)
}

func placementScopeRank(scope db.RunnerPlacementScope) int {
	if scope == db.RunnerPlacementProject {
		return 0
	}
	return 1
}

func rejectedPlacementSummary(
	tags []string,
	evaluated []evaluatedRunnerPlacement,
) (reason string, actionHint string) {
	if len(evaluated) == 0 {
		return "no runners are configured", "Create and register an active project or global runner."
	}
	matching := make([]evaluatedRunnerPlacement, 0, len(evaluated))
	for _, item := range evaluated {
		if !containsPlacementCriterion(item.evaluation.RejectedCriteria, "tag policy did not match") &&
			!containsPlacementCriterion(item.evaluation.RejectedCriteria, "different project") {
			matching = append(matching, item)
		}
	}
	if len(matching) == 0 {
		if len(tags) == 0 {
			return "no default runner is eligible", "Mark an active registered runner as default or select template tags."
		}
		return fmt.Sprintf("no runner matched requested tags: %s", strings.Join(tags, ", ")),
			"Add the requested tags to a runner or change the template tag policy."
	}
	imageCompatible := make([]evaluatedRunnerPlacement, 0, len(matching))
	for _, item := range matching {
		if !containsPlacementCriterion(item.evaluation.RejectedCriteria, "executor image unsupported") {
			imageCompatible = append(imageCompatible, item)
		}
	}
	if len(imageCompatible) == 0 {
		return "matching runners do not support executor image overrides",
			"Use a Docker or Kubernetes runner, or clear the template executor image."
	}
	matching = imageCompatible
	blockers := make(map[string]int)
	for _, item := range matching {
		for _, rejected := range item.evaluation.RejectedCriteria {
			blockers[rejected]++
		}
	}
	for _, item := range []struct {
		criterion string
		reason    string
		hint      string
	}{
		{"offline", "all matching runners are offline", "Start a matching runner and wait for a fresh heartbeat."},
		{"at capacity", "all matching runners are at capacity", "Wait for a runner slot or increase max parallel tasks."},
		{"not registered", "matching runners are not registered", "Complete runner registration before retrying the task."},
		{"inactive", "matching runners are inactive", "Activate a matching runner before retrying the task."},
	} {
		if blockers[item.criterion] == len(matching) {
			return item.reason, item.hint
		}
	}
	ordered := []string{"inactive", "not registered", "offline", "at capacity"}
	active := make([]string, 0, len(ordered))
	for _, blocker := range ordered {
		if blockers[blocker] > 0 {
			active = append(active, fmt.Sprintf("%s (%d)", blocker, blockers[blocker]))
		}
	}
	return "matching runners were rejected by: " + strings.Join(active, ", "),
		"Activate and register matching runners, restore their heartbeat, or free a runner slot."
}

func containsPlacementCriterion(criteria []string, expected string) bool {
	for _, criterion := range criteria {
		if criterion == expected {
			return true
		}
	}
	return false
}

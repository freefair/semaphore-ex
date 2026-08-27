package tasks

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideRunnerPlacementDeterministicEligibility(t *testing.T) {
	now := time.Date(2026, 8, 27, 14, 0, 0, 0, time.UTC)
	touched := now.Add(-time.Second)
	stale := now.Add(-time.Hour)
	projectID := 7
	otherProjectID := 8
	candidates := []RunnerPlacementCandidate{
		{Runner: db.Runner{ID: 20, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"linux", "gpu"}, Touched: &touched}},
		{Runner: db.Runner{ID: 10, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"GPU", " linux "}, Touched: &touched}},
		{Runner: db.Runner{ID: 8, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"linux", "gpu"}, Touched: &touched, MaxParallelTasks: 1}, RunningTasks: 1},
		{Runner: db.Runner{ID: 5, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"linux", "gpu"}, Touched: &stale}},
		{Runner: db.Runner{ID: 2, ProjectID: &otherProjectID, Active: true, Token: "x", Tags: []string{"linux", "gpu"}, Touched: &touched}},
		{Runner: db.Runner{ID: 1, Active: true, Token: "x", Tags: []string{"linux", "gpu"}, Touched: &touched}},
	}

	decision := DecideRunnerPlacement(
		projectID, []string{" GPU ", "linux", "gpu"}, db.RunnerTagMatchAll,
		candidates, now, 2*time.Minute,
	)

	require.NotNil(t, decision.SelectedRunnerID)
	assert.Equal(t, 10, *decision.SelectedRunnerID)
	assert.Equal(t, []string{"gpu", "linux"}, decision.RequestedTags)
	require.Len(t, decision.Evaluations, len(candidates))
	for _, evaluation := range decision.Evaluations {
		if evaluation.RunnerID == 10 {
			assert.Contains(t, evaluation.AcceptedCriteria, "project scope accepted")
		}
		if evaluation.RunnerID == 2 {
			assert.Contains(t, evaluation.RejectedCriteria, "different project")
		}
	}
	reversed := append([]RunnerPlacementCandidate(nil), candidates...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	reordered := DecideRunnerPlacement(
		projectID, []string{"linux", "gpu"}, db.RunnerTagMatchAll,
		reversed, now, 2*time.Minute,
	)
	require.NotNil(t, reordered.SelectedRunnerID)
	assert.Equal(t, *decision.SelectedRunnerID, *reordered.SelectedRunnerID)
}

func TestDecideRunnerPlacementEligibilityTable(t *testing.T) {
	now := time.Date(2026, 8, 27, 14, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Second)
	stale := now.Add(-time.Hour)
	projectID := 7
	otherProjectID := 8
	base := db.Runner{
		ID: 11, ProjectID: &projectID, Active: true, Token: "registered",
		Tags: []string{"gpu", "linux"}, Touched: &fresh,
	}

	tests := []struct {
		name              string
		mutate            func(*RunnerPlacementCandidate)
		expectedCriterion string
	}{
		{name: "eligible", expectedCriterion: ""},
		{name: "different project", mutate: func(c *RunnerPlacementCandidate) {
			c.Runner.ProjectID = &otherProjectID
		}, expectedCriterion: "different project"},
		{name: "inactive", mutate: func(c *RunnerPlacementCandidate) {
			c.Runner.Active = false
		}, expectedCriterion: "inactive"},
		{name: "not registered", mutate: func(c *RunnerPlacementCandidate) {
			c.Runner.Token = ""
		}, expectedCriterion: "not registered"},
		{name: "offline", mutate: func(c *RunnerPlacementCandidate) {
			c.Runner.Touched = &stale
		}, expectedCriterion: "offline"},
		{name: "at capacity", mutate: func(c *RunnerPlacementCandidate) {
			c.Runner.MaxParallelTasks = 1
			c.RunningTasks = 1
		}, expectedCriterion: "at capacity"},
		{name: "tag mismatch", mutate: func(c *RunnerPlacementCandidate) {
			c.Runner.Tags = []string{"amd64"}
		}, expectedCriterion: "tag policy did not match"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := RunnerPlacementCandidate{Runner: base}
			if tt.mutate != nil {
				tt.mutate(&candidate)
			}
			decision := DecideRunnerPlacement(
				projectID, []string{"gpu", "linux"}, db.RunnerTagMatchAll,
				[]RunnerPlacementCandidate{candidate}, now, 2*time.Minute,
			)
			require.Len(t, decision.Evaluations, 1)
			if tt.expectedCriterion == "" {
				require.NotNil(t, decision.SelectedRunnerID)
				assert.True(t, decision.Evaluations[0].Eligible)
				return
			}
			assert.Nil(t, decision.SelectedRunnerID)
			assert.False(t, decision.Evaluations[0].Eligible)
			assert.Contains(t, decision.Evaluations[0].RejectedCriteria, tt.expectedCriterion)
		})
	}
}

func TestDecideRunnerPlacementMatchModesAndDefaults(t *testing.T) {
	now := time.Now()
	touched := now
	projectID := 1
	candidates := []RunnerPlacementCandidate{
		{Runner: db.Runner{ID: 3, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"linux"}, Touched: &touched}},
		{Runner: db.Runner{ID: 4, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"linux", "gpu"}, Touched: &touched}},
		{Runner: db.Runner{ID: 5, ProjectID: &projectID, Active: true, Token: "x", IsDefault: true, Touched: &touched}},
	}

	all := DecideRunnerPlacement(projectID, []string{"linux", "gpu"}, db.RunnerTagMatchAll, candidates, now, time.Minute)
	require.NotNil(t, all.SelectedRunnerID)
	assert.Equal(t, 4, *all.SelectedRunnerID)
	any := DecideRunnerPlacement(projectID, []string{"linux", "gpu"}, db.RunnerTagMatchAny, candidates, now, time.Minute)
	require.NotNil(t, any.SelectedRunnerID)
	assert.Equal(t, 3, *any.SelectedRunnerID)
	defaults := DecideRunnerPlacement(projectID, nil, db.RunnerTagMatchAll, candidates, now, time.Minute)
	require.NotNil(t, defaults.SelectedRunnerID)
	assert.Equal(t, 5, *defaults.SelectedRunnerID)
}

func TestDecideRunnerPlacementRejectsWithActionableReason(t *testing.T) {
	now := time.Now()
	touched := now
	projectID := 1
	decision := DecideRunnerPlacement(
		projectID, []string{"arm64"}, db.RunnerTagMatchAny,
		[]RunnerPlacementCandidate{{Runner: db.Runner{
			ID: 1, ProjectID: &projectID, Active: true, Token: "x", Tags: []string{"amd64"}, Touched: &touched,
		}}},
		now, time.Minute,
	)

	assert.Nil(t, decision.SelectedRunnerID)
	assert.Equal(t, "no runner matched requested tags: arm64", decision.Reason)
	assert.NotEmpty(t, decision.ActionHint)
}

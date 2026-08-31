package sql

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowApprovalContributionRejectsRevokedMembership(t *testing.T) {
	store, repository, projectID, user, approval, now := workflowApprovalContributionFixture(t)
	defer store.Close()
	secondOwner, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-second-owner", Name: "Workflow Second Owner", Email: "workflow-second-owner@example.invalid"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: secondOwner.ID, Role: db.ProjectOwner})
	require.NoError(t, err)

	require.NoError(t, store.DeleteProjectUser(projectID, user.ID))
	result, err := repository.SubmitWorkflowApprovalContribution(workflowApprovalSubmission(projectID, approval, user.ID, now))
	require.NoError(t, err)
	assert.False(t, result.Committed)
	contributions, err := repository.GetWorkflowApprovalContributions(approval.ID)
	require.NoError(t, err)
	assert.Empty(t, contributions)
}

func TestWorkflowApprovalContributionRejectsDeletedCustomRoleWithoutMutatingSnapshot(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, first, second := workflowRunResources(t, store, projectID)
	secondOwner, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-role-owner", Name: "Workflow Role Owner", Email: "workflow-role-owner@example.invalid"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: secondOwner.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "release-approver", Slug: "release-approver", Name: "Release approver", ProjectID: &projectID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, Role: db.ProjectNone, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, first.ID, second.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{first.ID: first, second.ID: second}, user.ID, "deleted-role", now)
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	policy := db.WorkflowApprovalRolePolicy{Revision: 1, Mode: db.WorkflowApprovalRoleModeAnyOf, RoleIDs: []db.ProjectRoleReference{db.ProjectRoleReferenceForCustomRole(role.ID)}, MinimumDistinctApprovers: 1}
	payload, err := json.Marshal(db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 1, Policy: policy})
	require.NoError(t, err)
	deadline := now.Add(time.Minute)
	approval, opened, err := repository.OpenWorkflowApproval(db.WorkflowApproval{ProjectID: projectID, WorkflowRunID: run.ID, WorkflowNodeID: workflow.Nodes[0].ID, Status: db.WorkflowApprovalPending, Created: now, Deadline: &deadline, Prompt: "approve", EligiblePermission: db.CanRunProjectTasks, RolePolicySnapshotJSON: string(payload), RolePolicySnapshotRevision: 1, RolePolicySnapshot: db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 1, Policy: policy}, RequestActorUserID: user.ID + 1, TimeoutOutcome: db.WorkflowApprovalTimeoutReject, CorrelationID: "deleted-role"})
	require.NoError(t, err)
	require.True(t, opened)
	member, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	member.Role, member.RoleID = db.ProjectGuest, nil
	require.NoError(t, store.UpdateProjectUser(member))
	require.NoError(t, store.DeleteProjectRole(projectID, role.ID, role.Revision))

	result, err := repository.SubmitWorkflowApprovalContribution(workflowApprovalSubmission(projectID, approval, user.ID, now))
	require.NoError(t, err)
	assert.True(t, result.Denied)
	contributions, err := repository.GetWorkflowApprovalContributions(approval.ID)
	require.NoError(t, err)
	assert.Empty(t, contributions)
	reloaded, err := repository.GetWorkflowApproval(projectID, run.ID, approval.WorkflowNodeID)
	require.NoError(t, err)
	assert.Equal(t, string(payload), reloaded.RolePolicySnapshotJSON)
	assert.Equal(t, db.ProjectRoleReferenceForCustomRole(role.ID), reloaded.RolePolicySnapshot.Policy.RoleIDs[0])
}

func TestWorkflowApprovalContributionTimeoutWinsAndFinalizesOnce(t *testing.T) {
	store, repository, projectID, user, approval, now := workflowApprovalContributionFixture(t)
	defer store.Close()

	result, err := repository.SubmitWorkflowApprovalContribution(workflowApprovalSubmission(projectID, approval, user.ID, now.Add(2*time.Minute)))
	require.NoError(t, err)
	require.True(t, result.TimedOut)
	require.True(t, result.Terminal)
	assert.Equal(t, db.WorkflowApprovalExpired, result.Approval.Status)
	contributions, err := repository.GetWorkflowApprovalContributions(approval.ID)
	require.NoError(t, err)
	assert.Empty(t, contributions)

	first, err := repository.FinalizeWorkflowRunApprovalNode(projectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowRunNodeBlocked, "expired", `{}`, now.Add(2*time.Minute))
	require.NoError(t, err)
	second, err := repository.FinalizeWorkflowRunApprovalNode(projectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowRunNodeBlocked, "expired", `{}`, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, first)
	assert.False(t, second)
}

func TestWorkflowApprovalContributionRacesTimeoutToOneTerminalWinner(t *testing.T) {
	store, repository, projectID, user, approval, now := workflowApprovalContributionFixture(t)
	defer store.Close()
	start := make(chan struct{})
	var wait sync.WaitGroup
	results := make(chan db.WorkflowApprovalContributionResult, 1)
	errors := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		result, err := repository.SubmitWorkflowApprovalContribution(workflowApprovalSubmission(projectID, approval, user.ID, now.Add(2*time.Minute)))
		results <- result
		errors <- err
	}()
	go func() {
		defer wait.Done()
		<-start
		at := now.Add(2 * time.Minute)
		candidate := approval
		candidate.Status, candidate.Resolved, candidate.ResolvedByUserID = db.WorkflowApprovalExpired, &at, nil
		candidate.DecisionSource = db.WorkflowApprovalDecisionSourceTimeout
		_, err := repository.ResolveWorkflowApprovalIfPending(candidate)
		errors <- err
	}()
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	result := <-results
	current, err := repository.GetWorkflowApproval(projectID, approval.WorkflowRunID, approval.WorkflowNodeID)
	require.NoError(t, err)
	assert.True(t, current.Status == db.WorkflowApprovalExpired || current.Status == db.WorkflowApprovalApproved)
	contributions, err := repository.GetWorkflowApprovalContributions(approval.ID)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(contributions), 1)
	if current.Status == db.WorkflowApprovalExpired {
		assert.True(t, result.TimedOut || !result.Committed)
	}
	first, err := repository.FinalizeWorkflowRunApprovalNode(projectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowRunNodeBlocked, "terminal", `{}`, now.Add(2*time.Minute))
	require.NoError(t, err)
	second, err := repository.FinalizeWorkflowRunApprovalNode(projectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowRunNodeBlocked, "terminal", `{}`, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, first)
	assert.False(t, second)
}

func TestWorkflowApprovalContributionAllowsOneActorRowUnderConcurrency(t *testing.T) {
	store, repository, projectID, user, approval, now := workflowApprovalContributionFixture(t)
	defer store.Close()

	start := make(chan struct{})
	results := make(chan db.WorkflowApprovalContributionResult, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := repository.SubmitWorkflowApprovalContribution(workflowApprovalSubmission(projectID, approval, user.ID, now))
			results <- result
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errors)
	committed, duplicate, terminal := 0, 0, 0
	for err := range errors {
		require.NoError(t, err)
	}
	for result := range results {
		if result.Committed {
			committed++
		}
		if result.Duplicate {
			duplicate++
		}
		if !result.Committed && result.Terminal {
			terminal++
		}
	}
	assert.Equal(t, 1, committed)
	assert.Equal(t, 1, duplicate+terminal)
	contributions, err := repository.GetWorkflowApprovalContributions(approval.ID)
	require.NoError(t, err)
	require.Len(t, contributions, 1)
}

func workflowApprovalContributionFixture(t *testing.T) (*coresql.SqlDb, *WorkflowStoreImpl, int, db.User, db.WorkflowApproval, time.Time) {
	t.Helper()
	store, repository, projectID := workflowRepositoryFixture(t)
	user, first, second := workflowRunResources(t, store, projectID)
	_, err := store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, first.ID, second.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{first.ID: first, second.ID: second}, user.ID, "approval-contribution", now)
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	policy := db.WorkflowApprovalRolePolicy{Revision: 1, Mode: db.WorkflowApprovalRoleModeAnyOf, RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}, MinimumDistinctApprovers: 1}
	payload, err := json.Marshal(db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 1, Policy: policy})
	require.NoError(t, err)
	deadline := now.Add(time.Minute)
	approval, opened, err := repository.OpenWorkflowApproval(db.WorkflowApproval{
		ProjectID: projectID, WorkflowRunID: run.ID, WorkflowNodeID: workflow.Nodes[0].ID,
		Status: db.WorkflowApprovalPending, Created: now, Deadline: &deadline, Prompt: "approve",
		EligiblePermission: db.CanRunProjectTasks, RolePolicySnapshotJSON: string(payload), RolePolicySnapshotRevision: 1,
		RolePolicySnapshot: db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 1, Policy: policy},
		RequestActorUserID: user.ID + 1, TimeoutOutcome: db.WorkflowApprovalTimeoutReject, CorrelationID: "approval-contribution",
	})
	require.NoError(t, err)
	require.True(t, opened)
	return store, repository, projectID, user, approval, now
}

func workflowApprovalSubmission(projectID int, approval db.WorkflowApproval, actorID int, at time.Time) db.WorkflowApprovalContributionSubmission {
	return db.WorkflowApprovalContributionSubmission{
		ProjectID: projectID, WorkflowRunID: approval.WorkflowRunID, WorkflowNodeID: approval.WorkflowNodeID,
		ActorUserID: actorID, Decision: db.WorkflowApprovalDecision{Status: db.WorkflowApprovalApproved, Source: db.WorkflowApprovalDecisionSourceUser},
		CorrelationID: "approval-contribution", At: at,
	}
}

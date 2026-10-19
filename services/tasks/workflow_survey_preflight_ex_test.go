package tasks

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro/db/factory"
	workflowServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run the real workflow planner and production review issuer together. A
// permissive workflow-service enqueuer cannot prove value-bound review tokens.
func TestWorkflowExecutionPreflightRejectsChangedSurveyAnswer(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	_, err := store.CreateProjectUser(db.ProjectUser{ProjectID: template.ProjectID, UserID: actor.ID, Role: db.ProjectOwner, Revision: 1})
	require.NoError(t, err)
	template.RunnerTags = nil
	require.NoError(t, store.UpdateTemplate(template))
	key := make([]byte, 32)
	_, err = rand.Read(key)
	require.NoError(t, err)
	issuer, err := NewExecutionPreflightReviewTokenIssuer(key, time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	repository := factory.NewWorkflowStore(store)
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(store, db.WorkflowTemplate{
		ProjectID: template.ProjectID, Name: "Reviewed survey", MaxParallelTasks: 1,
		SurveyVars: []db.SurveyVar{{Name: "release", Required: true}},
		Nodes:      []db.WorkflowNode{{ID: -1, TemplateID: template.ID, SurveyInputs: []db.WorkflowNodeSurveyInput{{Var: "region", WorkflowVar: "release"}}}},
		Edges:      []db.WorkflowEdge{},
	})
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, _, err := repository.(db.WorkflowVersionStore).CreateWorkflowTemplateVersioned(prepared, db.WorkflowVersionMutation{AuthorUserID: actor.ID, Message: "reviewed survey"})
	require.NoError(t, err)
	service := workflowServer.NewWorkflowService(repository, store, &pool, nil).(pro_interfaces.WorkflowExecutionPreflightService)
	first := db.WorkflowRunInput{Environment: db.MapStringAnyField{"release": "v1"}}
	preview, err := service.PreviewWorkflowExecution(workflow, &actor, first)
	require.NoError(t, err)
	second := db.WorkflowRunInput{Environment: db.MapStringAnyField{"release": "v2"}}
	_, err = service.StartWorkflowWithExecutionPreflight(workflow, &actor, "survey-stale", pro_interfaces.ExecutionPreflightReview{Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken}, second)
	var stale *pro_interfaces.ExecutionPreflightStaleError
	require.ErrorAs(t, err, &stale)
	assert.Contains(t, stale.Changes, pro_interfaces.ExecutionChangeInput)
	runs, err := repository.GetWorkflowRuns(template.ProjectID, workflow.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, runs)
	tasks, err := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, tasks)
	fresh, err := service.PreviewWorkflowExecution(workflow, &actor, second)
	require.NoError(t, err)
	run, err := service.StartWorkflowWithExecutionPreflight(workflow, &actor, "survey-fresh", pro_interfaces.ExecutionPreflightReview{Fingerprint: fresh.Fingerprint, ReviewToken: fresh.ReviewToken}, second)
	require.NoError(t, err)
	assert.Positive(t, run.ID)
	assert.Equal(t, db.MapStringAnyField{"release": "v2"}, run.Environment)
}

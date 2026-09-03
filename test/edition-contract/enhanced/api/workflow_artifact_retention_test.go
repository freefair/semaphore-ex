package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowArtifactRetentionServiceStub struct {
	scope     db.WorkflowArtifactRetentionScope
	projectID *int
	update    pro_interfaces.WorkflowArtifactRetentionUpdate
	actorID   int
	err       error
}

func (stub *workflowArtifactRetentionServiceStub) GetWorkflowArtifactRetention(_ context.Context, scope db.WorkflowArtifactRetentionScope, projectID *int) (pro_interfaces.WorkflowArtifactRetentionState, error) {
	stub.scope, stub.projectID = scope, projectID
	return pro_interfaces.WorkflowArtifactRetentionState{Effective: db.DefaultWorkflowArtifactRetentionSnapshot()}, stub.err
}

func (stub *workflowArtifactRetentionServiceStub) PublishWorkflowArtifactRetention(_ context.Context, scope db.WorkflowArtifactRetentionScope, projectID *int, update pro_interfaces.WorkflowArtifactRetentionUpdate, actorID int) (pro_interfaces.WorkflowArtifactRetentionState, error) {
	stub.scope, stub.projectID, stub.update, stub.actorID = scope, projectID, update, actorID
	return pro_interfaces.WorkflowArtifactRetentionState{Effective: db.DefaultWorkflowArtifactRetentionSnapshot()}, stub.err
}

type workflowArtifactRetentionAuditRecorder struct {
	events []pro_interfaces.AuditEvent
}

func (recorder *workflowArtifactRetentionAuditRecorder) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	recorder.events = append(recorder.events, event)
	return nil
}

func workflowArtifactRetentionRequest(method, path, body string, projectID *int) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 11})
	if projectID != nil {
		request = helpers.SetContextValue(request, "project", db.Project{ID: *projectID})
	}
	return request
}

func TestWorkflowArtifactRetentionControllerDerivesGlobalAndProjectScope(t *testing.T) {
	stub := &workflowArtifactRetentionServiceStub{}
	audit := &workflowArtifactRetentionAuditRecorder{}
	controller := NewWorkflowArtifactRetentionController(stub)
	controller.(pro_interfaces.WorkflowArtifactRetentionAuditConfigurer).ConfigureWorkflowArtifactRetentionAudit(audit)
	body := `{"expected_revision":0,"retention_seconds":604800,"max_artifact_bytes":1048576,"max_run_bytes":2097152}`

	globalResponse := httptest.NewRecorder()
	controller.PublishGlobalWorkflowArtifactRetention(globalResponse, workflowArtifactRetentionRequest(http.MethodPut, "/api/workflow-artifact-retention", body, nil))
	require.Equal(t, http.StatusOK, globalResponse.Code)
	assert.Equal(t, db.WorkflowArtifactRetentionGlobal, stub.scope)
	assert.Nil(t, stub.projectID)
	assert.Equal(t, 11, stub.actorID)

	projectID := 7
	projectResponse := httptest.NewRecorder()
	controller.PublishProjectWorkflowArtifactRetention(projectResponse, workflowArtifactRetentionRequest(http.MethodPut, "/api/project/7/workflow-artifact-retention", body, &projectID))
	require.Equal(t, http.StatusOK, projectResponse.Code)
	assert.Equal(t, db.WorkflowArtifactRetentionProject, stub.scope)
	require.NotNil(t, stub.projectID)
	assert.Equal(t, projectID, *stub.projectID)
	require.Len(t, audit.events, 2)
	assert.Equal(t, "global", audit.events[0].TargetID)
	assert.Equal(t, "project:7", audit.events[1].TargetID)
}

func TestWorkflowArtifactRetentionControllerRejectsUnboundedOrConflictingWrites(t *testing.T) {
	stub := &workflowArtifactRetentionServiceStub{}
	audit := &workflowArtifactRetentionAuditRecorder{}
	controller := NewWorkflowArtifactRetentionController(stub)
	controller.(pro_interfaces.WorkflowArtifactRetentionAuditConfigurer).ConfigureWorkflowArtifactRetentionAudit(audit)

	invalid := httptest.NewRecorder()
	controller.PublishGlobalWorkflowArtifactRetention(invalid, workflowArtifactRetentionRequest(http.MethodPut, "/api/workflow-artifact-retention", `{"expected_revision":0,"retention_seconds":604800,"max_artifact_bytes":1048576,"max_run_bytes":2097152,"project_id":99}`, nil))
	assert.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)

	stub.err = pro_interfaces.ErrWorkflowArtifactRetentionConflict
	conflict := httptest.NewRecorder()
	controller.PublishGlobalWorkflowArtifactRetention(conflict, workflowArtifactRetentionRequest(http.MethodPut, "/api/workflow-artifact-retention", `{"expected_revision":0,"retention_seconds":604800,"max_artifact_bytes":1048576,"max_run_bytes":2097152}`, nil))
	assert.Equal(t, http.StatusConflict, conflict.Code)
	require.Len(t, audit.events, 2)
	assert.Equal(t, pro_interfaces.AuditOutcomeFailure, audit.events[1].Outcome)
}

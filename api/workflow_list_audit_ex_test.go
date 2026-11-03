package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deniedWorkflowListService struct {
	pro_interfaces.WorkflowDefinitionService
}

func (deniedWorkflowListService) List(int, db.RetrieveQueryParams, ...*db.User) ([]db.WorkflowTemplate, error) {
	return nil, db.ErrNotFound
}

func TestWorkflowCollectionRouteAuditsDeniedList(t *testing.T) {
	store := setupSessionTest(t)
	util.Config.Debugging = &util.DebuggingConfig{}
	util.Config.TmpPath = t.TempDir()
	project, err := store.CreateProject(db.Project{Name: "Workflow list audit"})
	require.NoError(t, err)
	user := createUserOptionsTestUser(t, store, "workflow-list-audit")
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest})
	require.NoError(t, err)
	pool := poolWithTasks()
	router := Route(store, nil, nil, nil, pool, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, deniedWorkflowListService{}, nil, proServer.NewLogWriteService(), nil, metrics.NewMetrics(), nil, nil)
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/project/%d/workflows", project.ID), nil)
	request = addSessionCookie(t, store, request, user, db.SessionVerificationNone)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "task_pool", pool)
	request, _ = withAuditRecorder(request)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	found := false
	for _, event := range events {
		if event.Description == nil {
			continue
		}
		var audit pro_interfaces.AuditEvent
		if json.Unmarshal([]byte(*event.Description), &audit) == nil && audit.Action == pro_interfaces.AuditActionWorkflowList && audit.Outcome == pro_interfaces.AuditOutcomeDenied {
			found = true
		}
	}
	assert.True(t, found, "a denied workflow list is audited")
}

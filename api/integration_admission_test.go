package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errIntegrationAdmissionCaptured = errors.New("integration admission captured")

type integrationAdmissionCapture struct {
	request pro_interfaces.DeploymentWindowAdmissionRequest
}

func (s *integrationAdmissionCapture) Claim(request pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	s.request = request
	return pro_interfaces.DeploymentWindowAdmissionClaim{}, errIntegrationAdmissionCaptured
}

func TestIntegrationControllerClaimsServerDerivedDeploymentWindowAdmission(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "integration admission"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "integration repository",
		GitURL: "https://example.invalid/integration.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "integration deploy", Playbook: "deploy.yml",
	})
	require.NoError(t, err)

	capture := &integrationAdmissionCapture{}
	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	pool.ConfigureDeploymentWindowAdmission(capture)
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/release", strings.NewReader(`{"event":"release"}`))
	request = helpers.SetContextValue(request, "task_pool", &pool)

	task := NewIntegrationController(store, nil).RunIntegration(
		db.Integration{ID: 13, ProjectID: project.ID, TemplateID: template.ID}, project, request, []byte(`{"event":"release"}`),
	)

	assert.Nil(t, task, "the capture stops the normal enqueue path after recording the controller request")
	assert.Equal(t, project.ID, capture.request.ProjectID)
	assert.Equal(t, pro_interfaces.DeploymentWindowSourceIntegration, capture.request.Source)
	assert.Equal(t, pro_interfaces.DeploymentWindowOriginIntegration, capture.request.Origin)
	require.NotNil(t, capture.request.TemplateID)
	assert.Equal(t, template.ID, *capture.request.TemplateID)
	assert.Nil(t, capture.request.WorkflowID)
	assert.True(t, strings.HasPrefix(capture.request.DecisionKey, "integration-"))
	assert.Greater(t, len(capture.request.DecisionKey), len("integration-"), "the controller, rather than incoming payload data, creates the idempotency key")
}

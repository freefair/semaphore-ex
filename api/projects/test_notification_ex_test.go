package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendTestNotification_ReportsChannelsInsteadOfClaimingSuccess(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "alerts", Alert: true})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/project/1/notifications/test", nil)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "store", store)
	recorder := httptest.NewRecorder()

	(&ProjectController{}).SendTestNotification(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var report tasks.TestAlertReport
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &report))
	assert.False(t, report.Sent)
	assert.Len(t, report.Channels, 7)
	assert.Contains(t, recorder.Body.String(), `"sent":false`)
	assert.Contains(t, recorder.Body.String(), `"configured":false`)
}

func TestSendTestNotification_ProjectAlertsDisabled(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "quiet", Alert: false})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/project/1/notifications/test", nil)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "store", store)
	recorder := httptest.NewRecorder()

	(&ProjectController{}).SendTestNotification(recorder, request)

	assert.Equal(t, http.StatusConflict, recorder.Code)
}

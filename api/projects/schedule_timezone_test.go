package projects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scheduleAPITestEncryptionService struct{}

func (*scheduleAPITestEncryptionService) RekeyAccessKeys(string) error          { return nil }
func (*scheduleAPITestEncryptionService) SerializeSecret(*db.AccessKey) error   { return nil }
func (*scheduleAPITestEncryptionService) DeserializeSecret(*db.AccessKey) error { return nil }
func (*scheduleAPITestEncryptionService) FillEnvironmentSecrets(*db.Environment, bool) error {
	return nil
}
func (*scheduleAPITestEncryptionService) DeleteSecret(*db.AccessKey) error { return nil }
func (*scheduleAPITestEncryptionService) CreateTaskSurveySecrets(int, int, string, time.Time) error {
	return nil
}
func (*scheduleAPITestEncryptionService) GetTaskSurveySecrets(int, int) (string, error) {
	return "", nil
}
func (*scheduleAPITestEncryptionService) DeleteTaskSurveySecrets(int, int) error { return nil }

type scheduleAPITestKeyInstaller struct{}

func (*scheduleAPITestKeyInstaller) Install(db.AccessKey, db.AccessKeyRole, task_logger.Logger) (ssh.AccessKeyInstallation, error) {
	return ssh.AccessKeyInstallation{}, nil
}

type scheduleAPITestLogWriter struct{}

func (*scheduleAPITestLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (*scheduleAPITestLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (*scheduleAPITestLogWriter) WriteResult(any) error                             { return nil }

func TestScheduleTimezoneAPIValidateCreateUpdateListAndDetail(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "schedule-timezone-admin", Name: "Schedule Timezone Admin", Email: "schedule-timezone-admin@example.test", Admin: true,
	})
	require.NoError(t, err)

	originalScheduleConfig := util.Config.Schedule
	util.Config.Schedule = &util.ScheduleConfig{Timezone: "America/New_York"}
	t.Cleanup(func() { util.Config.Schedule = originalScheduleConfig })
	pool := schedules.CreateSchedulePool(
		store, &tasks.TaskPool{}, &scheduleAPITestKeyInstaller{}, &scheduleAPITestEncryptionService{},
	)
	t.Cleanup(pool.Destroy)

	validation := serveScheduleTimezoneHandler(t, store, project, user, pool, nil,
		http.MethodPost, "/api/project/1/schedules/validate", map[string]any{
			"cron_format": "CRON_TZ=Asia/Tokyo 30 9 * * *", "timezone": "Europe/Berlin",
		}, ValidateScheduleCronFormat)
	require.Equal(t, http.StatusOK, validation.Code)
	var preview db.Schedule
	require.NoError(t, json.Unmarshal(validation.Body.Bytes(), &preview))
	assert.Equal(t, "Asia/Tokyo", preview.EffectiveTimezone)
	require.NotNil(t, preview.NextRun)

	createdResponse := serveScheduleTimezoneHandler(t, store, project, user, pool, nil,
		http.MethodPost, "/api/project/1/schedules", map[string]any{
			"name": "Morning", "template_id": template.ID, "cron_format": "30 9 * * *",
			"timezone": "Europe/Berlin", "active": true,
		}, AddSchedule)
	require.Equal(t, http.StatusCreated, createdResponse.Code, createdResponse.Body.String())
	var created db.Schedule
	require.NoError(t, json.Unmarshal(createdResponse.Body.Bytes(), &created))
	assert.Equal(t, "Europe/Berlin", created.EffectiveTimezone)
	require.NotNil(t, created.NextRun)
	require.NotNil(t, created.Timezone)

	created.Timezone = scheduleTimezonePointer("Etc/GMT+5")
	updatedResponse := serveScheduleTimezoneHandler(t, store, project, user, pool, &created,
		http.MethodPut, "/api/project/1/schedules/1", created, UpdateSchedule)
	require.Equal(t, http.StatusOK, updatedResponse.Code, updatedResponse.Body.String())
	var updated db.Schedule
	require.NoError(t, json.Unmarshal(updatedResponse.Body.Bytes(), &updated))
	assert.Equal(t, "Etc/GMT+5", updated.EffectiveTimezone)
	require.NotNil(t, updated.NextRun)

	listResponse := serveScheduleTimezoneHandler(t, store, project, user, pool, nil,
		http.MethodGet, "/api/project/1/schedules", nil, GetProjectSchedules)
	require.Equal(t, http.StatusOK, listResponse.Code)
	var listed []db.ScheduleWithTpl
	require.NoError(t, json.Unmarshal(listResponse.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "Etc/GMT+5", listed[0].EffectiveTimezone)
	require.NotNil(t, listed[0].NextRun)

	detailResponse := serveScheduleTimezoneHandler(t, store, project, user, pool, &updated,
		http.MethodGet, "/api/project/1/schedules/1", nil, GetSchedule)
	require.Equal(t, http.StatusOK, detailResponse.Code)
	var detail db.Schedule
	require.NoError(t, json.Unmarshal(detailResponse.Body.Bytes(), &detail))
	assert.Equal(t, "Etc/GMT+5", detail.EffectiveTimezone)
	require.NotNil(t, detail.NextRun)
}

func TestScheduleTimezoneAPIRejectsInvalidStoredAndExpressionZones(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, _ := createTemplatePermissionFixture(t, store)
	user := db.User{ID: 1, Admin: true}

	originalScheduleConfig := util.Config.Schedule
	util.Config.Schedule = &util.ScheduleConfig{Timezone: "UTC"}
	t.Cleanup(func() { util.Config.Schedule = originalScheduleConfig })

	for _, body := range []map[string]any{
		{"cron_format": "0 9 * * *", "timezone": "CET"},
		{"cron_format": "0 9 * * *", "timezone": "Not/A_Zone"},
		{"cron_format": "CRON_TZ=PST 0 9 * * *", "timezone": "Europe/Berlin"},
		{"cron_format": "CRON_TZ=Europe/../UTC 0 9 * * *"},
	} {
		response := serveScheduleTimezoneHandler(t, store, project, user, nil, nil,
			http.MethodPost, "/api/project/1/schedules/validate", body, ValidateScheduleCronFormat)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "zoneinfo")
		assert.NotContains(t, response.Body.String(), "/usr/")
	}
}

func serveScheduleTimezoneHandler(
	t *testing.T,
	store db.Store,
	project db.Project,
	user db.User,
	pool *schedules.SchedulePool,
	schedule *db.Schedule,
	method string,
	path string,
	body any,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "log_writer", &scheduleAPITestLogWriter{})
	if pool != nil {
		request = helpers.SetContextValue(request, "schedule_pool", pool)
	}
	if schedule != nil {
		request = helpers.SetContextValue(request, "schedule", *schedule)
	}
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

func scheduleTimezonePointer(value string) *string { return &value }

package runners

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobPool_CommonHeadersReportHealthMetadata(t *testing.T) {
	initConfig(t)
	previousVersion := util.Ver
	util.Ver = "2.20.4"
	t.Cleanup(func() { util.Ver = previousVersion })
	pool := NewJobPool(nil)
	pool.addRunningJob(1, &runningJob{job: &tasks.LocalExecutor{Task: db.Task{ID: 1}}})
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	pool.setCommonHeaders(request)

	assert.Contains(t, request.Header.Get(RunnerVersionHeader), "2.20.4")
	assert.NotEmpty(t, request.Header.Get(RunnerPlatformHeader))
	assert.Equal(t, "1", request.Header.Get(RunnerCurrentLoadHeader))
	assert.NotEmpty(t, request.Header.Get("X-Runner-Started-At"))
}

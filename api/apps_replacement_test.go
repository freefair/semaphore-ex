package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type appReplacementStore struct{ db.Store }

func (appReplacementStore) SetOption(string, string) error { return nil }
func (appReplacementStore) DeleteOptions(string) error     { return nil }

func TestSetAppClearsReplacedFieldsImmediately(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{Apps: map[string]util.App{"test": {Title: "Test", AppPath: "/bin/test", Color: "#123456", AppArgs: []string{"old"}}}}
	req := httptest.NewRequest(http.MethodPut, "/apps/test", strings.NewReader(`{"title":"Test","path":"/bin/test","color":"","args":[]}`))
	req = helpers.SetContextValue(req, "app_id", "test")
	req = helpers.SetContextValue(req, "store", appReplacementStore{})
	response := httptest.NewRecorder()
	setApp(response, req)
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.Empty(t, util.Config.Apps["test"].Color)
	assert.Empty(t, util.Config.Apps["test"].AppArgs)
}

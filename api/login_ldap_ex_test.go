package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyDefaultMethodLDAPFailureRemainsUnauthorized(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	setupLoginConfig()
	util.Config.LdapServer = "127.0.0.1:1"

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"auth":"jdoe","password":"wrong"}`))
	request = helpers.SetContextValue(request, "store", store)
	recorder := httptest.NewRecorder()

	login(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

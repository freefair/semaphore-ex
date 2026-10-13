package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proApi "github.com/semaphoreui/semaphore/pro/api"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditLogMiddleware_DeniesNonAdminAndRecordsIt(t *testing.T) {
	r, rec := withAuditRecorder(asActor(httptest.NewRequest(http.MethodGet, "/api/audit-log/events", nil),
		db.User{ID: 5, Username: "mallory"}))
	w := httptest.NewRecorder()
	called := false

	auditLogMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, called)
	got := onlyEvent(t, rec, audit.AuthAuthorizationDeny)
	assert.Equal(t, audit.DenyMetadata{Method: http.MethodGet, Permission: "audit_log"}, got.Event.Metadata)
}

func TestAuditLogMiddleware_LetsAdminThrough(t *testing.T) {
	r, rec := withAuditRecorder(asActor(httptest.NewRequest(http.MethodGet, "/api/audit-log/events", nil),
		db.User{ID: 1, Admin: true}))
	called := false

	auditLogMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).
		ServeHTTP(httptest.NewRecorder(), r)

	assert.True(t, called)
	assert.Empty(t, rec.All())
}

func TestAuditLogMiddleware_LetsOnlySameOriginBrowserRequestsThrough(t *testing.T) {
	for site, allowed := range map[string]bool{"cross-site": false, "same-site": false, "same-origin": true,
		"none": true, "": true} {
		r, rec := withAuditRecorder(asActor(httptest.NewRequest(http.MethodGet, "/api/audit-log/events/export", nil),
			db.User{ID: 1, Admin: true}))
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		called := false

		auditLogMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)

		assert.Equal(t, allowed, called, site)
		if allowed {
			assert.Equal(t, http.StatusOK, w.Code, site)
		} else {
			assert.Equal(t, http.StatusForbidden, w.Code, site)
		}
		assert.Empty(t, rec.All(), site)
	}
}

func auditLogRequest(t *testing.T, target string, events int) *http.Request {
	t.Helper()
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	for i := 0; i < events; i++ {
		_, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{
			EventID: "e", SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start",
			Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", ActorName: "admin",
			InstanceID: "test", Metadata: "{}",
		})
		require.NoError(t, err)
	}
	return helpers.SetContextValue(httptest.NewRequest(http.MethodGet, target, nil), "store", store)
}

func notFiltered(t *testing.T) http.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) { t.Fatal("the request went to the filtered handler") }
}

func TestGetAuditEvents_PagesTheWholeLog(t *testing.T) {
	w := httptest.NewRecorder()

	getAuditEvents(notFiltered(t))(w, auditLogRequest(t, "/api/audit-log/events?before=3", 3))

	require.Equal(t, http.StatusOK, w.Code)
	var page auditPage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Events, 2)
	assert.Equal(t, int64(2), page.Events[0].Seq)
	assert.Equal(t, "auth.login", page.Events[0].EventCode)
	assert.Nil(t, page.Older)
	require.NotNil(t, page.Newer)
	assert.Equal(t, int64(2), *page.Newer)
}

func TestGetAuditEvents_SendsAnyOtherParameterToTheFilteredHandler(t *testing.T) {
	for _, target := range []string{"/api/audit-log/events?user=1", "/api/audit-log/events?before=3&ip=10.0.0.1",
		"/api/audit-log/events?anything=x"} {
		called := false
		getAuditEvents(func(http.ResponseWriter, *http.Request) { called = true })(httptest.NewRecorder(),
			auditLogRequest(t, target, 0))
		assert.True(t, called, target)
	}
}

func TestGetAuditEvents_RejectsBadCursor(t *testing.T) {
	for _, target := range []string{"/api/audit-log/events?before=x", "/api/audit-log/events?after=0",
		"/api/audit-log/events?before=5&after=2", "/api/audit-log/events?after=9223372036854775807",
		"/api/audit-log/events?before=5&before=9", "/api/audit-log/events?after=5&after=9"} {
		w := httptest.NewRecorder()
		getAuditEvents(notFiltered(t))(w, auditLogRequest(t, target, 0))
		assert.Equal(t, http.StatusBadRequest, w.Code, target)
	}
}

func TestGetAuditEvents_StoreFailureAnswers500(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events", 1)
	_, err := helpers.Store(r).(*sql.SqlDb).Sql().Exec("update audit_event set metadata = 'not json'")
	require.NoError(t, err)
	w := httptest.NewRecorder()
	getAuditEvents(notFiltered(t))(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	_, err = helpers.Store(r).(*sql.SqlDb).Sql().Exec("drop table audit_event")
	require.NoError(t, err)
	w = httptest.NewRecorder()
	getAuditEvents(notFiltered(t))(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAuditController_FiltersAndExportAreIncluded(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events?user=1", 2)
	controller := proApi.NewAuditController(helpers.Store(r))
	r = asActor(r, db.User{ID: 1, Admin: true})
	w := httptest.NewRecorder()
	controller.GetEvents(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var page auditPage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Events, 2)
	r.URL.RawQuery = "format=jsonl&user=1"
	w = httptest.NewRecorder()
	controller.ExportEvents(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "auth.login")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
}

func TestUserResponse_AuditPermissionIsExplicit(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{}
	for _, admin := range []bool{false, true} {
		r := asActor(httptest.NewRequest(http.MethodGet, "/api/user", nil), db.User{ID: 1, Admin: admin})
		w := httptest.NewRecorder()
		NewUserController().GetUser(w, r)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, admin, body["can_read_audit_log"])
		assert.Equal(t, false, body["has_active_subscription"])
	}
}

func TestAuditRoutes_AreGetOnly(t *testing.T) {
	prev := util.Config
	t.Cleanup(func() { util.Config = prev })
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.Debugging = &util.DebuggingConfig{}
	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, auditRouteLogWriter{}, nil, metrics.NewMetrics(), nil, nil)
	for _, target := range []string{"/api/audit-log/events", "/api/audit-log/events/export?format=csv"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodHead, target, nil))
		assert.Equal(t, http.StatusNotFound, w.Code, target)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		assert.Equal(t, http.StatusUnauthorized, w.Code, target)
	}
}

func TestAuditRoutes_PreserveLegacyAndCanonicalStreams(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events", 2)
	store := helpers.Store(r)
	util.Config.Debugging = &util.DebuggingConfig{}
	admin, err := store.CreateUserWithoutPassword(db.User{Username: "audit-admin", Name: "Audit Admin", Email: "admin@example.test", Admin: true})
	require.NoError(t, err)
	member, err := store.CreateUserWithoutPassword(db.User{Username: "audit-member", Name: "Audit Member", Email: "member@example.test"})
	require.NoError(t, err)
	for token, user := range map[string]db.User{"audit-admin-token": admin, "audit-member-token": member} {
		_, err = store.CreateAPIToken(db.APIToken{ID: token, UserID: user.ID, Name: "Audit route test"})
		require.NoError(t, err)
	}
	legacy := "legacy audit event"
	_, err = store.CreateEvent(db.Event{UserID: &admin.ID, Description: &legacy})
	require.NoError(t, err)
	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, auditRouteLogWriter{}, nil, metrics.NewMetrics(), nil, nil)
	request := func(target, token, site string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = helpers.SetContextValue(req, "store", store)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Sec-Fetch-Site", site)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for _, endpoint := range []string{"/api/audit-log/events", "/api/audit-log/events?user=1"} {
		response := request(endpoint, "audit-admin-token", "same-origin")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var page auditPage
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
		require.Len(t, page.Events, 2)
		assert.Equal(t, "auth.login", page.Events[0].EventCode)
		assert.Equal(t, http.StatusForbidden, request(endpoint, "audit-member-token", "same-origin").Code)
		assert.Equal(t, http.StatusForbidden, request(endpoint, "audit-admin-token", "cross-site").Code)
	}
	response := request("/api/audit/events", "audit-admin-token", "same-origin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var events []db.Event
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &events))
	require.NotEmpty(t, events)
	assert.Contains(t, response.Body.String(), legacy)
}

type auditRouteLogWriter struct{}

func (auditRouteLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (auditRouteLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (auditRouteLogWriter) WriteResult(any) error                             { return nil }

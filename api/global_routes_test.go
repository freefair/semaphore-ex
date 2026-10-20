package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/audit/audittest"
	taskServices "github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// globalRoute is one instance-wide route and the global permission that opens it. The body is what an allowed
// caller sends; it is chosen so that the handler answers with the nil services Route is built with here.
type globalRoute struct {
	method     string
	path       string
	body       string
	permission db.GlobalPermission
	// denyName is the permission name a denial is recorded with; empty means the catalog id of permission.
	denyName string
	// forbidsWithoutPlan marks a route whose handler can answer 403 itself
	// after the permission middleware admits the caller.
	forbidsWithoutPlan bool
}

// globalRoutes mirrors the "Route → permission" table of
// AGENTS/work/auth-and-identity/global-roles-and-permissions.md. A route added to systemAPI or rolesAdminAPI
// belongs here too.
func globalRoutes() []globalRoute {
	system := func(method, path, body string) globalRoute {
		return globalRoute{method: method, path: path, body: body, permission: db.CanManageGlobalSystem}
	}
	roles := func(method, path string) globalRoute {
		return globalRoute{method: method, path: path, permission: db.CanManageGlobalRoles, forbidsWithoutPlan: true}
	}
	audit := func(path string) globalRoute {
		return globalRoute{method: http.MethodGet, path: path, permission: db.CanReadGlobalAudit, forbidsWithoutPlan: true}
	}
	return []globalRoute{
		// manage system
		system(http.MethodGet, "/api/options", ""),
		system(http.MethodPost, "/api/options", `{"key":"test.option","value":"1"}`),
		system(http.MethodDelete, "/api/cache", ""),

		// manage roles
		roles(http.MethodGet, "/api/roles"),
		roles(http.MethodPost, "/api/roles"),
		roles(http.MethodGet, "/api/roles/custom"),
		roles(http.MethodPut, "/api/roles/custom"),
		roles(http.MethodPost, "/api/roles/custom"),
		roles(http.MethodDelete, "/api/roles/custom"),
		{method: http.MethodGet, path: "/api/global-roles/permissions", permission: db.CanManageGlobalRoles}, // the catalog needs no plan
		roles(http.MethodGet, "/api/global-roles"),
		roles(http.MethodPost, "/api/global-roles"),
		roles(http.MethodGet, "/api/global-roles/1"),
		roles(http.MethodPut, "/api/global-roles/1"),
		roles(http.MethodPost, "/api/global-roles/1"),
		roles(http.MethodDelete, "/api/global-roles/1"),
		roles(http.MethodGet, "/api/users/1/global-roles"),
		roles(http.MethodPost, "/api/users/1/global-roles"),
		roles(http.MethodDelete, "/api/users/1/global-roles/1"),
		roles(http.MethodGet, "/api/users/1/global-permissions"),

		// read audit
		audit("/api/audit/events"),
		{method: http.MethodGet, path: "/api/audit-log/events", permission: db.CanReadGlobalAudit, denyName: "audit_log"},
	}
}

// additionalGlobalPermissionCoverage records routes that intentionally have a
// multi-permission or capability-sensitive admission rule. The single-bit
// matrix above cannot model those rules without falsely claiming a unique
// permission; their controller contracts cover the admission details. Keeping
// their concrete router paths here still makes a newly exposed global route a
// review failure.
func additionalGlobalPermissionCoverage() []string {
	return []string{
		"GET /api/audit-log/events/export",
		"POST /api/notification-governance/routing/preview",
		"GET /api/global-credentials/grant-projects",
		"POST /api/notification-governance/deliveries/1/retry",
		"POST /api/policy-guardrails/publish",
		"GET /api/notification-governance/destinations/1",
		"POST /api/notification-governance/destinations/1/pause",
		"PUT /api/policy-guardrails/draft",
		"GET /api/policy-guardrails/evaluations",
		"DELETE /api/notification-governance/destinations/1",
		"POST /api/notification-governance/destinations/1/resume",
		"POST /api/global-credentials",
		"GET /api/policy-guardrails/diff",
		"GET /api/global-credentials/1",
		"GET /api/global-credentials/1/grants",
		"POST /api/policy-guardrails/validate",
		"POST /api/policy-guardrails/rollback",
		"PUT /api/workflow-artifact-retention",
		"GET /api/policy-guardrails",
		"GET /api/roles/project-permissions",
		"GET /api/notification-governance/rules",
		"GET /api/workflow-artifact-retention",
		"GET /api/roles/1",
		"GET /api/global-roles/project-permissions",
		"POST /api/notification-governance/rules",
		"PUT /api/roles/1",
		"PUT /api/notification-governance/destinations/1",
		"GET /api/notification-governance/deliveries",
		"PUT /api/global-credentials/1",
		"DELETE /api/notification-governance/rules/1",
		"GET /api/notification-governance/events",
		"POST /api/policy-guardrails/impact",
		"GET /api/notification-governance/destinations",
		"POST /api/global-credentials/1/enabled",
		"GET /api/global-credentials/1/usage",
		"POST /api/global-credentials/1/grants/1/restore",
		"DELETE /api/global-credentials/1/grants/1",
		"PUT /api/global-credentials/1/grants/1",
		"POST /api/global-credentials/1/grants/1/revoke",
		"POST /api/policy-guardrails/test",
		"POST /api/roles/1",
		"POST /api/notification-governance/destinations",
		"POST /api/global-credentials/1/grants",
		"GET /api/policy-guardrails/revisions",
		"DELETE /api/roles/1",
		"POST /api/notification-governance/destinations/1/test",
		"PUT /api/notification-governance/rules/1",
		"POST /api/global-credentials/1/rotate",
		"DELETE /api/global-credentials/1",
		"GET /api/roles/permissions",
	}
}

// fakeRoleMappingStore keeps the Pro role-mappings controller from dereferencing a nil store: no mappings,
// nothing to delete.
type fakeRoleMappingStore struct{}

func (fakeRoleMappingStore) GetRoleMappings() ([]db.RoleMapping, error) {
	return []db.RoleMapping{}, nil
}
func (fakeRoleMappingStore) CreateRoleMapping(db.RoleMapping) (db.RoleMapping, error) {
	return db.RoleMapping{}, db.ErrNotFound
}
func (fakeRoleMappingStore) DeleteRoleMapping(int) (db.RoleMapping, []db.MappedRole, error) {
	return db.RoleMapping{}, nil, db.ErrNotFound
}
func (fakeRoleMappingStore) SyncMappedRoles(int, string, []string) ([]db.MappedRole, []db.MappedRole, error) {
	return nil, nil, nil
}

// globalRoutesFixture drives the real router: session cookie, CSRF, store, global role store and the permission
// middleware chain, exactly as a request from the browser goes.
type globalRoutesFixture struct {
	store   db.Store
	router  http.Handler
	pool    *taskServices.TaskPool
	root    db.User
	plain   db.User
	holders map[db.GlobalPermission]db.User // exactly one bit
	others  map[db.GlobalPermission]db.User // every bit but that one
}

func newGlobalRoutesFixture(t *testing.T) globalRoutesFixture {
	t.Helper()
	store := setupSessionTest(t)
	util.Config.Debugging = &util.DebuggingConfig{}
	util.Config.TmpPath = t.TempDir()

	f := globalRoutesFixture{
		store:   store,
		pool:    poolWithTasks(),
		holders: map[db.GlobalPermission]db.User{},
		others:  map[db.GlobalPermission]db.User{},
	}
	f.root = makeAdmin(t, store, createUserOptionsTestUser(t, store, "root"))
	f.plain = createUserOptionsTestUser(t, store, "plain")
	for _, definition := range db.GlobalPermissionCatalog() {
		roleIdentity := strings.ReplaceAll(definition.ID, ".", "_")
		holder := createUserOptionsTestUser(t, store, "holder-"+definition.ID)
		role, err := store.CreateGlobalRole(db.Role{
			ID: db.ProjectRoleID("route_" + roleIdentity), Slug: "route_" + roleIdentity, Name: "Route " + definition.ID,
			GlobalPermissions: definition.Permission, Revision: 1,
		})
		require.NoError(t, err)
		_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{UserID: holder.ID, RoleID: role.ID, Revision: 1})
		require.NoError(t, err)
		f.holders[definition.Permission] = holder

		other := createUserOptionsTestUser(t, store, "others-"+definition.ID)
		otherRole, err := store.CreateGlobalRole(db.Role{
			ID: db.ProjectRoleID("route_others_" + roleIdentity), Slug: "route_others_" + roleIdentity, Name: "Other route " + definition.ID,
			GlobalPermissions: db.AllGlobalPermissions &^ definition.Permission, Revision: 1,
		})
		require.NoError(t, err)
		_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{UserID: other.ID, RoleID: otherRole.ID, Revision: 1})
		require.NoError(t, err)
		f.others[definition.Permission] = other
	}

	f.router = Route(store, nil, nil, nil, f.pool, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, proServer.NewLogWriteService(), nil, metrics.NewMetrics(), nil, nil)
	return f
}

func (f globalRoutesFixture) send(t *testing.T, route globalRoute, actor db.User) (*httptest.ResponseRecorder, *audittest.Recorder) {
	t.Helper()
	r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
	r = addSessionCookie(t, f.store, r, actor, db.SessionVerificationNone)
	r = helpers.SetContextValue(r, "store", f.store)
	r = helpers.SetContextValue(r, "task_pool", f.pool)
	r, rec := withAuditRecorder(r)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w, rec
}

func denials(rec *audittest.Recorder) []audittest.Recorded {
	var denied []audittest.Recorded
	for _, recorded := range rec.All() {
		if recorded.Event.Kind == audit.AuthAuthorizationDeny {
			denied = append(denied, recorded)
		}
	}
	return denied
}

func (route globalRoute) name() string { return route.method + " " + route.path }

func (route globalRoute) deniedAs() string {
	if route.denyName != "" {
		return route.denyName
	}
	return globalPermissionName(route.permission)
}

func (f globalRoutesFixture) assertAllowed(t *testing.T, route globalRoute, actor db.User) {
	t.Helper()
	w, rec := f.send(t, route, actor)
	assert.Empty(t, denials(rec), "%s: %s must not be denied", route.name(), actor.Username)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code, "%s: %s must be authenticated", route.name(), actor.Username)
	if !route.forbidsWithoutPlan {
		assert.NotEqual(t, http.StatusForbidden, w.Code, "%s: %s must pass", route.name(), actor.Username)
	}
}

func (f globalRoutesFixture) assertDenied(t *testing.T, route globalRoute, actor db.User) {
	t.Helper()
	w, rec := f.send(t, route, actor)
	assert.Equal(t, http.StatusForbidden, w.Code, "%s: %s must be refused", route.name(), actor.Username)
	denied := denials(rec)
	require.Len(t, denied, 1, "%s: %s must be refused with one audit event", route.name(), actor.Username)
	assert.Equal(t, audit.OutcomeFailure, denied[0].Event.Outcome)
	assert.Equal(t, audit.ReasonForbidden, denied[0].Event.Reason)
	assert.Equal(t, audit.DenyMetadata{Method: route.method, Permission: route.deniedAs()}, denied[0].Event.Metadata)
}

// Every explicitly delegated route is opened by its own global permission and by nothing else: not by the other
// bits one at a time, not by all the other bits together, and not by an account without roles. Built-in-admin
// boundaries are intentionally absent: a system grant must not open those diagnostics and executor routes.
func TestGlobalRoutes_PermissionTable(t *testing.T) {
	f := newGlobalRoutesFixture(t)

	for _, route := range globalRoutes() {
		t.Run(route.name(), func(t *testing.T) {
			f.assertAllowed(t, route, f.root)
			f.assertAllowed(t, route, f.holders[route.permission])

			f.assertDenied(t, route, f.plain)
			f.assertDenied(t, route, f.others[route.permission])
			for permission, holder := range f.holders {
				if permission != route.permission {
					f.assertDenied(t, route, holder)
				}
			}
		})
	}
}

// globalRouteParams substitutes the path parameters of a route template with values the probe below can send.
var globalRouteParams = map[string]string{
	"{role_slug}": "custom",
	"{task_id}":   "999", // no such task in the pool, so DELETE stops nothing
	"{app_id}":    "bash",
	"{type}":      "ldap",
	"{provider}":  "default",
}

// globalRouteProbeSkips names route templates the probe skips: they are not authenticated API routes, or they need
// services Route is built without here.
func globalRouteProbeSkips(template string) bool {
	for _, prefix := range []string{"/api/ping", "/api/metrics", "/api/ws", "/api/auth/", "/api/internal/",
		"/api/integrations/", "/api/terraform/", "/.well-known/"} {
		if strings.HasPrefix(template, prefix) {
			return true
		}
	}
	return strings.Contains(template, "{project_id}")
}

// TestGlobalRoutes_TableCoversTheRouter walks the real router and sends every instance-level route as a user
// without roles. A route that refuses that user with the name of a global permission is behind the global
// permission middleware and must have a row in the table above; otherwise a route added to systemAPI or
// rolesAdminAPI would go untested.
func TestGlobalRoutes_TableCoversTheRouter(t *testing.T) {
	f := newGlobalRoutesFixture(t)

	covered := map[string]bool{}
	for _, route := range globalRoutes() {
		covered[route.method+" "+strings.SplitN(route.path, "?", 2)[0]] = true
	}
	for _, route := range additionalGlobalPermissionCoverage() {
		covered[route] = true
	}
	globalPermissionNames := map[string]bool{"audit_log": true}
	for _, definition := range db.GlobalPermissionCatalog() {
		globalPermissionNames[definition.ID] = true
	}

	gated := map[string]bool{}
	err := f.router.(*mux.Router).Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, err := route.GetPathTemplate()
		if err != nil || globalRouteProbeSkips(template) {
			return nil
		}
		methods, err := route.GetMethods()
		if err != nil {
			return nil
		}
		path := template
		for param, value := range globalRouteParams {
			path = strings.ReplaceAll(path, param, value)
		}
		path = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "1")
		for _, method := range methods {
			if method == http.MethodHead || method == http.MethodOptions {
				continue
			}
			rec := f.probe(t, method, path)
			for _, denied := range denials(rec) {
				metadata, _ := denied.Event.Metadata.(audit.DenyMetadata)
				if globalPermissionNames[metadata.Permission] {
					gated[method+" "+path] = true
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, gated, "the probe found no gated route: the walk or the actor is broken")

	for name := range gated {
		assert.True(t, covered[name], "%s is behind a global permission but missing from globalRoutes()", name)
	}
}

// probe sends one request as the user without roles. The middleware answers before the handler on a gated
// route; on an open route a handler may hit one of the nil services and panic, which only says the route is
// not gated, so the panic is swallowed.
func (f globalRoutesFixture) probe(t *testing.T, method string, path string) *audittest.Recorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r = addSessionCookie(t, f.store, r, f.plain, db.SessionVerificationNone)
	r = helpers.SetContextValue(r, "store", f.store)
	r = helpers.SetContextValue(r, "task_pool", f.pool)
	r, rec := withAuditRecorder(r)
	func() {
		defer func() { _ = recover() }()
		f.router.ServeHTTP(httptest.NewRecorder(), r)
	}()
	return rec
}

package projects

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/audit/audittest"
	"github.com/stretchr/testify/assert"
)

type fakeTemplateRoles struct {
	db.TemplateManager
	existing  db.TemplateRolePerm
	getErr    error
	deleteErr error
}

func (f *fakeTemplateRoles) CreateTemplateRole(perm db.TemplateRolePerm) (db.TemplateRolePerm, error) {
	perm.ID = 5
	return perm, nil
}
func (f *fakeTemplateRoles) UpdateTemplateRole(role db.TemplateRolePerm, _ int) (db.TemplateRolePerm, error) {
	if f.existing.RoleSlug != "" {
		return f.existing, nil
	}
	return role, nil
}
func (f *fakeTemplateRoles) GetTemplateRole(int, int, int) (db.TemplateRolePerm, error) {
	return f.existing, f.getErr
}
func (f *fakeTemplateRoles) DeleteTemplateRole(int, int, int, int) error { return f.deleteErr }

func templatePermRequest(method string, body string, permID string) (*http.Request, *audittest.Recorder) {
	rec := &audittest.Recorder{}
	r := httptest.NewRequest(method, "/api/project/12/templates/3/perms", bytes.NewBufferString(body))
	r = helpers.SetContextValue(r, "template", db.Template{ID: 3, ProjectID: 12})
	r = helpers.SetContextValue(r, "audit", rec)
	if permID != "" {
		r = mux.SetURLVars(r, map[string]string{"perm_id": permID})
	}
	return r, rec
}

func TestTemplatePermissions_AreRecorded(t *testing.T) {
	controller := NewTemplateController(&fakeTemplateRoles{existing: db.TemplateRolePerm{ID: 5, RoleSlug: "ops", Permissions: db.CanRunProjectTasks, Revision: 1}}, nil)

	r, rec := templatePermRequest(http.MethodPost, `{"role_slug":"ops","permissions":1}`, "")
	controller.AddTemplatePerm(httptest.NewRecorder(), r)
	got := only(t, rec, audit.IAMTemplatePermissionCreate)
	assert.Equal(t, &audit.Target{Type: audit.TargetTemplatePermission, ID: "5"}, got.Event.Target)
	assert.Equal(t, 12, got.Event.ProjectID)
	assert.Equal(t, audit.TemplatePermissionMetadata{TemplateID: 3, RoleSlug: "ops", Permissions: []string{"run_tasks"}}, got.Event.Metadata)

	r, rec = templatePermRequest(http.MethodPut, `{"role_slug":"ops","permissions":5,"revision":1}`, "5")
	controller.UpdateTemplatePerm(httptest.NewRecorder(), r)
	assert.Equal(t, audit.TemplatePermissionMetadata{TemplateID: 3, RoleSlug: "ops", Permissions: []string{"run_tasks"}},
		only(t, rec, audit.IAMTemplatePermissionUpdate).Event.Metadata)

	r, rec = templatePermRequest(http.MethodDelete, "", "5")
	r.URL.RawQuery = "revision=1"
	controller.DeleteTemplatePerm(httptest.NewRecorder(), r)
	deleted := only(t, rec, audit.IAMTemplatePermissionDelete)
	assert.Equal(t, &audit.Target{Type: audit.TargetTemplatePermission, ID: "5"}, deleted.Event.Target)
	assert.Equal(t, audit.TemplatePermissionMetadata{TemplateID: 3}, deleted.Event.Metadata, "a delete carries identity only")
}

func TestDeleteTemplatePerm_UnknownIDRecordsNothing(t *testing.T) {
	controller := NewTemplateController(&fakeTemplateRoles{deleteErr: db.ErrNotFound}, nil)
	r, rec := templatePermRequest(http.MethodDelete, "", "999")
	r.URL.RawQuery = "revision=1"
	w := httptest.NewRecorder()

	controller.DeleteTemplatePerm(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code, "a missing permission retains the API error")
	assert.Empty(t, rec.All())
}

func TestTemplatePermissions_LongRoleSlugIsBounded(t *testing.T) {
	controller := NewTemplateController(&fakeTemplateRoles{}, nil)
	r, rec := templatePermRequest(http.MethodPost, `{"role_slug":"`+strings.Repeat("s", 5000)+`","permissions":1}`, "")

	controller.AddTemplatePerm(httptest.NewRecorder(), r)

	meta := only(t, rec, audit.IAMTemplatePermissionCreate).Event.Metadata.(audit.TemplatePermissionMetadata)
	assert.Len(t, meta.RoleSlug, audit.MaxNameBytes)
}

func TestUpdateTemplatePerm_RecordsStoredRoleSlug(t *testing.T) {
	controller := NewTemplateController(&fakeTemplateRoles{existing: db.TemplateRolePerm{ID: 5, RoleSlug: "ops", Revision: 1}}, nil)
	r, rec := templatePermRequest(http.MethodPut, `{"role_slug":"guest","permissions":1,"revision":1}`, "5")
	w := httptest.NewRecorder()

	controller.UpdateTemplatePerm(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	meta := only(t, rec, audit.IAMTemplatePermissionUpdate).Event.Metadata.(audit.TemplatePermissionMetadata)
	assert.Equal(t, "ops", meta.RoleSlug)
}

func TestTemplatePermissions_RejectUnknownBits(t *testing.T) {
	controller := NewTemplateController(&fakeTemplateRoles{existing: db.TemplateRolePerm{ID: 5, RoleSlug: "ops"}}, nil)
	tests := []struct {
		name   string
		method string
		permID string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{"add", http.MethodPost, "", controller.AddTemplatePerm},
		{"update", http.MethodPut, "5", controller.UpdateTemplatePerm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, rec := templatePermRequest(tt.method, `{"role_slug":"ops","permissions":17}`, tt.permID)
			w := httptest.NewRecorder()
			tt.handle(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Empty(t, rec.All())
		})
	}
}

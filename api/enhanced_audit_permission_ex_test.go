package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

// The audit classifies a refused project mutation by the historic
// manage-resources boundary. Under D5 that boundary is the complete set of
// resource mutation permissions, whatever representation the role carries.
func TestProjectPermissionDeniedUsesTheCompleteMutationSet(t *testing.T) {
	member := &db.User{ID: 7}
	admin := &db.User{ID: 1, Admin: true}
	tests := []struct {
		name        string
		method      string
		user        *db.User
		permissions db.ProjectUserPermission
		denied      bool
	}{
		{"complete fine-grained set", http.MethodPost, member, db.AllProjectResourceMutationPermissions, false},
		{"legacy coarse bit", http.MethodPost, member, db.CanManageProjectResources, false},
		{"partial set", http.MethodPost, member, db.AllProjectResourceMutationPermissions &^ db.CanDeleteViews, true},
		{"view only", http.MethodPut, member, db.AllProjectResourceViewPermissions, true},
		{"administrator", http.MethodDelete, admin, 0, false},
		{"read request", http.MethodGet, member, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, "/api/project/1/runners", nil)
			request = helpers.SetContextValue(request, "user", tt.user)
			request = helpers.SetContextValue(request, "permissions", tt.permissions)

			assert.Equal(t, tt.denied, projectPermissionDenied(request))
		})
	}
}

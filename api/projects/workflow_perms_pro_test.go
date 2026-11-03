package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	sqlstore "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
)

// Workflow grants are part of the full product without subscription gates.
func TestWorkflowPermissions_AvailableWithoutSubscription(t *testing.T) {
	store := sqlstore.InitConfigCreateTestStore()
	controller := NewWorkflowPermController(&fakeWorkflowRoles{existing: db.WorkflowRolePerm{ID: 5, RoleID: "builtin:guest"}})
	r, _ := workflowPermRequest(t, store, http.MethodGet, "", "5")
	w := httptest.NewRecorder()
	controller.GetWorkflowPerm(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

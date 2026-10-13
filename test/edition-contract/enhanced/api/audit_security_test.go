package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/audit/audittest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func auditSecurityRequest(t *testing.T, target string, user any) (*AuditController, *http.Request, *audittest.Recorder) {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	_, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "event", SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", InstanceID: "test", Metadata: "{}"})
	require.NoError(t, err)
	recorder := &audittest.Recorder{}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = helpers.SetContextValue(req, "user", user)
	req = helpers.SetContextValue(req, "audit", recorder)
	return NewAuditController(store), req, recorder
}

func TestAuditController_DeniesCrossOriginAndUnauthorisedRequests(t *testing.T) {
	for name, user := range map[string]any{"missing": nil, "nil": (*db.User)(nil), "non-admin": &db.User{ID: 2}} {
		t.Run(name, func(t *testing.T) {
			controller, request, _ := auditSecurityRequest(t, "/api/audit-log/events?format=jsonl", user)
			response := httptest.NewRecorder()
			controller.GetEvents(response, request)
			if name == "missing" {
				assert.Equal(t, http.StatusUnauthorized, response.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, response.Code)
			}
		})
	}
	controller, request, recorder := auditSecurityRequest(t, "/api/audit-log/events/export?format=jsonl", &db.User{ID: 1, Admin: true})
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	controller.ExportEvents(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, recorder.All())
}

type failingAuditWriter struct{ header http.Header }

func (w *failingAuditWriter) Header() http.Header     { return w.header }
func (*failingAuditWriter) WriteHeader(int)           {}
func (*failingAuditWriter) Write([]byte) (int, error) { return 0, errors.New("client disconnected") }

func TestAuditController_DoesNotRecordSuccessfulExportAfterWriteFailure(t *testing.T) {
	for _, format := range []string{"jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			controller, request, recorder := auditSecurityRequest(t, "/api/audit-log/events/export?format="+format, &db.User{ID: 1, Admin: true})
			controller.ExportEvents(&failingAuditWriter{header: make(http.Header)}, request.WithContext(audit.WithActor(request.Context(), audit.UserActor(1, "admin", audit.AuthSession, ""))))
			assert.Empty(t, recorder.All())
		})
	}
}

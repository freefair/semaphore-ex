package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	proApi "github.com/semaphoreui/semaphore/pro/api"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditExportPreservesCanonicalEnvelopeInBothFormats(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events/export", 1)
	store := helpers.Store(r)
	controller := proApi.NewAuditController(store)
	for _, format := range []string{"csv", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			req := asActor(r.Clone(r.Context()), db.User{ID: 1, Admin: true})
			req.URL.RawQuery = "format=" + format
			req, rec := withAuditRecorder(req)
			w := httptest.NewRecorder()
			controller.ExportEvents(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
			var envelope audit.Envelope
			if format == "csv" {
				rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
				require.NoError(t, err)
				require.Len(t, rows, 2)
				column := -1
				for i, name := range rows[0] {
					if name == "event_json" {
						column = i
					}
				}
				require.GreaterOrEqual(t, column, 0, "CSV must preserve complete canonical envelope")
				require.NoError(t, json.Unmarshal([]byte(rows[1][column]), &envelope))
			} else {
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			}
			assert.Equal(t, "e", envelope.EventID)
			assert.Equal(t, "admin", envelope.Actor.Name)
			event := onlyEvent(t, rec, audit.AuditLogExport)
			assert.Equal(t, audit.ExportMetadata{Format: format, Filters: url.Values{"format": {format}}, Events: 1, Complete: true}, event.Event.Metadata)
		})
	}
}

func TestAuditFiltersApplyAllFieldsAndExclusiveEnd(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events", 0)
	store := helpers.Store(r).(*coresql.SqlDb)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	project := 42
	for i := 0; i < 3; i++ {
		event, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: fmt.Sprintf("filtered-%d", i), SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "failure", ActorType: "user", ActorID: "7", SourceIP: "203.0.113.7", TargetType: "user", TargetID: "7", ProjectID: &project, InstanceID: "test", Metadata: "{}"})
		require.NoError(t, err)
		_, err = store.Sql().Exec("UPDATE audit_event SET created = ? WHERE seq = ?", start.Add(time.Duration(i)*time.Minute), event.Seq)
		require.NoError(t, err)
	}
	controller := proApi.NewAuditController(store)
	query := url.Values{"from": {start.Format(time.RFC3339)}, "to": {start.Add(2 * time.Minute).Format(time.RFC3339)}, "user": {"7"}, "project": {"42"}, "kind": {"auth.login/authenticate", "iam.user/create"}, "outcome": {"failure"}, "ip": {"203.0.113.7"}, "target_type": {"user"}, "target_id": {"7"}}
	r = asActor(r, db.User{ID: 1, Admin: true})
	r.URL.RawQuery = query.Encode()
	w := httptest.NewRecorder()
	controller.GetEvents(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page auditPage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Events, 2)
	assert.Equal(t, "filtered-1", page.Events[0].EventID)
	assert.Equal(t, "filtered-0", page.Events[1].EventID)
	query.Set("project", "43")
	r.URL.RawQuery = query.Encode()
	w = httptest.NewRecorder()
	controller.GetEvents(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Empty(t, page.Events)
}

func TestAuditExportRejectsMalformedRowsBeforeAttachment(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events/export", 1)
	store := helpers.Store(r).(*coresql.SqlDb)
	_, err := store.Sql().Exec("UPDATE audit_event SET metadata = 'broken'")
	require.NoError(t, err)
	controller := proApi.NewAuditController(store)
	for _, format := range []string{"csv", "jsonl"} {
		req := asActor(r.Clone(r.Context()), db.User{ID: 1, Admin: true})
		req.URL.RawQuery = "format=" + format
		w := httptest.NewRecorder()
		controller.ExportEvents(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code, format)
		assert.Empty(t, w.Header().Get("Content-Disposition"), format)
	}
}

func TestAuditExportRejectsOversizedResultBeforeAttachment(t *testing.T) {
	r := auditLogRequest(t, "/api/audit-log/events/export", 1)
	store := helpers.Store(r).(*coresql.SqlDb)
	page, err := store.ListAuditEvents(context.Background(), 0, 0, 1)
	require.NoError(t, err)
	tx, err := store.Sql().Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	for i := 2; i <= 10001; i++ {
		event := page.Events[0]
		event.Seq = int64(i)
		require.NoError(t, tx.Insert(&event))
	}
	require.NoError(t, tx.Commit())
	controller := proApi.NewAuditController(store)
	for _, format := range []string{"csv", "jsonl"} {
		req := asActor(r.Clone(r.Context()), db.User{ID: 1, Admin: true})
		req.URL.RawQuery = "format=" + format
		req, rec := withAuditRecorder(req)
		w := httptest.NewRecorder()
		controller.ExportEvents(w, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, format)
		assert.Empty(t, w.Header().Get("Content-Disposition"), format)
		assert.Empty(t, rec.All(), format)
	}
}

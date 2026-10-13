package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	prosql "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/services/audit"
)

const auditExportLimit = 10000

type AuditController struct{ repository *prosql.AuditEventRepository }

func NewAuditController(store db.Store) *AuditController {
	sqlStore, _ := store.(*coresql.SqlDb)
	return &AuditController{repository: prosql.NewAuditEventRepository(sqlStore)}
}

func (c *AuditController) GetEvents(w http.ResponseWriter, r *http.Request) {
	if !auditRequestAllowed(w, r) {
		return
	}
	filter, err := parseAuditFilter(r.URL.Query(), false)
	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}
	if c.repository == nil {
		helpers.WriteErrorStatus(w, "Audit log is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := c.repository.List(r.Context(), filter, 50)
	if err != nil {
		helpers.WriteErrorStatus(w, "Failed to read audit events", http.StatusInternalServerError)
		return
	}
	body := auditPage{Events: []audit.Envelope{}}
	for _, row := range page.Events {
		event, err := audit.EnvelopeFromRow(row)
		if err != nil {
			helpers.WriteErrorStatus(w, "Failed to read audit events", http.StatusInternalServerError)
			return
		}
		body.Events = append(body.Events, event)
	}
	if page.Older > 0 {
		body.Older = &page.Older
	}
	if page.Newer > 0 {
		body.Newer = &page.Newer
	}
	helpers.WriteJSON(w, http.StatusOK, body)
}

func (c *AuditController) ExportEvents(w http.ResponseWriter, r *http.Request) {
	if !auditRequestAllowed(w, r) {
		return
	}
	filter, err := parseAuditFilter(r.URL.Query(), true)
	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}
	if c.repository == nil {
		helpers.WriteErrorStatus(w, "Audit log is unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, complete, err := c.repository.Export(r.Context(), filter, auditExportLimit)
	if err != nil {
		helpers.WriteErrorStatus(w, "Failed to export audit events", http.StatusInternalServerError)
		return
	}
	if !complete {
		helpers.WriteErrorStatus(w, "Audit export exceeds 10000 events; narrow the filters", http.StatusRequestEntityTooLarge)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "csv" {
		encoded := make([][]string, 0, len(rows)+1)
		encoded = append(encoded, []string{"seq", "timestamp", "event_code", "action", "outcome", "actor_id", "project_id", "source_ip", "target_type", "target_id", "event_json"})
		for _, row := range rows {
			event, err := audit.EnvelopeFromRow(row)
			if err != nil {
				helpers.WriteErrorStatus(w, "Failed to export audit events", http.StatusInternalServerError)
				return
			}
			canonical, err := json.Marshal(event)
			if err != nil {
				helpers.WriteErrorStatus(w, "Failed to export audit events", http.StatusInternalServerError)
				return
			}
			project := ""
			if row.ProjectID != nil {
				project = strconv.Itoa(*row.ProjectID)
			}
			encoded = append(encoded, []string{strconv.FormatInt(row.Seq, 10), row.Created.UTC().Format(time.RFC3339Nano), csvSafe(row.EventCode), csvSafe(row.Action), csvSafe(row.Outcome), csvSafe(row.ActorID), project, csvSafe(row.SourceIP), csvSafe(row.TargetType), csvSafe(row.TargetID), string(canonical)})
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-events.csv"`)
		writer := csv.NewWriter(w)
		for _, record := range encoded {
			if err := writer.Write(record); err != nil {
				return
			}
		}
		writer.Flush()
		if writer.Error() != nil {
			return
		}
	} else {
		encoded := make([]audit.Envelope, 0, len(rows))
		for _, row := range rows {
			event, err := audit.EnvelopeFromRow(row)
			if err != nil {
				helpers.WriteErrorStatus(w, "Failed to export audit events", http.StatusInternalServerError)
				return
			}
			encoded = append(encoded, event)
		}
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-events.jsonl"`)
		encoder := json.NewEncoder(w)
		for _, event := range encoded {
			if encoder.Encode(event) != nil {
				return
			}
		}
	}
	helpers.Audit(r).Record(r.Context(), audit.Event{Kind: audit.AuditLogExport, Metadata: audit.ExportMetadata{Format: format, Filters: r.URL.Query(), Events: int64(len(rows)), Complete: true}})
}

type auditPage struct {
	Events []audit.Envelope `json:"events"`
	Older  *int64           `json:"older"`
	Newer  *int64           `json:"newer"`
}

func auditRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	raw, ok := helpers.GetOkFromContext(r, "user")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	user, ok := raw.(*db.User)
	if !ok || user == nil || !user.Admin {
		helpers.RecordDenied(r, "audit_log", 0)
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return true
}

func parseAuditFilter(query url.Values, exporting bool) (prosql.AuditFilter, error) {
	allowed := map[string]bool{"before": true, "after": true, "from": true, "to": true, "user": true, "project": true, "kind": true, "outcome": true, "ip": true, "target_type": true, "target_id": true}
	if exporting {
		allowed["format"] = true
	}
	for name, values := range query {
		if !allowed[name] {
			return prosql.AuditFilter{}, fmt.Errorf("unknown audit parameter %q", name)
		}
		if name != "kind" && len(values) != 1 {
			return prosql.AuditFilter{}, fmt.Errorf("%s must be given once", name)
		}
	}
	if exporting && query.Get("format") != "csv" && query.Get("format") != "jsonl" {
		return prosql.AuditFilter{}, fmt.Errorf("format must be csv or jsonl")
	}
	f := prosql.AuditFilter{Kinds: query["kind"], Outcome: query.Get("outcome"), IP: query.Get("ip"), TargetType: query.Get("target_type"), TargetID: query.Get("target_id")}
	var err error
	if f.Before, err = auditCursor(query.Get("before"), true); err != nil {
		return f, err
	}
	if f.After, err = auditCursor(query.Get("after"), false); err != nil {
		return f, err
	}
	if f.Before > 0 && f.After > 0 {
		return f, fmt.Errorf("before and after cannot be used together")
	}
	if f.From, err = auditTime(query.Get("from")); err != nil {
		return f, err
	}
	if f.To, err = auditTime(query.Get("to")); err != nil {
		return f, err
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return f, fmt.Errorf("from must be before to")
	}
	if f.User, err = auditInteger(query.Get("user")); err != nil {
		return f, fmt.Errorf("user must be a positive number")
	}
	if f.Project, err = auditInteger(query.Get("project")); err != nil {
		return f, fmt.Errorf("project must be a positive number")
	}
	if len(f.Kinds) > 32 {
		return f, fmt.Errorf("at most 32 kinds are allowed")
	}
	for _, kind := range f.Kinds {
		code, action, hasAction := strings.Cut(kind, "/")
		if !hasAction || code == "" || action == "" || len(kind) > 256 {
			return f, fmt.Errorf("kind must be event_code/action")
		}
	}
	if f.Outcome != "" && f.Outcome != "success" && f.Outcome != "failure" {
		return f, fmt.Errorf("outcome must be success or failure")
	}
	if f.IP != "" && net.ParseIP(f.IP) == nil {
		return f, fmt.Errorf("ip must be an IP address")
	}
	if (f.TargetType == "") != (f.TargetID == "") {
		return f, fmt.Errorf("target_type and target_id must be given together")
	}
	return f, nil
}
func auditCursor(value string, before bool) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || (!before && n == math.MaxInt64) {
		return 0, fmt.Errorf("cursor must be a positive number")
	}
	return n, nil
}
func auditInteger(value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("invalid integer")
	}
	return &n, nil
}
func auditTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("time must be RFC3339")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
func csvSafe(value string) string {
	if value != "" && strings.ContainsAny(value[:1], "=+-@\t\r\n") {
		return "'" + value
	}
	return value
}

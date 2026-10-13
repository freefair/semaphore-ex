package sql

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
)

type AuditEventRepository struct{ store *coresql.SqlDb }
type AuditFilter struct {
	Before, After                     int64
	From, To                          *time.Time
	User, Project                     *int
	Kinds                             []string
	Outcome, IP, TargetType, TargetID string
}
type AuditPage struct {
	Events       []db.AuditEvent
	Older, Newer int64
}

func NewAuditEventRepository(store *coresql.SqlDb) *AuditEventRepository {
	return &AuditEventRepository{store: store}
}
func (r *AuditEventRepository) List(ctx context.Context, f AuditFilter, limit int) (AuditPage, error) {
	var out AuditPage
	rows, more, err := r.query(ctx, f, limit+1)
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		more = true
		rows = rows[:limit]
	}
	if f.After > 0 {
		slices.Reverse(rows)
		out.Older = f.After + 1
		if len(rows) > 0 {
			out.Older = rows[len(rows)-1].Seq
		}
		if more && len(rows) > 0 {
			out.Newer = rows[0].Seq
		}
	} else {
		if more && len(rows) > 0 {
			out.Older = rows[len(rows)-1].Seq
		}
		if f.Before > 0 {
			out.Newer = f.Before - 1
		}
	}
	out.Events = rows
	return out, nil
}
func (r *AuditEventRepository) Export(ctx context.Context, f AuditFilter, limit int) ([]db.AuditEvent, bool, error) {
	rows, more, err := r.query(ctx, f, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(rows) > limit {
		return nil, false, nil
	}
	if f.After > 0 {
		slices.Reverse(rows)
	}
	return rows, !more, nil
}
func (r *AuditEventRepository) query(ctx context.Context, f AuditFilter, limit int) ([]db.AuditEvent, bool, error) {
	if r == nil || r.store == nil {
		return nil, false, db.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q := squirrel.Select("*").From("audit_event").Limit(uint64(limit))
	if f.Before > 0 {
		q = q.Where(squirrel.Lt{"seq": f.Before})
	}
	if f.After > 0 {
		q = q.Where(squirrel.Gt{"seq": f.After})
	}
	if f.From != nil {
		q = q.Where(squirrel.GtOrEq{"created": *f.From})
	}
	if f.To != nil {
		q = q.Where(squirrel.Lt{"created": *f.To})
	}
	if f.User != nil {
		// actor_id is text in all supported databases; include the actor type so
		// a numeric runner or integration identifier cannot match a user filter.
		q = q.Where(squirrel.Eq{"actor_type": "user", "actor_id": strconv.Itoa(*f.User)})
	}
	if f.Project != nil {
		q = q.Where(squirrel.Eq{"project_id": *f.Project})
	}
	if len(f.Kinds) > 0 {
		parts := make(squirrel.Or, 0, len(f.Kinds))
		for _, kind := range f.Kinds {
			code, action, _ := strings.Cut(kind, "/")
			category, _, _ := strings.Cut(code, ".")
			parts = append(parts, squirrel.And{squirrel.Eq{"category": category}, squirrel.Eq{"event_code": code}, squirrel.Eq{"action": action}})
		}
		q = q.Where(parts)
	}
	if f.Outcome != "" {
		q = q.Where(squirrel.Eq{"outcome": f.Outcome})
	}
	if f.IP != "" {
		q = q.Where(squirrel.Eq{"source_ip": f.IP})
	}
	if f.TargetType != "" {
		q = q.Where(squirrel.Eq{"target_type": f.TargetType, "target_id": f.TargetID})
	}
	if f.After > 0 {
		q = q.OrderBy("seq asc")
	} else {
		q = q.OrderBy("seq desc")
	}
	statement, args, err := q.ToSql()
	if err != nil {
		return nil, false, err
	}
	var rows []db.AuditEvent
	_, err = r.store.Sql().WithContext(ctx).Select(&rows, r.store.PrepareQuery(statement), args...)
	return rows, false, err
}

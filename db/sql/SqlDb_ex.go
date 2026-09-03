package sql

import (
	"context"
	"database/sql"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
)

// QueryContext exposes bounded row streaming without exposing the underlying
// database handle to edition-specific repositories.
func (d *SqlDbConnection) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if d == nil || d.sql == nil || d.sql.Db == nil || ctx == nil {
		return nil, db.ErrInvalidOperation
	}
	return d.sql.Db.QueryContext(ctx, d.PrepareQuery(query), formatArgs(args)...)
}

func (d *SqlDbConnection) Begin() (*gorp.Transaction, error) {
	return d.sql.Begin()
}

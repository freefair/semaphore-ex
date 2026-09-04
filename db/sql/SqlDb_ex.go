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

// ExecContext exposes bounded mutations without leaking the underlying
// database handle across the replaceable-edition boundary.
func (d *SqlDbConnection) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if d == nil || d.sql == nil || d.sql.Db == nil || ctx == nil {
		return nil, db.ErrInvalidOperation
	}
	return d.sql.Db.ExecContext(ctx, d.PrepareQuery(query), formatArgs(args)...)
}

func (d *SqlDbConnection) Begin() (*gorp.Transaction, error) {
	return d.sql.Begin()
}

// SelectAllContext exposes context-bound gorp mapping to replaceable-edition
// repositories without leaking the underlying database handle.
func (d *SqlDbConnection) SelectAllContext(ctx context.Context, i any, query string, args ...any) ([]any, error) {
	if d == nil || d.sql == nil || d.sql.Db == nil || ctx == nil {
		return nil, db.ErrInvalidOperation
	}
	return d.sql.WithContext(ctx).Select(i, d.PrepareQuery(query), args...)
}

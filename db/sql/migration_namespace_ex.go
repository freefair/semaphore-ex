package sql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/go-gorp/gorp/v3"
)

const exMigrationHistorySQL = `
create table ` + "`ex_migrations`" + ` (
	` + "`version`" + ` varchar(255) not null primary key,
	` + "`upgraded_date`" + ` datetime null,
	` + "`notes`" + ` text null
);
`

// exMigration65 replaces upstream's definition-node foreign key with the
// workflow-run snapshot key. A delay is executable state for one immutable
// run, so accepting a node from another run would break the execution fence.
type exMigration65 struct {
	db *SqlDb
}

func (m exMigration65) PreApply(tx *gorp.Transaction) error {
	switch m.db.Sql().Dialect.(type) {
	case gorp.MySQLDialect:
		return dropMysqlForeignKey(tx, "project__workflow_delay", "workflow_node_id")
	case gorp.PostgresDialect:
		_, err := tx.Exec(m.db.PrepareQuery(
			"alter table `project__workflow_delay` drop constraint `project__workflow_delay_workflow_node_id_fkey`"))
		return err
	}
	return nil
}

func (m exMigration65) PreRollback(tx *gorp.Transaction) error {
	switch m.db.Sql().Dialect.(type) {
	case gorp.MySQLDialect:
		return dropMysqlForeignKey(tx, "project__workflow_delay", "workflow_node_id")
	case gorp.PostgresDialect:
		_, err := tx.Exec(m.db.PrepareQuery(
			"alter table `project__workflow_delay` drop constraint `project__workflow_delay__run_node`"))
		return err
	}
	return nil
}

// WithMigrationLock serializes both upstream and EX migration phases on HA
// database engines. The lock stays on a dedicated connection while the caller
// uses the normal pool, so transaction work cannot accidentally release it.
func (d *SqlDb) WithMigrationLock(operation func() error) (err error) {
	if operation == nil {
		return fmt.Errorf("migration lock operation is nil")
	}

	ctx := context.Background()
	switch d.Sql().Dialect.(type) {
	case gorp.PostgresDialect:
		const advisoryKey int64 = 0x53454d4558504c
		connection, connErr := d.Sql().Db.Conn(ctx)
		if connErr != nil {
			return fmt.Errorf("open PostgreSQL migration lock connection: %w", connErr)
		}
		defer connection.Close() //nolint:errcheck
		if _, err = connection.ExecContext(ctx, d.PrepareQuery("select pg_advisory_lock(?)"), advisoryKey); err != nil {
			return fmt.Errorf("acquire PostgreSQL migration lock: %w", err)
		}
		defer func() {
			if _, unlockErr := connection.ExecContext(ctx, d.PrepareQuery("select pg_advisory_unlock(?)"), advisoryKey); unlockErr != nil && err == nil {
				err = fmt.Errorf("release PostgreSQL migration lock: %w", unlockErr)
			}
		}()
	case gorp.MySQLDialect:
		connection, connErr := d.Sql().Db.Conn(ctx)
		if connErr != nil {
			return fmt.Errorf("open MySQL migration lock connection: %w", connErr)
		}
		defer connection.Close() //nolint:errcheck

		var acquired sql.NullInt64
		err = connection.QueryRowContext(ctx,
			d.PrepareQuery("select get_lock(concat('semex:', substring(sha2(database(), 256), 1, 58)), ?)"), 30,
		).Scan(&acquired)
		if err != nil {
			return fmt.Errorf("acquire MySQL migration lock: %w", err)
		}
		if !acquired.Valid || acquired.Int64 != 1 {
			return fmt.Errorf("acquire MySQL migration lock: another migration is active")
		}
		defer func() {
			if _, unlockErr := connection.ExecContext(ctx,
				d.PrepareQuery("select release_lock(concat('semex:', substring(sha2(database(), 256), 1, 58)))"),
			); unlockErr != nil && err == nil {
				err = fmt.Errorf("release MySQL migration lock: %w", unlockErr)
			}
		}()
	case gorp.SqliteDialect:
		// SQLite is a single-host development store, not an HA deployment. Its
		// one-connection pool would deadlock if a pinned lock connection waited
		// for work issued through that same pool.
		return operation()
	}

	return operation()
}

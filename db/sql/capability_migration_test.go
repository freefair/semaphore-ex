package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
)

func TestCapabilityMigrationPreparesAutoIncrementForEverySQLDialect(t *testing.T) {
	tests := []struct {
		name       string
		dialect    string
		gorp       gorp.Dialect
		expectedID string
	}{
		{"sqlite", "sqlite", gorp.SqliteDialect{}, "integer primary key autoincrement"},
		{"mysql", "mysql", gorp.MySQLDialect{}, "integer primary key auto_increment"},
		{"postgres", "postgres", gorp.PostgresDialect{}, "serial primary key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: tt.gorp}}}
			queries := getVersionSQL(tt.dialect, "v2.20.2.sql", false)
			prepared := make([]string, len(queries))
			for i, query := range queries {
				prepared[i] = store.prepareMigration(query)
			}
			joined := strings.ToLower(strings.Join(prepared, ";"))
			if !strings.Contains(joined, tt.expectedID) {
				t.Fatalf("prepared migration does not contain %q:\n%s", tt.expectedID, joined)
			}
			if tt.name != "sqlite" && strings.Contains(joined, "autoincrement") {
				t.Fatalf("prepared %s migration retains SQLite syntax:\n%s", tt.name, joined)
			}
		})
	}
}

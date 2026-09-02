package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
)

func TestGlobalCredentialMigrationPreparesForEverySQLDialect(t *testing.T) {
	tests := []struct {
		name       string
		dialect    string
		gorp       gorp.Dialect
		expectedID string
	}{
		{"sqlite", "sqlite", gorp.SqliteDialect{}, "integer primary key autoincrement"},
		{"mysql", "mysql", gorp.MySQLDialect{}, "integer primary key auto_increment"},
		{"mariadb", "mysql", gorp.MySQLDialect{}, "integer primary key auto_increment"},
		{"postgres", "postgres", gorp.PostgresDialect{}, "serial primary key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: tt.gorp}}}
			queries := getVersionSQL(tt.dialect, "v2.20.56.sql", false)
			prepared := make([]string, len(queries))
			for index, query := range queries {
				prepared[index] = store.prepareMigration(query)
			}
			joined := strings.ToLower(strings.Join(prepared, ";"))
			if !strings.Contains(joined, tt.expectedID) {
				t.Fatalf("prepared migration does not contain %q:\n%s", tt.expectedID, joined)
			}
			if tt.name != "sqlite" && strings.Contains(joined, "autoincrement") {
				t.Fatalf("prepared %s migration retains SQLite syntax:\n%s", tt.name, joined)
			}
			if tt.name == "mysql" || tt.name == "mariadb" {
				if strings.Contains(joined, "encrypted_material longtext not null default") {
					t.Fatalf("prepared %s migration assigns a forbidden LONGTEXT default:\n%s", tt.name, joined)
				}
			}
		})
	}
}

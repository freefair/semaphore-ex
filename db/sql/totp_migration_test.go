package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
	"github.com/stretchr/testify/assert"
)

func TestTOTPMigrationPreparesForEverySQLDialect(t *testing.T) {
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
			queries := getVersionSQL(tt.dialect, "v2.20.12.sql", false)
			prepared := make([]string, len(queries))
			for index, query := range queries {
				prepared[index] = store.prepareMigration(query)
			}
			joined := strings.ToLower(strings.Join(prepared, ";"))
			assert.Contains(t, joined, tt.expectedID)
			assert.Contains(t, joined, "encrypted_secret")
			assert.Contains(t, joined, "last_used_step")
			assert.Contains(t, joined, "user__totp_recovery_code")
			assert.Contains(t, joined, "totp_capability_transition")
		})
	}
}

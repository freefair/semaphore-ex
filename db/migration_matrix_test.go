package db

import (
	"testing"

	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationRegistryOrderingAndDialectSelection(t *testing.T) {
	tests := []struct {
		name         string
		dialect      string
		firstVersion string
	}{
		{name: "SQLite snapshot", dialect: util.DbDriverSQLite, firstVersion: "2.15.1.sqlite"},
		{name: "MySQL and MariaDB history", dialect: util.DbDriverMySQL, firstVersion: "0.0.0"},
		{name: "PostgreSQL history", dialect: util.DbDriverPostgres, firstVersion: "0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			migrations := GetMigrations(tt.dialect)
			require.NotEmpty(t, migrations)
			assert.Equal(t, tt.firstVersion, migrations[0].Version)
			assert.Equal(t, "2.20.40", migrations[len(migrations)-1].Version)

			seen := make(map[string]struct{}, len(migrations))
			for index, migration := range migrations {
				_, duplicate := seen[migration.Version]
				assert.Falsef(t, duplicate, "migration %s is registered more than once", migration.Version)
				seen[migration.Version] = struct{}{}

				if index == 0 || migration.Version == "2.15.2" {
					continue
				}
				previous := migrations[index-1]
				assert.Lessf(t, previous.Compare(migration), 0,
					"migration %s must precede %s", previous.Version, migration.Version)
			}
		})
	}
}

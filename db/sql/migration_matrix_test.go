package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const currentDevelopSchemaVersion = "2.20.1"

type migrationMatrixConfig struct {
	Dialect  string
	Database string
	Host     string
	Username string
	Pass     string
}

type migrationMatrixReport struct {
	RollbackVersion                         string
	FreshSchema                             []migrationSemanticColumn
	UpgradedSchema                          []migrationSemanticColumn
	CommunityDataSurvivedRollbackAndUpgrade bool
	RestartPreservedEnhancedData            bool
}

type migrationSemanticColumn struct {
	Table      string
	Name       string
	Type       string
	Nullable   bool
	PrimaryKey bool
}

type migrationMatrixFixture struct {
	Version string
	User    db.User
}

var currentDevelopSchemaFixtures = map[string]migrationMatrixFixture{
	util.DbDriverSQLite: {
		Version: currentDevelopSchemaVersion,
		User:    db.User{Username: "matrix-sqlite", Name: "Migration Matrix", Email: "matrix-sqlite@example.com"},
	},
	util.DbDriverMySQL: {
		Version: currentDevelopSchemaVersion,
		User:    db.User{Username: "matrix-mysql", Name: "Migration Matrix", Email: "matrix-mysql@example.com"},
	},
	util.DbDriverPostgres: {
		Version: currentDevelopSchemaVersion,
		User:    db.User{Username: "matrix-postgres", Name: "Migration Matrix", Email: "matrix-postgres@example.com"},
	},
}

func TestMigrationMatrix(t *testing.T) {
	report := runMigrationMatrix(t, migrationMatrixConfigFromEnvironment(t))
	require.Equal(t, currentDevelopSchemaVersion, report.RollbackVersion)
	assert.Equal(t, report.FreshSchema, report.UpgradedSchema)
	assert.True(t, report.CommunityDataSurvivedRollbackAndUpgrade)
	assert.True(t, report.RestartPreservedEnhancedData)
}

func TestEnhancedRollbackMigrationsUseMySQLDropIndexSyntax(t *testing.T) {
	for _, filename := range []string{
		"v2.20.15.err.sql",
		"v2.20.20.err.sql",
		"v2.20.21.err.sql",
		"v2.20.22.err.sql",
		"v2.20.23.err.sql",
		"v2.20.37.err.sql",
		"v2.20.39.err.sql",
		"v2.20.49.err.sql",
		"v2.20.50.err.sql",
		"v2.20.60.err.sql",
		"v2.20.62.err.sql",
		"v2.20.64.err.sql",
	} {
		t.Run(filename, func(t *testing.T) {
			for _, query := range getVersionSQL(util.DbDriverMySQL, filename, true) {
				normalized := strings.ToLower(strings.TrimSpace(query))
				if strings.HasPrefix(normalized, "drop index") {
					assert.Contains(t, normalized, " on ", "MySQL DROP INDEX requires its table")
				}
			}
		})
	}
}

func runMigrationMatrix(t testing.TB, config migrationMatrixConfig) migrationMatrixReport {
	t.Helper()
	fixture, ok := currentDevelopSchemaFixtures[config.Dialect]
	require.Truef(t, ok, "unsupported migration matrix dialect %q", config.Dialect)

	configureMigrationMatrix(t, config)
	store := CreateDb(config.Dialect)
	store.Connect()
	t.Cleanup(func() { store.Close() })

	require.Empty(t, matrixUserTables(t, store), "migration matrix database must be empty")
	require.NoError(t, db.Migrate(store, nil))
	freshSchema := captureCapabilitySchema(t, store)
	assertCapabilitySchema(t, freshSchema)
	assertWorkflowParameterColumns(t, store, true)
	assertWorkflowTriggerSchema(t, store, true)
	assertWorkflowProgressionSchema(t, store, true)
	assertGlobalTemplateRoleSchema(t, store, true)
	assertDeploymentWindowSchema(t, store, true)
	assertProjectRoleMutationLock(t, store, config.Dialect)

	user, err := store.CreateUserWithoutPassword(fixture.User)
	require.NoError(t, err)

	require.NoError(t, db.Rollback(store, fixture.Version))
	assertCapabilityTablesAbsent(t, store)
	assertWorkflowParameterColumns(t, store, false)
	assertWorkflowTriggerSchema(t, store, false)
	assertWorkflowProgressionSchema(t, store, false)
	assertGlobalTemplateRoleSchema(t, store, false)
	assertDeploymentWindowSchema(t, store, false)

	legacyUser, err := store.GetUser(user.ID)
	require.NoError(t, err)
	communityAtLegacySchema := legacyUser.Username == fixture.User.Username

	require.NoError(t, db.Migrate(store, nil))
	upgradedSchema := captureCapabilitySchema(t, store)
	assertCapabilitySchema(t, upgradedSchema)
	assertWorkflowParameterColumns(t, store, true)
	assertWorkflowTriggerSchema(t, store, true)
	assertWorkflowProgressionSchema(t, store, true)
	assertGlobalTemplateRoleSchema(t, store, true)
	assertDeploymentWindowSchema(t, store, true)
	assert.Equal(t, freshSchema, upgradedSchema)

	upgradedUser, err := store.GetUser(user.ID)
	require.NoError(t, err)
	communityDataSurvived := communityAtLegacySchema && upgradedUser.Username == fixture.User.Username

	now := time.Unix(1_700_000_000, 0).UTC()
	require.NoError(t, store.SaveCapabilityConfig(db.CapabilityConfig{
		CapabilityID: "lifecycle_test",
		State:        "active",
		Updated:      now,
	}))
	created, err := store.CreateCapabilityTestRecord(db.CapabilityTestRecord{
		Value:   "restart-preserved",
		Source:  "migration_matrix",
		Created: now,
	})
	require.NoError(t, err)
	require.NotZero(t, created.ID)
	require.NoError(t, store.SaveLDAPProvider(db.LDAPProvider{
		ID: "matrix-ldap", DisplayName: "Matrix LDAP", State: "shadow",
		ServerURL: "ldaps://ldap.example.test:636", TLSMode: "ldaps", TrustMode: "system",
		BindDN: "cn=reader,dc=example,dc=test", EncryptedBindPassword: "encrypted-fixture",
		SearchBaseDN: "ou=people,dc=example,dc=test", UserFilter: "(uid={{username}})",
		IdentityAttribute: "entryUUID", UsernameAttribute: "uid",
		NameAttribute: "cn", EmailAttribute: "mail", ReadinessStatus: "untested",
		ReadinessCode: "matrix", Created: now, Updated: now,
	}))
	matrixRole, err := store.CreateGlobalRole(db.Role{
		ID: "matrix_global_role", Slug: "matrix_global_role", Name: "Matrix global role",
		GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: user.ID, RoleID: matrixRole.ID, Revision: 1,
	})
	require.NoError(t, err)

	store.Close()
	store = CreateDb(config.Dialect)
	store.Connect()

	persistedConfig, err := store.GetCapabilityConfig("lifecycle_test")
	require.NoError(t, err)
	persistedRecords, err := store.GetCapabilityTestRecords()
	require.NoError(t, err)
	persistedLDAP, err := store.GetLDAPProvider("matrix-ldap")
	require.NoError(t, err)
	persistedGlobalAssignments, err := store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	restartPreserved := persistedConfig.State == "active" &&
		len(persistedRecords) == 1 && persistedRecords[0].Value == "restart-preserved" &&
		persistedLDAP.State == "shadow" && persistedLDAP.ServerURL == "ldaps://ldap.example.test:636" &&
		len(persistedGlobalAssignments) == 1 &&
		persistedGlobalAssignments[0].RoleID == matrixRole.ID

	return migrationMatrixReport{
		RollbackVersion:                         fixture.Version,
		FreshSchema:                             freshSchema,
		UpgradedSchema:                          upgradedSchema,
		CommunityDataSurvivedRollbackAndUpgrade: communityDataSurvived,
		RestartPreservedEnhancedData:            restartPreserved,
	}
}

func assertProjectRoleMutationLock(t testing.TB, store *SqlDb, dialect string) {
	t.Helper()
	project, err := store.CreateProject(db.Project{Name: "Role lock " + dialect})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "role-lock-" + dialect,
		Name:     "Role lock " + dialect,
		Email:    "role-lock-" + dialect + "@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      db.ProjectOwner,
	})
	require.NoError(t, err)
}

func assertGlobalTemplateRoleSchema(t testing.TB, store *SqlDb, expected bool) {
	t.Helper()
	tables := matrixUserTables(t, store)
	for _, table := range []string{"global_role_state", "user__global_role"} {
		assert.Equalf(t, expected, containsString(tables, table), "%s presence", table)
	}
	for table, columns := range map[string][]string{
		"role": {"global_permissions"},
		"project__template_role": {
			"role_id", "allowed_permissions", "denied_permissions", "revision",
		},
	} {
		for _, column := range columns {
			assert.Equalf(
				t,
				expected,
				matrixColumnExists(t, store, table, column),
				"%s.%s presence",
				table,
				column,
			)
		}
	}
}

func assertDeploymentWindowSchema(t testing.TB, store *SqlDb, expected bool) {
	t.Helper()
	tables := matrixUserTables(t, store)
	for _, table := range []string{
		"project__deployment_window_policy", "project__deployment_window_rule", "project__deployment_window_decision",
	} {
		assert.Equalf(t, expected, containsString(tables, table), "%s presence", table)
	}
	for _, column := range []string{
		"decision_key", "source", "origin", "policy_revision", "effective_timezone", "evaluated_at", "matched_rules",
	} {
		assert.Equalf(t, expected, matrixColumnExists(t, store, "project__deployment_window_decision", column), "project__deployment_window_decision.%s presence", column)
	}
}

func matrixColumnExists(t testing.TB, store *SqlDb, table string, column string) bool {
	t.Helper()
	var count int
	switch store.GetDialect() {
	case util.DbDriverSQLite:
		rows, err := store.Sql().Db.QueryContext(
			context.Background(),
			"pragma table_info("+table+")",
		)
		require.NoError(t, err)
		defer rows.Close() //nolint:errcheck
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			require.NoError(t, rows.Scan(
				&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey,
			))
			if name == column {
				return true
			}
		}
		require.NoError(t, rows.Err())
		return false
	case util.DbDriverMySQL:
		require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
			select count(*) from information_schema.columns
			where table_schema=database() and table_name=? and column_name=?`,
			table, column,
		).Scan(&count))
	case util.DbDriverPostgres:
		require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
			select count(*) from information_schema.columns
			where table_schema=current_schema() and table_name=$1 and column_name=$2`,
			table, column,
		).Scan(&count))
	default:
		t.Fatalf("unsupported migration matrix dialect %q", store.GetDialect())
	}
	return count == 1
}

func assertWorkflowParameterColumns(t testing.TB, store *SqlDb, expected bool) {
	t.Helper()
	tables := map[string]string{
		"project__workflow_template": "parameter_definitions",
		"project__workflow_node":     "override_policy",
		"project__workflow_run":      "parameter_snapshot",
		"project__workflow_run_node": "override_snapshot",
	}
	for table, column := range tables {
		found := false
		switch store.GetDialect() {
		case util.DbDriverSQLite:
			rows, err := store.Sql().Db.QueryContext(context.Background(), "pragma table_info("+table+")")
			require.NoError(t, err)
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue sql.NullString
				require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
				if name == column {
					found = true
					assert.Equal(t, 1, notNull)
					assert.Equal(t, "text", normalizeMigrationType(columnType))
				}
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		case util.DbDriverMySQL:
			var count int
			require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
				select count(*) from information_schema.columns
				where table_schema=database() and table_name=? and column_name=?
					and data_type in ('text', 'longtext') and is_nullable='NO'`, table, column).Scan(&count))
			found = count == 1
		case util.DbDriverPostgres:
			var count int
			require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
				select count(*) from information_schema.columns
				where table_schema=current_schema() and table_name=$1 and column_name=$2
					and data_type='text' and is_nullable='NO'`, table, column).Scan(&count))
			found = count == 1
		default:
			t.Fatalf("unsupported migration matrix dialect %q", store.GetDialect())
		}
		assert.Equalf(t, expected, found, "%s.%s presence", table, column)
	}
}

func assertWorkflowTriggerSchema(t testing.TB, store *SqlDb, expected bool) {
	t.Helper()
	tables := matrixUserTables(t, store)
	for _, table := range []string{"project__workflow_trigger", "project__workflow_trigger_invocation"} {
		assert.Equalf(t, expected, containsString(tables, table), "%s presence", table)
	}
	foundColumn := false
	switch store.GetDialect() {
	case util.DbDriverSQLite:
		rows, err := store.Sql().Db.QueryContext(context.Background(), "pragma table_info(project__workflow_run)")
		require.NoError(t, err)
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
			if name == "trigger_snapshot" {
				foundColumn = true
				assert.Equal(t, 1, notNull)
				assert.Equal(t, "text", normalizeMigrationType(columnType))
			}
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	case util.DbDriverMySQL:
		var count int
		require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
			select count(*) from information_schema.columns
			where table_schema=database() and table_name='project__workflow_run'
				and column_name='trigger_snapshot' and data_type in ('text', 'longtext') and is_nullable='NO'`).Scan(&count))
		foundColumn = count == 1
	case util.DbDriverPostgres:
		var count int
		require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
			select count(*) from information_schema.columns
			where table_schema=current_schema() and table_name='project__workflow_run'
				and column_name='trigger_snapshot' and data_type='text' and is_nullable='NO'`).Scan(&count))
		foundColumn = count == 1
	default:
		t.Fatalf("unsupported migration matrix dialect %q", store.GetDialect())
	}
	assert.Equal(t, expected, foundColumn, "project__workflow_run.trigger_snapshot presence")
}

func assertWorkflowProgressionSchema(t testing.TB, store *SqlDb, expected bool) {
	t.Helper()
	tables := matrixUserTables(t, store)
	assert.Equal(t, expected, containsString(tables, "cluster__workflow_reconciliation"), "workflow ownership table presence")
	foundColumn := false
	switch store.GetDialect() {
	case util.DbDriverSQLite:
		rows, err := store.Sql().Db.QueryContext(context.Background(), "pragma table_info(project__workflow_run_node)")
		require.NoError(t, err)
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
			if name == "progression_fencing_token" {
				foundColumn = true
				assert.Equal(t, 1, notNull)
				assert.Equal(t, "integer", normalizeMigrationType(columnType))
			}
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	case util.DbDriverMySQL:
		var count int
		require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
			select count(*) from information_schema.columns
			where table_schema=database() and table_name='project__workflow_run_node'
				and column_name='progression_fencing_token' and data_type='bigint' and is_nullable='NO'`).Scan(&count))
		foundColumn = count == 1
	case util.DbDriverPostgres:
		var count int
		require.NoError(t, store.Sql().Db.QueryRowContext(context.Background(), `
			select count(*) from information_schema.columns
			where table_schema=current_schema() and table_name='project__workflow_run_node'
				and column_name='progression_fencing_token' and data_type='bigint' and is_nullable='NO'`).Scan(&count))
		foundColumn = count == 1
	default:
		t.Fatalf("unsupported migration matrix dialect %q", store.GetDialect())
	}
	assert.Equal(t, expected, foundColumn, "project__workflow_run_node.progression_fencing_token presence")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func migrationMatrixConfigFromEnvironment(t *testing.T) migrationMatrixConfig {
	t.Helper()
	dialect := os.Getenv("SEMAPHORE_MIGRATION_MATRIX_DIALECT")
	if dialect == "" || dialect == util.DbDriverSQLite {
		return migrationMatrixConfig{
			Dialect:  util.DbDriverSQLite,
			Database: filepath.Join(t.TempDir(), "migration-matrix.sqlite"),
		}
	}

	if os.Getenv("SEMAPHORE_MIGRATION_MATRIX") != "1" {
		t.Skip("external migration matrix requires SEMAPHORE_MIGRATION_MATRIX=1")
	}

	config := migrationMatrixConfig{
		Dialect:  dialect,
		Database: os.Getenv("SEMAPHORE_MIGRATION_MATRIX_DB_NAME"),
		Host:     os.Getenv("SEMAPHORE_MIGRATION_MATRIX_DB_HOST"),
		Username: os.Getenv("SEMAPHORE_MIGRATION_MATRIX_DB_USER"),
		Pass:     os.Getenv("SEMAPHORE_MIGRATION_MATRIX_DB_PASS"),
	}
	require.Contains(t, []string{util.DbDriverMySQL, util.DbDriverPostgres}, config.Dialect)
	require.NotEmpty(t, config.Host)
	require.NotEmpty(t, config.Username)
	require.Truef(t, strings.HasSuffix(config.Database, "_matrix"),
		"external migration matrix database %q must end in _matrix", config.Database)
	return config
}

func configureMigrationMatrix(t testing.TB, config migrationMatrixConfig) {
	t.Helper()
	for _, key := range []string{
		"SEMAPHORE_DB_HOST",
		"SEMAPHORE_DB_NAME",
		"SEMAPHORE_DB_USER",
		"SEMAPHORE_DB_PASS",
	} {
		value, present := os.LookupEnv(key)
		t.Cleanup(func() {
			if present {
				require.NoError(t, os.Setenv(key, value))
			} else {
				require.NoError(t, os.Unsetenv(key))
			}
		})
		require.NoError(t, os.Unsetenv(key))
	}

	dbConfig := &util.DbConfig{
		Hostname: config.Host,
		Username: config.Username,
		Password: config.Pass,
		DbName:   config.Database,
	}
	if config.Dialect == util.DbDriverSQLite {
		dbConfig.Hostname = config.Database
	}
	if config.Dialect == util.DbDriverPostgres {
		dbConfig.Options = map[string]string{"sslmode": "disable"}
	}

	util.Config = &util.ConfigType{
		Dialect:  config.Dialect,
		MySQL:    dbConfig,
		Postgres: dbConfig,
		SQLite:   dbConfig,
		Log: &util.ConfigLog{
			Events: &util.EventLogType{},
			Tasks:  &util.TaskLogType{},
		},
		Process: &util.ConfigProcess{},
		Runners: &util.RunnersConfig{},
		Apps: map[string]util.App{
			"ansible": {},
			"bash":    {},
		},
	}
}

func matrixUserTables(t testing.TB, store *SqlDb) []string {
	t.Helper()
	queries := map[string]string{
		util.DbDriverSQLite:   `select name from sqlite_master where type='table' and name not like 'sqlite_%' order by name`,
		util.DbDriverMySQL:    `select table_name from information_schema.tables where table_schema=database() order by table_name`,
		util.DbDriverPostgres: `select table_name from information_schema.tables where table_schema=current_schema() and table_type='BASE TABLE' order by table_name`,
	}
	query, ok := queries[store.GetDialect()]
	require.True(t, ok)
	rows, err := store.Sql().Db.QueryContext(context.Background(), query)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck

	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		tables = append(tables, table)
	}
	require.NoError(t, rows.Err())
	return tables
}

func captureCapabilitySchema(t testing.TB, store *SqlDb) []migrationSemanticColumn {
	t.Helper()
	var columns []migrationSemanticColumn
	switch store.GetDialect() {
	case util.DbDriverSQLite:
		for _, table := range enhancedMigrationTables() {
			rows, err := store.Sql().Db.QueryContext(context.Background(), "pragma table_info("+table+")")
			require.NoError(t, err)
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue sql.NullString
				require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
				columns = append(columns, migrationSemanticColumn{
					Table: table, Name: name, Type: normalizeMigrationType(columnType),
					Nullable: notNull == 0 && primaryKey == 0, PrimaryKey: primaryKey > 0,
				})
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}
	case util.DbDriverMySQL:
		rows, err := store.Sql().Db.QueryContext(context.Background(), `
			select table_name, column_name, data_type, is_nullable, column_key
			from information_schema.columns
			where table_schema=database() and table_name in (
				'capability_config', 'capability_test_record', 'ldap_provider',
				'ldap_provider_selected_user', 'ldap_auth_attempt', 'ldap_capability_transition',
				'project__workflow_artifact')`)
		require.NoError(t, err)
		columns = scanInformationSchema(t, rows)
	case util.DbDriverPostgres:
		rows, err := store.Sql().Db.QueryContext(context.Background(), `
			select c.table_name, c.column_name, c.data_type, c.is_nullable,
				case when exists (
					select 1 from information_schema.table_constraints tc
					join information_schema.key_column_usage kcu
						on tc.constraint_name=kcu.constraint_name and tc.table_schema=kcu.table_schema
					where tc.constraint_type='PRIMARY KEY'
						and tc.table_schema=c.table_schema and tc.table_name=c.table_name
						and kcu.column_name=c.column_name
				) then 'PRI' else '' end
			from information_schema.columns c
			where c.table_schema=current_schema()
				and c.table_name in (
					'capability_config', 'capability_test_record', 'ldap_provider',
					'ldap_provider_selected_user', 'ldap_auth_attempt', 'ldap_capability_transition',
					'project__workflow_artifact')`)
		require.NoError(t, err)
		columns = scanInformationSchema(t, rows)
	default:
		t.Fatalf("unsupported migration matrix dialect %q", store.GetDialect())
	}

	sort.Slice(columns, func(i, j int) bool {
		if columns[i].Table == columns[j].Table {
			return columns[i].Name < columns[j].Name
		}
		return columns[i].Table < columns[j].Table
	})
	return columns
}

func scanInformationSchema(t testing.TB, rows *sql.Rows) []migrationSemanticColumn {
	t.Helper()
	defer rows.Close() //nolint:errcheck
	var columns []migrationSemanticColumn
	for rows.Next() {
		var table, name, columnType, nullable, key string
		require.NoError(t, rows.Scan(&table, &name, &columnType, &nullable, &key))
		columns = append(columns, migrationSemanticColumn{
			Table: table, Name: name, Type: normalizeMigrationType(columnType),
			Nullable: nullable == "YES", PrimaryKey: key == "PRI",
		})
	}
	require.NoError(t, rows.Err())
	return columns
}

func normalizeMigrationType(columnType string) string {
	normalized := strings.ToLower(columnType)
	switch {
	case strings.Contains(normalized, "int"):
		return "integer"
	case strings.Contains(normalized, "char"), strings.Contains(normalized, "text"):
		return "text"
	case strings.Contains(normalized, "date"), strings.Contains(normalized, "time"):
		return "datetime"
	default:
		return normalized
	}
}

func assertCapabilitySchema(t testing.TB, actual []migrationSemanticColumn) {
	t.Helper()
	expected := []migrationSemanticColumn{
		{Table: "capability_config", Name: "capability_id", Type: "text", PrimaryKey: true},
		{Table: "capability_config", Name: "expires_at", Type: "datetime", Nullable: true},
		{Table: "capability_config", Name: "state", Type: "text"},
		{Table: "capability_config", Name: "updated", Type: "datetime"},
		{Table: "capability_test_record", Name: "created", Type: "datetime"},
		{Table: "capability_test_record", Name: "id", Type: "integer", PrimaryKey: true},
		{Table: "capability_test_record", Name: "source", Type: "text"},
		{Table: "capability_test_record", Name: "value", Type: "text"},
		{Table: "ldap_auth_attempt", Name: "blocked_until", Type: "datetime", Nullable: true},
		{Table: "ldap_auth_attempt", Name: "failure_count", Type: "integer"},
		{Table: "ldap_auth_attempt", Name: "provider_id", Type: "text", PrimaryKey: true},
		{Table: "ldap_auth_attempt", Name: "subject_hash", Type: "text", PrimaryKey: true},
		{Table: "ldap_auth_attempt", Name: "updated", Type: "datetime"},
		{Table: "ldap_auth_attempt", Name: "window_started", Type: "datetime"},
		{Table: "ldap_capability_transition", Name: "actor_id", Type: "integer"},
		{Table: "ldap_capability_transition", Name: "created", Type: "datetime"},
		{Table: "ldap_capability_transition", Name: "from_state", Type: "text"},
		{Table: "ldap_capability_transition", Name: "id", Type: "integer", PrimaryKey: true},
		{Table: "ldap_capability_transition", Name: "provider_id", Type: "text"},
		{Table: "ldap_capability_transition", Name: "to_state", Type: "text"},
		{Table: "ldap_provider", Name: "bind_dn", Type: "text"},
		{Table: "ldap_provider", Name: "ca_pem", Type: "text"},
		{Table: "ldap_provider", Name: "config_version", Type: "integer"},
		{Table: "ldap_provider", Name: "created", Type: "datetime"},
		{Table: "ldap_provider", Name: "display_name", Type: "text"},
		{Table: "ldap_provider", Name: "email_attribute", Type: "text"},
		{Table: "ldap_provider", Name: "encrypted_bind_password", Type: "text"},
		{Table: "ldap_provider", Name: "group_filter", Type: "text"},
		{Table: "ldap_provider", Name: "group_identity_attribute", Type: "text"},
		{Table: "ldap_provider", Name: "group_max_depth", Type: "integer"},
		{Table: "ldap_provider", Name: "group_member_attribute", Type: "text"},
		{Table: "ldap_provider", Name: "group_search_base_dn", Type: "text"},
		{Table: "ldap_provider", Name: "group_user_filter", Type: "text"},
		{Table: "ldap_provider", Name: "id", Type: "text", PrimaryKey: true},
		{Table: "ldap_provider", Name: "identity_attribute", Type: "text"},
		{Table: "ldap_provider", Name: "name_attribute", Type: "text"},
		{Table: "ldap_provider", Name: "readiness_checked_at", Type: "datetime", Nullable: true},
		{Table: "ldap_provider", Name: "readiness_code", Type: "text"},
		{Table: "ldap_provider", Name: "readiness_status", Type: "text"},
		{Table: "ldap_provider", Name: "recovery_admin_user_id", Type: "integer", Nullable: true},
		{Table: "ldap_provider", Name: "recovery_checked_at", Type: "datetime", Nullable: true},
		{Table: "ldap_provider", Name: "search_base_dn", Type: "text"},
		{Table: "ldap_provider", Name: "server_url", Type: "text"},
		{Table: "ldap_provider", Name: "state", Type: "text"},
		{Table: "ldap_provider", Name: "tls_mode", Type: "text"},
		{Table: "ldap_provider", Name: "trust_mode", Type: "text"},
		{Table: "ldap_provider", Name: "updated", Type: "datetime"},
		{Table: "ldap_provider", Name: "user_filter", Type: "text"},
		{Table: "ldap_provider", Name: "username_attribute", Type: "text"},
		{Table: "ldap_provider_selected_user", Name: "created", Type: "datetime"},
		{Table: "ldap_provider_selected_user", Name: "provider_id", Type: "text", PrimaryKey: true},
		{Table: "ldap_provider_selected_user", Name: "user_id", Type: "integer", PrimaryKey: true},
		{Table: "project__workflow_artifact", Name: "attempt", Type: "integer"},
		{Table: "project__workflow_artifact", Name: "availability", Type: "text"},
		{Table: "project__workflow_artifact", Name: "diagnostic", Type: "text"},
		{Table: "project__workflow_artifact", Name: "encrypted_value", Type: "text", Nullable: true},
		{Table: "project__workflow_artifact", Name: "id", Type: "integer", PrimaryKey: true},
		{Table: "project__workflow_artifact", Name: "name", Type: "text"},
		{Table: "project__workflow_artifact", Name: "project_id", Type: "integer"},
		{Table: "project__workflow_artifact", Name: "reference_fingerprint", Type: "text"},
		{Table: "project__workflow_artifact", Name: "schema", Type: "text"},
		{Table: "project__workflow_artifact", Name: "sensitive", Type: "integer"},
		{Table: "project__workflow_artifact", Name: "size_bytes", Type: "integer"},
		{Table: "project__workflow_artifact", Name: "task_id", Type: "integer"},
		{Table: "project__workflow_artifact", Name: "value_json", Type: "text", Nullable: true},
		{Table: "project__workflow_artifact", Name: "workflow_node_id", Type: "integer"},
		{Table: "project__workflow_artifact", Name: "workflow_run_id", Type: "integer"},
	}
	assert.Equal(t, expected, actual)
}

func assertCapabilityTablesAbsent(t testing.TB, store *SqlDb) {
	t.Helper()
	for _, table := range enhancedMigrationTables() {
		_, err := store.Sql().Db.ExecContext(context.Background(), fmt.Sprintf("select count(1) from %s", table))
		assert.Error(t, err)
	}
}

func enhancedMigrationTables() []string {
	return []string{
		"capability_config", "capability_test_record", "ldap_provider",
		"ldap_provider_selected_user", "ldap_auth_attempt", "ldap_capability_transition",
		"project__workflow_artifact",
	}
}

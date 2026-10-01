package sql

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteImmediateTransactionConnectionString(t *testing.T) {
	connectionString, err := sqliteImmediateTransactionConnectionString("file:/tmp/task%20status.db?cache=shared&_txlock=deferred")
	require.NoError(t, err)

	connectionURL, err := url.Parse(connectionString)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/task status.db", connectionURL.Path)
	assert.Equal(t, "shared", connectionURL.Query().Get("cache"))
	assert.Equal(t, "immediate", connectionURL.Query().Get("_txlock"))
}

func TestSQLiteTaskLifecycleTransactionAcquiresWriteReservation(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	databasePath := filepath.Join(t.TempDir(), "tasks.db")
	util.Config = &util.ConfigType{
		SQLite: &util.DbConfig{
			Hostname: databasePath,
			Options: map[string]string{
				"_txlock": "deferred",
			},
		},
		Dialect: util.DbDriverSQLite,
	}

	storeConnection, err := connect()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storeConnection.Close()) })
	contenderConnection, err := connect()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, contenderConnection.Close()) })

	for _, connection := range []*sql.DB{storeConnection, contenderConnection} {
		_, err = connection.Exec("pragma journal_mode = WAL")
		require.NoError(t, err)
		_, err = connection.Exec("pragma busy_timeout = 1000")
		require.NoError(t, err)
	}
	_, err = storeConnection.Exec("create table task (id integer primary key, status text); insert into task values (1, 'waiting')")
	require.NoError(t, err)

	tx, err := storeConnection.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	var status string
	require.NoError(t, tx.QueryRow("select status from task where id=1").Scan(&status))

	contender := make(chan error, 1)
	go func() {
		_, updateErr := contenderConnection.Exec("update task set status='contender' where id=1")
		contender <- updateErr
	}()

	select {
	case contenderErr := <-contender:
		require.Failf(t, "contender wrote before lifecycle update committed", "unexpected error: %v", contenderErr)
	case <-time.After(100 * time.Millisecond):
	}

	_, err = tx.Exec("update task set status='starting' where id=1")
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.NoError(t, <-contender)
}

func TestSQLiteUpdateTaskWaitsBeforeReadingConcurrentWriterSnapshot(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	databasePath := filepath.Join(t.TempDir(), "tasks.db")
	util.Config = &util.ConfigType{
		SQLite:  &util.DbConfig{Hostname: databasePath},
		Dialect: util.DbDriverSQLite,
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
	store := CreateDb(util.DbDriverSQLite)
	store.Connect()
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))

	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "task lifecycle", Playbook: "site.yml",
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus, Playbook: "site.yml", Created: time.Now(),
	}, 0)
	require.NoError(t, err)

	contenderConnection, err := connect()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, contenderConnection.Close()) })
	holder, err := contenderConnection.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback() })
	_, err = holder.Exec("update task set message='concurrent writer' where id=?", task.ID)
	require.NoError(t, err)

	updated := task
	updated.Status = task_logger.TaskStartingStatus
	updateResult := make(chan error, 1)
	go func() { updateResult <- store.UpdateTask(updated) }()

	select {
	case updateErr := <-updateResult:
		require.Failf(t, "UpdateTask completed while another writer held the reservation", "unexpected error: %v", updateErr)
	case <-time.After(100 * time.Millisecond):
	}

	require.NoError(t, holder.Commit())
	require.NoError(t, <-updateResult)
	stored, err := store.GetTask(projectID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStartingStatus, stored.Status)
}

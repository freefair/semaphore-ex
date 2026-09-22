package sql

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/require"
)

// External runs explicitly opt into a disposable database; the ordinary suite
// exercises identical admission logic using two independent SQLite connections.
func taskGroupMultiConnectionStores(t *testing.T) (*SqlDb, *SqlDb) {
	t.Helper()
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	driver := os.Getenv("TG_QA_DB_DRIVER")
	if driver == "" {
		driver = util.DbDriverSQLite
	}
	config := &util.ConfigType{Dialect: driver, Log: &util.ConfigLog{Events: &util.EventLogType{}, Tasks: &util.TaskLogType{}}, Process: &util.ConfigProcess{}, Runners: &util.RunnersConfig{}}
	if driver == util.DbDriverSQLite {
		config.SQLite = &util.DbConfig{Hostname: filepath.Join(t.TempDir(), "groups.sqlite")}
	} else {
		require.Contains(t, []string{util.DbDriverPostgres, util.DbDriverMySQL}, driver)
		name := os.Getenv("TG_QA_DATABASE")
		require.True(t, strings.HasPrefix(name, "task_groups_qa_"), "external tests require an explicitly named disposable database")
		connection := &util.DbConfig{Hostname: os.Getenv("TG_QA_DB_HOST"), Username: "semaphore", DbName: name}
		connection.Password = os.Getenv("TG_QA_PASSWORD")
		require.NotEmpty(t, connection.Hostname)
		require.NotEmpty(t, connection.Password)
		if driver == util.DbDriverPostgres {
			connection.Options = map[string]string{"sslmode": "disable"}
			config.Postgres = connection
		} else {
			config.MySQL = connection
		}
	}
	util.Config = config
	first := CreateDb(driver)
	first.Connect()
	t.Cleanup(first.Close)
	require.NoError(t, db.Migrate(first, nil))
	second := CreateDb(driver)
	second.Connect()
	t.Cleanup(second.Close)
	return first, second
}

func TestTaskGroupSQLMultiConnectionAdmission(t *testing.T) {
	first, second := taskGroupMultiConnectionStores(t)
	project, repository := newTemplateTestProject(t, first)
	template, err := first.CreateTemplate(db.Template{ProjectID: project, RepositoryID: repository, Name: "parallel group", Playbook: "probe.sh"})
	require.NoError(t, err)
	group, err := first.CreateTaskGroup(db.TaskGroup{ProjectID: project, Name: "capacity two", MaxParallelTasks: 2})
	require.NoError(t, err)
	var pending []db.Task
	for range 12 {
		pending = append(pending, createGroupedTask(t, first, template, taskGroupKey(group)))
	}
	type outcome struct {
		task    db.Task
		started bool
		err     error
	}
	results := make(chan outcome, len(pending))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index, task := range pending {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			store := first
			if index%2 == 1 {
				store = second
			}
			row, started, claimErr := store.ClaimTaskStart(project, task.ID, 0)
			results <- outcome{row, started, claimErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var admitted []db.Task
	for result := range results {
		if result.started {
			require.NoError(t, result.err)
			admitted = append(admitted, result.task)
		} else {
			require.ErrorAs(t, result.err, new(taskGroupsBusyError))
		}
	}
	require.Len(t, admitted, 2, "capacity must hold across independent SQL connections")
	replacement := createGroupedTask(t, first, template, taskGroupKey(group))
	_, err = first.exec("update task set status=? where id=?", task_logger.TaskStoppingStatus, admitted[0].ID)
	require.NoError(t, err)
	_, started, err := second.ClaimTaskStart(project, replacement.ID, 0)
	require.ErrorAs(t, err, new(taskGroupsBusyError))
	require.False(t, started)
	_, err = first.exec("update task set status=?, `end`=? where id=?", task_logger.TaskStoppedStatus, time.Now(), admitted[0].ID)
	require.NoError(t, err)
	_, started, err = second.ClaimTaskStart(project, replacement.ID, 0)
	require.NoError(t, err)
	require.True(t, started, "terminal evidence releases exactly one slot")
	free := createTaskGroup(t, first, project, "independent")
	blocked := createGroupedTask(t, first, template, taskGroupKey(free), taskGroupKey(group))
	_, started, err = second.ClaimTaskStart(project, blocked.ID, 0)
	require.ErrorAs(t, err, new(taskGroupsBusyError))
	require.False(t, started)
	independent := createGroupedTask(t, first, template, taskGroupKey(free))
	_, started, err = first.ClaimTaskStart(project, independent.ID, 0)
	require.NoError(t, err)
	require.True(t, started, "waiting for another group must not reserve the free group")
}

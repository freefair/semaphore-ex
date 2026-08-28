package tasks

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskSummaryRepositoryStub struct {
	projectID int
	taskID    int
	event     db.TaskSummaryEvent
}

func (*taskSummaryRepositoryStub) CreateAnsibleTaskHost(db.AnsibleTaskHost) error   { return nil }
func (*taskSummaryRepositoryStub) CreateAnsibleTaskError(db.AnsibleTaskError) error { return nil }
func (*taskSummaryRepositoryStub) GetAnsibleTaskHosts(int, int) ([]db.AnsibleTaskHost, error) {
	return nil, nil
}
func (*taskSummaryRepositoryStub) GetAnsibleTaskErrors(int, int) ([]db.AnsibleTaskError, error) {
	return nil, nil
}
func (r *taskSummaryRepositoryStub) IngestTaskSummaryEvent(projectID, taskID int, event db.TaskSummaryEvent, _ time.Time) error {
	r.projectID = projectID
	r.taskID = taskID
	r.event = event
	return nil
}
func (*taskSummaryRepositoryStub) FinalizeTaskSummary(int, int, task_logger.TaskStatus, *time.Time, *time.Time) error {
	return nil
}
func (*taskSummaryRepositoryStub) RepairTaskSummary(int, int, task_logger.TaskStatus) error {
	return nil
}
func (*taskSummaryRepositoryStub) GetTaskSummary(int, int) (db.TaskSummary, error) {
	return db.TaskSummary{}, nil
}
func (*taskSummaryRepositoryStub) GetTaskSummaryHosts(int, int, db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryHost], error) {
	return db.TaskSummaryPage[db.TaskSummaryHost]{}, nil
}
func (*taskSummaryRepositoryStub) GetTaskSummaryStages(int, int, db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryStage], error) {
	return db.TaskSummaryPage[db.TaskSummaryStage]{}, nil
}
func (*taskSummaryRepositoryStub) GetTaskSummaryErrors(int, int, db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryError], error) {
	return db.TaskSummaryPage[db.TaskSummaryError]{}, nil
}

type resultLogWriterStub struct{ result any }

func (*resultLogWriterStub) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (*resultLogWriterStub) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (w *resultLogWriterStub) WriteResult(result any) error {
	w.result = result
	return nil
}

type workflowOutputServiceStub struct {
	task    db.Task
	outputs map[string]json.RawMessage
}

func (*workflowOutputServiceStub) StartWorkflow(db.WorkflowTemplate, *db.User, string, ...db.WorkflowRunInput) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (*workflowOutputServiceStub) ProgressWorkflowRun(int, int, *db.User) error { return nil }
func (*workflowOutputServiceStub) StopWorkflowRun(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (*workflowOutputServiceStub) ResolveWorkflowApproval(int, int, int, int, db.WorkflowApprovalStatus, *db.User) (db.WorkflowApproval, error) {
	return db.WorkflowApproval{}, nil
}
func (s *workflowOutputServiceStub) HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error {
	s.task = task
	s.outputs = outputs
	return nil
}
func (*workflowOutputServiceStub) HandleWorkflowTaskCompletion(db.Task) error { return nil }
func (*workflowOutputServiceStub) GetWorkflowRunArtifacts(int, int, *int) ([]db.WorkflowArtifactMetadata, error) {
	return []db.WorkflowArtifactMetadata{}, nil
}

func TestTaskRunnerExportsParsedAnsibleResultToStructuredLog(t *testing.T) {
	repository := &taskSummaryRepositoryStub{}
	writer := &resultLogWriterStub{}
	pool := &TaskPool{ansibleTaskRepo: repository, logWriteService: writer}
	runner := &TaskRunner{
		pool:     pool,
		Task:     db.Task{ID: 11, ProjectID: 22},
		Template: db.Template{App: db.AppAnsible},
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	runner.LogWithTime(now, `SEMAPHORE_TASK_RESULT {"version":1,"event":"host_summary","event_id":"host-web","host":"web","status":"success","password":"must-not-parse"}`)

	assert.Equal(t, 22, repository.projectID)
	assert.Equal(t, 11, repository.taskID)
	assert.Equal(t, "host-web", repository.event.EventID)
	record, ok := writer.result.(pro_interfaces.ResultLogRecord)
	require.True(t, ok)
	assert.Equal(t, 11, record.TaskID)
	assert.Equal(t, 22, record.ProjectID)
	assert.Equal(t, "host-web", record.CorrelationID)
	assert.Equal(t, string(db.TaskSummaryEventHost), record.EventType)
	assert.Equal(t, repository.event, record.Result)
}

func TestTaskRunnerRoutesWorkflowOutputsWithoutLoggingValues(t *testing.T) {
	repository := &taskSummaryRepositoryStub{}
	writer := &resultLogWriterStub{}
	workflow := &workflowOutputServiceStub{}
	pool := &TaskPool{ansibleTaskRepo: repository, logWriteService: writer, workflowService: workflow}
	runner := &TaskRunner{
		pool: pool, Task: db.Task{ID: 11, ProjectID: 22}, Template: db.Template{App: db.AppAnsible},
	}

	runner.LogWithTime(time.Now(), `SEMAPHORE_TASK_RESULT {"version":1,"event":"workflow_outputs","event_id":"run:outputs","outputs":{"token":"must-stay-private"}}`)

	assert.Equal(t, 11, workflow.task.ID)
	assert.JSONEq(t, `"must-stay-private"`, string(workflow.outputs["token"]))
	assert.Empty(t, repository.event.EventID, "workflow values must not enter task-summary persistence")
	assert.Nil(t, writer.result, "workflow values must not enter the structured result log")
}

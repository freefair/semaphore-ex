package sql

import (
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"time"
)

// Task-summary extensions are supplied by the selected Enhanced module.
// The unwired Community seam fails explicitly rather than accepting writes.
func (d *AnsibleTaskStoreImpl) IngestTaskSummaryEvent(projectID int, taskID int, event db.TaskSummaryEvent, outputTime time.Time) error {
	return fmt.Errorf("task summaries require the Enhanced implementation")
}
func (d *AnsibleTaskStoreImpl) FinalizeTaskSummary(projectID int, taskID int, status task_logger.TaskStatus, started *time.Time, ended *time.Time) error {
	return fmt.Errorf("task summaries require the Enhanced implementation")
}
func (d *AnsibleTaskStoreImpl) RepairTaskSummary(projectID int, taskID int, status task_logger.TaskStatus) error {
	return fmt.Errorf("task summaries require the Enhanced implementation")
}
func (d *AnsibleTaskStoreImpl) GetTaskSummary(projectID int, taskID int) (db.TaskSummary, error) {
	return db.TaskSummary{}, fmt.Errorf("task summaries require the Enhanced implementation")
}
func (d *AnsibleTaskStoreImpl) GetTaskSummaryHosts(projectID int, taskID int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryHost], error) {
	return db.TaskSummaryPage[db.TaskSummaryHost]{}, fmt.Errorf("task summaries require the Enhanced implementation")
}
func (d *AnsibleTaskStoreImpl) GetTaskSummaryStages(projectID int, taskID int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryStage], error) {
	return db.TaskSummaryPage[db.TaskSummaryStage]{}, fmt.Errorf("task summaries require the Enhanced implementation")
}
func (d *AnsibleTaskStoreImpl) GetTaskSummaryErrors(projectID int, taskID int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryError], error) {
	return db.TaskSummaryPage[db.TaskSummaryError]{}, fmt.Errorf("task summaries require the Enhanced implementation")
}

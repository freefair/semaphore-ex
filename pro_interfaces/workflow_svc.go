package pro_interfaces

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
)

// WorkflowService orchestrates workflow runs: starting a run, progressing it as
// upstream tasks finish, resolving approvals and merging run artifacts. It is a
// Pro feature — the open-source build wires a no-op stub
// (pro/services/server/workflow_svc.go); the licensed build provides the real
// implementation (pro_impl/services/server/workflow_svc.go).
type WorkflowService interface {
	StartWorkflow(workflow db.WorkflowTemplate, user *db.User, correlationID string, input ...db.WorkflowRunInput) (db.WorkflowRun, error)
	ProgressWorkflowRun(projectID int, runID int, user *db.User) error
	// StopWorkflowRun stops a non-finished run: it signals every in-flight task
	// of the run to stop and marks the run as stopped (terminal).
	StopWorkflowRun(projectID int, runID int, user *db.User) (db.WorkflowRun, error)
	RequestWorkflowRunStop(projectID int, runID int, user *db.User) (db.WorkflowRun, error)
	ReconcileWorkflowRun(projectID int, runID int) (db.WorkflowRun, error)
	RetryWorkflowRunReconciliation(projectID int, runID int, user *db.User) (db.WorkflowRun, error)
	GetWorkflowApprovalInbox(projectID int, user *db.User) ([]db.WorkflowApproval, error)
	ResolveWorkflowApproval(projectID int, workflowID int, runID int, nodeID int, decision db.WorkflowApprovalDecision, user *db.User) (db.WorkflowApproval, error)
	HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error
	HandleWorkflowTaskCompletion(task db.Task) error
	GetWorkflowRunArtifacts(projectID int, runID int, currentTaskID *int) ([]db.WorkflowArtifactMetadata, error)
}

// WorkflowTaskEnqueuer is the slice of the task pool the workflow service needs
// to launch a node's task. It is implemented by *services/tasks.TaskPool.
// Declaring it here keeps the pro modules dependent only on pro_interfaces + db,
// avoiding an import of (and a cycle with) the services/tasks package.
type WorkflowTaskEnqueuer interface {
	AddTask(task db.Task, userID *int, username string, projectID int, needAlias bool) (db.Task, error)
	AddWorkflowTask(task db.Task, template db.Template, userID *int, username string, projectID int, needAlias bool) (db.Task, error)
	// StopTasksByWorkflowRun stops every active (queued or running) task that
	// belongs to the given workflow run. forceStop kills running tasks
	// immediately instead of letting them stop gracefully.
	StopTasksByWorkflowRun(projectID int, runID int, forceStop bool)
}

// WorkflowRunLocker provides cluster-wide mutual exclusion for workflow run
// progression. In HA mode every progression trigger (task completion, API
// poll, reconciler tick) may fire on any node; the locker ensures only one
// node progresses a given run at a time. The lock is an optimization, not a
// correctness guarantee — conditional DB updates remain the source of truth.
type WorkflowRunLocker interface {
	// TryLockRun acquires durable progression ownership for a run. Returns
	// ok=false when another holder owns it; release must be called when ok.
	TryLockRun(projectID int, runID int) (lease WorkflowReconciliationLease, release func(), ok bool, err error)
	// TryLockStart acquires a short-lived lock serializing run creation per
	// workflow template (run version minting). Returns ok=false on contention.
	TryLockStart(projectID int, templateID int) (release func(), ok bool)
	RecordReconciled(lease WorkflowReconciliationLease) error
	Drain() error
	Resume()
}

// WorkflowReconciler periodically progresses every non-terminal workflow run
// so approval timeouts fire and run statuses converge without requiring an
// open browser or a task completion (and after a node crash mid-progression).
type WorkflowReconciler interface {
	Start()
	Stop()
}

package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/pkg/git"
	"github.com/semaphoreui/semaphore/pkg/tz"

	"github.com/go-gorp/gorp/v3"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
)

type DefaultTaskParams struct {
}

type TerraformTaskParams struct {
	Plan        bool `json:"plan"`
	Destroy     bool `json:"destroy"`
	AutoApprove bool `json:"auto_approve"`
	Upgrade     bool `json:"upgrade"`
	Reconfigure bool `json:"reconfigure"`
}

type AnsibleTaskParams struct {
	Debug             bool     `json:"debug"`
	DebugLevel        int      `json:"debug_level"`
	DryRun            bool     `json:"dry_run"`
	Diff              bool     `json:"diff"`
	Limit             []string `json:"limit"`
	Tags              []string `json:"tags"`
	SkipTags          []string `json:"skip_tags"`
	SkipGalaxyInstall bool     `json:"skip_galaxy_install"`
}

// Task is a model of a task which will be executed by the runner
type Task struct {
	ID         int `db:"id" json:"id"`
	TemplateID int `db:"template_id" json:"template_id" binding:"required"`
	ProjectID  int `db:"project_id" json:"project_id"`

	Status task_logger.TaskStatus `db:"status" json:"status"`

	// override variables
	Playbook    string  `db:"playbook" json:"playbook"`
	Environment string  `db:"environment" json:"environment,omitempty"`
	Secret      string  `db:"-" json:"secret,omitempty"`
	Arguments   *string `db:"arguments" json:"arguments,omitempty"`
	GitBranch   *string `db:"git_branch" json:"git_branch,omitempty"`

	UserID        *int `db:"user_id" json:"user_id,omitempty"`
	IntegrationID *int `db:"integration_id" json:"integration_id,omitempty"`
	ScheduleID    *int `db:"schedule_id" json:"schedule_id,omitempty"`
	// ScheduleOccurrenceKey binds an HA schedule task to exactly one intended
	// fire. It is internal because operators inspect occurrence history through
	// the coordinator rather than task API payloads.
	ScheduleOccurrenceKey *string `db:"schedule_occurrence_key" json:"-"`
	// RunnerID is set while a task is assigned to a remote runner (cleared when the task finishes).
	// Used so runner progress API can authorize updates on any HA node.
	RunnerID             *int    `db:"runner_id" json:"-"`
	RunnerSnapshotID     *int    `db:"runner_id_snapshot" json:"-"`
	RunnerName           *string `db:"runner_name" json:"-"`
	AssignmentGeneration int     `db:"assignment_generation" json:"assignment_generation,omitempty"`
	// TaskControlFencingToken is internal to HA recovery. Every recovery-only
	// task mutation must match the token installed atomically by the current
	// task-control owner.
	TaskControlFencingToken int64                    `db:"task_control_fencing_token" json:"-"`
	RunnerAssignedAt        *time.Time               `db:"runner_assigned_at" json:"runner_assigned_at,omitempty"`
	RecoveryReason          string                   `db:"recovery_reason" json:"recovery_reason,omitempty"`
	PlacementDecision       *RunnerPlacementDecision `db:"placement_decision" json:"placement_decision,omitempty"`
	RequestedExecutorImage  *string                  `db:"requested_executor_image" json:"requested_executor_image,omitempty"`
	ResolvedExecutorImage   *string                  `db:"resolved_executor_image" json:"resolved_executor_image,omitempty"`

	Created time.Time  `db:"created" json:"created"`
	Start   *time.Time `db:"start" json:"start,omitempty"`
	End     *time.Time `db:"end" json:"end,omitempty"`

	Message string `db:"message" json:"message,omitempty"`

	// CommitHash is a git commit hash of playbook repository which
	// was active when task was created.
	CommitHash *string `db:"commit_hash" json:"commit_hash,omitempty"`
	// CommitMessage contains message retrieved from git repository after checkout to CommitHash.
	// It is readonly by API.
	CommitMessage  string `db:"commit_message" json:"commit_message,omitempty"`
	BuildTaskID    *int   `db:"build_task_id" json:"build_task_id,omitempty"`
	WorkflowRunID  *int   `db:"workflow_run_id" json:"workflow_run_id,omitempty"`
	WorkflowNodeID *int   `db:"workflow_node_id" json:"workflow_node_id,omitempty"`
	// WorkflowTemplateSnapshot freezes the referenced template for workflow
	// tasks so a later template edit cannot change queued or restarted work.
	WorkflowTemplateSnapshot *string `db:"workflow_template_snapshot" json:"-"`
	// Version is a build version.
	// This field available only for Build tasks.
	Version *string `db:"version" json:"version,omitempty"`

	InventoryID *int `db:"inventory_id" json:"inventory_id,omitempty"`

	Params MapStringAnyField `db:"params" json:"params,omitempty"`

	Artifacts *string `db:"artifacts" json:"artifacts,omitempty"`

	// Limit is deprecated, use Params.Limit instead
	Limit string `db:"-" json:"limit"`
}

type RunnerAttemptOutcome string

const (
	RunnerAttemptActive    RunnerAttemptOutcome = "active"
	RunnerAttemptRequeued  RunnerAttemptOutcome = "requeued"
	RunnerAttemptSucceeded RunnerAttemptOutcome = "succeeded"
	RunnerAttemptFailed    RunnerAttemptOutcome = "failed"
	RunnerAttemptStopped   RunnerAttemptOutcome = "stopped"
)

// RunnerAttempt records one immutable runner assignment and its terminal result.
type RunnerAttempt struct {
	ID                     int                  `db:"id" json:"id"`
	ProjectID              int                  `db:"project_id" json:"project_id"`
	TaskID                 int                  `db:"task_id" json:"task_id"`
	Generation             int                  `db:"generation" json:"generation"`
	RunnerID               int                  `db:"runner_id" json:"runner_id"`
	RunnerName             string               `db:"runner_name" json:"runner_name"`
	AssignedAt             time.Time            `db:"assigned_at" json:"assigned_at"`
	EndedAt                *time.Time           `db:"ended_at" json:"ended_at,omitempty"`
	Outcome                RunnerAttemptOutcome `db:"outcome" json:"outcome"`
	Reason                 string               `db:"reason" json:"reason,omitempty"`
	RequestedTags          StringArrayField     `db:"requested_tags" json:"requested_tags,omitempty"`
	MatchMode              RunnerTagMatchMode   `db:"match_mode" json:"match_mode,omitempty"`
	PlacementReason        string               `db:"placement_reason" json:"placement_reason,omitempty"`
	RequestedExecutorImage *string              `db:"requested_executor_image" json:"requested_executor_image,omitempty"`
	ResolvedExecutorImage  *string              `db:"resolved_executor_image" json:"resolved_executor_image,omitempty"`
	ExecutorType           RunnerExecutorType   `db:"executor_type" json:"executor_type,omitempty"`
	ContainerID            string               `db:"container_id" json:"container_id,omitempty"`
	ContainerName          string               `db:"container_name" json:"container_name,omitempty"`
}

// RunnerExecutorMetadata is the bounded runtime identity a runner may attach
// to its current assignment. Task arguments, environment, labels, mounts, and
// daemon details intentionally never cross this API boundary.
type RunnerExecutorMetadata struct {
	ExecutorType  RunnerExecutorType `json:"executor_type"`
	ContainerID   string             `json:"container_id,omitempty"`
	ContainerName string             `json:"container_name,omitempty"`
}

const MaxRunnerContainerIdentityLength = 128

// Validate checks the small runner-owned identity contract before metadata is
// persisted or shown to project users.
func (m RunnerExecutorMetadata) Validate(expected RunnerExecutorType) error {
	executorType, err := NormalizeRunnerExecutorType(m.ExecutorType)
	if err != nil {
		return err
	}
	if executorType != expected {
		return fmt.Errorf("executor metadata type %q does not match runner type %q", executorType, expected)
	}
	if executorType != RunnerExecutorDocker {
		if m.ContainerID != "" || m.ContainerName != "" {
			return errors.New("container identity is only valid for Docker executor metadata")
		}
		return nil
	}
	if m.ContainerName == "" {
		return errors.New("Docker executor metadata requires a container name")
	}
	for field, value := range map[string]string{"container ID": m.ContainerID, "container name": m.ContainerName} {
		if len(value) > MaxRunnerContainerIdentityLength {
			return fmt.Errorf("%s must contain at most %d bytes", field, MaxRunnerContainerIdentityLength)
		}
		for _, character := range value {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
				continue
			}
			return fmt.Errorf("%s contains an unsupported character", field)
		}
	}
	return nil
}

// TaskExecutionEvidenceState is a value-free observation from a complete
// runner snapshot. Unknown must never be treated as evidence of absence.
type TaskExecutionEvidenceState string

const (
	TaskExecutionEvidenceRunning  TaskExecutionEvidenceState = "running"
	TaskExecutionEvidenceTerminal TaskExecutionEvidenceState = "terminal"
	TaskExecutionEvidenceAbsent   TaskExecutionEvidenceState = "absent"
	TaskExecutionEvidenceUnknown  TaskExecutionEvidenceState = "unknown"
)

// TaskExecutionEvidence identifies one runner execution generation without
// carrying task output, secrets, or executor metadata.
type TaskExecutionEvidence struct {
	TaskID     int                        `json:"task_id"`
	Generation int                        `json:"generation"`
	State      TaskExecutionEvidenceState `json:"state"`
	Status     task_logger.TaskStatus     `json:"status,omitempty"`
}

// TaskExecutionEvidenceRecorder is an optional store capability. Community
// stores need not implement it; HA code uses it only when available.
type TaskExecutionEvidenceRecorder interface {
	RecordTaskExecutionSnapshot(runnerID int, evidence []TaskExecutionEvidence) error
}

func (task *Task) ExtractParams(target any) (err error) {
	content, err := json.Marshal(task.Params)
	if err != nil {
		return
	}
	err = json.Unmarshal(content, target)
	return
}

// PreInsert is a hook which is called before inserting task into database.
// Called directly in BoltDB implementation.
func (task *Task) PreInsert(gorp.SqlExecutor) error {
	task.Created = tz.In(task.Created)

	if _, ok := task.Params["limit"]; !ok {
		if task.Params == nil {
			task.Params = make(MapStringAnyField)
		}

		if task.Limit != "" {
			limits := strings.Split(task.Limit, ",")

			for i := range limits {
				limits[i] = strings.TrimSpace(limits[i])
			}

			task.Params["limit"] = limits
		}
	}

	return nil
}

func (task *Task) PreUpdate(gorp.SqlExecutor) error {
	if task.Start != nil {
		start := tz.In(*task.Start)
		task.Start = &start
	}

	if task.End != nil {
		end := tz.In(*task.End)
		task.End = &end
	}
	return nil
}

func (task *Task) GetIncomingVersion(d Store) *string {
	if task.BuildTaskID == nil {
		return nil
	}

	buildTask, err := d.GetTask(task.ProjectID, *task.BuildTaskID)

	if err != nil {
		return nil
	}

	tpl, err := d.GetTemplate(task.ProjectID, buildTask.TemplateID)
	if err != nil {
		return nil
	}

	if tpl.Type == TemplateBuild {
		return buildTask.Version
	}

	return buildTask.GetIncomingVersion(d)
}

func (task *Task) GetUrl() *string {
	if util.Config.WebHost != "" {
		taskUrl := fmt.Sprintf("%s/project/%d/history?t=%d", util.Config.WebHost, task.ProjectID, task.ID)
		return &taskUrl
	}

	return nil
}

func (task *Task) ValidateNewTask(template Template) error {
	if task.GitBranch != nil {
		if err := git.ValidateGitBranch(*task.GitBranch, "task"); err != nil {
			return err
		}
	}

	if task.CommitHash != nil {
		if err := git.ValidateCommitHash(*task.CommitHash, "task"); err != nil {
			return err
		}
	}

	if err := ValidatePlaybookPath(task.Playbook, "task"); err != nil {
		return err
	}

	var params any
	switch template.App {
	case AppAnsible:
		params = &AnsibleTaskParams{}
	case AppTerraform, AppTofu, AppTerragrunt:
		params = &TerraformTaskParams{}
	default:
		params = &DefaultTaskParams{}
	}

	return task.ExtractParams(params)
}

func (task *TaskWithTpl) Fill(d Store) error {
	if task.BuildTaskID != nil {
		build, err := d.GetTask(task.ProjectID, *task.BuildTaskID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		task.BuildTask = &build
	}
	return nil
}

// TaskWithTpl is the task data with additional fields
type TaskWithTpl struct {
	Task
	TemplatePlaybook string       `db:"tpl_playbook" json:"tpl_playbook"`
	TemplateAlias    string       `db:"tpl_alias" json:"tpl_alias"`
	TemplateType     TemplateType `db:"tpl_type" json:"tpl_type,omitempty"`
	TemplateApp      TemplateApp  `db:"tpl_app" json:"tpl_app,omitempty"`
	UserName         *string      `db:"user_name" json:"user_name,omitempty"`
	// UsedRunnerID exposes Task.RunnerID through the API. Task.RunnerID itself
	// stays unexported (json:"-"); we re-select it under a distinct column so the
	// embedded struct's mapping is not duplicated.
	UsedRunnerID   *int    `db:"used_runner_id" json:"used_runner_id,omitempty"`
	UsedRunnerName *string `db:"used_runner_name" json:"used_runner_name,omitempty"`
	BuildTask      *Task   `db:"-" json:"build_task,omitempty"`
}

// TaskOutput is the ansible log output from the task
type TaskOutput struct {
	ID      int       `db:"id" json:"id"`
	TaskID  int       `db:"task_id" json:"task_id"`
	Time    time.Time `db:"time" json:"time"`
	Output  string    `db:"output" json:"output"`
	StageID *int      `db:"stage_id" json:"stage_id"`
}

type TaskStageType string

const (
	TaskStageInit          TaskStageType = "init"
	TaskStageTerraformPlan TaskStageType = "terraform_plan"
	TaskStageRunning       TaskStageType = "running"
	TaskStagePrintResult   TaskStageType = "print_result"
)

type TaskStage struct {
	ID     int           `db:"id" json:"id"`
	TaskID int           `db:"task_id" json:"task_id"`
	Start  *time.Time    `db:"start" json:"start"`
	End    *time.Time    `db:"end" json:"end"`
	Type   TaskStageType `db:"type" json:"type"`
}

type TaskStageWithResult struct {
	ID            int           `db:"id" json:"id"`
	TaskID        int           `db:"task_id" json:"task_id"`
	Start         *time.Time    `db:"start" json:"start"`
	End           *time.Time    `db:"end" json:"end"`
	StartOutputID *int          `db:"start_output_id" json:"start_output_id"`
	EndOutputID   *int          `db:"end_output_id" json:"end_output_id"`
	Type          TaskStageType `db:"type" json:"type"`
	JSON          string        `db:"json" json:"-"`
	Result        any           `db:"-" json:"result"`
}

type TaskStageResult struct {
	ID      int    `db:"id" json:"id"`
	TaskID  int    `db:"task_id" json:"task_id"`
	StageID int    `db:"stage_id" json:"stage_id"`
	JSON    string `db:"json" json:"json"`
}

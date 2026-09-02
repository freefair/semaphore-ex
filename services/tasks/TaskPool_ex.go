package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

// SetExecutionPreflightReviewTokenIssuer replaces the review-token issuer.
// Production pools derive it from the shared cookie signing key; tests may
// inject a deterministic issuer without changing process-wide configuration.
func (p *TaskPool) SetExecutionPreflightReviewTokenIssuer(issuer *ExecutionPreflightReviewTokenIssuer) {
	p.executionPreflightIssuer = issuer
}

// SetGlobalCredentialRuntimeResolver installs the edition-provided runtime
// authority. Community installs a fail-closed stub; a nil resolver is also
// fail-closed whenever a task has persisted global credential bindings.
func (p *TaskPool) SetGlobalCredentialRuntimeResolver(resolver pro_interfaces.GlobalCredentialRuntimeResolver) {
	p.globalCredentialResolver = resolver
}

// ConfigureCrossProjectWorkflowTaskStore attaches the Enhanced transactional
// dispatch fence. It is configured during service wiring before the pool is
// used and is intentionally absent from the Community db.Store interface.
func (p *TaskPool) ConfigureCrossProjectWorkflowTaskStore(store pro_interfaces.CrossProjectWorkflowTaskStore) {
	p.crossProjectTaskStore = store
}

// SetExecutorImageCapabilityResolver injects the replaceable-edition entitlement decision.
func (p *TaskPool) SetExecutorImageCapabilityResolver(resolver func(*db.User) bool) {
	p.executorImageAvailable = resolver
}

// SetTaskControlLifecycle installs the optional Enhanced HA ownership port.
// Community and single-node builds leave it nil.
func (p *TaskPool) SetTaskControlLifecycle(lifecycle TaskControlLifecycle) {
	p.taskControlLifecycle = lifecycle
}

// GetOwnedRunningTasks returns only work whose task-state claim belongs to
// this process. Enhanced HA uses it to backfill durable task controls during
// rolling upgrades without claiming another node's shared running work.
func (p *TaskPool) GetOwnedRunningTasks() []*TaskRunner {
	return p.state.OwnedRunningRange()
}

func (p *TaskPool) writeStructuredDebug(record pro_interfaces.DebugLogRecord) {
	debugWriter, ok := p.logWriteService.(pro_interfaces.DebugLogService)
	if !ok {
		return
	}
	if err := debugWriter.WriteDebug(record); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"context": record.Component, "event_type": record.EventType,
		}).Warn("failed to enqueue structured debug log")
	}
}

// discardSupersededTask removes a stale dispatcher's local bookkeeping after
// another server won the SQL start claim. It deliberately does not release the
// task-control lifecycle: that lifecycle belongs to the winning assignment.
func (p *TaskPool) discardSupersededTask(t *TaskRunner) {
	p.state.RemoveActive(t.Task.ProjectID, t.Task.ID)
	p.state.DeleteRunning(t.Task.ID)
	p.state.DeleteClaim(t.Task.ID)
	if t.Alias != "" {
		p.state.DeleteAlias(t.Alias)
	}
}

// AddWorkflowTask creates a normal task while freezing the template selected
// by the workflow run snapshot. The rest of task validation, persistence,
// placement, queueing, logging, and completion remains the standard TaskPool
// path.
func (p *TaskPool) AddWorkflowTask(
	taskObj db.Task,
	template db.Template,
	userID *int,
	username string,
	projectID int,
	needAlias bool,
) (newTask db.Task, err error) {
	if template.ID <= 0 || template.ID != taskObj.TemplateID || template.ProjectID != projectID {
		return db.Task{}, fmt.Errorf("workflow task template snapshot does not match the task")
	}
	snapshot, err := json.Marshal(template)
	if err != nil {
		return db.Task{}, fmt.Errorf("encode workflow task template snapshot: %w", err)
	}
	encoded := string(snapshot)
	taskObj.WorkflowTemplateSnapshot = &encoded
	return p.addTask(taskObj, &template, userID, username, projectID, needAlias, nil, nil)
}

func (p *TaskPool) AddWorkflowTaskFenced(
	taskObj db.Task,
	template db.Template,
	userID *int,
	username string,
	projectID int,
	needAlias bool,
	lease pro_interfaces.WorkflowReconciliationLease,
) (newTask db.Task, err error) {
	if template.ID <= 0 || template.ID != taskObj.TemplateID || template.ProjectID != projectID {
		return db.Task{}, fmt.Errorf("workflow task template snapshot does not match the task")
	}
	snapshot, err := json.Marshal(template)
	if err != nil {
		return db.Task{}, fmt.Errorf("encode workflow task template snapshot: %w", err)
	}
	encoded := string(snapshot)
	taskObj.WorkflowTemplateSnapshot = &encoded
	return p.addTask(taskObj, &template, userID, username, projectID, needAlias, &lease, nil)
}

// AddCrossProjectWorkflowTaskFenced persists a consumer-scoped workflow task
// only through the Enhanced grant-aware SQL fence. It reconstructs the owner
// template from the immutable version snapshot and never falls back to the
// normal local-template path.
func (p *TaskPool) AddCrossProjectWorkflowTaskFenced(
	taskObj db.Task,
	provenance db.CrossProjectTemplateProvenance,
	userID *int,
	username string,
	consumerProjectID int,
	lease *pro_interfaces.WorkflowReconciliationLease,
) (db.Task, error) {
	if p.crossProjectTaskStore == nil {
		return db.Task{}, errors.New("cross-project workflow task fencing is unavailable")
	}
	if consumerProjectID <= 0 || taskObj.WorkflowRunID == nil || taskObj.WorkflowNodeID == nil ||
		(taskObj.ProjectID != 0 && taskObj.ProjectID != consumerProjectID) || provenance.Validate() != nil ||
		taskObj.TemplateID != provenance.Reference.TemplateID {
		return db.Task{}, errors.New("cross-project workflow task provenance is invalid")
	}
	template, err := provenance.TemplateSnapshot.ReconstructTemplate(
		provenance.Reference.OwnerProjectID, provenance.Reference.TemplateID,
	)
	if err != nil {
		return db.Task{}, fmt.Errorf("reconstruct cross-project workflow template: %w", err)
	}
	encodedTemplate, err := json.Marshal(template)
	if err != nil {
		return db.Task{}, fmt.Errorf("encode cross-project workflow template snapshot: %w", err)
	}
	provenanceCopy := provenance
	taskProvenance := db.WorkflowTemplateProvenance{CrossProject: &provenanceCopy}
	encodedProvenance, err := taskProvenance.CanonicalJSON()
	if err != nil {
		return db.Task{}, fmt.Errorf("encode cross-project workflow task provenance: %w", err)
	}
	taskObj.TemplateID = template.ID
	taskObj.WorkflowTemplateSnapshot = stringPointer(string(encodedTemplate))
	taskObj.WorkflowTemplateProvenance = &taskProvenance
	taskObj.WorkflowTemplateProvenanceJSON = stringPointer(encodedProvenance)
	return p.addTask(
		taskObj, &template, userID, username, consumerProjectID,
		template.App.NeedTaskAlias(), lease, &provenanceCopy,
	)
}

func stringPointer(value string) *string {
	return &value
}

func (p *TaskPool) addTask(
	taskObj db.Task,
	templateSnapshot *db.Template,
	userID *int,
	username string,
	projectID int,
	needAlias bool,
	workflowLease *pro_interfaces.WorkflowReconciliationLease,
	crossProjectProvenance *db.CrossProjectTemplateProvenance,
) (newTask db.Task, err error) {
	taskObj.Created = tz.Now()
	taskObj.Status = task_logger.TaskWaitingStatus
	taskObj.UserID = userID
	taskObj.ProjectID = projectID
	extraSecretVars := taskObj.Secret
	taskObj.Secret = ""

	var tpl db.Template
	if templateSnapshot == nil {
		tpl, err = p.store.GetTemplate(projectID, taskObj.TemplateID)
		if err != nil {
			return
		}
	} else {
		tpl = *templateSnapshot
	}

	requestedImage, err := tpl.ResolveExecutorImage()
	if err != nil {
		return newTask, err
	}
	if requestedImage != nil {
		var user *db.User
		if userID != nil {
			loadedUser, userErr := p.store.GetUser(*userID)
			if userErr != nil {
				return newTask, userErr
			}
			user = &loadedUser
		}
		if p.executorImageAvailable == nil || !p.executorImageAvailable(user) {
			return newTask, db.ErrExecutorImageCapabilityUnavailable
		}
		if err = p.validateExecutorImageCompatibility(taskObj, tpl); err != nil {
			return newTask, err
		}
	}

	err = taskObj.ValidateNewTask(tpl)
	if err != nil {
		return
	}
	taskObj.RequestedExecutorImage = requestedImage
	taskObj.ResolvedExecutorImage = taskObj.RequestedExecutorImage

	// A task-supplied commit hash redirects the checkout away from the
	// template's pinned branch, so it is honored only when the template allows
	// branch overrides — the same gate used for task.GitBranch (see
	// resolveGitBranch). Otherwise a user who may only run tasks could pin a
	// branch-locked template to an arbitrary commit/ref.
	if !tpl.AllowOverrideBranchInTask {
		taskObj.CommitHash = nil
	}

	if tpl.Type == db.TemplateBuild { // get next version for TaskRunner if it is a Build
		if crossProjectProvenance != nil {
			// A consumer-owned external task must not derive its build identity
			// from mutable owner task history. The exact published build-template
			// dependency remains in immutable provenance instead.
			taskObj.Version = tpl.StartVersion
		} else {
			var builds []db.TaskWithTpl
			builds, err = p.store.GetTemplateTasks(tpl.ProjectID, tpl.ID, db.RetrieveQueryParams{Count: 1})
			if err != nil {
				return
			}
			if len(builds) == 0 || builds[0].Version == nil {
				taskObj.Version = tpl.StartVersion
			} else {
				taskObj.Version = new(db.GetNextBuildVersion(*tpl.StartVersion, *builds[0].Version))
			}
		}
	}

	if crossProjectProvenance != nil {
		if workflowLease != nil && (workflowLease.ProjectID != taskObj.ProjectID || workflowLease.WorkflowRunID != *taskObj.WorkflowRunID) {
			return db.Task{}, errors.New("cross-project workflow reconciliation ownership is invalid")
		}
		newTask, err = p.crossProjectTaskStore.CreateCrossProjectWorkflowTaskFenced(taskObj, *crossProjectProvenance, workflowLease)
	} else if workflowLease != nil {
		creator, ok := p.store.(interface {
			CreateWorkflowTaskFenced(db.Task, int, pro_interfaces.WorkflowReconciliationLease) (db.Task, error)
		})
		if !ok {
			return db.Task{}, errors.New("workflow task fencing is unavailable")
		}
		newTask, err = creator.CreateWorkflowTaskFenced(taskObj, util.Config.MaxTasksPerTemplate, *workflowLease)
	} else {
		newTask, err = p.store.CreateTask(taskObj, util.Config.MaxTasksPerTemplate)
	}
	if err != nil {
		return
	}

	taskRunner := NewTaskRunner(newTask, p, username, p.keyInstallationService)

	if needAlias {
		// A unique, randomly-generated identifier that persists throughout the task's lifecycle.
		taskRunner.Alias = random.String(32)
	}

	err = taskRunner.populateDetails()
	if err != nil {
		taskRunner.Log("Error: " + err.Error())
		taskRunner.SetStatus(task_logger.TaskFailStatus)
		return
	}

	// Persist survey secret variables as a task-bound, expiring access key so
	// any HA node can decrypt them at dispatch time (local or remote runner).
	// The plaintext never reaches the task row, API payloads, or events.
	if hasSurveySecrets(extraSecretVars) {
		err = p.encryptionService.CreateTaskSurveySecrets(
			projectID, newTask.ID, extraSecretVars, taskSecretExpireAt())
		if err != nil {
			taskRunner.Log("Error: failed to store survey secrets: " + err.Error())
			taskRunner.SetStatus(task_logger.TaskFailStatus)
			return
		}
	}

	var job Job

	if util.Config.IsUseRemoteRunner() ||
		len(taskRunner.Template.EffectiveRunnerTags()) > 0 ||
		taskRunner.Inventory.RunnerTag != nil ||
		taskRunner.Task.ResolvedExecutorImage != nil {

		tags, matchMode, legacyTag := runnerPlacementPolicy(taskRunner.Template, taskRunner.Inventory)

		job = &RemoteJob{
			RunnerTag:          legacyTag,
			RunnerTags:         tags,
			RunnerTagMatchMode: matchMode,
			ExecutorImage:      taskRunner.Task.ResolvedExecutorImage,
			Task:               taskRunner.Task,
			taskPool:           p,
		}
	} else {
		app := db_lib.CreateApp(
			taskRunner.Template,
			taskRunner.Repository,
			taskRunner.Inventory,
			taskRunner)

		job = &LocalExecutor{
			Task:         taskRunner.Task,
			Template:     taskRunner.Template,
			Inventory:    taskRunner.Inventory,
			Repository:   taskRunner.Repository,
			Environment:  taskRunner.Environment,
			Secret:       extraSecretVars,
			Logger:       app.SetLogger(taskRunner),
			App:          app,
			KeyInstaller: p.keyInstallationService,
			RepoLock:     p.repoLock,
		}
	}

	taskRunner.job = job

	p.register <- taskRunner

	taskRunner.createTaskEvent()

	return
}

package tasks

import (
	"fmt"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type autorunDeploymentWindowAdmissionCapture struct {
	store           *sql.SqlDb
	buildTaskID     int
	childTemplateID int
	requests        []pro_interfaces.DeploymentWindowAdmissionRequest
	record          db.DeploymentWindowDecisionRecord
}

func (s *autorunDeploymentWindowAdmissionCapture) Claim(request pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	s.requests = append(s.requests, request)
	if s.record.ID != 0 {
		tasks, err := s.store.GetProjectTasks(request.ProjectID, db.RetrieveQueryParams{})
		if err != nil {
			return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
		}
		for _, existing := range tasks {
			if existing.TemplateID == s.childTemplateID && existing.BuildTaskID != nil && *existing.BuildTaskID == s.buildTaskID {
				id := existing.ID
				s.record.TaskID = &id
				break
			}
		}
		return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: s.record}, nil
	}

	now := time.Now().UTC()
	result, err := s.store.GetConnection().Exec(
		"insert into project__deployment_window_decision(project_id, decision_key, source, origin, template_id, policy_revision, effective_timezone, evaluated_at, state, reason, next_eligible_known, matched_rules, created) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		request.ProjectID, request.DecisionKey, request.Source, request.Origin, request.TemplateID,
		1, "UTC", now, pro_interfaces.DeploymentWindowDecisionAllowed, pro_interfaces.DeploymentWindowReasonDefaultAllow, true, "[]", now,
	)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	s.record = db.DeploymentWindowDecisionRecord{
		ID: int(id), ProjectID: request.ProjectID, DecisionKey: request.DecisionKey, Source: string(request.Source), Origin: string(request.Origin),
		TemplateID: request.TemplateID, PolicyRevision: 1, EffectiveTimezone: "UTC", EvaluatedAt: now,
		State: string(pro_interfaces.DeploymentWindowDecisionAllowed), Reason: string(pro_interfaces.DeploymentWindowReasonDefaultAllow),
		NextEligibleKnown: true, MatchedRulesJSON: "[]", Created: now,
	}
	return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: s.record, Inserted: true}, nil
}

func TestAutorunDeploymentWindowAdmissionIsStableAndReplayReturnsExistingTask(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "autorun deployment window"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "autorun repository",
		GitURL: "https://example.invalid/autorun.git", GitBranch: "main",
	})
	require.NoError(t, err)
	buildTemplate, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "build", Playbook: "build.yml", Type: db.TemplateBuild,
	})
	require.NoError(t, err)
	childTemplate, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy", Playbook: "deploy.yml",
		BuildTemplateID: &buildTemplate.ID, Autorun: true,
	})
	require.NoError(t, err)

	parent, err := store.CreateTask(db.Task{
		ProjectID: project.ID, TemplateID: buildTemplate.ID, Status: task_logger.TaskSuccessStatus,
	}, 0)
	require.NoError(t, err)
	buildTaskID := parent.ID
	capture := &autorunDeploymentWindowAdmissionCapture{
		store: store, buildTaskID: buildTaskID, childTemplateID: childTemplate.ID,
	}
	pool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{}, &EncryptionServiceMock{},
		&KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	pool.register = make(chan *TaskRunner, 2)
	pool.ConfigureDeploymentWindowAdmission(capture)
	runner := &TaskRunner{Task: db.Task{
		ID: buildTaskID, ProjectID: project.ID, TemplateID: buildTemplate.ID, Status: task_logger.TaskSuccessStatus,
	}, pool: &pool}

	first, err := runner.addAutorunTask(childTemplate)
	require.NoError(t, err)
	replayed, err := runner.addAutorunTask(childTemplate)
	require.NoError(t, err)
	assert.Equal(t, first.ID, replayed.ID, "the replay must surface the existing decision-bound task")

	require.Len(t, capture.requests, 2)
	for _, request := range capture.requests {
		assert.Equal(t, pro_interfaces.DeploymentWindowSourceAutorun, request.Source)
		assert.Equal(t, pro_interfaces.DeploymentWindowOriginAutorun, request.Origin)
		require.NotNil(t, request.TemplateID)
		assert.Equal(t, childTemplate.ID, *request.TemplateID)
		assert.Equal(t, fmt.Sprintf("autorun-%d-%d", buildTaskID, childTemplate.ID), request.DecisionKey)
	}
	allTasks, err := store.GetProjectTasks(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	created := make([]db.Task, 0, 1)
	for _, task := range allTasks {
		if task.TemplateID == childTemplate.ID {
			created = append(created, task.Task)
		}
	}
	require.Len(t, created, 1, "replaying a completed build must return the decision-bound task instead of creating another child")
	assert.Equal(t, childTemplate.ID, created[0].TemplateID)
	require.NotNil(t, created[0].BuildTaskID)
	assert.Equal(t, buildTaskID, *created[0].BuildTaskID)
	require.NotNil(t, capture.record.TaskID)
	assert.Equal(t, created[0].ID, *capture.record.TaskID)
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowsql "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowTriggerServiceIssuesCredentialOnceAndDeduplicatesExternalStart(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	require.NotEmpty(t, created.Credential)
	assert.NotContains(t, created.Trigger.CredentialHash, created.Credential)
	persisted, err := fixture.repository.GetWorkflowTrigger(fixture.projectID, fixture.workflow.ID, created.Trigger.ID)
	require.NoError(t, err)
	assert.True(t, db.WorkflowTriggerCredentialMatches(persisted.CredentialHash, created.Credential))

	request := map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)}
	first, err := fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "deploy-once", request)
	require.NoError(t, err)
	second, err := fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "deploy-once", request)
	require.NoError(t, err)
	assert.Equal(t, first.Run.ID, second.Run.ID)
	assert.Equal(t, first.Invocation.ID, second.Invocation.ID)
	assert.True(t, second.Duplicate)
	assert.Equal(t, 1, fixture.starter.calls)
	require.NotNil(t, fixture.starter.input.TriggerSnapshot)
	assert.Equal(t, created.Trigger.ID, fixture.starter.input.TriggerSnapshot.ID)
	assert.JSONEq(t, `"eu"`, string(fixture.starter.input.TriggerValues["region"]))
}

func TestWorkflowTriggerServiceRotationRevokesOldCredential(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	rotated, err := fixture.service.RotateCredential(
		context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, created.Trigger.Revision, &fixture.actor,
	)
	require.NoError(t, err)
	assert.NotEqual(t, created.Credential, rotated.Credential)
	assert.Equal(t, created.Trigger.CredentialGeneration+1, rotated.Trigger.CredentialGeneration)

	request := map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)}
	_, err = fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "old", request)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerCredentialRejected)
	_, err = fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, rotated.Credential, "new", request)
	require.NoError(t, err)
}

func TestWorkflowTriggerServiceRevalidatesDefinitionAtFireTime(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	fixture.workflow.ParameterDefinitions = nil
	fixture.workflow.ParameterDefinitionsJSON = "[]"
	updated, err := fixture.repository.UpdateWorkflowTemplate(fixture.workflow)
	require.NoError(t, err)
	fixture.workflow = updated

	_, err = fixture.service.FireExternal(
		context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID,
		db.WorkflowTriggerAPI, created.Credential, "definition-change",
		map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)},
	)
	assert.ErrorContains(t, err, "unknown parameter")
	assert.Zero(t, fixture.starter.calls)
}

func TestWorkflowTriggerServiceRetriesFailedScheduledOccurrenceWithoutSecondRun(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	fixture.starter.failures = 1
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Nightly", Type: db.WorkflowTriggerSchedule, Enabled: true, CronFormat: "0 1 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
	}, &fixture.actor)
	require.NoError(t, err)
	scheduledAt := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)

	_, err = fixture.service.FireScheduled(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, scheduledAt)
	require.Error(t, err)
	result, err := fixture.service.FireScheduled(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, scheduledAt)
	require.NoError(t, err)
	duplicate, err := fixture.service.FireScheduled(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, scheduledAt)
	require.NoError(t, err)
	assert.Equal(t, result.Run.ID, duplicate.Run.ID)
	assert.Equal(t, result.Invocation.ID, duplicate.Invocation.ID)
	assert.Equal(t, 2, fixture.starter.calls)
}

func TestWorkflowTriggerSchedulerDrainRejectsNewRunsUntilResume(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Nightly", Type: db.WorkflowTriggerSchedule, Enabled: true, CronFormat: "0 1 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
	}, &fixture.actor)
	require.NoError(t, err)
	require.NotZero(t, created.Trigger.ID)
	scheduler := NewWorkflowTriggerScheduler(fixture.repository, fixture.service)
	drainer, ok := scheduler.(pro_interfaces.ClusterDrainer)
	require.True(t, ok)
	require.NoError(t, drainer.Drain())

	scheduledAt := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	scheduler.RunOnce(context.Background(), scheduledAt)
	assert.Zero(t, fixture.starter.calls)
	drainer.Resume()
	scheduler.RunOnce(context.Background(), scheduledAt)
	assert.Equal(t, 1, fixture.starter.calls)
}

func TestWorkflowTriggerServiceEnforcesCapabilityAndCurrentProjectPermission(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	fixture.identity.member.Role = db.ProjectGuest
	_, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Manual", Type: db.WorkflowTriggerManual, Enabled: true,
	}, &fixture.actor)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerPermissionDenied)

	fixture.identity.member.Role = db.ProjectOwner
	fixture.capability.allowed = false
	_, err = fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Manual", Type: db.WorkflowTriggerManual, Enabled: true,
	}, &fixture.actor)
	var denied pro_interfaces.CapabilityDeniedError
	assert.ErrorAs(t, err, &denied)
}

func TestWorkflowTriggerServiceScopesHistoryToWorkflow(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	otherWorkflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Other", DefinitionVersion: db.WorkflowDefinitionVersion,
	})
	require.NoError(t, err)

	_, err = fixture.service.History(
		context.Background(), fixture.projectID, otherWorkflow.ID, created.Trigger.ID,
		db.RetrieveQueryParams{}, &fixture.actor,
	)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestWorkflowTriggerSchedulerRunsMatchingUTCMinuteOnce(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Nightly", Type: db.WorkflowTriggerSchedule, Enabled: true, CronFormat: "30 8 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
	}, &fixture.actor)
	require.NoError(t, err)
	scheduler := NewWorkflowTriggerScheduler(fixture.repository, fixture.service)
	require.NotNil(t, scheduler)

	scheduler.RunOnce(context.Background(), time.Date(2026, 8, 29, 8, 29, 59, 0, time.UTC))
	assert.Zero(t, fixture.starter.calls)
	scheduler.RunOnce(context.Background(), time.Date(2026, 8, 29, 8, 30, 2, 0, time.UTC))
	scheduler.RunOnce(context.Background(), time.Date(2026, 8, 29, 8, 30, 45, 0, time.UTC))
	assert.Equal(t, 1, fixture.starter.calls)
	history, err := fixture.repository.GetWorkflowTriggerInvocations(fixture.projectID, created.Trigger.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, created.Trigger.ID, history[0].WorkflowTriggerID)
}

type workflowTriggerServiceFixture struct {
	store      *coresql.SqlDb
	repository *workflowsql.WorkflowStoreImpl
	service    pro_interfaces.WorkflowTriggerService
	starter    *workflowTriggerStarter
	identity   *workflowTriggerIdentity
	capability *workflowTriggerCapability
	actor      db.User
	projectID  int
	workflow   db.WorkflowTemplate
}

func newWorkflowTriggerServiceFixture(t *testing.T) *workflowTriggerServiceFixture {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Trigger service"})
	require.NoError(t, err)
	repository := workflowsql.NewWorkflowStore(store.GetConnection())
	workflow, err := repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Deploy", DefinitionVersion: db.WorkflowDefinitionVersion,
		ParameterDefinitions:     []db.WorkflowParameterDeclaration{{Name: "region", Type: db.WorkflowParameterString, Required: true}},
		ParameterDefinitionsJSON: `[{"name":"region","type":"string","required":true}]`,
	})
	require.NoError(t, err)
	actor := db.User{ID: 41, Username: "owner"}
	starter := &workflowTriggerStarter{runs: map[string]db.WorkflowRun{}, repository: repository}
	identity := &workflowTriggerIdentity{
		user: actor, member: db.ProjectUser{ProjectID: project.ID, UserID: actor.ID, Role: db.ProjectOwner},
	}
	capability := &workflowTriggerCapability{allowed: true}
	return &workflowTriggerServiceFixture{
		store: store, repository: repository, starter: starter, identity: identity, capability: capability,
		service: NewWorkflowTriggerService(repository, repository, starter, identity, capability),
		actor:   actor, projectID: project.ID, workflow: workflow,
	}
}

func (f *workflowTriggerServiceFixture) createAPITrigger(t *testing.T) pro_interfaces.WorkflowTriggerCredentialResult {
	t.Helper()
	created, err := f.service.Create(context.Background(), f.projectID, f.workflow.ID, db.WorkflowTrigger{
		Name: "Deploy API", Type: db.WorkflowTriggerAPI, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	}, &f.actor)
	require.NoError(t, err)
	return created
}

type workflowTriggerStarter struct {
	calls      int
	failures   int
	input      db.WorkflowRunInput
	runs       map[string]db.WorkflowRun
	repository db.WorkflowManager
}

func (s *workflowTriggerStarter) StartWorkflow(workflow db.WorkflowTemplate, user *db.User, correlationID string, inputs ...db.WorkflowRunInput) (db.WorkflowRun, error) {
	if existing, ok := s.runs[correlationID]; ok {
		return existing, nil
	}
	s.calls++
	if s.failures > 0 {
		s.failures--
		return db.WorkflowRun{}, errors.New("temporary workflow start outage")
	}
	s.input = inputs[0]
	now := time.Now().UTC()
	run, err := s.repository.CreateWorkflowRun(db.WorkflowRun{
		ProjectID: workflow.ProjectID, WorkflowTemplateID: workflow.ID, Status: db.WorkflowRunPending,
		ActorUserID: user.ID, DefinitionVersion: workflow.DefinitionVersion, DefinitionRevision: workflow.Revision,
		DefinitionSnapshotJSON: `{}`, ParameterSnapshotJSON: `{}`, TriggerSnapshotJSON: `{}`,
		CorrelationID: correlationID, Created: now,
	})
	if err != nil {
		return db.WorkflowRun{}, err
	}
	s.runs[correlationID] = run
	return run, nil
}

func (s *workflowTriggerStarter) ProgressWorkflowRun(int, int, *db.User) error { return nil }
func (s *workflowTriggerStarter) StopWorkflowRun(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) RequestWorkflowRunStop(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) ReconcileWorkflowRun(int, int) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) RetryWorkflowRunReconciliation(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) GetWorkflowApprovalInbox(int, *db.User) ([]db.WorkflowApproval, error) {
	return nil, nil
}
func (s *workflowTriggerStarter) ResolveWorkflowApproval(int, int, int, int, db.WorkflowApprovalDecision, *db.User) (db.WorkflowApproval, error) {
	return db.WorkflowApproval{}, nil
}
func (s *workflowTriggerStarter) HandleWorkflowTaskOutputs(db.Task, map[string]json.RawMessage) error {
	return nil
}
func (s *workflowTriggerStarter) HandleWorkflowTaskCompletion(db.Task) error { return nil }
func (s *workflowTriggerStarter) GetWorkflowRunArtifacts(int, int, *int) ([]db.WorkflowArtifactMetadata, error) {
	return nil, nil
}

type workflowTriggerIdentity struct {
	user   db.User
	member db.ProjectUser
}

func (s *workflowTriggerIdentity) GetUser(int) (db.User, error) { return s.user, nil }
func (s *workflowTriggerIdentity) GetProjectUser(int, int) (db.ProjectUser, error) {
	return s.member, nil
}
func (s *workflowTriggerIdentity) GetProjectOrGlobalRoleBySlug(int, string) (db.Role, error) {
	return db.Role{}, db.ErrNotFound
}

type workflowTriggerCapability struct{ allowed bool }

func (p *workflowTriggerCapability) Resolve(_ context.Context, request pro_interfaces.CapabilityRequest) (pro_interfaces.CapabilitySnapshot, error) {
	access := []pro_interfaces.CapabilityAccess(nil)
	state := pro_interfaces.CapabilityStateDisabled
	reason := pro_interfaces.CapabilityReasonDisabledByAdmin
	if p.allowed {
		access = []pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
			pro_interfaces.CapabilityAccessExecute,
		}
		state = pro_interfaces.CapabilityStateActive
		reason = pro_interfaces.CapabilityReasonActive
	}
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(pro_interfaces.CapabilityWorkflowTriggers, state, reason, access, nil),
	}), nil
}

func (p *workflowTriggerCapability) Configure(context.Context, pro_interfaces.CapabilityRequest, pro_interfaces.CapabilityConfiguration) (pro_interfaces.CapabilitySnapshot, error) {
	panic("not used")
}

type blockingWorkflowTriggerRepository struct {
	db.WorkflowTriggerManager
	claimEntered  chan struct{}
	continueClaim chan struct{}
}

func (r *blockingWorkflowTriggerRepository) ClaimWorkflowTriggerInvocation(
	invocation db.WorkflowTriggerInvocation,
	now time.Time,
) (db.WorkflowTriggerInvocation, bool, error) {
	close(r.claimEntered)
	<-r.continueClaim
	return r.WorkflowTriggerManager.ClaimWorkflowTriggerInvocation(invocation, now)
}

func TestWorkflowTriggerServiceRejectsInvocationClaimAfterRevocation(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(*workflowTriggerServiceFixture, pro_interfaces.WorkflowTriggerCredentialResult) error
	}{
		{
			name: "credential rotation",
			revoke: func(fixture *workflowTriggerServiceFixture, created pro_interfaces.WorkflowTriggerCredentialResult) error {
				_, err := fixture.service.RotateCredential(
					context.Background(), fixture.projectID, fixture.workflow.ID,
					created.Trigger.ID, created.Trigger.Revision, &fixture.actor,
				)
				return err
			},
		},
		{
			name: "disable",
			revoke: func(fixture *workflowTriggerServiceFixture, created pro_interfaces.WorkflowTriggerCredentialResult) error {
				_, err := fixture.service.SetEnabled(
					context.Background(), fixture.projectID, fixture.workflow.ID,
					created.Trigger.ID, created.Trigger.Revision, false, &fixture.actor,
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowTriggerServiceFixture(t)
			created := fixture.createAPITrigger(t)
			blocking := &blockingWorkflowTriggerRepository{
				WorkflowTriggerManager: fixture.repository,
				claimEntered:           make(chan struct{}),
				continueClaim:          make(chan struct{}),
			}
			fixture.service = NewWorkflowTriggerService(
				blocking, fixture.repository, fixture.starter, fixture.identity, fixture.capability,
			)

			fireResult := make(chan error, 1)
			go func() {
				_, err := fixture.service.FireExternal(
					context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID,
					db.WorkflowTriggerAPI, created.Credential, "revocation-race",
					map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)},
				)
				fireResult <- err
			}()

			select {
			case <-blocking.claimEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("external trigger did not reach the invocation claim")
			}

			require.NoError(t, test.revoke(fixture, created))
			close(blocking.continueClaim)

			select {
			case err := <-fireResult:
				require.ErrorIs(t, err, db.ErrWorkflowTriggerStateChanged)
			case <-time.After(5 * time.Second):
				t.Fatal("external trigger did not return after revocation")
			}
			assert.Zero(t, fixture.starter.calls)
		})
	}
}

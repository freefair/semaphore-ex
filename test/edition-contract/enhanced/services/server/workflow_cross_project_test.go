package server

import (
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type crossProjectWorkflowFixture struct {
	store       *coresql.SqlDb
	repository  *workflowSQL.WorkflowStoreImpl
	definition  db.WorkflowTemplate
	definitionS db.WorkflowVersionStore
	grantStore  db.CrossProjectTemplateGrantStore
	consumerID  int
	ownerID     int
	ownerTpl    db.Template
	version     db.TemplateVersion
	grant       db.CrossProjectTemplateGrant
	actor       db.User
}

func newCrossProjectWorkflowFixture(t *testing.T, operations db.CrossProjectTemplateGrantOperation) crossProjectWorkflowFixture {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	actor, err := store.CreateUserWithoutPassword(db.User{
		Username: "cross-project-admin", Name: "Cross project admin", Email: "cross-project-admin@example.invalid", Admin: true,
	})
	require.NoError(t, err)
	consumer, err := store.CreateProject(db.Project{Name: "workflow consumer"})
	require.NoError(t, err)
	owner, err := store.CreateProject(db.Project{Name: "workflow owner"})
	require.NoError(t, err)
	ownerTemplateID := insertWorkflowTestTemplate(t, store, owner.ID)
	ownerTemplate, err := store.GetTemplate(owner.ID, ownerTemplateID)
	require.NoError(t, err)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	version, _, err := versionStore.PublishTemplateVersion(ownerTemplate, actor.ID, time.Now().UTC())
	require.NoError(t, err)
	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: owner.ID, ConsumerProjectID: consumer.ID, TemplateID: ownerTemplate.ID,
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: operations, Status: db.CrossProjectTemplateGrantPending, Revision: 1,
		CreatedByUserID: actor.ID, Created: time.Now().UTC(), Reason: "immutable workflow use",
	})
	require.NoError(t, err)
	return crossProjectWorkflowFixture{store: store, repository: repository, definitionS: any(repository).(db.WorkflowVersionStore), grantStore: grantStore, consumerID: consumer.ID, ownerID: owner.ID, ownerTpl: ownerTemplate, version: version, grant: grant, actor: actor}
}

func (f *crossProjectWorkflowFixture) accept(t *testing.T) {
	t.Helper()
	accepted, err := f.grantStore.AcceptCrossProjectTemplateGrant(f.consumerID, f.grant.ID, f.actor.ID, f.grant.Revision, time.Now().UTC())
	require.NoError(t, err)
	f.grant = accepted
}

func (f crossProjectWorkflowFixture) inputReference() *db.CrossProjectTemplateReference {
	return &db.CrossProjectTemplateReference{GrantID: f.grant.ID, TemplateVersionNumber: f.version.VersionNumber, OwnerProjectID: 999, TemplateID: 998, TemplateVersionID: 997, ContentFingerprint: "sha256:forged", GrantRevision: 996}
}

func (f crossProjectWorkflowFixture) workflow() db.WorkflowTemplate {
	return db.WorkflowTemplate{Name: "External deploy", Nodes: []db.WorkflowNode{{ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: 999, CrossProjectTemplateReference: f.inputReference()}}}
}

func TestCrossProjectDefinitionLifecycleNormalizesAndRejectsLiveGrantFailures(t *testing.T) {
	fixture := newCrossProjectWorkflowFixture(t, db.CrossProjectTemplateGrantReference|db.CrossProjectTemplateGrantRun)
	defer fixture.store.Close()
	service := NewWorkflowDefinitionService(fixture.repository, fixture.store)
	_, _, err := service.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	assert.ErrorIs(t, err, db.ErrNotFound)
	workflows, err := fixture.repository.GetWorkflowTemplates(fixture.consumerID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, workflows)

	fixture.accept(t)
	created, validation, err := service.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	reference := created.Nodes[0].CrossProjectTemplateReference
	require.NotNil(t, reference)
	assert.Equal(t, fixture.ownerID, reference.OwnerProjectID)
	assert.Equal(t, fixture.ownerTpl.ID, reference.TemplateID)
	assert.Equal(t, fixture.version.ID, reference.TemplateVersionID)
	assert.Equal(t, fixture.version.ContentFingerprint, reference.ContentFingerprint)
	assert.Equal(t, fixture.grant.Revision, reference.GrantRevision)

	updatedCandidate := created
	updatedCandidate.Name = "External deploy update"
	updatedCandidate.Nodes[0].CrossProjectTemplateReference.OwnerProjectID = 123
	updated, validation, err := service.Update(fixture.consumerID, created.ID, updatedCandidate, &fixture.actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, fixture.ownerID, updated.Nodes[0].CrossProjectTemplateReference.OwnerProjectID)

	_, err = fixture.grantStore.RevokeCrossProjectTemplateGrant(fixture.ownerID, fixture.grant.ID, fixture.actor.ID, fixture.grant.Revision, "withdrawn", time.Now().UTC())
	require.NoError(t, err)
	_, _, err = service.RestoreVersion(fixture.consumerID, created.ID, 1, "restore", &fixture.actor)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestCrossProjectStartSnapshotsExactProvenanceDispatchesRootAndReplays(t *testing.T) {
	fixture := newCrossProjectWorkflowFixture(t, db.CrossProjectTemplateGrantReference|db.CrossProjectTemplateGrantRun)
	defer fixture.store.Close()
	fixture.accept(t)
	definitionService := NewWorkflowDefinitionService(fixture.repository, fixture.store)
	workflow, validation, err := definitionService.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	enqueuer := &workflowTestEnqueuer{store: fixture.store}
	service := NewWorkflowService(fixture.repository, fixture.store, enqueuer, nil)
	run, err := service.StartWorkflow(workflow, &fixture.actor, "cross-project-replay")
	require.NoError(t, err)
	require.Len(t, run.Nodes, 1)
	require.NotNil(t, run.Nodes[0].CrossProjectTemplateProvenance)
	assert.Equal(t, *workflow.Nodes[0].CrossProjectTemplateReference, run.Nodes[0].CrossProjectTemplateProvenance.Reference)
	assert.Equal(t, db.WorkflowRunNodeQueued, run.Nodes[0].Status)
	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, fixture.consumerID, enqueuer.tasks[0].ProjectID)
	require.NotNil(t, enqueuer.tasks[0].WorkflowTemplateProvenance)
	require.NotNil(t, enqueuer.tasks[0].WorkflowTemplateProvenance.CrossProject)
	assert.Equal(t, fixture.version.ContentFingerprint, enqueuer.tasks[0].WorkflowTemplateProvenance.CrossProject.Reference.ContentFingerprint)

	_, err = fixture.grantStore.RevokeCrossProjectTemplateGrant(fixture.ownerID, fixture.grant.ID, fixture.actor.ID, fixture.grant.Revision, "withdrawn", time.Now().UTC())
	require.NoError(t, err)
	replayed, err := service.StartWorkflow(workflow, &fixture.actor, "cross-project-replay")
	require.NoError(t, err)
	assert.Equal(t, run.ID, replayed.ID)
	_, err = service.StartWorkflow(workflow, &fixture.actor, "cross-project-new")
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestCrossProjectDefinitionRejectsOutOfRangeAndStartRejectsMissingRunOperation(t *testing.T) {
	fixture := newCrossProjectWorkflowFixture(t, db.CrossProjectTemplateGrantReference)
	defer fixture.store.Close()
	fixture.accept(t)
	definitionService := NewWorkflowDefinitionService(fixture.repository, fixture.store)
	outOfRange := fixture.workflow()
	outOfRange.Nodes[0].CrossProjectTemplateReference.TemplateVersionNumber++
	_, _, err := definitionService.Create(fixture.consumerID, outOfRange, &fixture.actor)
	assert.ErrorIs(t, err, db.ErrNotFound)
	workflow, validation, err := definitionService.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	service := NewWorkflowService(fixture.repository, fixture.store, &workflowTestEnqueuer{store: fixture.store}, nil)
	_, err = service.StartWorkflow(workflow, &fixture.actor, "reference-only-start")
	assert.ErrorIs(t, err, db.ErrNotFound)
	runs, err := fixture.repository.GetWorkflowRuns(fixture.consumerID, workflow.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, runs)
}

func TestCrossProjectDispatchReauthorizesActorFromStore(t *testing.T) {
	fixture := newCrossProjectWorkflowFixture(t, db.CrossProjectTemplateGrantReference|db.CrossProjectTemplateGrantRun)
	defer fixture.store.Close()
	fixture.accept(t)
	definitionService := NewWorkflowDefinitionService(fixture.repository, fixture.store)
	workflow, validation, err := definitionService.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	_, err = fixture.store.CreateUserWithoutPassword(db.User{
		Username: "replacement-admin", Name: "Replacement admin", Email: "replacement-admin@example.invalid", Admin: true,
	})
	require.NoError(t, err)
	current, err := fixture.store.GetUser(fixture.actor.ID)
	require.NoError(t, err)
	current.Admin = false
	require.NoError(t, fixture.store.UpdateUser(db.UserWithPwd{User: current}))

	enqueuer := &workflowTestEnqueuer{store: fixture.store}
	service := NewWorkflowService(fixture.repository, fixture.store, enqueuer, nil)
	run, err := service.StartWorkflow(workflow, &fixture.actor, "cross-project-runtime-reauth")
	require.NoError(t, err)
	require.Len(t, run.Nodes, 1)
	assert.Equal(t, db.WorkflowRunNodeBlocked, run.Nodes[0].Status)
	assert.Empty(t, enqueuer.tasks)
}

func TestCrossProjectSaveAndStartSerializeWithRevocation(t *testing.T) {
	fixture := newCrossProjectWorkflowFixture(t, db.CrossProjectTemplateGrantReference|db.CrossProjectTemplateGrantRun)
	defer fixture.store.Close()
	fixture.accept(t)
	definitionService := NewWorkflowDefinitionService(fixture.repository, fixture.store)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var created db.WorkflowTemplate
	var createErr error
	var revokeErr error
	go func() {
		defer wait.Done()
		<-start
		created, _, createErr = definitionService.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	}()
	go func() {
		defer wait.Done()
		<-start
		_, revokeErr = fixture.grantStore.RevokeCrossProjectTemplateGrant(fixture.ownerID, fixture.grant.ID, fixture.actor.ID, fixture.grant.Revision, "withdrawn", time.Now().UTC())
	}()
	close(start)
	wait.Wait()
	require.NoError(t, revokeErr)
	if createErr == nil {
		reference := created.Nodes[0].CrossProjectTemplateReference
		require.NotNil(t, reference)
		assert.Equal(t, fixture.grant.Revision, reference.GrantRevision)
	} else {
		assert.ErrorIs(t, createErr, db.ErrNotFound)
	}
}

func TestCrossProjectStartSerializesWithRevocation(t *testing.T) {
	fixture := newCrossProjectWorkflowFixture(t, db.CrossProjectTemplateGrantReference|db.CrossProjectTemplateGrantRun)
	defer fixture.store.Close()
	fixture.accept(t)
	definitionService := NewWorkflowDefinitionService(fixture.repository, fixture.store)
	workflow, validation, err := definitionService.Create(fixture.consumerID, fixture.workflow(), &fixture.actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	service := NewWorkflowService(fixture.repository, fixture.store, &workflowTestEnqueuer{store: fixture.store}, nil)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var run db.WorkflowRun
	var startErr error
	var revokeErr error
	go func() {
		defer wait.Done()
		<-start
		run, startErr = service.StartWorkflow(workflow, &fixture.actor, "start-revoke-race")
	}()
	go func() {
		defer wait.Done()
		<-start
		_, revokeErr = fixture.grantStore.RevokeCrossProjectTemplateGrant(fixture.ownerID, fixture.grant.ID, fixture.actor.ID, fixture.grant.Revision, "withdrawn", time.Now().UTC())
	}()
	close(start)
	wait.Wait()
	require.NoError(t, revokeErr)
	if startErr == nil {
		assert.Positive(t, run.ID)
		assert.Equal(t, db.WorkflowRunNodeBlocked, run.Nodes[0].Status)
	} else {
		assert.ErrorIs(t, startErr, db.ErrNotFound)
	}
}

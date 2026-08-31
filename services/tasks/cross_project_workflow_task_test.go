package tasks

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingCrossProjectWorkflowTaskStore struct {
	store      *sql.SqlDb
	task       db.Task
	provenance db.CrossProjectTemplateProvenance
	lease      *pro_interfaces.WorkflowReconciliationLease
}

func (s *recordingCrossProjectWorkflowTaskStore) CreateCrossProjectWorkflowTaskFenced(
	task db.Task,
	provenance db.CrossProjectTemplateProvenance,
	lease *pro_interfaces.WorkflowReconciliationLease,
) (db.Task, error) {
	s.task = task
	s.provenance = provenance
	s.lease = lease
	return s.store.CreateTask(task, 0)
}

func TestTaskPoolCrossProjectWorkflowTaskUsesImmutableOwnerSnapshot(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	defer store.Close()
	consumer, err := store.CreateProject(db.Project{Name: "consumer", Alert: true})
	require.NoError(t, err)
	owner, err := store.CreateProject(db.Project{Name: "owner"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &owner.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: owner.ID, SSHKeyID: key.ID, Name: "owner repository",
		GitURL: "https://example.invalid/owner.git", GitBranch: "main",
	})
	require.NoError(t, err)
	startVersion := "1.0.0"
	template, err := store.CreateTemplate(db.Template{
		ProjectID: owner.ID, RepositoryID: repository.ID, Name: "owner template", Playbook: "published.yml",
		Type: db.TemplateBuild, StartVersion: &startVersion,
	})
	require.NoError(t, err)
	ownerVersion := "9.9.9"
	_, err = store.CreateTask(db.Task{
		ProjectID: owner.ID, TemplateID: template.ID, Version: &ownerVersion, Created: time.Now().UTC(),
	}, 0)
	require.NoError(t, err)
	snapshot, err := db.NewTemplateVersionSnapshot(template)
	require.NoError(t, err)
	fingerprint, err := db.TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	provenance := db.CrossProjectTemplateProvenance{
		Reference: db.CrossProjectTemplateReference{
			GrantID: 7, TemplateVersionNumber: 3, OwnerProjectID: owner.ID,
			TemplateID: template.ID, TemplateVersionID: 11, ContentFingerprint: fingerprint, GrantRevision: 2,
		},
		TemplateSnapshot: snapshot,
	}
	require.NoError(t, provenance.Validate())

	template.Playbook = "mutable-live.yml"
	require.NoError(t, store.UpdateTemplate(template))

	crossStore := &recordingCrossProjectWorkflowTaskStore{store: store}
	pool := CreateTaskPool(
		store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{}, &EncryptionServiceMock{},
		&KeyInstallerMock{}, &mockLogWriteService{}, nil, nil,
	)
	pool.register = make(chan *TaskRunner, 1)
	pool.ConfigureCrossProjectWorkflowTaskStore(crossStore)
	runID, nodeID := 41, 73
	created, err := pool.AddCrossProjectWorkflowTaskFenced(
		db.Task{TemplateID: template.ID, WorkflowRunID: &runID, WorkflowNodeID: &nodeID},
		provenance, nil, "", consumer.ID, nil,
	)
	require.NoError(t, err)
	assert.Equal(t, consumer.ID, created.ProjectID)
	assert.Equal(t, template.ID, created.TemplateID)
	require.NotNil(t, created.Version)
	assert.Equal(t, startVersion, *created.Version)
	require.NotNil(t, created.WorkflowTemplateProvenance)
	require.NotNil(t, created.WorkflowTemplateProvenance.CrossProject)
	assert.Equal(t, provenance.Reference, created.WorkflowTemplateProvenance.CrossProject.Reference)
	require.NotNil(t, crossStore.task.WorkflowTemplateSnapshot)
	assert.NotContains(t, *crossStore.task.WorkflowTemplateSnapshot, "mutable-live.yml")
	assert.Nil(t, crossStore.lease)

	queued := <-pool.register
	assert.Equal(t, consumer.ID, queued.Task.ProjectID)
	assert.Equal(t, owner.ID, queued.Template.ProjectID)
	assert.Equal(t, "published.yml", queued.Template.Playbook)
	assert.True(t, queued.alert)
	state := pool.state.(*MemoryTaskStateStore)
	state.AddActive(owner.ID, &TaskRunner{Task: db.Task{ID: 801, ProjectID: owner.ID}, Template: queued.Template})
	assert.False(t, pool.blocks(queued), "owner activity must not consume consumer capacity")
	state.AddActive(consumer.ID, &TaskRunner{Task: db.Task{ID: 802, ProjectID: consumer.ID}, Template: queued.Template})
	assert.True(t, pool.blocks(queued), "consumer lifecycle rules must govern external task dispatch")

	persisted, err := store.GetTask(consumer.ID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, persisted.WorkflowTemplateProvenance)
	assert.Equal(t, provenance.Reference, persisted.WorkflowTemplateProvenance.CrossProject.Reference)
	consumerTasks, err := store.GetProjectTasks(consumer.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, consumerTasks, 1)
	ownerTasks, err := store.GetProjectTasks(owner.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, ownerTasks, 1)
	assert.Equal(t, ownerVersion, *ownerTasks[0].Version)

	var storedTemplate db.Template
	require.NoError(t, json.Unmarshal([]byte(*persisted.WorkflowTemplateSnapshot), &storedTemplate))
	assert.Equal(t, "published.yml", storedTemplate.Playbook)
	assert.WithinDuration(t, time.Now(), created.Created, time.Second)
}

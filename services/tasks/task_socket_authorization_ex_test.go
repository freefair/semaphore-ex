package tasks

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type socketViewerStore struct {
	db.Store
	viewers []int
}

func (s *socketViewerStore) GetTaskViewers(int, int, *int) ([]int, error) {
	return append([]int(nil), s.viewers...), nil
}

type socketRecorder struct{ users []int }

func (*socketRecorder) Start()                         {}
func (*socketRecorder) Stop()                          {}
func (r *socketRecorder) Publish(userID int, _ []byte) { r.users = append(r.users, userID) }

func TestTaskSocketDeliveryRechecksRevokedViewer(t *testing.T) {
	store := &socketViewerStore{viewers: []int{17}}
	recorder := &socketRecorder{}
	sockets.SetBroadcaster(recorder)
	t.Cleanup(func() { sockets.SetBroadcaster(nil) })
	runner := &TaskRunner{Task: db.Task{ID: 1, ProjectID: 2, TemplateID: 3}, pool: &TaskPool{store: store}}
	runner.sendToWs(time.Now(), "before")
	runner.publishStatus()
	store.viewers = nil
	runner.sendToWs(time.Now(), "after")
	runner.publishStatus()
	assert.Equal(t, []int{17, 17}, recorder.users)
}

func TestTaskSocketStopsAfterTemplateGrantRevocation(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "socket authorization"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.invalid/repo", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repo.ID, Name: "task", Playbook: "task.yml"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "socket-role", Slug: "socket-role", Name: "socket role", ProjectID: &project.ID, Revision: 1})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "socket-user", Name: "socket user", Email: "socket-user@example.invalid"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	grant, err := store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: project.ID, TemplateID: template.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate})
	require.NoError(t, err)
	recorder := &socketRecorder{}
	sockets.SetBroadcaster(recorder)
	t.Cleanup(func() { sockets.SetBroadcaster(nil) })
	runner := &TaskRunner{Task: db.Task{ID: 1, ProjectID: project.ID, TemplateID: template.ID}, pool: &TaskPool{store: store}}
	runner.sendToWs(time.Now(), "before revocation")
	require.NoError(t, store.DeleteTemplateRole(project.ID, template.ID, grant.ID, grant.Revision))
	runner.sendToWs(time.Now(), "after revocation")
	assert.Equal(t, []int{user.ID}, recorder.users)
}

package server

import (
	"fmt"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossProjectTemplateServiceRequiresDualProjectAdministrationAndHidesUnavailableDiscovery(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	ownerProject, err := store.CreateProject(db.Project{Name: "cross-owner"})
	require.NoError(t, err)
	consumerProject, err := store.CreateProject(db.Project{Name: "cross-consumer"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "cross-other"})
	require.NoError(t, err)
	owner := crossProjectTemplateServiceUser(t, store, "owner")
	consumer := crossProjectTemplateServiceUser(t, store, "consumer")
	other := crossProjectTemplateServiceUser(t, store, "other")
	for _, membership := range []db.ProjectUser{
		{ProjectID: ownerProject.ID, UserID: owner.ID, Role: db.ProjectManager},
		{ProjectID: consumerProject.ID, UserID: consumer.ID, Role: db.ProjectManager},
		{ProjectID: otherProject.ID, UserID: other.ID, Role: db.ProjectManager},
	} {
		_, err = store.CreateProjectUser(membership)
		require.NoError(t, err)
	}
	template := crossProjectTemplateServiceTemplate(t, store, ownerProject.ID)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewCrossProjectTemplateService(store, repository)

	first, reused, err := service.PublishTemplateVersion(ownerProject.ID, template.ID, &owner)
	require.NoError(t, err)
	assert.False(t, reused)
	template.Name = "cross-template-v2"
	require.NoError(t, store.UpdateTemplate(template))
	second, reused, err := service.PublishTemplateVersion(ownerProject.ID, template.ID, &owner)
	require.NoError(t, err)
	assert.False(t, reused)
	require.Equal(t, first.VersionNumber+1, second.VersionNumber)

	_, err = service.CreateGrant(ownerProject.ID, template.ID, pro_interfaces.CrossProjectTemplateGrantCreate{
		ConsumerProjectID: 999999, MinVersion: first.VersionNumber, MaxVersion: first.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Reason: "phantom consumer",
	}, &owner)
	assert.ErrorIs(t, err, db.ErrNotFound, "owners must not create dangling grants for nonexistent projects")

	grant, err := service.CreateGrant(ownerProject.ID, template.ID, pro_interfaces.CrossProjectTemplateGrantCreate{
		ConsumerProjectID: consumerProject.ID, MinVersion: first.VersionNumber, MaxVersion: first.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Reason: "reference v1",
	}, &owner)
	require.NoError(t, err)
	_, _, err = service.ListReferences(consumerProject.ID, grant.ID, db.RetrieveQueryParams{}, &consumer)
	assert.ErrorIs(t, err, db.ErrNotFound, "pending grants must remain hidden")

	active, err := service.AcceptGrant(consumerProject.ID, grant.ID, grant.Revision, &consumer)
	require.NoError(t, err)
	refs, discoveredGrant, err := service.ListReferences(consumerProject.ID, grant.ID, db.RetrieveQueryParams{}, &consumer)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, first.VersionNumber, refs[0].TemplateVersionNumber)
	assert.Equal(t, active.Revision, discoveredGrant.Revision)

	_, _, err = service.ListReferences(otherProject.ID, grant.ID, db.RetrieveQueryParams{}, &other)
	assert.ErrorIs(t, err, db.ErrNotFound, "a manager in an unrelated project must not discover a grant")

	wrongOperation, err := service.CreateGrant(ownerProject.ID, template.ID, pro_interfaces.CrossProjectTemplateGrantCreate{
		ConsumerProjectID: consumerProject.ID, MinVersion: first.VersionNumber, MaxVersion: second.VersionNumber,
		Operations: db.CrossProjectTemplateGrantRun, Reason: "run only",
	}, &owner)
	require.NoError(t, err)
	_, err = service.AcceptGrant(consumerProject.ID, wrongOperation.ID, wrongOperation.Revision, &consumer)
	require.NoError(t, err)
	_, _, err = service.ListReferences(consumerProject.ID, wrongOperation.ID, db.RetrieveQueryParams{}, &consumer)
	assert.ErrorIs(t, err, db.ErrNotFound, "run-only grants must not disclose reference picker values")

	stale, err := service.CreateGrant(ownerProject.ID, template.ID, pro_interfaces.CrossProjectTemplateGrantCreate{
		ConsumerProjectID: consumerProject.ID, MinVersion: first.VersionNumber, MaxVersion: first.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Reason: "replace before acceptance",
	}, &owner)
	require.NoError(t, err)
	stale, err = service.UpdateGrant(ownerProject.ID, stale.ID, pro_interfaces.CrossProjectTemplateGrantUpdate{
		MinVersion: second.VersionNumber, MaxVersion: second.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Reason: "reference v2",
		ExpectedRevision: stale.Revision,
	}, &owner)
	require.NoError(t, err)
	_, err = service.AcceptGrant(consumerProject.ID, stale.ID, stale.Revision, &consumer)
	require.NoError(t, err)
	refs, _, err = service.ListReferences(consumerProject.ID, stale.ID, db.RetrieveQueryParams{}, &consumer)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, second.VersionNumber, refs[0].TemplateVersionNumber, "only exact current mappings may be discovered")

	revoked, err := service.RevokeGrant(ownerProject.ID, grant.ID, active.Revision, "withdrawn", &owner)
	require.NoError(t, err)
	assert.Equal(t, db.CrossProjectTemplateGrantRevoked, revoked.Status)
	_, _, err = service.ListReferences(consumerProject.ID, grant.ID, db.RetrieveQueryParams{}, &consumer)
	assert.ErrorIs(t, err, db.ErrNotFound, "revoked grants must remain hidden")
}

func crossProjectTemplateServiceUser(t *testing.T, store db.Store, name string) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "cross-" + name, Name: "Cross " + name,
		Email: fmt.Sprintf("cross-%s@example.test", name),
	})
	require.NoError(t, err)
	return user
}

func crossProjectTemplateServiceTemplate(t *testing.T, store db.Store, projectID int) db.Template {
	t.Helper()
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &projectID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: projectID, Name: "cross-repository", GitURL: "https://example.test/cross.git",
		GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repository.ID, Name: "cross-template-v1", Playbook: "deploy.yml",
	})
	require.NoError(t, err)
	loaded, err := store.GetTemplate(projectID, template.ID)
	require.NoError(t, err)
	return loaded
}

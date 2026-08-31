package sql

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateVersionPublicationIsImmutableAndIdempotent(t *testing.T) {
	store, projectID, template := templateVersionGrantFixture(t, "owner")
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)

	createdAt := time.Now().UTC().Add(-time.Minute)
	first, reused, err := versionStore.PublishTemplateVersion(template, 17, createdAt)
	require.NoError(t, err)
	assert.False(t, reused)
	assert.Equal(t, 1, first.VersionNumber)
	assert.Equal(t, projectID, first.OwnerProjectID)
	assert.NotEmpty(t, first.ContentFingerprint)
	assert.Empty(t, first.Snapshot.Dependencies.Vaults)

	same, reused, err := versionStore.PublishTemplateVersion(template, 18, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, reused)
	assert.Equal(t, first.ID, same.ID)
	unpersisted := template
	unpersisted.Name = "must not publish caller-provided mutation"
	stored, reused, err := versionStore.PublishTemplateVersion(unpersisted, 18, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, reused)
	assert.Equal(t, first.ID, stored.ID)
	assert.Equal(t, "template-owner", stored.Snapshot.Execution.Name)

	template.Name = "Deploy newer"
	require.NoError(t, store.UpdateTemplate(template))
	second, reused, err := versionStore.PublishTemplateVersion(template, 18, time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, reused)
	assert.Equal(t, 2, second.VersionNumber)

	versions, err := versionStore.GetTemplateVersions(projectID, template.ID, db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, second.ID, versions[0].ID)
	assert.Equal(t, first.Snapshot.Execution.Name, "template-owner")
}

func TestCrossProjectTemplateGrantLifecycleDeletionGuardAndRevision(t *testing.T) {
	store, ownerProjectID, template := templateVersionGrantFixture(t, "owner")
	consumer, err := store.CreateProject(db.Project{Name: "consumer"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	version, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)

	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: template.ID,
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Status:     db.CrossProjectTemplateGrantPending, Revision: 1,
		CreatedByUserID: 17, Created: time.Now().UTC(), Reason: "approved deployment reuse",
	})
	require.NoError(t, err)
	assert.Equal(t, db.CrossProjectTemplateGrantPending, grant.Status)
	assert.ErrorIs(t, repository.DeleteTemplateWithCrossProjectGrantGuard(ownerProjectID, template.ID), db.ErrCrossProjectTemplateGrantUnrevokedReference)
	var grantVersionReferences []struct {
		GrantID           int `db:"grant_id"`
		TemplateVersionID int `db:"template_version_id"`
	}
	_, err = store.Sql().Select(&grantVersionReferences,
		"select grant_id, template_version_id from project__cross_project_template_grant_version where grant_id=?", grant.ID)
	require.NoError(t, err)
	require.Len(t, grantVersionReferences, 1)
	updatedPending, err := grantStore.UpdateCrossProjectTemplateGrant(ownerProjectID, grant.ID, db.CrossProjectTemplateGrantUpdate{
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Reason:     "",
	}, grant.Revision)
	require.NoError(t, err)
	assert.Equal(t, grant.Revision+1, updatedPending.Revision)
	assert.Empty(t, updatedPending.Reason)
	_, err = grantStore.UpdateCrossProjectTemplateGrant(ownerProjectID, grant.ID, db.CrossProjectTemplateGrantUpdate{
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Reason: "stale",
	}, grant.Revision)
	assert.ErrorIs(t, err, db.ErrCrossProjectTemplateGrantRevisionConflict)

	accepted, err := grantStore.AcceptCrossProjectTemplateGrant(consumer.ID, grant.ID, 23, updatedPending.Revision, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, db.CrossProjectTemplateGrantActive, accepted.Status)
	assert.Equal(t, updatedPending.Revision+1, accepted.Revision)
	active, err := grantStore.HasActiveCrossProjectTemplateGrants(ownerProjectID, template.ID)
	require.NoError(t, err)
	assert.True(t, active)
	unrevoked, err := grantStore.HasUnrevokedCrossProjectTemplateGrants(ownerProjectID, template.ID)
	require.NoError(t, err)
	assert.True(t, unrevoked)
	assert.ErrorIs(t, repository.DeleteTemplateWithCrossProjectGrantGuard(ownerProjectID, template.ID), db.ErrCrossProjectTemplateGrantUnrevokedReference)
	assert.ErrorIs(t, grantStore.DeleteCrossProjectTemplateGrant(ownerProjectID, accepted.ID, accepted.Revision), db.ErrCrossProjectTemplateGrantActiveReference)

	err = versionStore.DeleteTemplateVersion(ownerProjectID, template.ID, version.VersionNumber)
	assert.ErrorIs(t, err, db.ErrCrossProjectTemplateGrantActiveReference)

	_, err = grantStore.RevokeCrossProjectTemplateGrant(ownerProjectID, accepted.ID, 17, updatedPending.Revision, "rotated", time.Now().UTC())
	assert.ErrorIs(t, err, db.ErrCrossProjectTemplateGrantRevisionConflict)

	revoked, err := grantStore.RevokeCrossProjectTemplateGrant(ownerProjectID, accepted.ID, 17, accepted.Revision, "rotated", time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, db.CrossProjectTemplateGrantRevoked, revoked.Status)
	active, err = grantStore.HasActiveCrossProjectTemplateGrants(ownerProjectID, template.ID)
	require.NoError(t, err)
	assert.False(t, active)
	unrevoked, err = grantStore.HasUnrevokedCrossProjectTemplateGrants(ownerProjectID, template.ID)
	require.NoError(t, err)
	assert.False(t, unrevoked)
	require.NoError(t, versionStore.DeleteTemplateVersion(ownerProjectID, template.ID, version.VersionNumber))
	require.NoError(t, grantStore.DeleteCrossProjectTemplateGrant(ownerProjectID, revoked.ID, revoked.Revision))
	require.NoError(t, repository.DeleteTemplateWithCrossProjectGrantGuard(ownerProjectID, template.ID))

	_, err = grantStore.GetCrossProjectTemplateGrant(ownerProjectID+999, grant.ID)
	assert.True(t, errors.Is(err, db.ErrNotFound))
}

func TestCrossProjectTemplateGrantListUsesBoundedKeysetPagination(t *testing.T) {
	store, ownerProjectID, template := templateVersionGrantFixture(t, "pagination")
	consumer, err := store.CreateProject(db.Project{Name: "consumer pagination"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	version, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	for index := 0; index < 2; index++ {
		_, err = grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
			OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: template.ID,
			MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
			Operations: db.CrossProjectTemplateGrantReference, Status: db.CrossProjectTemplateGrantPending,
			Revision: 1, CreatedByUserID: 17, Created: time.Now().UTC(), Reason: "list grant",
		})
		require.NoError(t, err)
	}
	page, err := grantStore.GetCrossProjectTemplateGrants(ownerProjectID, db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, 2, page[0].ID)
	previous, err := grantStore.GetCrossProjectTemplateGrants(ownerProjectID, db.RetrieveQueryParams{Count: 1, BeforeID: page[0].ID})
	require.NoError(t, err)
	require.Len(t, previous, 1)
	assert.Equal(t, 1, previous[0].ID)
	_, err = grantStore.GetCrossProjectTemplateGrants(ownerProjectID, db.RetrieveQueryParams{Count: db.MaxCrossProjectTemplateGrantPageSize + 1})
	assert.Error(t, err)
}

func TestResolveActiveCrossProjectTemplateGrantNormalizesOnlyExactAcceptedVersion(t *testing.T) {
	store, ownerProjectID, template := templateVersionGrantFixture(t, "resolve")
	consumer, err := store.CreateProject(db.Project{Name: "consumer resolve"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	version, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: template.ID,
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Status:     db.CrossProjectTemplateGrantPending, Revision: 1, CreatedByUserID: 17,
		Created: time.Now().UTC(), Reason: "exact immutable workflow reference",
	})
	require.NoError(t, err)
	request := db.CrossProjectTemplateReference{GrantID: grant.ID, TemplateVersionNumber: version.VersionNumber, OwnerProjectID: 999, TemplateID: 999}
	_, _, err = grantStore.ResolveActiveCrossProjectTemplateGrant(consumer.ID, request, db.CrossProjectTemplateGrantReference)
	assert.ErrorIs(t, err, db.ErrNotFound)
	accepted, err := grantStore.AcceptCrossProjectTemplateGrant(consumer.ID, grant.ID, 23, grant.Revision, time.Now().UTC())
	require.NoError(t, err)
	normalized, resolved, err := grantStore.ResolveActiveCrossProjectTemplateGrant(consumer.ID, request, db.CrossProjectTemplateGrantReference)
	require.NoError(t, err)
	assert.Equal(t, ownerProjectID, normalized.OwnerProjectID)
	assert.Equal(t, template.ID, normalized.TemplateID)
	assert.Equal(t, version.ID, normalized.TemplateVersionID)
	assert.Equal(t, accepted.Revision, normalized.GrantRevision)
	assert.Equal(t, version.ContentFingerprint, normalized.ContentFingerprint)
	assert.Equal(t, version.ID, resolved.ID)
	_, err = grantStore.RevokeCrossProjectTemplateGrant(ownerProjectID, accepted.ID, 17, accepted.Revision, "withdrawn", time.Now().UTC())
	require.NoError(t, err)
	_, _, err = grantStore.ResolveActiveCrossProjectTemplateGrant(consumer.ID, request, db.CrossProjectTemplateGrantRun)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestCrossProjectTemplateGrantRejectsRangeWithMissingPublishedVersion(t *testing.T) {
	store, ownerProjectID, template := templateVersionGrantFixture(t, "range")
	consumer, err := store.CreateProject(db.Project{Name: "consumer range"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	first, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	template.Name = "range second"
	require.NoError(t, store.UpdateTemplate(template))
	second, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	template.Name = "range third"
	require.NoError(t, store.UpdateTemplate(template))
	third, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, versionStore.DeleteTemplateVersion(ownerProjectID, template.ID, second.VersionNumber))

	_, err = grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: template.ID,
		MinTemplateVersion: first.VersionNumber, MaxTemplateVersion: third.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Status: db.CrossProjectTemplateGrantPending,
		Revision: 1, CreatedByUserID: 17, Created: time.Now().UTC(),
	})
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestAcceptAndDeleteTemplateVersionSerializeWithoutDanglingGrant(t *testing.T) {
	store, ownerProjectID, template := templateVersionGrantFixture(t, "accept-delete-race")
	consumer, err := store.CreateProject(db.Project{Name: "consumer accept-delete-race"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	version, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: template.ID,
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Status:     db.CrossProjectTemplateGrantPending, Revision: 1,
		CreatedByUserID: 17, Created: time.Now().UTC(),
	})
	require.NoError(t, err)

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var accepted db.CrossProjectTemplateGrant
	var acceptErr error
	var deleteErr error
	go func() {
		defer wait.Done()
		<-start
		accepted, acceptErr = grantStore.AcceptCrossProjectTemplateGrant(consumer.ID, grant.ID, 23, grant.Revision, time.Now().UTC())
	}()
	go func() {
		defer wait.Done()
		<-start
		deleteErr = versionStore.DeleteTemplateVersion(ownerProjectID, template.ID, version.VersionNumber)
	}()
	close(start)
	wait.Wait()
	require.NoError(t, acceptErr)
	assert.ErrorIs(t, deleteErr, db.ErrCrossProjectTemplateGrantActiveReference)
	assert.Equal(t, db.CrossProjectTemplateGrantActive, accepted.Status)
	_, err = versionStore.GetTemplateVersion(ownerProjectID, template.ID, version.VersionNumber)
	require.NoError(t, err)
	active, err := grantStore.GetCrossProjectTemplateGrant(consumer.ID, grant.ID)
	require.NoError(t, err)
	assert.Equal(t, db.CrossProjectTemplateGrantActive, active.Status)
}

func TestAcceptRejectsGrantWithMissingExactVersionReference(t *testing.T) {
	store, ownerProjectID, template := templateVersionGrantFixture(t, "accept-reference-integrity")
	consumer, err := store.CreateProject(db.Project{Name: "consumer accept-reference-integrity"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	version, _, err := versionStore.PublishTemplateVersion(template, 17, time.Now().UTC())
	require.NoError(t, err)
	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: template.ID,
		MinTemplateVersion: version.VersionNumber, MaxTemplateVersion: version.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference, Status: db.CrossProjectTemplateGrantPending,
		Revision: 1, CreatedByUserID: 17, Created: time.Now().UTC(),
	})
	require.NoError(t, err)
	_, err = store.Sql().Exec("delete from project__cross_project_template_grant_version where grant_id=?", grant.ID)
	require.NoError(t, err)
	_, err = grantStore.AcceptCrossProjectTemplateGrant(consumer.ID, grant.ID, 23, grant.Revision, time.Now().UTC())
	assert.ErrorIs(t, err, db.ErrNotFound)
	persisted, err := grantStore.GetCrossProjectTemplateGrant(consumer.ID, grant.ID)
	require.NoError(t, err)
	assert.Equal(t, db.CrossProjectTemplateGrantPending, persisted.Status)
}

func TestTemplateVersionPublicationPinsCurrentBuildTemplateVersion(t *testing.T) {
	store, projectID, buildTemplate := templateVersionGrantFixture(t, "build")
	deployTemplate, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: buildTemplate.RepositoryID, Name: "deploy-build", Playbook: "deploy.yml",
		BuildTemplateID: &buildTemplate.ID,
	})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	deployVersion, _, err := versionStore.PublishTemplateVersion(deployTemplate, 17, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, deployVersion.Snapshot.Dependencies.BuildTemplateVersion)
	assert.NotContains(t, deployVersion.SnapshotJSON, "build_template_id")
	buildReference := deployVersion.Snapshot.Dependencies.BuildTemplateVersion
	assert.Equal(t, projectID, buildReference.OwnerProjectID)
	assert.Equal(t, buildTemplate.ID, buildReference.TemplateID)
	assert.Equal(t, 1, buildReference.VersionNumber)
	buildVersion, err := versionStore.GetTemplateVersion(projectID, buildTemplate.ID, buildReference.VersionNumber)
	require.NoError(t, err)
	assert.Equal(t, buildVersion.ContentFingerprint, buildReference.ContentFingerprint)

	buildTemplate.Name = "build newer"
	require.NoError(t, store.UpdateTemplate(buildTemplate))
	publishedDeploy, reused, err := versionStore.PublishTemplateVersion(deployTemplate, 17, time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, reused)
	assert.Equal(t, 2, publishedDeploy.VersionNumber)
	require.NotNil(t, publishedDeploy.Snapshot.Dependencies.BuildTemplateVersion)
	assert.Equal(t, 2, publishedDeploy.Snapshot.Dependencies.BuildTemplateVersion.VersionNumber)
}

func templateVersionGrantFixture(t *testing.T, suffix string) (*coresql.SqlDb, int, db.Template) {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "template-version-" + suffix})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, Name: "repository-" + suffix, GitURL: "https://example.com/repository.git",
		GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "template-" + suffix, Playbook: "deploy.yml",
	})
	require.NoError(t, err)
	loaded, err := store.GetTemplate(project.ID, template.ID)
	require.NoError(t, err)
	return store, project.ID, loaded
}

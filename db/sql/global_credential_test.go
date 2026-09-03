package sql

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalCredentialRepositoryFiltersByProjectOperationAndExpiry(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "credential-owner", Name: "Credential Owner", Email: "credential-owner@example.test"})
	require.NoError(t, err)
	firstProject, err := store.CreateProject(db.Project{Name: "Credential Project One"})
	require.NoError(t, err)
	secondProject, err := store.CreateProject(db.Project{Name: "Credential Project Two"})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	record, version, err := store.CreateGlobalCredential(db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Deploy token", OwnerUserID: owner.ID, Enabled: true, Created: now}, db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
	require.NoError(t, err)
	assert.NotEmpty(t, version.Fingerprint)

	_, err = store.CreateGlobalCredentialGrant(db.GlobalCredentialGrant{CredentialID: record.ID, ProjectID: firstProject.ID, Operations: db.GlobalCredentialGrantOperationReference, Status: db.GlobalCredentialGrantStatusActive, CreatedByUserID: owner.ID, Created: now})
	require.NoError(t, err)
	_, err = store.CreateGlobalCredentialGrant(db.GlobalCredentialGrant{CredentialID: record.ID, ProjectID: secondProject.ID, Operations: db.GlobalCredentialGrantOperationConsume, Status: db.GlobalCredentialGrantStatusActive, CreatedByUserID: owner.ID, Created: now})
	require.NoError(t, err)
	visible, err := store.GetEffectiveGlobalCredentialMetadata(firstProject.ID, now, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, visible, 1)
	assert.Equal(t, record.ID, visible[0].CredentialID)
	other, err := store.GetEffectiveGlobalCredentialMetadata(secondProject.ID, now, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Empty(t, other, "consume-only grants must not appear in metadata selection")

	expires := now.Add(time.Second)
	grant, err := store.GetGlobalCredentialGrants(record.ID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	updated := grant[0]
	updated.ExpiresAt = &expires
	_, err = store.UpdateGlobalCredentialGrant(updated, updated.Revision, now)
	require.NoError(t, err)
	visible, err = store.GetEffectiveGlobalCredentialMetadata(firstProject.ID, expires, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Empty(t, visible, "expiry is strict at the exact UTC boundary")
}

func TestGlobalCredentialRepositoryGuardsStateTransitionsAndStaleWrites(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "credential-state-owner", Name: "Owner", Email: "credential-state-owner@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Credential state project"})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	credential, _, err := store.CreateGlobalCredential(db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Deploy", OwnerUserID: owner.ID, Enabled: true, Created: now}, db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
	require.NoError(t, err)
	grant, err := store.CreateGlobalCredentialGrant(db.GlobalCredentialGrant{CredentialID: credential.ID, ProjectID: project.ID, Operations: db.GlobalCredentialGrantOperationReference, Status: db.GlobalCredentialGrantStatusActive, CreatedByUserID: owner.ID, Created: now})
	require.NoError(t, err)

	assert.ErrorIs(t, store.DeleteGlobalCredential(credential.ID, credential.Revision), db.ErrGlobalCredentialEnabled)
	disabled, err := store.SetGlobalCredentialEnabled(credential.ID, false, credential.Revision, now)
	require.NoError(t, err)
	_, err = store.SetGlobalCredentialEnabled(credential.ID, true, credential.Revision, now)
	assert.ErrorIs(t, err, db.ErrGlobalCredentialRevisionConflict)
	assert.ErrorIs(t, store.DeleteGlobalCredential(credential.ID, disabled.Revision), db.ErrGlobalCredentialGrantsExist)

	actorID := owner.ID
	revoked, err := store.SetGlobalCredentialGrantStatus(credential.ID, grant.ID, db.GlobalCredentialGrantStatusRevoked, grant.Revision, &actorID, now)
	require.NoError(t, err)
	assert.ErrorIs(t, store.DeleteGlobalCredentialGrant(credential.ID, grant.ID, grant.Revision), db.ErrGlobalCredentialGrantConflict)
	_, err = store.SetGlobalCredentialGrantStatus(credential.ID, grant.ID, db.GlobalCredentialGrantStatusRevoked, revoked.Revision, &actorID, now)
	assert.ErrorIs(t, err, db.ErrInvalidOperation)
	restored, err := store.SetGlobalCredentialGrantStatus(credential.ID, grant.ID, db.GlobalCredentialGrantStatusActive, revoked.Revision, &actorID, now)
	require.NoError(t, err)
	assert.Nil(t, restored.RevokedAt)
	revoked, err = store.SetGlobalCredentialGrantStatus(credential.ID, grant.ID, db.GlobalCredentialGrantStatusRevoked, restored.Revision, &actorID, now)
	require.NoError(t, err)
	require.NoError(t, store.DeleteGlobalCredentialGrant(credential.ID, grant.ID, revoked.Revision))
	require.NoError(t, store.DeleteGlobalCredential(credential.ID, disabled.Revision))
}

func TestGlobalCredentialRepositoryConcurrentRotationUsesRevisionCAS(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "credential-race-owner", Name: "Owner", Email: "race-owner@example.test"})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	credential, _, err := store.CreateGlobalCredential(
		db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Deploy", OwnerUserID: owner.ID, Enabled: true, Created: now},
		db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope-initial", CreatedByUserID: owner.ID},
	)
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, _, rotateErr := store.RotateGlobalCredential(credential.ID, credential.Revision,
				db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope-race-" + string(rune('a'+index)), CreatedByUserID: owner.ID}, now)
			errs <- rotateErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(errs)

	successes, conflicts := 0, 0
	for rotateErr := range errs {
		if rotateErr == nil {
			successes++
			continue
		}
		if errors.Is(rotateErr, db.ErrGlobalCredentialRevisionConflict) {
			conflicts++
			continue
		}
		require.NoError(t, rotateErr)
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	current, err := store.GetGlobalCredential(credential.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, current.Revision)
	assert.Equal(t, 2, current.CurrentVersion)
	versions, err := store.GetGlobalCredentialVersions(credential.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 2)
}

func TestGlobalCredentialUsageHistoryIsBoundedAndTaskScoped(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{
		Username: "credential-usage-owner", Name: "Owner", Email: "credential-usage@example.test",
	})
	require.NoError(t, err)
	firstProject, err := store.CreateProject(db.Project{Name: "Credential usage project one"})
	require.NoError(t, err)
	secondProject, err := store.CreateProject(db.Project{Name: "Credential usage project two"})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	credential, _, err := store.CreateGlobalCredential(
		db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Usage", OwnerUserID: owner.ID, Enabled: true, Created: now},
		db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID},
	)
	require.NoError(t, err)
	for index, usage := range []db.GlobalCredentialUsage{
		{TaskID: 41, ProjectID: firstProject.ID, ActorID: owner.ID, Target: "token", CredentialID: credential.ID, Outcome: "allowed", Reason: "allowed"},
		{TaskID: 41, ProjectID: firstProject.ID, ActorID: owner.ID, DispatchGeneration: 1, Target: "retry_token", CredentialID: credential.ID, Outcome: "allowed", Reason: "allowed"},
		{TaskID: 41, ProjectID: secondProject.ID, ActorID: owner.ID, Target: "token", CredentialID: credential.ID, Outcome: "denied", Reason: "grant_unavailable"},
		{TaskID: 42, ProjectID: firstProject.ID, ActorID: owner.ID, Target: "token", CredentialID: credential.ID, Outcome: "failure", Reason: "provider_unavailable"},
	} {
		usage.OccurredAt = now.Add(time.Duration(index) * time.Minute)
		_, err = store.CreateGlobalCredentialUsage(usage)
		require.NoError(t, err)
	}

	firstTask, err := store.GetTaskGlobalCredentialUsage(firstProject.ID, 41, db.GlobalCredentialUsageQuery{Count: 10})
	require.NoError(t, err)
	require.Len(t, firstTask, 2)
	assert.Equal(t, firstProject.ID, firstTask[0].ProjectID)
	assert.Equal(t, "allowed", firstTask[0].Outcome)
	initialGeneration := 0
	initialTask, err := store.GetTaskGlobalCredentialUsage(firstProject.ID, 41, db.GlobalCredentialUsageQuery{Count: 10, DispatchGeneration: &initialGeneration})
	require.NoError(t, err)
	require.Len(t, initialTask, 1)
	assert.Equal(t, 0, initialTask[0].DispatchGeneration)

	failures := "failure"
	filtered, err := store.GetGlobalCredentialUsage(credential.ID, db.GlobalCredentialUsageQuery{
		ProjectID: &firstProject.ID, Outcome: &failures, Count: 1,
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, 42, filtered[0].TaskID)

	impact, err := store.GetGlobalCredentialImpact(credential.ID, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 4, impact.UsageCount)
	assert.Equal(t, 2, impact.ProjectCount)
	require.NotNil(t, impact.LastUsedAt)
	assert.Equal(t, now.Add(3*time.Minute), *impact.LastUsedAt)
}

func TestGlobalCredentialGrantProjectSelectorIsOrderedAndBounded(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	for index := 0; index < globalCredentialGrantProjectLimit+1; index++ {
		_, err := store.CreateProject(db.Project{Name: fmt.Sprintf("selector-%03d", index)})
		require.NoError(t, err)
	}

	projects, err := store.GetGlobalCredentialGrantProjects()
	require.NoError(t, err)
	require.Len(t, projects, globalCredentialGrantProjectLimit)
	assert.Equal(t, "selector-000", projects[0].Name)
	assert.Equal(t, "selector-199", projects[len(projects)-1].Name)
}

func TestGlobalCredentialDeleteMapsOnlyProvenForeignKeyDependencies(t *testing.T) {
	foreignKey := errors.New("cannot delete or update a parent row: a foreign key constraint fails")
	assert.ErrorIs(t, mapGlobalCredentialDeleteError(foreignKey), db.ErrGlobalCredentialGrantsExist)

	unrelatedConstraint := errors.New("check constraint fails")
	assert.ErrorIs(t, mapGlobalCredentialDeleteError(unrelatedConstraint), unrelatedConstraint)
}

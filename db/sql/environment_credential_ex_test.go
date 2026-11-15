package sql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentCredentialReferencesRoundTrip(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "env-credential-owner", Name: "Owner", Email: "env-credential-owner@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Env credential project"})
	require.NoError(t, err)
	now := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	credential, _, err := store.CreateGlobalCredential(
		db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "License key", OwnerUserID: owner.ID, Enabled: true, Created: now},
		db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
	require.NoError(t, err)
	empty := "{}"

	created, err := store.CreateEnvironment(db.Environment{
		ProjectID: project.ID, Name: "Deploy", JSON: "{}", ENV: &empty,
		Credentials: []db.EnvironmentCredential{
			{Name: "license_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: credential.ID},
			{Name: "TF_VAR_license", Target: db.EnvironmentCredentialTargetEnv, CredentialID: credential.ID},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []db.EnvironmentCredential{
		{Name: "TF_VAR_license", Target: db.EnvironmentCredentialTargetEnv, CredentialID: credential.ID},
		{Name: "license_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: credential.ID},
	}, created.Credentials)

	t.Run("an update without the field keeps the references", func(t *testing.T) {
		loaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		loaded.Name = "Deploy renamed"
		loaded.Credentials = nil
		loaded.CredentialsSet = false
		require.NoError(t, store.UpdateEnvironment(loaded))
		reloaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		assert.Len(t, reloaded.Credentials, 2)
	})

	t.Run("an explicit list replaces the references", func(t *testing.T) {
		loaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		loaded.Credentials = []db.EnvironmentCredential{{Name: "api_token", Target: db.EnvironmentCredentialTargetVar, CredentialID: credential.ID}}
		loaded.CredentialsSet = true
		require.NoError(t, store.UpdateEnvironment(loaded))
		reloaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		assert.Equal(t, loaded.Credentials, reloaded.Credentials)
		listed, err := store.GetEnvironments(project.ID, db.RetrieveQueryParams{})
		require.NoError(t, err)
		require.Len(t, listed, 1)
		assert.Equal(t, loaded.Credentials, listed[0].Credentials)
	})

	t.Run("invalid references are rejected", func(t *testing.T) {
		loaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		loaded.Credentials = []db.EnvironmentCredential{{Name: "Not-Valid", Target: db.EnvironmentCredentialTargetVar, CredentialID: credential.ID}}
		loaded.CredentialsSet = true
		assert.Error(t, store.UpdateEnvironment(loaded))
	})

	t.Run("a reference outlives a deleted credential", func(t *testing.T) {
		loaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		gone, _, err := store.CreateGlobalCredential(
			db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Gone", OwnerUserID: owner.ID, Enabled: false, Created: now},
			db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
		require.NoError(t, err)
		loaded.Credentials = []db.EnvironmentCredential{{Name: "gone_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: gone.ID}}
		loaded.CredentialsSet = true
		require.NoError(t, store.UpdateEnvironment(loaded))
		_, err = store.exec("delete from global_credential where id=?", gone.ID)
		require.NoError(t, err)
		reloaded, err := store.GetEnvironment(project.ID, created.ID)
		require.NoError(t, err)
		assert.Equal(t, loaded.Credentials, reloaded.Credentials, "the task must be blocked, not run without the value")
	})

	t.Run("deleting the variable group removes its references", func(t *testing.T) {
		require.NoError(t, store.DeleteEnvironment(project.ID, created.ID))
		var count int
		require.NoError(t, store.selectOne(&count, "select count(*) from project__environment_credential where environment_id=?", created.ID))
		assert.Zero(t, count)
	})
}

func TestEnvironmentCredentialsDecodeKeepsOmittedFieldApart(t *testing.T) {
	var omitted, explicit db.Environment
	require.NoError(t, json.Unmarshal([]byte(`{"name":"a","json":"{}"}`), &omitted))
	require.NoError(t, json.Unmarshal([]byte(`{"name":"a","json":"{}","credentials":[]}`), &explicit))
	assert.False(t, omitted.CredentialsSet)
	assert.True(t, explicit.CredentialsSet)
}

func TestValidateEnvironmentCredentials(t *testing.T) {
	tests := []struct {
		name  string
		input []db.EnvironmentCredential
		valid bool
	}{
		{"extra variable", []db.EnvironmentCredential{{Name: "db_password", Target: db.EnvironmentCredentialTargetVar, CredentialID: 1}}, true},
		{"environment variable", []db.EnvironmentCredential{{Name: "TF_VAR_token", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1}}, true},
		{"uppercase extra variable", []db.EnvironmentCredential{{Name: "DB", Target: db.EnvironmentCredentialTargetVar, CredentialID: 1}}, false},
		{"lowercase environment variable", []db.EnvironmentCredential{{Name: "token", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1}}, true},
		{"environment variable with a dash", []db.EnvironmentCredential{{Name: "MY-TOKEN", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1}}, false},
		{"environment variable starting with a digit", []db.EnvironmentCredential{{Name: "1TOKEN", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1}}, false},
		{"names differing only by case", []db.EnvironmentCredential{
			{Name: "TOKEN", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
			{Name: "token", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
		}, false},
		{"same name for both targets", []db.EnvironmentCredential{
			{Name: "token", Target: db.EnvironmentCredentialTargetVar, CredentialID: 1},
			{Name: "token", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
		}, false},
		{"reserved environment variable", []db.EnvironmentCredential{{Name: "SEMAPHORE_JWT", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1}}, false},
		{"unknown target", []db.EnvironmentCredential{{Name: "token", Target: "file", CredentialID: 1}}, false},
		{"missing credential", []db.EnvironmentCredential{{Name: "token", Target: db.EnvironmentCredentialTargetVar}}, false},
		{"duplicate name", []db.EnvironmentCredential{
			{Name: "token", Target: db.EnvironmentCredentialTargetVar, CredentialID: 1},
			{Name: "token", Target: db.EnvironmentCredentialTargetVar, CredentialID: 2},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.ValidateEnvironmentCredentials(tt.input)
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestValidateGlobalCredentialGrantUnattendedNeedsConsume(t *testing.T) {
	base := db.GlobalCredentialGrant{CredentialID: 1, ProjectID: 2, Revision: 1, CreatedByUserID: 3, Status: db.GlobalCredentialGrantStatusActive}
	tests := []struct {
		name       string
		operations db.GlobalCredentialGrantOperation
		valid      bool
	}{
		{"consume and unattended", db.GlobalCredentialGrantOperationConsume | db.GlobalCredentialGrantOperationConsumeUnattended, true},
		{"all operations", db.GlobalCredentialGrantOperationReference | db.GlobalCredentialGrantOperationConsume | db.GlobalCredentialGrantOperationConsumeUnattended, true},
		{"unattended without consume", db.GlobalCredentialGrantOperationReference | db.GlobalCredentialGrantOperationConsumeUnattended, false},
		{"unknown operation bit", db.GlobalCredentialGrantOperation(8), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grant := base
			grant.Operations = tt.operations
			if tt.valid {
				assert.NoError(t, db.ValidateGlobalCredentialGrant(grant))
			} else {
				assert.Error(t, db.ValidateGlobalCredentialGrant(grant))
			}
		})
	}
}

package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateVersionSnapshotExcludesVaultPayloadAndHasStableFingerprint(t *testing.T) {
	script := "echo deploy"
	resolvedCredential := "resolved-secret-must-not-enter-snapshot"
	snapshot, err := NewTemplateVersionSnapshot(Template{
		RepositoryID: 8, Name: "Deploy", Playbook: "deploy.yml", EnvironmentIDs: []int{7, 3},
		Vaults: []TemplateVault{
			{ID: 9, Type: TemplateVaultPassword, VaultKeyID: intPointer(11), Vault: &AccessKey{Secret: &resolvedCredential}},
			{ID: 5, Type: TemplateVaultScript, Script: &script, Vault: &AccessKey{Secret: &resolvedCredential}},
		},
	})
	require.NoError(t, err)
	payload, err := snapshot.CanonicalJSON()
	require.NoError(t, err)
	assert.NotContains(t, string(payload), resolvedCredential)
	require.Len(t, snapshot.Dependencies.Vaults, 2)
	assert.Equal(t, []int{5, 9}, []int{snapshot.Dependencies.Vaults[0].ID, snapshot.Dependencies.Vaults[1].ID})
	assert.Equal(t, script, *snapshot.Dependencies.Vaults[0].Script)
	assert.Equal(t, 11, *snapshot.Dependencies.Vaults[1].VaultKeyID)
	fingerprint, err := TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	assert.Len(t, fingerprint, len("sha256:")+64)
}

func TestTemplateVersionSnapshotDeepCopiesPointerSliceAndMapMetadata(t *testing.T) {
	argument := "[\"initial\"]"
	branch := "main"
	script := "echo initial"
	name := "vault"
	params := MapStringAnyField{"nested": map[string]any{"value": "initial"}}
	template := Template{
		RepositoryID: 1, Name: "Deploy", Playbook: "deploy.yml", Arguments: &argument, GitBranch: &branch,
		RunnerTags: StringArrayField{"linux"}, TaskParams: params,
		JWTParams: &TemplateJWTParams{Enabled: true, Audience: []string{"deploy"}},
		Vaults:    []TemplateVault{{ID: 2, Type: TemplateVaultScript, Name: &name, Script: &script}},
	}
	snapshot, err := NewTemplateVersionSnapshot(template)
	require.NoError(t, err)
	argument = "[\"mutated\"]"
	branch = "other"
	template.RunnerTags[0] = "mutated"
	params["nested"].(map[string]any)["value"] = "mutated"
	template.JWTParams.Audience[0] = "mutated"
	script = "echo mutated"
	name = "other vault"
	assert.Equal(t, "[\"initial\"]", *snapshot.Execution.Arguments)
	assert.Equal(t, "main", *snapshot.Execution.GitBranch)
	assert.Equal(t, "linux", snapshot.Execution.RunnerTags[0])
	assert.Equal(t, "initial", snapshot.Execution.TaskParams["nested"].(map[string]any)["value"])
	assert.Equal(t, "deploy", snapshot.Execution.JWTParams.Audience[0])
	assert.Equal(t, "echo initial", *snapshot.Dependencies.Vaults[0].Script)
	assert.Equal(t, "vault", *snapshot.Dependencies.Vaults[0].Name)
}

func TestTemplateVersionSnapshotReconstructsWithoutMutableBuildTemplateLink(t *testing.T) {
	snapshot, err := NewTemplateVersionSnapshot(Template{
		RepositoryID: 9, Name: "Deploy", Playbook: "deploy.yml",
	})
	require.NoError(t, err)
	snapshot.Dependencies.BuildTemplateVersion = &TemplateVersionReference{
		OwnerProjectID: 4, TemplateID: 8, VersionNumber: 3,
		ContentFingerprint: "sha256:0f5d5ab6b88531f41c7f2ac5c9ecf1af1904590ebc1b3d16279056856a10a257",
	}
	template, err := snapshot.ReconstructTemplate(4, 12)
	require.NoError(t, err)
	assert.Equal(t, 4, template.ProjectID)
	assert.Equal(t, 12, template.ID)
	assert.Nil(t, template.BuildTemplateID)
	require.NotNil(t, snapshot.Dependencies.BuildTemplateVersion)
	assert.Equal(t, 8, snapshot.Dependencies.BuildTemplateVersion.TemplateID)
	assert.Equal(t, 3, snapshot.Dependencies.BuildTemplateVersion.VersionNumber)
}

func TestCrossProjectTemplateGrantLifecycleRequiresExpectedRevision(t *testing.T) {
	created := time.Now().UTC().Add(-time.Minute)
	grant := CrossProjectTemplateGrant{
		OwnerProjectID: 1, ConsumerProjectID: 2, TemplateID: 3,
		MinTemplateVersion: 4, MaxTemplateVersion: 5,
		Operations: CrossProjectTemplateGrantReference | CrossProjectTemplateGrantRun,
		Status:     CrossProjectTemplateGrantPending, Revision: 1,
		CreatedByUserID: 6, Created: created, Reason: "release workflow",
	}
	require.NoError(t, grant.Validate())
	_, err := grant.Accept(8, 2, time.Now().UTC())
	assert.ErrorIs(t, err, ErrCrossProjectTemplateGrantRevisionConflict)
	accepted, err := grant.Accept(8, 1, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, CrossProjectTemplateGrantActive, accepted.Status)
	assert.Equal(t, 2, accepted.Revision)
	_, err = accepted.Revoke(6, 1, "rotated", time.Now().UTC())
	assert.ErrorIs(t, err, ErrCrossProjectTemplateGrantRevisionConflict)
	revoked, err := accepted.Revoke(6, 2, "rotated", time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, accepted.BlocksTemplateVersionDeletion(1, 3, 4))
	assert.False(t, revoked.BlocksTemplateVersionDeletion(1, 3, 4))
}

func TestCrossProjectTemplateGrantUpdateIsPendingOnly(t *testing.T) {
	created := time.Now().UTC().Add(-time.Minute)
	grant := CrossProjectTemplateGrant{
		OwnerProjectID: 1, ConsumerProjectID: 2, TemplateID: 3,
		MinTemplateVersion: 1, MaxTemplateVersion: 1, Operations: CrossProjectTemplateGrantReference,
		Status: CrossProjectTemplateGrantPending, Revision: 1, CreatedByUserID: 4, Created: created,
	}
	updated, err := grant.Update(CrossProjectTemplateGrantUpdate{
		MinTemplateVersion: 1, MaxTemplateVersion: 2,
		Operations: CrossProjectTemplateGrantReference | CrossProjectTemplateGrantRun,
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	assert.True(t, updated.Operations.Allows(CrossProjectTemplateGrantRun))
	accepted, err := updated.Accept(5, 2, time.Now().UTC())
	require.NoError(t, err)
	_, err = accepted.Update(CrossProjectTemplateGrantUpdate{
		MinTemplateVersion: 1, MaxTemplateVersion: 2, Operations: CrossProjectTemplateGrantReference,
	}, accepted.Revision)
	assert.ErrorIs(t, err, ErrCrossProjectTemplateGrantInvalidTransition)
}

func intPointer(value int) *int {
	return &value
}

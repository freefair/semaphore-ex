package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	storepkg "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type globalCredentialTestCipher struct{}

func (globalCredentialTestCipher) OptionEncryptionEnabled() bool { return true }
func (globalCredentialTestCipher) EncryptOption(value []byte) (string, error) {
	return "sealed:" + string(value), nil
}

type disabledGlobalCredentialTestCipher struct{}

func (disabledGlobalCredentialTestCipher) OptionEncryptionEnabled() bool { return false }
func (disabledGlobalCredentialTestCipher) EncryptOption([]byte) (string, error) {
	return "", nil
}

func TestGlobalCredentialRejectsLocalMaterialWhenEncryptionIsDisabled(t *testing.T) {
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "gc-disabled", Name: "Owner", Email: "disabled@example.test"})
	require.NoError(t, err)
	service := NewGlobalCredentialService(store, disabledGlobalCredentialTestCipher{})
	secret := "must-not-persist"
	_, err = service.CreateGlobalCredential(context.Background(), owner.ID, pro_interfaces.GlobalCredentialInput{
		Type: db.GlobalCredentialTypeString, DisplayName: "Local", Material: &pro_interfaces.GlobalCredentialMaterialInput{StringValue: &secret},
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialEncryptionRequired)
	credentials, err := store.GetGlobalCredentials(db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Empty(t, credentials)
}

func TestGlobalCredentialListAndMutationViewsDoNotExposeMetadataDetail(t *testing.T) {
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "gc-owner", Name: "Owner", Email: "owner@example.test"})
	require.NoError(t, err)
	service := NewGlobalCredentialService(store, globalCredentialTestCipher{})
	secret := "only-write"
	created, err := service.CreateGlobalCredential(context.Background(), owner.ID, pro_interfaces.GlobalCredentialInput{
		Type: db.GlobalCredentialTypeString, DisplayName: "Deploy", Material: &pro_interfaces.GlobalCredentialMaterialInput{StringValue: &secret},
	})
	require.NoError(t, err)
	assert.NotZero(t, created.ID)

	list, err := service.ListGlobalCredentials(context.Background(), db.RetrieveQueryParams{Count: 25})
	require.NoError(t, err)
	require.Len(t, list, 1)
	payload, err := json.Marshal(list[0])
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "owner_user_id")
	assert.NotContains(t, string(payload), "external_reference")
	assert.NotContains(t, string(payload), "sealed:")

	detail, err := service.GetGlobalCredential(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, owner.ID, detail.OwnerUserID)
}

func TestGlobalCredentialExternalReferenceNeverAppearsInSummary(t *testing.T) {
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "gc-owner-ref", Name: "Owner", Email: "owner-ref@example.test"})
	require.NoError(t, err)
	service := NewGlobalCredentialService(store, globalCredentialTestCipher{})
	reference := db.GlobalCredentialExternalReference{Provider: "vault", ProviderID: "provider", Mount: "kv", Path: "team/deploy", Version: 1, Field: "token"}
	created, err := service.CreateGlobalCredential(context.Background(), owner.ID, pro_interfaces.GlobalCredentialInput{
		Type: db.GlobalCredentialTypeString, DisplayName: "External", Material: &pro_interfaces.GlobalCredentialMaterialInput{ExternalReference: &reference},
	})
	require.NoError(t, err)
	payload, err := json.Marshal(created)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "team/deploy")
	assert.NotContains(t, string(payload), "provider")
}

func TestGlobalCredentialGrantProjectSelectorReturnsOnlyBoundedIdentity(t *testing.T) {
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	_, err := store.CreateProject(db.Project{Name: "Zulu"})
	require.NoError(t, err)
	_, err = store.CreateProject(db.Project{Name: "Alpha"})
	require.NoError(t, err)

	projects, err := NewGlobalCredentialService(store, globalCredentialTestCipher{}).
		ListGlobalCredentialGrantProjects(context.Background())

	require.NoError(t, err)
	require.Len(t, projects, 2)
	assert.Equal(t, "Alpha", projects[0].Name)
	payload, err := json.Marshal(projects[0])
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	assert.Len(t, fields, 2)
}

func TestGlobalCredentialGrantProjectSelectorCapsResults(t *testing.T) {
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	for index := 0; index < 201; index++ {
		_, err := store.CreateProject(db.Project{Name: fmt.Sprintf("selector-cap-%03d", index)})
		require.NoError(t, err)
	}

	projects, err := NewGlobalCredentialService(store, globalCredentialTestCipher{}).
		ListGlobalCredentialGrantProjects(context.Background())

	require.NoError(t, err)
	require.Len(t, projects, 200)
	assert.Equal(t, "selector-cap-000", projects[0].Name)
	assert.Equal(t, "selector-cap-199", projects[len(projects)-1].Name)
}

func TestGlobalCredentialServiceRejectsMalformedInputsBeforePersistence(t *testing.T) {
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "gc-invalid", Name: "Owner", Email: "invalid@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Invalid input project"})
	require.NoError(t, err)
	service := NewGlobalCredentialService(store, globalCredentialTestCipher{})

	malformedReference := db.GlobalCredentialExternalReference{Provider: "vault", ProviderID: "provider", Mount: "kv", Path: "../escape", Version: 1, Field: "token"}
	_, err = service.CreateGlobalCredential(context.Background(), owner.ID, pro_interfaces.GlobalCredentialInput{
		Type: db.GlobalCredentialTypeString, DisplayName: "External", Material: &pro_interfaces.GlobalCredentialMaterialInput{ExternalReference: &malformedReference},
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialInvalidInput)

	secret := "write-only"
	credential, err := service.CreateGlobalCredential(context.Background(), owner.ID, pro_interfaces.GlobalCredentialInput{
		Type: db.GlobalCredentialTypeString, DisplayName: "Local", Material: &pro_interfaces.GlobalCredentialMaterialInput{StringValue: &secret},
	})
	require.NoError(t, err)
	_, err = service.CreateGlobalCredentialGrant(context.Background(), owner.ID, credential.ID, pro_interfaces.GlobalCredentialGrantInput{
		ProjectID: project.ID, Operations: db.GlobalCredentialGrantOperation(1 << 10),
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialInvalidInput)

	_, err = service.UpdateGlobalCredential(context.Background(), owner.ID, credential.ID, credential.Revision, pro_interfaces.GlobalCredentialMetadataInput{
		DisplayName: string(make([]byte, 129)),
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialInvalidInput)
}

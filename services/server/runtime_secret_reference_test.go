package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeSecretWriteCapability struct{ enabled bool }

func (p runtimeSecretWriteCapability) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	state := pro_interfaces.CapabilityStateDisabled
	reason := pro_interfaces.CapabilityReasonDisabledByAdmin
	access := []pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead}
	if p.enabled {
		state = pro_interfaces.CapabilityStateActive
		reason = pro_interfaces.CapabilityReasonActive
		access = append(access, pro_interfaces.CapabilityAccessWrite, pro_interfaces.CapabilityAccessExecute)
	}
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets, state, reason, access, nil,
		),
	}), nil
}

func (p runtimeSecretWriteCapability) Configure(
	context.Context,
	pro_interfaces.CapabilityRequest,
	pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.CapabilitySnapshot{}, nil
}

type capturingRuntimeAccessKeyRepo struct {
	db.AccessKeyManager
	created  db.AccessKey
	updated  db.AccessKey
	existing db.AccessKey
}

func (r *capturingRuntimeAccessKeyRepo) CreateAccessKey(key db.AccessKey) (db.AccessKey, error) {
	r.created = key
	key.ID = 12
	return key, nil
}

func (r *capturingRuntimeAccessKeyRepo) GetAccessKey(int, int) (db.AccessKey, error) {
	return r.existing, nil
}

func (r *capturingRuntimeAccessKeyRepo) UpdateAccessKey(key db.AccessKey) error {
	r.updated = key
	return nil
}

type runtimeReadOnlyEncryption struct{ AccessKeyEncryptionService }

func (runtimeReadOnlyEncryption) SerializeSecret(*db.AccessKey) error {
	return ErrReadOnlyStorage
}

type runtimeStorageRepository struct {
	db.SecretStorageRepository
	storage db.SecretStorage
}

func (r *runtimeStorageRepository) GetSecretStorage(projectID int, storageID int) (db.SecretStorage, error) {
	if r.storage.ProjectID != projectID || r.storage.ID != storageID {
		return db.SecretStorage{}, db.ErrNotFound
	}
	return r.storage, nil
}

func (r *runtimeStorageRepository) CreateSecretStorage(storage db.SecretStorage) (db.SecretStorage, error) {
	storage.ID = 9
	r.storage = storage
	return storage, nil
}

func TestRuntimeSecretAccessKeyPersistsCanonicalReferenceWithoutPlaintext(t *testing.T) {
	projectID := 3
	storageID := 9
	storageRepo := &runtimeStorageRepository{storage: db.SecretStorage{
		ID: storageID, ProjectID: projectID, Type: db.SecretStorageTypeVault,
		Params: db.MapStringAnyField{"mount": "team"}, ReadOnly: true,
	}}
	accessRepo := &capturingRuntimeAccessKeyRepo{}
	encryption := NewAccessKeyEncryptionService(accessRepo, nil, storageRepo, nil)
	service := NewAccessKeyService(
		accessRepo, encryption, storageRepo, runtimeSecretWriteCapability{enabled: true},
	)
	sourceType := db.AccessKeySourceStorageVault
	path := "apps/api"

	_, err := service.Create(db.AccessKey{
		Name: "runtime", Type: db.AccessKeyString, ProjectID: &projectID,
		String: "must-never-be-persisted", SourceStorageID: &storageID,
		SourceStorageType: &sourceType, SourceStorageKey: &path,
		SourceStorageVersion: 4, SourceStorageField: "password",
	})

	require.NoError(t, err)
	require.NotNil(t, accessRepo.created.SourceStorageKey)
	reference, err := pro_interfaces.DecodeSecretReference(*accessRepo.created.SourceStorageKey)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.SecretReference{
		StorageID: storageID, Mount: "team", Path: path, Version: 4, Field: "password",
	}, reference)
	assert.Empty(t, accessRepo.created.String)
	assert.Nil(t, accessRepo.created.Secret)
	encoded, err := json.Marshal(accessRepo.created)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "must-never-be-persisted")

	exposed := accessRepo.created
	ExposeRuntimeSecretReference(&exposed)
	assert.Equal(t, path, *exposed.SourceStorageKey)
	assert.Equal(t, "team", exposed.SourceStorageMount)
	assert.Equal(t, 4, exposed.SourceStorageVersion)
	assert.Equal(t, "password", exposed.SourceStorageField)
}

func TestRuntimeSecretAccessKeyWriteIsBlockedWhenCapabilityIsDisabled(t *testing.T) {
	projectID := 3
	storageID := 9
	storageRepo := &runtimeStorageRepository{storage: db.SecretStorage{
		ID: storageID, ProjectID: projectID, Type: db.SecretStorageTypeVault,
		Params: db.MapStringAnyField{"mount": "team"}, ReadOnly: true,
	}}
	accessRepo := &capturingRuntimeAccessKeyRepo{}
	encryption := NewAccessKeyEncryptionService(accessRepo, nil, storageRepo, nil)
	service := NewAccessKeyService(
		accessRepo, encryption, storageRepo, runtimeSecretWriteCapability{enabled: false},
	)
	sourceType := db.AccessKeySourceStorageVault
	path := "apps/api"

	_, err := service.Create(db.AccessKey{
		Name: "runtime", Type: db.AccessKeyString, ProjectID: &projectID,
		SourceStorageID: &storageID, SourceStorageType: &sourceType, SourceStorageKey: &path,
		SourceStorageField: "password",
	})

	var denied pro_interfaces.CapabilityDeniedError
	require.ErrorAs(t, err, &denied)
	assert.Empty(t, accessRepo.created.Name)
}

func TestRuntimeSecretReferenceRequiresProjectScopedVaultOrOpenBaoStorage(t *testing.T) {
	projectID := 3
	storageID := 9
	sourceType := db.AccessKeySourceStorageVault
	path := "apps/api"
	accessRepo := &capturingRuntimeAccessKeyRepo{}
	storageRepo := &runtimeStorageRepository{storage: db.SecretStorage{
		ID: storageID, ProjectID: projectID, Type: db.SecretStorageTypeAwsSm,
	}}
	service := NewAccessKeyService(
		accessRepo, runtimeReadOnlyEncryption{}, storageRepo,
		runtimeSecretWriteCapability{enabled: true},
	)

	_, err := service.Create(db.AccessKey{
		Name: "runtime", Type: db.AccessKeyString, ProjectID: &projectID,
		SourceStorageID: &storageID, SourceStorageType: &sourceType, SourceStorageKey: &path,
		SourceStorageField: "password",
	})

	require.Error(t, err)
	assert.Empty(t, accessRepo.created.Name)
}

func TestRuntimeSecretReferenceCanBeUpdatedWithoutWritingProviderValue(t *testing.T) {
	projectID := 3
	storageID := 9
	sourceType := db.AccessKeySourceStorageVault
	oldEncoded, err := (pro_interfaces.SecretReference{
		StorageID: storageID, Mount: "team", Path: "apps/old", Field: "password",
	}).Encode()
	require.NoError(t, err)
	accessRepo := &capturingRuntimeAccessKeyRepo{existing: db.AccessKey{
		ID: 12, ProjectID: &projectID, SourceStorageID: &storageID,
		SourceStorageType: &sourceType, SourceStorageKey: &oldEncoded,
	}}
	storageRepo := &runtimeStorageRepository{storage: db.SecretStorage{
		ID: storageID, ProjectID: projectID, Type: db.SecretStorageTypeVault,
		Params: db.MapStringAnyField{"mount": "team"}, ReadOnly: true,
	}}
	service := NewAccessKeyService(
		accessRepo, runtimeReadOnlyEncryption{}, storageRepo,
		runtimeSecretWriteCapability{enabled: true},
	)
	newPath := "apps/new"

	err = service.Update(db.AccessKey{
		ID: 12, ProjectID: &projectID, Name: "runtime", Type: db.AccessKeyString,
		SourceStorageID: &storageID, SourceStorageType: &sourceType,
		SourceStorageKey: &newPath, SourceStorageMount: "team", SourceStorageField: "token",
		OverrideSecret: true,
	})

	require.NoError(t, err)
	require.NotNil(t, accessRepo.updated.SourceStorageKey)
	reference, err := pro_interfaces.DecodeSecretReference(*accessRepo.updated.SourceStorageKey)
	require.NoError(t, err)
	assert.Equal(t, "apps/new", reference.Path)
	assert.Equal(t, "token", reference.Field)
	assert.Empty(t, accessRepo.updated.String)
}

func TestSecretStorageBootstrapCredentialIsEncryptedAndWriteOnly(t *testing.T) {
	previous := util.Config
	defer func() { util.Config = previous }()
	util.Config = &util.ConfigType{
		AccessKeyEncryption: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)),
	}
	projectID := 3
	accessRepo := &capturingRuntimeAccessKeyRepo{}
	storageRepo := &runtimeStorageRepository{}
	encryption := NewAccessKeyEncryptionService(accessRepo, nil, storageRepo, nil)
	accessKeys := NewAccessKeyService(accessRepo, encryption, storageRepo)
	service := NewSecretStorageService(storageRepo, accessRepo, accessKeys, encryption)

	created, err := service.Create(db.SecretStorage{
		ProjectID: projectID, Name: "Vault", Type: db.SecretStorageTypeVault,
		Params: db.MapStringAnyField{"url": "https://vault.example", "auth_method": "token"},
		Secret: "bootstrap-token",
	})

	require.NoError(t, err)
	assert.Empty(t, created.Secret)
	assert.True(t, created.ReadOnly)
	require.NotNil(t, accessRepo.created.Secret)
	assert.NotContains(t, *accessRepo.created.Secret, "bootstrap-token")
	plaintext, err := util.Config.DecryptAccessSecret(*accessRepo.created.Secret)
	require.NoError(t, err)
	assert.Equal(t, "bootstrap-token", string(plaintext))
	for index := range plaintext {
		plaintext[index] = 0
	}
}

func TestRuntimeSecretStorageValidationRejectsUnsafeTLSAndUnsupportedProviders(t *testing.T) {
	for _, storage := range []db.SecretStorage{
		{Type: db.SecretStorageTypeVault, Params: db.MapStringAnyField{"url": "http://vault.example"}, Secret: "x"},
		{Type: db.SecretStorageTypeVault, Params: db.MapStringAnyField{"url": "https://vault.example", "tls_skip_verify": true}, Secret: "x"},
		{Type: db.SecretStorageTypeAwsSm, Params: db.MapStringAnyField{"url": "https://aws.example"}, Secret: "x"},
	} {
		current := storage
		assert.Error(t, ValidateRuntimeSecretStorage(&current))
	}
}

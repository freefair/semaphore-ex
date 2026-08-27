package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type managedSyncStorageRepo struct {
	db.SecretStorageRepository
	storage db.SecretStorage
}

func (r managedSyncStorageRepo) GetSecretStorage(projectID int, storageID int) (db.SecretStorage, error) {
	if r.storage.ProjectID != projectID || r.storage.ID != storageID {
		return db.SecretStorage{}, db.ErrNotFound
	}
	return r.storage, nil
}

type managedSyncAccessKeyRepo struct {
	db.AccessKeyManager
	key db.AccessKey
}

func (r managedSyncAccessKeyRepo) GetAccessKey(projectID int, keyID int) (db.AccessKey, error) {
	if r.key.ProjectID == nil || *r.key.ProjectID != projectID || r.key.ID != keyID {
		return db.AccessKey{}, db.ErrNotFound
	}
	return r.key, nil
}

type managedSyncProvider struct {
	localValue  string
	remoteValue []byte
	version     int
	readErr     error
	writeErr    error
	writes      int
	expectedCAS []int
}

func (p *managedSyncProvider) DeserializeSecret(key *db.AccessKey) error {
	key.String = p.localValue
	return nil
}

func (p *managedSyncProvider) ReadManagedSecretField(
	context.Context,
	int,
	pro_interfaces.SecretReference,
) (pro_interfaces.ManagedSecretField, error) {
	if p.readErr != nil {
		return pro_interfaces.ManagedSecretField{}, p.readErr
	}
	return pro_interfaces.ManagedSecretField{
		Value: append([]byte(nil), p.remoteValue...), Version: p.version, Exists: p.remoteValue != nil,
	}, nil
}

func (p *managedSyncProvider) WriteManagedSecretField(
	_ context.Context,
	_ int,
	_ pro_interfaces.SecretReference,
	value []byte,
	expectedVersion int,
) (int, error) {
	p.writes++
	p.expectedCAS = append(p.expectedCAS, expectedVersion)
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	if expectedVersion != p.version {
		return 0, pro_interfaces.SecretProviderError{
			Category: pro_interfaces.SecretProviderErrorConflict, Operation: "write_managed",
		}
	}
	p.remoteValue = append([]byte(nil), value...)
	p.version++
	return p.version, nil
}

func TestManagedSecretSyncWritesOnceAndRetryAdoptsSameContent(t *testing.T) {
	sync, storageRepo, keyRepo := managedSyncFixture(db.SecretSyncDirectionOutbound, false)
	provider := &managedSyncProvider{localValue: "local-secret"}

	first, err := SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, nil, nil, storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	require.Len(t, first.Outcomes, 1)
	assert.Equal(t, db.SecretSyncItemChanged, first.Outcomes[0].Status)
	assert.Equal(t, []int{0}, provider.expectedCAS)
	require.Len(t, first.Paths, 1)
	assert.NotEmpty(t, first.Paths[0].ContentFingerprint)
	assert.NotContains(t, first.Paths[0].ContentFingerprint, "local-secret")

	sync.Paths = first.Paths
	retry, err := SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, nil, nil, storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncItemSkipped, retry.Outcomes[0].Status)
	assert.Equal(t, "unchanged", retry.Outcomes[0].ErrorCategory)
	assert.Equal(t, 1, provider.writes, "retry must not create a second logical write")
	encoded, err := json.Marshal(retry)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "local-secret")
}

func TestManagedSecretSyncDetectsAndExplicitlyResolvesConflict(t *testing.T) {
	sync, storageRepo, keyRepo := managedSyncFixture(db.SecretSyncDirectionOutbound, false)
	sync.Paths[0].ContentFingerprint = pro_interfaces.SecretContentFingerprint([]byte("previous"))
	sync.Paths[0].RemoteVersion = 4
	provider := &managedSyncProvider{
		localValue: "new-local", remoteValue: []byte("changed-remotely"), version: 5,
	}

	conflict, err := SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, nil, nil, storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	require.Len(t, conflict.Outcomes, 1)
	assert.Equal(t, db.SecretSyncItemConflict, conflict.Outcomes[0].Status)
	assert.Equal(t, 5, conflict.Outcomes[0].RemoteVersion)
	assert.Zero(t, provider.writes)

	resolvedOperation := &db.SecretSyncOperation{
		Status: db.SecretSyncOperationConflict,
		Outcomes: []db.SecretSyncItemOutcome{{
			MappingID: sync.Paths[0].ID, Status: db.SecretSyncItemConflict, RemoteVersion: 5,
		}},
	}
	resolved, err := SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, resolvedOperation, nil,
		storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncItemChanged, resolved.Outcomes[0].Status)
	assert.Equal(t, []int{5}, provider.expectedCAS)
	assert.Equal(t, "new-local", string(provider.remoteValue))
}

func TestManagedSecretSyncReadOnlyAndOutageRecovery(t *testing.T) {
	readOnlySync, readOnlyStorage, keyRepo := managedSyncFixture(db.SecretSyncDirectionReadOnly, true)
	provider := &managedSyncProvider{localValue: "must-not-write"}
	readOnly, err := SyncSecrets(
		context.Background(), readOnlySync, db.SecretSyncOperation{}, nil, nil,
		readOnlyStorage, keyRepo, provider,
	)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncItemSkipped, readOnly.Outcomes[0].Status)
	assert.Equal(t, "read_only", readOnly.Outcomes[0].ErrorCategory)
	assert.Zero(t, provider.writes)

	outbound, storageRepo, keyRepo := managedSyncFixture(db.SecretSyncDirectionOutbound, false)
	provider.readErr = pro_interfaces.SecretProviderError{
		Category: pro_interfaces.SecretProviderErrorUnavailable, Operation: "read_managed",
	}
	failed, err := SyncSecrets(
		context.Background(), outbound, db.SecretSyncOperation{}, nil, nil, storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncItemFailed, failed.Outcomes[0].Status)
	assert.Equal(t, "unavailable", failed.Outcomes[0].ErrorCategory)
	provider.readErr = nil
	recovered, err := SyncSecrets(
		context.Background(), outbound, db.SecretSyncOperation{}, nil, nil, storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncItemChanged, recovered.Outcomes[0].Status)
	assert.Equal(t, 1, provider.writes)
}

func TestManagedSecretSyncRejectsStaleConflictResolution(t *testing.T) {
	sync, storageRepo, keyRepo := managedSyncFixture(db.SecretSyncDirectionOutbound, false)
	provider := &managedSyncProvider{
		localValue: "new-local", remoteValue: []byte("newer-remote"), version: 8,
	}
	resolvedOperation := &db.SecretSyncOperation{
		Status: db.SecretSyncOperationConflict,
		Outcomes: []db.SecretSyncItemOutcome{{
			MappingID: sync.Paths[0].ID, Status: db.SecretSyncItemConflict, RemoteVersion: 7,
		}},
	}
	result, err := SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, resolvedOperation, nil,
		storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncItemConflict, result.Outcomes[0].Status)
	assert.Equal(t, "remote_changed", result.Outcomes[0].ErrorCategory)
	assert.Zero(t, provider.writes)
}

func TestManagedSecretSyncRenewsLeaseBeforeProviderAccess(t *testing.T) {
	sync, storageRepo, keyRepo := managedSyncFixture(db.SecretSyncDirectionOutbound, false)
	provider := &managedSyncProvider{localValue: "local-secret"}
	renewals := 0

	result, err := SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, nil, func() error {
			renewals++
			return nil
		}, storageRepo, keyRepo, provider,
	)
	require.NoError(t, err)
	require.Len(t, result.Outcomes, 1)
	assert.Equal(t, 2, renewals)
	assert.Equal(t, 1, provider.writes)

	provider.writes = 0
	_, err = SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, nil, func() error {
			return errors.New("lease unavailable")
		}, storageRepo, keyRepo, provider,
	)
	require.Error(t, err)
	assert.Zero(t, provider.writes)
}

func managedSyncFixture(
	direction db.SecretSyncDirection,
	readOnly bool,
) (db.SecretSync, managedSyncStorageRepo, managedSyncAccessKeyRepo) {
	projectID := 3
	storageID := 9
	path := db.SecretSyncPath{
		ID: 12, SyncID: 6, AccessKeyID: 17, Mount: "secret", Path: "apps/api", Field: "password",
	}
	return db.SecretSync{
			ID: 6, ProjectID: projectID, StorageID: storageID, Direction: direction,
			Revision: 2, Paths: []db.SecretSyncPath{path},
		}, managedSyncStorageRepo{storage: db.SecretStorage{
			ID: storageID, ProjectID: projectID, Type: db.SecretStorageTypeVault, ReadOnly: readOnly,
		}}, managedSyncAccessKeyRepo{key: db.AccessKey{
			ID: 17, ProjectID: &projectID, Name: "API password", Type: db.AccessKeyString,
			Owner: db.AccessKeyShared,
		}}
}

var _ DvlsStorageTokenDeserializer = (*managedSyncProvider)(nil)
var _ pro_interfaces.ManagedSecretProvider = (*managedSyncProvider)(nil)

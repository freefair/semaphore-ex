package server

import (
	"context"
	"errors"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// globalKVStore serves global Vault/OpenBao connections with the same
// parameter rules as project storages and a client of its own, so global
// and project storages never share a cached token.
type globalKVStore struct {
	client *vaultOpenBaoClient
}

// NewGlobalKVStore returns the global Vault/OpenBao port.
func NewGlobalKVStore() pro_interfaces.GlobalKVStore {
	return &globalKVStore{client: newVaultOpenBaoClient()}
}

// clientFor returns the shared client for saved connections and a fresh one,
// with an empty token cache, for drafts.
func (s *globalKVStore) clientFor(connection pro_interfaces.GlobalKVConnection) *vaultOpenBaoClient {
	if connection.Draft {
		return newVaultOpenBaoClient()
	}
	return s.client
}

func (s *globalKVStore) configuration(connection pro_interfaces.GlobalKVConnection) (pro_interfaces.SecretProviderConfiguration, error) {
	if connection.StorageID <= 0 || len(connection.Credential) == 0 {
		return pro_interfaces.SecretProviderConfiguration{}, providerError(pro_interfaces.SecretProviderErrorValidation, "configure")
	}
	configuration, err := parseProviderConfiguration(db.SecretStorage{
		ID: connection.StorageID, Type: db.SecretStorageType(connection.Type), Params: connection.Params,
	})
	if err != nil {
		return pro_interfaces.SecretProviderConfiguration{}, err
	}
	configuration.Auth.BootstrapCredential = connection.Credential
	return configuration, nil
}

func (s *globalKVStore) ValidateGlobalKV(storageType string, params map[string]any) error {
	if _, err := parseProviderConfiguration(db.SecretStorage{ID: 1, Type: db.SecretStorageType(storageType), Params: params}); err != nil {
		return errors.New("the Vault/OpenBao connection settings are invalid")
	}
	return nil
}

func (s *globalKVStore) ReadGlobalKV(
	ctx context.Context,
	connection pro_interfaces.GlobalKVConnection,
	reference pro_interfaces.SecretReference,
) ([]byte, error) {
	configuration, err := s.configuration(connection)
	if err != nil {
		return nil, err
	}
	reference.StorageID = configuration.StorageID
	return s.clientFor(connection).ReadKV(ctx, configuration, reference)
}

func (s *globalKVStore) GlobalKVIdentity(
	ctx context.Context,
	connection pro_interfaces.GlobalKVConnection,
) (pro_interfaces.SecretBrowseIdentity, error) {
	configuration, err := s.configuration(connection)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	return s.clientFor(connection).Identity(ctx, configuration)
}

func (s *globalKVStore) BrowseGlobalKV(
	ctx context.Context,
	connection pro_interfaces.GlobalKVConnection,
	location pro_interfaces.SecretBrowseLocation,
) ([]pro_interfaces.SecretBrowseNode, error) {
	configuration, err := s.configuration(connection)
	if err != nil {
		return nil, err
	}
	return pro_interfaces.BrowseKV(ctx, s.clientFor(connection), configuration, location)
}

var _ pro_interfaces.GlobalKVStore = (*globalKVStore)(nil)

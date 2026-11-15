package server

import (
	"context"
	"errors"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// RuntimeSecretProviderIdentity tests a project Vault/OpenBao storage through
// the runtime resolver, which holds its credentials.
func (s *accessKeyEncryptionServiceImpl) RuntimeSecretProviderIdentity(
	ctx context.Context,
	projectID int,
	storageID int,
) (pro_interfaces.SecretBrowseIdentity, error) {
	browser, ok := s.vaultDeserializer.(pro_interfaces.RuntimeSecretBrowser)
	if !ok {
		return pro_interfaces.SecretBrowseIdentity{}, errors.New("runtime secret browsing unavailable")
	}
	return browser.RuntimeSecretProviderIdentity(ctx, projectID, storageID)
}

// BrowseRuntimeSecretProvider lists one level of a project Vault/OpenBao
// storage through the runtime resolver.
func (s *accessKeyEncryptionServiceImpl) BrowseRuntimeSecretProvider(
	ctx context.Context,
	projectID int,
	storageID int,
	location pro_interfaces.SecretBrowseLocation,
) ([]pro_interfaces.SecretBrowseNode, error) {
	browser, ok := s.vaultDeserializer.(pro_interfaces.RuntimeSecretBrowser)
	if !ok {
		return nil, errors.New("runtime secret browsing unavailable")
	}
	return browser.BrowseRuntimeSecretProvider(ctx, projectID, storageID, location)
}

var _ pro_interfaces.RuntimeSecretBrowser = (*accessKeyEncryptionServiceImpl)(nil)

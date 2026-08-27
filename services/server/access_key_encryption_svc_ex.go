package server

import (
	"context"
	"errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type RuntimeSecretProviderTester interface {
	TestRuntimeSecretProvider(context.Context, int, int) (pro_interfaces.SecretProviderHealth, error)
}

func (s *accessKeyEncryptionServiceImpl) TestRuntimeSecretProvider(
	ctx context.Context,
	projectID int,
	storageID int,
) (pro_interfaces.SecretProviderHealth, error) {
	resolver, ok := s.vaultDeserializer.(pro_interfaces.RuntimeSecretResolver)
	if !ok {
		return pro_interfaces.SecretProviderHealth{}, errors.New("runtime secret resolver unavailable")
	}
	return resolver.TestRuntimeSecretProvider(ctx, projectID, storageID)
}

func (s *accessKeyEncryptionServiceImpl) ReadManagedSecretField(
	ctx context.Context,
	projectID int,
	reference pro_interfaces.SecretReference,
) (pro_interfaces.ManagedSecretField, error) {
	provider, ok := s.vaultDeserializer.(pro_interfaces.ManagedSecretProvider)
	if !ok {
		return pro_interfaces.ManagedSecretField{}, errors.New("managed secret provider unavailable")
	}
	return provider.ReadManagedSecretField(ctx, projectID, reference)
}

func (s *accessKeyEncryptionServiceImpl) WriteManagedSecretField(
	ctx context.Context,
	projectID int,
	reference pro_interfaces.SecretReference,
	value []byte,
	expectedVersion int,
) (int, error) {
	provider, ok := s.vaultDeserializer.(pro_interfaces.ManagedSecretProvider)
	if !ok {
		return 0, errors.New("managed secret provider unavailable")
	}
	return provider.WriteManagedSecretField(ctx, projectID, reference, value, expectedVersion)
}

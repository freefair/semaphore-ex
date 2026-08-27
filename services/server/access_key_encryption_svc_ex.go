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

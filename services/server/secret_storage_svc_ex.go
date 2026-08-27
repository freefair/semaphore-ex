package server

import (
	"context"
	"errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func (s *SecretStorageServiceImpl) TestConnection(
	ctx context.Context,
	projectID int,
	storageID int,
) (pro_interfaces.SecretProviderHealth, error) {
	tester, ok := s.encryptionService.(RuntimeSecretProviderTester)
	if !ok {
		return pro_interfaces.SecretProviderHealth{}, errors.New("runtime secret provider tester unavailable")
	}
	return tester.TestRuntimeSecretProvider(ctx, projectID, storageID)
}

package server

import (
	"context"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func (s *AccessKeyServiceImpl) requireRuntimeSecretWrite(key db.AccessKey) error {
	if key.SourceStorageType == nil || *key.SourceStorageType != db.AccessKeySourceStorageVault {
		return nil
	}
	if s.capabilityProvider == nil {
		return pro_interfaces.CapabilityDeniedError{
			Decision: pro_interfaces.NewCapabilityDecision(
				pro_interfaces.CapabilityRuntimeSecrets,
				pro_interfaces.CapabilityStateUnavailable,
				pro_interfaces.CapabilityReasonProviderUnavailable,
				nil,
				nil,
			),
			Required: pro_interfaces.CapabilityAccessWrite,
		}
	}
	snapshot, err := s.capabilityProvider.Resolve(context.Background(), pro_interfaces.CapabilityRequest{
		IsAdmin: true,
		At:      tz.Now(),
	})
	if err != nil {
		return err
	}
	return snapshot.Require(pro_interfaces.CapabilityRuntimeSecrets, pro_interfaces.CapabilityAccessWrite)
}

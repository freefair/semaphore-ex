package server

import (
	"context"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"time"
)

func (d *VaultAccessKeyDeserializer) ResolveRuntimeSecret(
	_ context.Context,
	_ int,
	_ pro_interfaces.SecretReference,
) ([]byte, error) {
	return nil, d.denied(pro_interfaces.CapabilityAccessExecute)
}

func (d *VaultAccessKeyDeserializer) TestRuntimeSecretProvider(
	_ context.Context,
	_ int,
	storageID int,
) (pro_interfaces.SecretProviderHealth, error) {
	err := d.denied(pro_interfaces.CapabilityAccessExecute)
	return pro_interfaces.SecretProviderHealth{
		StorageID:     storageID,
		State:         pro_interfaces.SecretProviderHealthFailed,
		ErrorCategory: pro_interfaces.SecretProviderErrorCapabilityDisabled,
		CheckedAt:     time.Now().UTC(),
	}, err
}

func (d *VaultAccessKeyDeserializer) denied(access pro_interfaces.CapabilityAccess) error {
	decision := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityRuntimeSecrets,
		pro_interfaces.CapabilityStateUnavailable,
		pro_interfaces.CapabilityReasonProviderUnavailable,
		nil,
		nil,
	)
	return pro_interfaces.CapabilityDeniedError{Decision: decision, Required: access}
}

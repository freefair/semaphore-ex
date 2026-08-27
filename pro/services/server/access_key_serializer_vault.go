package server

import (
	"context"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type VaultStorageTokenDeserializer interface {
	DeserializeSecret(key *db.AccessKey) error
}

type VaultAccessKeyDeserializer struct {
	capabilityProvider pro_interfaces.CapabilityProvider
}

func NewVaultAccessKeyDeserializer(
	_ db.AccessKeyManager,
	_ db.SecretStorageRepository,
	_ VaultStorageTokenDeserializer,
	providers ...pro_interfaces.CapabilityProvider,
) *VaultAccessKeyDeserializer {
	var provider pro_interfaces.CapabilityProvider
	if len(providers) > 0 {
		provider = providers[0]
	}
	return &VaultAccessKeyDeserializer{capabilityProvider: provider}
}

func (d *VaultAccessKeyDeserializer) DeleteSecret(key *db.AccessKey) error {
	return nil
}

func (d *VaultAccessKeyDeserializer) SerializeSecret(key *db.AccessKey) (err error) {
	return
}

func (d *VaultAccessKeyDeserializer) DeserializeSecret(key *db.AccessKey) (res string, err error) {
	return "", d.denied(pro_interfaces.CapabilityAccessExecute)
}

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

var _ pro_interfaces.RuntimeSecretResolver = (*VaultAccessKeyDeserializer)(nil)
